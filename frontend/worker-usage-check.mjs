import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';

const code = ts.transpileModule(readFileSync(new URL('./src/app/lib/workerUsage.ts', import.meta.url), 'utf8'), { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText;
const { workerChartRows, workerKey, workerLabel } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
const a = { cli: 'codex', workerSlot: 'codex-a', model: 'same-model', inputTokens: 100, outputTokens: 20, cacheReadTokens: 50, cacheWriteTokens: 0, costKnown: true, estimatedCostUsd: 0.1 };
const b = { ...a, workerSlot: 'codex-b', inputTokens: 200, costKnown: false };
assert.notEqual(workerKey(a), workerKey(b));
assert.equal(workerLabel(a, 'unknown'), 'Codex A');
const rounds = [{ round: 1, byWorker: [a, b] }, { round: 2, byWorker: [b] }];
assert.deepEqual(workerChartRows(rounds, [a, b], 'totalTokens', false), [{ round: 1, worker0: 170, worker1: 270 }, { round: 2, worker0: 0, worker1: 270 }]);
assert.deepEqual(workerChartRows(rounds, [a, b], 'totalTokens', true), [{ round: 1, worker0: 170, worker1: 270 }, { round: 2, worker0: 170, worker1: 540 }]);
assert.equal(workerChartRows(rounds, [a, b], 'estimatedCostUsd', true)[1].worker1, null);
const changed = { ...a, model: 'new-model' };
assert.notEqual(workerKey(changed), workerKey(a));
assert.equal(workerChartRows([{ round: 3, byWorker: [changed] }], [a, changed], 'inputTokens', false)[0].worker1, 100);
console.log('Worker usage: A/B, cumulative totals, model changes and unknown prices passed.');

const va = { ...a, workerSlot: 'verifier:codex-a' }, vb = { ...b, workerSlot: 'verifier:codex-b' };
assert.equal(workerLabel(va, 'unknown'), 'Codex A · verifier');
assert.equal(workerLabel(vb, 'unknown'), 'Codex B · verifier');
assert.equal(new Set([a, b, va, vb].map(workerKey)).size, 4);
assert.deepEqual(workerChartRows([{ round: 1, byWorker: [a, b, va, vb] }], [a, b, va, vb], 'totalTokens', false)[0], { round: 1, worker0: 170, worker1: 270, worker2: 170, worker3: 270 });

const roundCode = ts.transpileModule(readFileSync(new URL('./src/app/lib/roundModelUsage.ts', import.meta.url), 'utf8'), { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText;
const { roundModelUsage: detailedRoundModelUsage, modelUsageTotals } = await import(`data:text/javascript;base64,${Buffer.from(roundCode).toString('base64')}`);
const roundModelUsage = (...args) => detailedRoundModelUsage(...args).map(({ round, model, totalTokens }) => ({ round, model, totalTokens }));
for (const [firstCLI, secondCLI] of [['codex', 'codex'], ['claude', 'claude'], ['claude', 'codex']]) {
  const workers = [a, b, va, vb].map((worker, i) => ({ ...worker, cli: i % 2 ? secondCLI : firstCLI, reasoningTokens: 10 }));
  const input = [{ round: 1, totalTokens: 880, byWorker: workers }];
  const before = JSON.stringify(input);
  assert.deepEqual(roundModelUsage(input), [{ round: 1, model: 'same-model', totalTokens: 880 }]);
  assert.equal(JSON.stringify(input), before);
}
assert.deepEqual(roundModelUsage([
  { round: 3, totalTokens: 50 },
  { round: 1, totalTokens: 500, byWorker: [a, { ...b, model: 'different-model' }, { ...a, model: '', inputTokens: 0, outputTokens: 10, cacheReadTokens: 0 }] },
]), [
  { round: 1, model: 'same-model', totalTokens: 170 },
  { round: 1, model: 'different-model', totalTokens: 270 },
  { round: 1, model: '', totalTokens: 60 },
  { round: 3, model: '', totalTokens: 50 },
]);
assert.deepEqual(roundModelUsage([{ round: 2, totalTokens: 25, byWorker: [{ ...a, inputTokens: 0, outputTokens: 0, cacheReadTokens: 0, cacheWriteTokens: 25 }] }]), [{ round: 2, model: 'same-model', totalTokens: 25 }]);
assert.deepEqual(roundModelUsage(), []);
assert.deepEqual(roundModelUsage(null), []);
assert.deepEqual(roundModelUsage([{ round: 1, totalTokens: 0, byWorker: [] }]), []);
console.log('Round/model summary: three CLI pairs, verification, cache, model changes, legacy remainder and immutable input passed.');

assert.deepEqual(detailedRoundModelUsage([{ round: 1, totalTokens: 880, byWorker: [a, b, va, vb] }]), [{ round: 1, model: 'same-model', inputTokens: 600, outputTokens: 80, cacheTokens: 200, totalTokens: 880, estimatedCostUsd: .4, costKnown: false }]);
assert.deepEqual(detailedRoundModelUsage([{ round: 2, inputTokens: 10, outputTokens: 20, cacheTokens: 30, totalTokens: 60, estimatedCostUsd: .5 }]), [{ round: 2, model: '', inputTokens: 10, outputTokens: 20, cacheTokens: 30, totalTokens: 60, estimatedCostUsd: .5, costKnown: false }]);
assert.equal(detailedRoundModelUsage([{ round: 1, totalTokens: 340, byWorker: [a, { ...a, estimatedCostUsd: .7 }] }])[0].estimatedCostUsd, .7999999999999999);
assert.equal(detailedRoundModelUsage([{ round: 1, totalTokens: 0, byWorker: [{ ...a, inputTokens: 0, outputTokens: 0, cacheReadTokens: 0 }] }])[0].estimatedCostUsd, .1);
console.log('Round breakdown: input/output/cache, per-call quotes, partial pricing and legacy totals passed.');

const modelRounds = detailedRoundModelUsage([
  { round: 1, totalTokens: 440, byWorker: [a, { ...b, model: 'second-model', costKnown: true, estimatedCostUsd: .3 }] },
  { round: 2, totalTokens: 610, byWorker: [a, va, { ...b, model: 'second-model', costKnown: true, estimatedCostUsd: .7 }] },
  { round: 3, totalTokens: 25 },
]);
const originalRows = JSON.stringify(modelRounds);
assert.deepEqual(modelUsageTotals(modelRounds), [
  { model: 'same-model', inputTokens: 300, outputTokens: 60, cacheTokens: 150, totalTokens: 510, estimatedCostUsd: .30000000000000004, costKnown: true },
  { model: 'second-model', inputTokens: 400, outputTokens: 40, cacheTokens: 100, totalTokens: 540, estimatedCostUsd: 1, costKnown: true },
  { model: '', inputTokens: 0, outputTokens: 0, cacheTokens: 0, totalTokens: 25, estimatedCostUsd: 0, costKnown: false },
]);
assert.equal(JSON.stringify(modelRounds), originalRows);
assert.equal(modelUsageTotals(detailedRoundModelUsage([{ round: 1, totalTokens: 440, byWorker: [a, b] }]))[0].costKnown, false);
assert.deepEqual(modelUsageTotals([]), []);
console.log('Model totals: cross-round/worker merging, original quotes, legacy remainder, partial costs and immutable input passed.');
