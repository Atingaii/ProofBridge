import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
const compile = file => ts.transpileModule(readFileSync(new URL(file, import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
}).outputText;
const dataURL = code => `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
const code = compile('./src/app/lib/runReadRequests.ts')
  .replace("'./api'", JSON.stringify(dataURL(compile('./src/app/lib/api.ts'))))
  .replace("'./statsRequest'", JSON.stringify(dataURL(compile('./src/app/lib/statsRequest.ts'))));
const { createRunReadRequests } = await import(dataURL(code));
const originalFetch = globalThis.fetch;
try {
  const calls = [];
  globalThis.fetch = (path, options) => new Promise(resolve => calls.push({ path, signal: options.signal, resolve: data => resolve({ ok: true, json: async () => data }) }));
  const scope = createRunReadRequests();
  const first = scope.read('/run/a');
  assert.equal(scope.read('/run/a'), first, 'overlapping recovery and polling share one request');
  await Promise.resolve();
  assert.equal(calls.length, 1);
  const rejectFirst = assert.rejects(first, { name: 'AbortError' });
  scope.cancel();
  const latest = scope.read('/run/a');
  await rejectFirst;
  await Promise.resolve();
  assert.equal(calls[0].signal.aborted, true);
  calls[0].resolve({ obsolete: true });
  await Promise.resolve();
  assert.equal(scope.read('/run/a'), latest, 'old cleanup must not remove new request for same URL');
  calls[1].resolve({ fresh: true });
  assert.deepEqual(await latest, { fresh: true });
  const refresh = scope.read('/run/a');
  await Promise.resolve();
  assert.equal(calls.length, 3, 'completed responses must not be cached across refreshes');
  calls[2].resolve({ fresh: 'refreshed' });
  assert.deepEqual(await refresh, { fresh: 'refreshed' });
  console.log('Run read deduplication, cancellation, ignored abort and same-URL reselection passed.');
} finally { globalThis.fetch = originalFetch; }
