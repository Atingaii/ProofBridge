import type { UserInputItem, UserInputPendingSubmission, UserInputReceipt, UserInputRequest, UserInputSnapshot, UserInputSyncError, UserInputStatus } from './types';

export type UserInputCollection = {
  items: UserInputItem[];
  snapshotRevision: number;
  instanceId?: string;
  closed: Record<string, number>;
  terminal: Record<string, UserInputStatus>;
  receipts: Record<string, UserInputReceipt>;
  submissionReceipts: Record<string, UserInputReceipt>;
  pendingSubmissions: UserInputPendingSubmission[];
};

export const emptyUserInputCollection = (): UserInputCollection => ({
  items: [], snapshotRevision: 0, closed: {}, terminal: {}, receipts: {}, submissionReceipts: {}, pendingSubmissions: [],
});

const userInputStoragePrefix = 'codexBridge.nativeInputPending.v3:';
const pendingFields = ['userId', 'runId', 'requestId', 'submissionId', 'executionId', 'instanceId'] as const;

export function loadUserInputStorage(storage: Storage, userId: string): UserInputPendingSubmission[] {
  const raw = storage.getItem(userInputStoragePrefix + userId);
  if (!raw) return [];
  const values: unknown = JSON.parse(raw);
  if (!Array.isArray(values) || values.length > 64 || !values.every((value) =>
    record(value) && value.userId === userId && pendingFields.every((field) =>
      typeof value[field] === 'string' && Boolean(value[field]) && value[field].length <= 128) &&
    typeof value.createdAt === 'number' && Number.isFinite(value.createdAt))) throw new Error('Invalid pending submission storage');
  // Reconstruct the allowlist; never retain any answer fields from storage.
  return values.map((value) => ({ userId: value.userId, runId: value.runId, requestId: value.requestId,
    submissionId: value.submissionId, executionId: value.executionId, instanceId: value.instanceId, createdAt: value.createdAt }));
}

export function saveUserInputStorage(storage: Storage, userId: string, values: UserInputPendingSubmission[]): void {
  if (values.length > 64) throw new Error('Pending submission storage is full');
  storage.setItem(userInputStoragePrefix + userId, JSON.stringify(values.map((value) => ({
    userId, runId: value.runId, requestId: value.requestId, submissionId: value.submissionId,
    executionId: value.executionId, instanceId: value.instanceId, createdAt: value.createdAt,
  }))));
}

export function clearUserInputStorage(storage: Storage, userId?: string): void {
  if (userId) { storage.removeItem(userInputStoragePrefix + userId); return; }
  const keys = Array.from({ length: storage.length }, (_, index) => storage.key(index));
  for (const key of keys) if (key?.startsWith(userInputStoragePrefix)) storage.removeItem(key);
}

const record = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

const revision = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;

const receiptValid = (value: unknown, runId: string): value is UserInputReceipt =>
  record(value) && typeof value.requestId === 'string' && Boolean(value.requestId) &&
  value.runId === runId && typeof value.executionId === 'string' && Boolean(value.executionId) &&
  typeof value.instanceId === 'string' && Boolean(value.instanceId) &&
  typeof value.submissionId === 'string' && Boolean(value.submissionId) && revision(value.revision) &&
  (value.outcome === 'delivered' || value.outcome === 'not_sent' || value.outcome === 'unknown');

const requestValid = (value: unknown, runId: string): value is UserInputRequest =>
  record(value) && typeof value.requestId === 'string' && Boolean(value.requestId) &&
  value.runId === runId && typeof value.executionId === 'string' && Boolean(value.executionId) &&
  revision(value.revision) && typeof value.cli === 'string' && typeof value.blocking === 'boolean' &&
  Array.isArray(value.questions) && value.questions.length > 0 && value.questions.every((question: unknown) =>
    record(question) && typeof question.id === 'string' && Boolean(question.id) &&
    typeof question.question === 'string' && Boolean(question.question) &&
    (question.selectionMode === 'single' || question.selectionMode === 'multiple') &&
    (question.options == null || (Array.isArray(question.options) && question.options.every((option: unknown) =>
      record(option) && typeof option.id === 'string' && typeof option.label === 'string'))));

const statusValid = (value: unknown, runId: string): value is UserInputStatus =>
  record(value) && typeof value.requestId === 'string' && Boolean(value.requestId) &&
  value.runId === runId && revision(value.revision) &&
  (value.status === 'queued' || value.status === 'delivered' || value.status === 'closed' || value.status === 'rejected') &&
  (value.receipt == null || (receiptValid(value.receipt, runId) &&
    value.receipt.requestId === value.requestId &&
    (value.executionId == null || value.receipt.executionId === value.executionId) &&
    (value.instanceId == null || value.receipt.instanceId === value.instanceId)));

