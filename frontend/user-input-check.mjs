import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import ts from 'typescript';

const source = readFileSync(fileURLToPath(new URL('./src/app/lib/userInput.ts', import.meta.url)), 'utf8');
const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText;
const { clearUserInputStorage, loadUserInputStorage, saveUserInputStorage, confirmPendingUserInputs, decodeUserInputStatus, decodeUserInputSyncError, decodeUserInputSnapshot, emptyUserInputCollection, receiveUserInputRequest, receiveUserInputSnapshot, receiveUserInputStatus, submittingUserInput, userInputReceiptKey } =
  await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`);

const request = (id, revision) => ({ requestId: id, runId: 'run1', executionId: 'exec1', cli: 'codex', blocking: false, revision,
  questions: [{ id: 'q1', question: 'Choose', selectionMode: 'single', options: [{ id: 'a', label: 'A' }] }] });

let state = emptyUserInputCollection();
state = receiveUserInputRequest(state, request('old', 1));
state = submittingUserInput(state, 'old', 'submission1');
assert.equal(state.items[0].status, 'submitting');
state = receiveUserInputStatus(state, { requestId: 'old', runId: 'run1', submissionId: 'submission1', status: 'delivered', revision: 2 });
assert.equal(state.items[0].status, 'delivered');
state = receiveUserInputStatus(state, { requestId: 'old', runId: 'run1', status: 'closed', revision: 3 });
assert.equal(state.items.length, 0);
assert.equal(state.terminal.old.status, 'closed');
state = receiveUserInputRequest(state, request('old', 1));
assert.equal(state.items.length, 0, 'late request must not revive a closed call');
state = receiveUserInputStatus(state, { requestId: 'old', runId: 'run1', submissionId: 'submission1', status: 'rejected', source: 'hub', phase: 'route', revision: 0 });
assert.equal(state.items.length, 0, 'Hub rejection after close cannot restore a writable question');

state = receiveUserInputRequest(state, request('new', 5));
state = receiveUserInputSnapshot(state, { runId: 'run1', syncId: 'sync-delivered', revision: 5, requests: [request('new', 5)], statuses: [
  { requestId: 'new', runId: 'run1', submissionId: 'answer2', status: 'delivered', revision: 5 },
] });
assert.equal(state.items[0].status, 'delivered', 'snapshot must restore answer delivery');
state = receiveUserInputSnapshot(state, { runId: 'run1', syncId: 'sync1', revision: 4, requests: [] });
assert.deepEqual(state.items.map((item) => item.request.requestId), ['new'], 'older snapshot must preserve newer live request');
state = receiveUserInputSnapshot(state, { runId: 'run1', syncId: 'sync2', revision: 6, requests: [] });
assert.equal(state.items.length, 0, 'current empty snapshot must remove absent requests');
state = receiveUserInputRequest(state, request('stale', 5));
assert.equal(state.items.length, 0, 'late request before snapshot watermark must be ignored');

let otherTab = receiveUserInputRequest(emptyUserInputCollection(), request('shared', 1));
otherTab = submittingUserInput(otherTab, 'shared', 'second-tab');
otherTab = receiveUserInputStatus(otherTab, { requestId: 'shared', runId: 'run1', submissionId: 'first-tab', status: 'delivered', revision: 2 });
assert.equal(otherTab.items[0].status, 'delivered', 'first accepted answer must settle all tabs');
otherTab = receiveUserInputStatus(otherTab, { requestId: 'shared', runId: 'run1', submissionId: 'second-tab', status: 'rejected', revision: 3 });
assert.equal(otherTab.items[0].status, 'delivered', 'late losing submission must not reopen the card');

let closedBeforeSnapshot = receiveUserInputStatus(emptyUserInputCollection(), {
  requestId: 'closed-first', runId: 'run1', status: 'closed', revision: 3,
});
closedBeforeSnapshot = receiveUserInputSnapshot(closedBeforeSnapshot, {
  runId: 'run1', syncId: 'old-sync', revision: 2, requests: [request('closed-first', 1)],
});
assert.equal(closedBeforeSnapshot.items.length, 0, 'older snapshot must not revive a request closed before its request frame');

const emptyDecoded = decodeUserInputSnapshot({ runId: 'run1', syncId: 'empty', revision: 10, requests: null, statuses: null });
assert.ok(emptyDecoded);
assert.deepEqual(emptyDecoded.requests, [], 'nil Go slice must decode to an empty array');
assert.equal(decodeUserInputSnapshot({ runId: 'run1', syncId: 'bad', revision: 11, requests: {} }), null);
assert.equal(decodeUserInputSnapshot({ runId: 'run1', syncId: 'bad', revision: 11, requests: [{ bad: true }] }), null);

const receipt = { requestId: 'lost-ack', runId: 'run1', executionId: 'exec1', instanceId: 'bridge-1',
  submissionId: 'answer-1', outcome: 'delivered', revision: 3 };
let lostAck = receiveUserInputRequest(emptyUserInputCollection(), { ...request('lost-ack', 1), instanceId: 'bridge-1' });
lostAck = submittingUserInput(lostAck, 'lost-ack', 'answer-1');
lostAck = confirmPendingUserInputs(lostAck);
assert.equal(lostAck.items[0].status, 'confirming', 'disconnect cannot make an uncertain answer editable');
lostAck = receiveUserInputSnapshot(lostAck, { runId: 'run1', syncId: 'recovery', instanceId: 'bridge-1',
  revision: 4, requests: [], receipts: [receipt], closed: [
    { requestId: 'lost-ack', runId: 'run1', executionId: 'exec1', instanceId: 'bridge-1', status: 'closed',
      lifecycle: 'closed', revision: 4, receipt },
  ] });
assert.equal(lostAck.items.length, 0);
assert.equal(lostAck.terminal['lost-ack'].status, 'closed');
assert.equal(lostAck.receipts['lost-ack'].outcome, 'delivered', 'snapshot must preserve receipt after native close');
lostAck = receiveUserInputSnapshot(lostAck, { runId: 'run1', syncId: 'later', instanceId: 'bridge-1',
  revision: 5, requests: [], closed: [], receipts: [] });
assert.equal(lostAck.receipts['lost-ack'].outcome, 'delivered', 'a later bounded snapshot cannot erase a known delivery');
lostAck = receiveUserInputStatus(lostAck, { requestId: 'lost-ack', runId: 'run1', status: 'rejected', source: 'hub', revision: 0, submissionId: 'answer-1' });
assert.equal(lostAck.items.length, 0);
lostAck = receiveUserInputRequest(lostAck, { ...request('lost-ack', 2), instanceId: 'bridge-1' });
assert.equal(lostAck.items.length, 0);
lostAck = receiveUserInputSnapshot(lostAck, { runId: 'run1', syncId: 'new-instance', instanceId: 'bridge-2',
  revision: 1, requests: [{ ...request('fresh', 1), instanceId: 'bridge-2' }], statuses: [] });
assert.deepEqual(lostAck.items.map((item) => item.request.requestId), ['fresh'], 'Bridge restart resets revision domain');
assert.equal(lostAck.receipts['lost-ack'], undefined, 'old Bridge receipts cannot cross instance boundaries');

let routeFailure = receiveUserInputRequest(emptyUserInputCollection(), { ...request('route', 1), instanceId: 'bridge-1' });
routeFailure = submittingUserInput(routeFailure, 'route', 'route-answer');
routeFailure = receiveUserInputStatus(routeFailure, { requestId: 'route', runId: 'run1', submissionId: 'other-answer',
  status: 'rejected', source: 'hub', phase: 'route', revision: 0 });
assert.equal(routeFailure.items[0].status, 'submitting', 'unrelated Hub rejection cannot affect a submission');
routeFailure = receiveUserInputStatus(routeFailure, { requestId: 'route', runId: 'run1', submissionId: 'route-answer',
  status: 'rejected', source: 'hub', phase: 'route', revision: 0 });
assert.equal(routeFailure.items[0].status, 'confirming', 'Hub routing cannot disprove earlier delivery');
routeFailure = receiveUserInputStatus(routeFailure, { requestId: 'route', runId: 'run1', status: 'closed',
  lifecycle: 'closed', instanceId: 'bridge-1', revision: 2 });
assert.equal(routeFailure.items.length, 0, 'native close takes precedence over routing errors');

let validationFailure = receiveUserInputRequest(emptyUserInputCollection(), { ...request('validate', 1), instanceId: 'bridge-1' });
validationFailure = submittingUserInput(validationFailure, 'validate', 'invalid-answer');
validationFailure = receiveUserInputStatus(validationFailure, { requestId: 'validate', runId: 'run1',
  submissionId: 'invalid-answer', status: 'rejected', source: 'bridge', phase: 'validate', lifecycle: 'open',
  revision: 2, receipt: { requestId: 'validate', runId: 'run1', executionId: 'exec1', instanceId: 'bridge-1',
    submissionId: 'invalid-answer', outcome: 'not_sent', revision: 2 } });
assert.equal(validationFailure.items[0].status, 'pending', 'authoritative not_sent allows a corrected new submission');
assert.equal(validationFailure.items[0].submissionId, undefined);

const firstFailed = { requestId: 'two-attempts', runId: 'run1', executionId: 'exec1', instanceId: 'bridge-1',
  submissionId: 'attempt-a', outcome: 'not_sent', revision: 2 };
const laterDelivered = { ...firstFailed, submissionId: 'attempt-b', outcome: 'delivered', revision: 4 };
let twoAttempts = receiveUserInputRequest(emptyUserInputCollection(), { ...request('two-attempts', 1), instanceId: 'bridge-1' });
twoAttempts = receiveUserInputSnapshot(twoAttempts, { runId: 'run1', syncId: 'two-attempts', instanceId: 'bridge-1',
  revision: 5, requests: [{ ...request('two-attempts', 1), instanceId: 'bridge-1' }],
  statuses: [{ requestId: 'two-attempts', runId: 'run1', status: 'delivered', instanceId: 'bridge-1',
    executionId: 'exec1', submissionId: 'attempt-b', revision: 4, receipt: laterDelivered }],
  receipts: [laterDelivered, firstFailed] });
assert.equal(twoAttempts.receipts['two-attempts'].submissionId, 'attempt-b',
  'an older not_sent receipt cannot displace a later delivered attempt');
assert.equal(twoAttempts.items[0].status, 'delivered');

const queued = { requestId: 'queued', runId: 'run1', executionId: 'exec1', instanceId: 'bridge-1',
  submissionId: 'winner', status: 'queued', lifecycle: 'open', source: 'bridge', revision: 4 };
let queuedTab = receiveUserInputRequest(emptyUserInputCollection(), { ...request('queued', 1), instanceId: 'bridge-1' });
queuedTab = submittingUserInput(queuedTab, 'queued', 'loser');
queuedTab = receiveUserInputStatus(queuedTab, queued);
assert.equal(queuedTab.items[0].status, 'submitting', 'a native reservation disables every tab while delivery is queued');
assert.equal(queuedTab.items[0].submissionId, 'winner');
const loserReceipt = { requestId: 'queued', runId: 'run1', executionId: 'exec1', instanceId: 'bridge-1',
  submissionId: 'loser', outcome: 'not_sent', revision: 3 };
queuedTab = receiveUserInputStatus(queuedTab, { ...queued, status: 'rejected', submissionId: 'loser', revision: 5, receipt: loserReceipt });
assert.equal(queuedTab.items[0].status, 'submitting', 'a losing submission rejection must not unlock the winner');
assert.equal(queuedTab.items[0].submissionId, 'winner');
queuedTab = receiveUserInputSnapshot(queuedTab, { runId: 'run1', syncId: 'queued-refresh', instanceId: 'bridge-1', revision: 5,
  requests: [{ ...request('queued', 1), instanceId: 'bridge-1' }], receipts: [loserReceipt], statuses: [queued] });
assert.equal(queuedTab.items[0].status, 'submitting', 'refresh preserves queued state rather than making it editable');
const freshTab = receiveUserInputSnapshot(emptyUserInputCollection(), { runId: 'run1', syncId: 'queued-new-tab',
  instanceId: 'bridge-1', revision: 5, requests: [{ ...request('queued', 1), instanceId: 'bridge-1' }],
  receipts: [loserReceipt], statuses: [queued] });
assert.equal(freshTab.items[0].status, 'submitting', 'a new tab must also honor the queued winner');
assert.equal(freshTab.items[0].submissionId, 'winner');
const newerLosingReceiptTab = receiveUserInputSnapshot(emptyUserInputCollection(), { runId: 'run1', syncId: 'queued-newer-loser',
  instanceId: 'bridge-1', revision: 5, requests: [{ ...request('queued', 1), instanceId: 'bridge-1' }],
  receipts: [{ ...loserReceipt, revision: 5 }], statuses: [queued] });
assert.equal(newerLosingReceiptTab.items[0].status, 'submitting', 'a newer losing receipt cannot hide the snapshot reservation');
assert.equal(newerLosingReceiptTab.items[0].submissionId, 'winner');
const winnerReceipt = { ...loserReceipt, submissionId: 'winner', outcome: 'delivered', revision: 6 };
queuedTab = receiveUserInputStatus(queuedTab, { ...queued, status: 'delivered', revision: 6, receipt: winnerReceipt });
queuedTab = receiveUserInputStatus(queuedTab, { ...queued, status: 'rejected', submissionId: 'loser', revision: 7,
  receipt: { ...loserReceipt, revision: 7 } });
assert.equal(queuedTab.items[0].status, 'delivered', 'a later losing receipt cannot replace confirmed delivery');
assert.equal(queuedTab.receipts.queued.outcome, 'delivered');
assert.equal(queuedTab.submissionReceipts[userInputReceiptKey('queued', 'loser')].outcome, 'not_sent');
assert.equal(queuedTab.submissionReceipts[userInputReceiptKey('queued', 'winner')].outcome, 'delivered');

// Refresh restoration contains only identities; a bootstrap cannot unlock them.
const pendingIdentity = { userId: 'user1', runId: 'run1', requestId: 'recover', submissionId: 'old-answer',
  executionId: 'exec1', instanceId: 'bridge-1', createdAt: 1 };
const storageValues = new Map();
const storage = { getItem: (key) => storageValues.get(key) ?? null,
  setItem: (key, value) => storageValues.set(key, value), removeItem: (key) => storageValues.delete(key),
  key: (index) => [...storageValues.keys()][index], get length() { return storageValues.size; } };
saveUserInputStorage(storage, 'user1', [{ ...pendingIdentity, answers: { secret: 'never-store-this' } }]);
assert.equal([...storageValues.values()][0].includes('never-store-this'), false);
assert.deepEqual(loadUserInputStorage(storage, 'user1'), [pendingIdentity]);
assert.throws(() => saveUserInputStorage(storage, 'user1', Array(65).fill(pendingIdentity)), /full/);
assert.throws(() => saveUserInputStorage({ setItem() { throw new Error('quota'); } }, 'user1', [pendingIdentity]), /quota/);
const restored = { ...emptyUserInputCollection(), pendingSubmissions: loadUserInputStorage(storage, 'user1') };
const recoverRequest = { ...request('recover', 1), instanceId: 'bridge-1' };
const bootstrapped = receiveUserInputSnapshot(restored, { runId: 'run1', syncId: 'bootstrap', instanceId: 'bridge-1',
  revision: 1, requests: [recoverRequest] });
assert.equal(bootstrapped.items[0].status, 'confirming');
assert.equal(bootstrapped.pendingSubmissions[0].submissionId, 'old-answer');
const fencedReceipt = { ...receipt, requestId: 'recover', submissionId: 'old-answer', outcome: 'not_sent', revision: 2 };
const reconciled = receiveUserInputSnapshot(bootstrapped, { runId: 'run1', syncId: 'fence', instanceId: 'bridge-1',
  revision: 2, requests: [recoverRequest], reconciliations: [{ requestId: 'recover', runId: 'run1',
    executionId: 'exec1', instanceId: 'bridge-1', submissionId: 'old-answer', status: 'rejected', lifecycle: 'open',
    source: 'bridge', receipt: fencedReceipt, revision: 2 }] });
assert.equal(reconciled.items[0].status, 'pending', 'only the authoritative fenced result releases editing');
assert.equal(reconciled.pendingSubmissions.length, 0);
assert.equal(submittingUserInput(reconciled, 'recover', 'new-answer').items[0].submissionId, 'new-answer');
const unknown = receiveUserInputSnapshot(bootstrapped, { runId: 'run1', syncId: 'unknown', instanceId: 'bridge-1',
  revision: 2, requests: [recoverRequest], reconcileError: 'receipt capacity is full' });
assert.equal(unknown.items[0].status, 'confirming', 'query failure cannot enable editing');
assert.equal(unknown.pendingSubmissions.length, 1);
const replacedBridge = receiveUserInputSnapshot(bootstrapped, { runId: 'run1', syncId: 'replacement',
  instanceId: 'bridge-2', revision: 0, requests: [] });
assert.equal(replacedBridge.items.length, 0);
assert.equal(replacedBridge.pendingSubmissions.length, 0);
assert.equal(replacedBridge.terminal.recover.status, 'closed');
assert.equal(replacedBridge.receipts.recover, undefined, 'instance replacement is not proof of non-delivery');
assert.equal(decodeUserInputSnapshot({ runId: 'run1', syncId: 'bad-reconciliation', revision: 2,
  reconciliations: [{ bad: true }] }), null);
clearUserInputStorage(storage, 'user1');
assert.equal(storage.length, 0);



// The exact Hub query rejection has no request identity and cannot enter the reducer.
const syncError = { runId: 'run1', requestId: '', syncId: 'query1', status: 'rejected',
  source: 'hub', phase: 'route', revision: 0, reason: 'CLI endpoint is offline' };
assert.deepEqual(decodeUserInputSyncError(syncError), syncError);
assert.equal(decodeUserInputStatus(syncError), null);
for (const patch of [{ requestId: 'request' }, { syncId: '' }, { runId: '' }, { source: 'bridge' },
  { revision: 1 }, { phase: 'queue' }, { status: 'delivered' }, { reason: null },
  { receipt: {} }, { lifecycle: 'closed' }, { executionId: 'exec' }, { submissionId: 'answer' }, { instanceId: 'bridge' }]) {
  assert.equal(decodeUserInputSyncError({ ...syncError, ...patch }), null, JSON.stringify(patch));
}

// A stale positive receipt must not clear a newer explicit unknown submission.
const unknownReceipt = { requestId: 'recover', runId: 'run1', executionId: recoverRequest.executionId,
  instanceId: 'bridge-1', submissionId: bootstrapped.pendingSubmissions[0].submissionId,
  outcome: 'unknown', revision: 10 };
const knownUnknown = receiveUserInputStatus(bootstrapped, { ...unknownReceipt, status: 'rejected', source: 'bridge', receipt: unknownReceipt });
const staleDelivered = { ...unknownReceipt, outcome: 'delivered', revision: 9 };
const stillUnknown = receiveUserInputStatus(knownUnknown, { ...staleDelivered, status: 'delivered', source: 'bridge', receipt: staleDelivered });
assert.equal(stillUnknown.pendingSubmissions.length, 1, 'lower revision receipt must not unlock pending delivery');
assert.equal(stillUnknown.submissionReceipts[userInputReceiptKey('recover', unknownReceipt.submissionId)].outcome, 'unknown');
assert.equal(stillUnknown.items[0].status, 'confirming');
console.log('Structured user input decoding, receipt recovery, and delayed-frame checks passed.');
