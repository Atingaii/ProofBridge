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
    const installCommand = `sh /tmp/fixture-github-install --client ${client}`;
    const prompt = automationSetupPrompt({ client, language, config, installCommand });
    assert.ok(prompt.includes(config), 'setup must carry the exact working configuration');
    assert.ok(prompt.includes(installCommand), 'setup must carry the matching GitHub installer');
    assert.ok(prompt.includes('proofbridge_get_context') && prompt.includes('proofbridge_list_endpoints'));
    assert.ok(prompt.includes(client === 'codex' ? '```toml' : '```json'));
    assert.match(prompt, language === 'zh' ? /不发起证明任务/ : /Do not start a proof task/);
    assert.match(prompt, language === 'zh' ? /最多进行一次诊断性重试/ : /at most one diagnostic retry/);
    assert.match(prompt, language === 'zh' ? /回显时隐去所有凭据/ : /Keep all credentials redacted/);
    assert.match(prompt, language === 'zh' ? /待完成/ : /pending/);
    assert.match(prompt, /Atingaii\/ProofBridge/);
    assert.match(prompt, language === 'zh' ? /同一固定版本，不从 Hub 下载/ : /same fixed version.*not downloaded from the Hub/);
    assert.match(prompt, language === 'zh' ? /不要向 GitHub 发送 MCP Token/ : /Do not send the MCP Token to GitHub/);
    assert.match(prompt, language === 'zh' ? /信任实验室 CA/ : /trust the lab CA/);
    assert.match(prompt, language === 'zh' ? /不要使用 -k\/--insecure/ : /Do not use -k\/--insecure/);
    assert.match(prompt, language === 'zh' ? /Skill 安装成功不等于 MCP 已连通/ : /Installing the Skill does not establish MCP connectivity/);
    assert.equal(automationSetupPrompt({ client, language, config: '', installCommand }), '');
    assert.equal(automationSetupPrompt({ client, language, config, installCommand: ' ' }), '');
  }
}
console.log('Automation setup prompts passed for both clients/languages, complete inputs and bounded read-only completion.');
