import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';

const source = readFileSync(new URL('./src/app/lib/statsRequest.ts', import.meta.url), 'utf8');
const code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText;
const { beginStatsRequest, StatsTimeoutError, STATS_REQUEST_TIMEOUT_MS } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
assert.ok(STATS_REQUEST_TIMEOUT_MS > 20_000, 'browser must leave time for the Hub deadline response');

let signal;
const timeout = beginStatsRequest((value) => { signal = value; return new Promise(() => {}); }, 10);
await assert.rejects(timeout.promise, StatsTimeoutError);
assert.equal(signal.aborted, true);

const canceledBeforeStart = beginStatsRequest(() => assert.fail('canceled request must not start'));
canceledBeforeStart.cancel();
await assert.rejects(canceledBeforeStart.promise, { name: 'AbortError' });

let lateResolve;
const canceled = beginStatsRequest(() => new Promise((resolve) => { lateResolve = resolve; }));
await Promise.resolve();
canceled.cancel();
await assert.rejects(canceled.promise, { name: 'AbortError' });
lateResolve('stale data');
assert.equal(await beginStatsRequest(async () => 'retry recovered').promise, 'retry recovered');

let pendingPeerSignal;
const failedPeer = beginStatsRequest((value) => {
  pendingPeerSignal = value;
  return Promise.all([Promise.reject(new Error('stats failed')), new Promise(() => {})]);
});
await assert.rejects(failedPeer.promise, /stats failed/);
assert.equal(pendingPeerSignal.aborted, true, 'failure must cancel unfinished peer requests');
console.log('Statistics request deadlines, ignored cancellation, retry and parallel peer cleanup passed.');
