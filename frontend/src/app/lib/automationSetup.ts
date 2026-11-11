import type { Language } from './types';

export function automationSetupPrompt({ client, language, config, installCommand }: {
  client: 'codex' | 'claude';
  language: Language;
  config: string;
  installCommand: string;
}): string {
  if (!config.trim() || !installCommand.trim()) return '';
  const name = client === 'codex' ? 'Codex' : 'Claude Code';
  const format = client === 'codex' ? 'toml' : 'json';
  const intro = language === 'en'
    ? `Connect this ${name} client to ProofBridge and install its Skill from GitHub.

Merge the configuration below into my personal settings; preserve other settings and keep the Token private. Run the installer, retain its safety checks, and read and load the Skill.
Only call proofbridge_get_context and proofbridge_list_endpoints. Do not start tasks or edit the project. On failure, retry once, then stop. If a restart is needed, mark verification pending.
Briefly report connectivity, Skill installation/loading, account and endpoints; redact credentials.`
    : `请将当前 ${name} 接入 ProofBridge，并从 GitHub 安装配套 Skill。

将以下配置合并到个人设置，保留其他配置并隐去 Token。执行安装命令，保留安装器安全检查，阅读并加载 Skill。
仅调用 proofbridge_get_context 和 proofbridge_list_endpoints，不发起任务、不编辑项目。失败最多重试一次；需要重启时标记验证待完成。
最后简报连接、Skill 安装及加载、账户和端点情况，隐去凭据。`;
  return `${intro}

\`\`\`${format}
${config.trim()}
\`\`\`

\`\`\`sh
${installCommand.trim()}
\`\`\``;
}
