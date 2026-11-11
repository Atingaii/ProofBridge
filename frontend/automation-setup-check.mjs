import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
const code = ts.transpileModule(readFileSync(new URL('./src/app/lib/automationSetup.ts', import.meta.url), 'utf8'), {
 compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 },
}).outputText;
const { automationSetupPrompt } = await import(`data:text/javascript;base64,${Buffer.from(code).toString('base64')}`);
for (const language of ['zh', 'en']) for (const client of ['codex', 'claude']) {
 const config = client === 'codex' ? '[mcp_servers.proofbridge]\\nurl="http://127.0.0.1:8088/mcp"' : '{"mcpServers":{"proofbridge":{"url":"http://127.0.0.1:8088/mcp"}}}';
 const installCommand = `github-installer --client ${client}`;
 const prompt = automationSetupPrompt({ client, language, config, installCommand });
 assert.ok(prompt.includes(config) && prompt.includes(installCommand));
 assert.ok(prompt.includes('proofbridge_get_context') && prompt.includes('proofbridge_list_endpoints'));
 assert.ok(prompt.includes(client === 'codex' ? '```toml' : '```json'));
 assert.match(prompt, language === 'zh' ? /不发起任务/ : /Do not start tasks/);
 assert.match(prompt, language === 'zh' ? /最多重试一次/ : /retry once/);
 assert.match(prompt, language === 'zh' ? /验证待完成/ : /verification pending/);
 assert.match(prompt, language === 'zh' ? /隐去凭据/ : /redact credentials/);
 assert.match(prompt, /GitHub/);
 assert.match(prompt, /VPN/);
 assert.match(prompt, language === 'zh' ? /仅连接 VPN 不会转发回环地址/ : /VPN alone does not redirect loopback/);
 assert.doesNotMatch(prompt, /8089|local-8088|CA/);
 const remote = automationSetupPrompt({ client, language, config: config.replaceAll('http://127.0.0.1:8088', 'https://hub.example.test'), installCommand });
 assert.doesNotMatch(remote, /SSH|VPN/);
 assert.ok(prompt.replace(config,'').replace(installCommand,'').length < (language === 'zh' ? 400 : 1000));
 assert.equal(automationSetupPrompt({ client, language, config: '', installCommand }), '');
 assert.equal(automationSetupPrompt({ client, language, config, installCommand: ' ' }), '');
}
console.log('Concise setup passed for both clients and languages: exact configs, GitHub installer, two read tools, no deployment boilerplate.');
