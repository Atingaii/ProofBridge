import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import vm from 'node:vm';
import ts from 'typescript';
const require = createRequire(import.meta.url);
function loadModule(relativePath, dependencies = {}) {
  const source = readFileSync(new URL(relativePath, import.meta.url), 'utf8');
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText;
  const context = { exports: {}, require: (name) => dependencies[name] || require(name) };
  vm.runInNewContext(compiled, context);
  return context.exports;
}
const { uiText } = loadModule('./src/app/lib/i18n.ts');
const { orchestrationParticipantLabel: label, visibleOrchestrationEvents, orchestrationTimelineItems, orchestrationTimelineGroups } =
  loadModule('./src/app/lib/utils.ts', { './i18n': { uiText } });
for (const cli of ['codex', 'claude']) {
  const name = cli === 'codex' ? 'Codex' : 'Claude';
  assert.equal(label(cli), name, 'unknown slot must not invent an A/B identity');
  for (const slot of ['a', 'b']) {
    assert.equal(label(cli, `${cli}-${slot}`), `${name} ${slot.toUpperCase()}`);
    assert.equal(label(cli, `verifier:${cli}-${slot}`), `${name} ${slot.toUpperCase()} · Verifier`);
  }
}
assert.equal(label('codex', 'custom-worker'), 'Codex · custom-worker');
assert.equal(label('claude', 'codex-b'), 'Codex B');
const run = { id: 'identity', status: 'running', maxTurns: 2 };
const events = [
  { runId: 'other', kind: 'turn.start', turnId: 'first', cli: 'claude', turnStartData: { workerSlot: 'claude-b' }, seq: 1 },
  { runId: run.id, kind: 'turn.start', turnId: 'first', cli: 'codex', role: 'worker', task: { name: 'candidate-b' }, turnStartData: { workerSlot: 'codex-a' }, seq: 2 },
  { runId: run.id, kind: 'turn.delta', turnId: 'first', cli: 'codex', role: 'worker', content: 'Candidate from Codex A', seq: 3 },
  { runId: run.id, kind: 'turn.start', turnId: 'second', cli: 'codex', role: 'reviewer', task: { name: 'review', workerSlot: 'codex-a' }, seq: 4 },
  { runId: run.id, kind: 'turn.delta', turnId: 'second', cli: 'codex', role: 'reviewer', content: 'Review from the same participant', seq: 5 },
  { runId: run.id, kind: 'turn.delta', turnId: 'legacy', cli: 'claude', role: 'worker', task: { name: 'candidate-b' }, content: 'Legacy event without an instance', seq: 6 },
];
const before = JSON.stringify(events);
const visible = visibleOrchestrationEvents(events, run.id, run, uiText.zh);
const first = visible.find((event) => event.content === 'Candidate from Codex A');
assert.equal(first.workerSlot, 'codex-a');
assert.equal(first.taskName, 'candidate-b', 'candidate B is a task, not worker B');
const review = visible.find((event) => event.content === 'Review from the same participant');
assert.equal(review.workerSlot, 'codex-a');
assert.equal(review.taskName, 'review');
assert.equal(visible.find((event) => event.turnId === 'legacy').workerSlot, undefined);
const groups = orchestrationTimelineGroups(orchestrationTimelineItems(visible, []), run, events);
assert.equal(groups.find((group) => group.turnId === 'first').workerSlot, 'codex-a');
assert.equal(JSON.stringify(events), before, 'display enrichment cannot mutate persisted events');
console.log('Participant identity: A/B, role changes, verifier isolation, missing slots and turn metadata passed.');
