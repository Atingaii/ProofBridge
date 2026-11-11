import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { webcrypto } from 'node:crypto';
import vm from 'node:vm';
import ts from 'typescript';
const source=ts.createSourceFile('utils.ts',readFileSync(new URL('./src/app/lib/utils.ts',import.meta.url),'utf8'),ts.ScriptTarget.Latest,true);
const fn=source.statements.find(n=>ts.isFunctionDeclaration(n)&&n.name?.text==='newUUID');
assert.ok(fn,'HTTP deployment needs a UUID generator that works without crypto.randomUUID');
const code=ts.transpileModule(ts.createPrinter().printNode(ts.EmitHint.Unspecified,fn,source),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText;
const load=crypto=>{const ctx={window:{crypto},exports:{}};vm.runInNewContext(code,ctx);return ctx.exports.newUUID;};
const uuid='12345678-1234-4234-8234-123456789abc';assert.equal(load({randomUUID:()=>uuid})(),uuid);
const fallback=load({getRandomValues:webcrypto.getRandomValues.bind(webcrypto)}),ids=new Set();
for(let i=0;i<4096;i++){const id=fallback();assert.match(id,/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);ids.add(id);}
assert.equal(ids.size,4096);
console.log('UUID generation: native API and HTTP-compatible secure fallback preserve RFC 4122 v4 format.');