export const decodeUserInputRequest = (value: unknown): UserInputRequest | null =>
  record(value) && typeof value.runId === 'string' && requestValid(value, value.runId) ? value : null;

export const decodeUserInputStatus = (value: unknown): UserInputStatus | null =>
  record(value) && typeof value.runId === 'string' && statusValid(value, value.runId) ? value : null;

// A sync rejection must not pass through the request reducer or unlock an answer.
export const decodeUserInputSyncError = (value: unknown): UserInputSyncError | null =>
  record(value) && value.requestId === '' &&
  typeof value.runId === 'string' && Boolean(value.runId) &&
  typeof value.syncId === 'string' && Boolean(value.syncId) && value.syncId.length <= 128 &&
  value.source === 'hub' && value.phase === 'route' && value.status === 'rejected' && value.revision === 0 &&
  typeof value.reason === 'string' && Boolean(value.reason) &&
  ['receipt', 'lifecycle', 'submissionId', 'executionId', 'instanceId'].every((key) => value[key] === undefined)
    ? value as UserInputSyncError : null;

// An older Bridge can encode a nil Go slice as null. Invalid nonempty frames
// are rejected whole, so they cannot erase live requests through a snapshot.
export function decodeUserInputSnapshot(value: unknown): UserInputSnapshot | null {
  if (!record(value) || typeof value.runId !== 'string' || !value.runId ||
      typeof value.syncId !== 'string' || !value.syncId || !revision(value.revision)) return null;
  const requests = value.requests == null ? [] : value.requests;
  const statuses = value.statuses == null ? [] : value.statuses;
  const closed = value.closed == null ? [] : value.closed;
  const receipts = value.receipts == null ? [] : value.receipts;
  const reconciliations = value.reconciliations == null ? [] : value.reconciliations;
  if (!Array.isArray(requests) || !Array.isArray(statuses) || !Array.isArray(closed) || !Array.isArray(receipts) || !Array.isArray(reconciliations) ||
      !requests.every((item) => requestValid(item, value.runId as string)) ||
      !statuses.every((item) => statusValid(item, value.runId as string)) ||
      !closed.every((item) => statusValid(item, value.runId as string) && item.status === 'closed') ||
      !receipts.every((item) => receiptValid(item, value.runId as string)) ||
      !reconciliations.every((item) => statusValid(item, value.runId as string)) ||
      (value.reconcileError != null && typeof value.reconcileError !== 'string')) return null;
  if (typeof value.instanceId === 'string' &&
      (requests.some((item) => item.instanceId && item.instanceId !== value.instanceId) ||
        statuses.some((item) => item.instanceId && item.instanceId !== value.instanceId) ||
        closed.some((item) => item.instanceId && item.instanceId !== value.instanceId) ||
        receipts.some((item) => item.instanceId !== value.instanceId) ||
        reconciliations.some((item) => item.instanceId && item.instanceId !== value.instanceId))) return null;
  return { ...value, requests, statuses, closed, receipts, reconciliations } as UserInputSnapshot;
}

function sameInstance(state: UserInputCollection, instanceId?: string): boolean {
  return !instanceId || !state.instanceId || instanceId === state.instanceId;
}

export const userInputReceiptKey = (requestId: string, submissionId: string): string =>
  JSON.stringify([requestId, submissionId]);

