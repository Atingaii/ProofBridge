import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';

const code = ts.transpileModule(readFileSync(new URL('./src/app/lib/automationSetup.ts', import.meta.url), 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
}).outputText;
const { automationSetupPrompt } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
for (const language of ['zh', 'en']) {
  for (const client of ['codex', 'claude']) {
    const config = client === 'codex' ? '[mcp_servers.proofbridge]\nhttp_headers = { Authorization = "Bearer fixture-personal-pat" }' : '{"mcpServers":{"proofbridge":{"headers":{"Authorization":"Bearer fixture-personal-pat"}}}}';
    const installCommand = `sh /tmp/fixture-install --client ${client} --hub https://fixture.invalid`;
    const prompt = automationSetupPrompt({ client, language, config, installCommand });
    assert.ok(prompt.includes(config), 'setup must carry the exact working configuration');
    assert.ok(prompt.includes(installCommand), 'setup must carry the matching Hub installer');
    assert.ok(prompt.includes('proofbridge_get_context') && prompt.includes('proofbridge_list_endpoints'));
    assert.ok(prompt.includes(client === 'codex' ? '```toml' : '```json'));
    assert.match(prompt, language === 'zh' ? /不发起证明任务/ : /Do not start a proof task/);
    assert.match(prompt, language === 'zh' ? /最多进行一次诊断性重试/ : /at most one diagnostic retry/);
    assert.match(prompt, language === 'zh' ? /回显时隐去所有凭据/ : /Keep all credentials redacted/);
    assert.match(prompt, language === 'zh' ? /待完成/ : /pending/);
    assert.equal(automationSetupPrompt({ client, language, config: '', installCommand }), '');
    assert.equal(automationSetupPrompt({ client, language, config, installCommand: ' ' }), '');
  }
}
console.log('Automation setup prompts passed for both clients/languages, complete inputs and bounded read-only completion.');