function addReceipt(state: UserInputCollection, receipt: UserInputReceipt): UserInputCollection {
  if (!sameInstance(state, receipt.instanceId)) return state;
  const key = userInputReceiptKey(receipt.requestId, receipt.submissionId);
  const priorSubmission = state.submissionReceipts[key];
  if (priorSubmission && priorSubmission.revision > receipt.revision) return state;
  if (receipt.outcome !== 'unknown') state = { ...state, pendingSubmissions: state.pendingSubmissions.filter((pending) =>
    pending.requestId !== receipt.requestId || pending.submissionId !== receipt.submissionId ||
    pending.executionId !== receipt.executionId || pending.instanceId !== receipt.instanceId) };
  const submissionReceipts = Object.fromEntries(Object.entries({ ...state.submissionReceipts, [key]: receipt })
    .sort((a, b) => b[1].revision - a[1].revision).slice(0, 256));
  const previous = state.receipts[receipt.requestId];
  if (previous && (previous.instanceId !== receipt.instanceId || previous.revision > receipt.revision ||
      (previous.outcome === 'delivered' && receipt.outcome !== 'delivered'))) return { ...state, submissionReceipts };
  const receipts = { ...state.receipts, [receipt.requestId]: receipt };
  const items: UserInputItem[] = state.items.map((item) => {
    if (item.request.requestId !== receipt.requestId || item.request.executionId !== receipt.executionId) return item;
    if (receipt.revision < item.revision) return item;
    // A failed losing submission cannot release another tab's reservation.
    if (receipt.outcome === 'not_sent' && item.submissionId && item.submissionId !== receipt.submissionId) return item;
    if (receipt.outcome === 'not_sent' && item.status === 'delivered') return item;
    if (receipt.outcome === 'not_sent') return { ...item, receipt, status: 'pending', revision: receipt.revision,
      submissionId: undefined, error: undefined };
    return { ...item, receipt, status: receipt.outcome === 'delivered' ? 'delivered' : 'confirming',
      revision: receipt.revision, submissionId: receipt.submissionId, error: undefined };
  });
  return { ...state, receipts, submissionReceipts, items };
}

export function receiveUserInputRequest(state: UserInputCollection, request: UserInputRequest): UserInputCollection {
  if (!request.requestId || !request.executionId || !request.questions?.length ||
      !sameInstance(state, request.instanceId) || request.revision <= state.snapshotRevision ||
      Object.hasOwn(state.closed, request.requestId)) return state;
  const prior = state.items.find((item) => item.request.requestId === request.requestId);
  if (prior && prior.revision >= request.revision) return state;
  const receipt = state.receipts[request.requestId];
  const pending = state.pendingSubmissions.find((entry) => entry.requestId === request.requestId && entry.executionId === request.executionId);
  const item: UserInputItem = { request, status: receipt?.outcome === 'delivered' ? 'delivered' : pending ? 'confirming' : 'pending',
    revision: request.revision, receipt, submissionId: pending?.submissionId };
  return { ...state, instanceId: state.instanceId || request.instanceId,
    items: prior ? state.items.map((current) => current === prior ? item : current) : [...state.items, item] };
}

export function receiveUserInputStatus(state: UserInputCollection, status: UserInputStatus): UserInputCollection {
  if (!sameInstance(state, status.instanceId)) return state;
  let next = status.receipt ? addReceipt(state, status.receipt) : state;
  const prior = next.items.find((item) => item.request.requestId === status.requestId);
  if (status.status === 'closed' || status.lifecycle === 'closed') {
    if (status.source === 'hub') return next;
    const oldRevision = next.closed[status.requestId] || 0;
    if (status.revision < oldRevision) return next;
    return { ...next, instanceId: next.instanceId || status.instanceId,
      pendingSubmissions: next.pendingSubmissions.filter((pending) => pending.requestId !== status.requestId ||
        (status.executionId != null && pending.executionId !== status.executionId)),
      closed: { ...next.closed, [status.requestId]: status.revision },
      terminal: { ...next.terminal, [status.requestId]: status },
      items: next.items.filter((item) => item.request.requestId !== status.requestId || item.revision > status.revision) };
  }
  if (!prior || Object.hasOwn(next.closed, status.requestId) || (status.revision > 0 && status.revision < prior.revision)) return next;
  if (status.status === 'queued') {
    if (status.source === 'hub' || !status.submissionId || prior.status === 'delivered') return next;
    return { ...next, items: next.items.map((item) => item === prior
      ? { ...item, status: 'submitting', revision: Math.max(item.revision, status.revision),
        submissionId: status.submissionId, receipt: undefined, error: undefined }
      : item) };
  }
  if (status.status === 'rejected') {
    if (prior.status !== 'submitting' || !status.submissionId || status.submissionId !== prior.submissionId) return next;
    // A Hub route error cannot prove that an earlier copy was not delivered.
    return { ...next, items: next.items.map((item) => item === prior
      ? { ...item, status: 'confirming', error: status.reason || 'Delivery is not confirmed.' }
      : item) };
  }
  if (status.status === 'delivered') {
    return { ...next, pendingSubmissions: next.pendingSubmissions.filter((pending) => pending.requestId !== status.requestId),
      items: next.items.map((item) => item === prior
      ? { ...item, status: 'delivered', revision: Math.max(item.revision, status.revision),
        submissionId: status.submissionId || item.submissionId, error: undefined }
      : item) };
  }
  return next;
}

export function receiveUserInputSnapshot(state: UserInputCollection, snapshot: UserInputSnapshot): UserInputCollection {
  const switched = Boolean(snapshot.instanceId && state.instanceId && snapshot.instanceId !== state.instanceId);
  const current = switched ? { ...emptyUserInputCollection(), pendingSubmissions: state.pendingSubmissions } : state;
  if (snapshot.revision < current.snapshotRevision) return current;
  const live = new Set(snapshot.requests.map((request) => request.requestId));
  const newer = current.items.filter((item) => item.revision > snapshot.revision && !live.has(item.request.requestId));
  const items: UserInputItem[] = snapshot.requests
    .filter((request) => !Object.hasOwn(current.closed, request.requestId))
    .map((request) => {
      const prior = current.items.find((item) => item.request.requestId === request.requestId);
      if (prior && prior.revision > request.revision) return prior;
      const pending = current.pendingSubmissions.find((entry) => entry.requestId === request.requestId && entry.executionId === request.executionId);
      return { request, revision: request.revision,
        status: prior?.status === 'submitting' || prior?.status === 'confirming' ? prior.status : pending ? 'confirming' : 'pending',
        submissionId: prior?.submissionId || pending?.submissionId };
    });
  let next: UserInputCollection = { ...current, instanceId: snapshot.instanceId || current.instanceId,
    items: [...items, ...newer], snapshotRevision: snapshot.revision };
  // Restore the reservation before individual submission receipts. A loser
  // can receive a newer rejection while another submission remains queued.
  for (const status of snapshot.statuses || []) next = receiveUserInputStatus(next, status);
  for (const receipt of snapshot.receipts || []) next = addReceipt(next, receipt);
  for (const status of snapshot.closed || []) next = receiveUserInputStatus(next, status);
  for (const status of snapshot.reconciliations || []) next = receiveUserInputStatus(next, status);
  // The current snapshot is authoritative for its bounded terminal window;
  // keep only newer local tombstones that could have arrived after it.
  const closedIDs = new Set((snapshot.closed || []).map((status) => status.requestId));
  for (const status of snapshot.reconciliations || []) if (status.status === 'closed' || status.lifecycle === 'closed') closedIDs.add(status.requestId);
  // A replacement Bridge cannot establish delivery facts about the old one.
  // Retire its editable card and metadata without inventing a not_sent receipt.
  if (snapshot.instanceId) {
    const retired = next.pendingSubmissions.filter((pending) => pending.instanceId !== snapshot.instanceId);
    for (const pending of retired) {
      closedIDs.add(pending.requestId);
      next = { ...next, terminal: { ...next.terminal, [pending.requestId]: {
        requestId: pending.requestId, runId: pending.runId, executionId: pending.executionId,
        submissionId: pending.submissionId, status: 'closed', lifecycle: 'closed', revision: snapshot.revision,
        reason: 'Bridge instance changed; delivery cannot be confirmed.',
      } }, closed: { ...next.closed, [pending.requestId]: snapshot.revision },
      items: next.items.filter((item) => item.request.requestId !== pending.requestId) };
    }
    if (retired.length) next = { ...next, pendingSubmissions: next.pendingSubmissions.filter((pending) => pending.instanceId === snapshot.instanceId) };
  }
  const terminal = Object.fromEntries(Object.entries(next.terminal).filter(([id, status]) =>
    closedIDs.has(id) || status.revision > snapshot.revision));
  const closed = Object.fromEntries(Object.entries(next.closed).filter(([id, value]) =>
    closedIDs.has(id) || value > snapshot.revision));
  // A snapshot's bounded terminal window is not a denial of a receipt already
  // observed by this tab. Keep confirmed facts locally, with a fixed cap.
  const receipts = Object.fromEntries(Object.entries(next.receipts)
    .sort((a, b) => b[1].revision - a[1].revision).slice(0, 256));
  return { ...next, terminal, closed, receipts };
}

export function submittingUserInput(state: UserInputCollection, requestID: string, submissionID: string): UserInputCollection {
  return { ...state, items: state.items.map((item) => item.request.requestId === requestID && item.status === 'pending'
    ? { ...item, status: 'submitting', submissionId: submissionID, error: undefined }
    : item) };
}

export function confirmPendingUserInputs(state: UserInputCollection): UserInputCollection {
  return { ...state, items: state.items.map((item) => item.status === 'submitting'
    ? { ...item, status: 'confirming', error: undefined }
    : item) };
}
