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
  if (language === 'en') return `Connect this ${name} client to ProofBridge (also called Codex Bridge) using the configuration below, and install its companion proofbridge Skill.

1. Inspect this client's supported MCP configuration or CLI help. Merge only the proofbridge server into my personal client configuration; preserve other servers, login settings, model settings and project files. Treat the included MCP Token as a credential: save it only in the personal configuration and redact it from command output, logs and your final report.
2. Run the Skill install command below. The installer and all Skill files come from the same fixed version in the official Atingaii/ProofBridge GitHub repository; they are not downloaded from the Hub. Do not send the MCP Token to GitHub. Read the installed proofbridge Skill. If installation finds an unmanaged directory, a symbolic link or another conflict, report it rather than bypassing the installer's checks.
3. Load the MCP server and Skill using the client's supported procedure. If a restart or a new session is required and cannot be done here, explain the remaining step and mark connection verification as pending.
4. Once tools are available, make only these read-only checks: proofbridge_get_context and proofbridge_list_endpoints. Report MCP connectivity, Skill installation/loading, account and available endpoints separately. Claim Skill loading only when you have observable evidence.
5. Finish after these checks. Do not start a proof task, change model settings or approval controls, or edit the project. If a step fails, make at most one diagnostic retry, then report the specific blocker and next step. If Skill downloads fail, check access to GitHub. MCP still connects to the configured Hub: check network/VPN access without inventing another server address. If its HTTPS certificate is untrusted (for example, curl error 60), report that the client must trust the lab CA or the administrator must use a trusted certificate. Do not use -k/--insecure or disable TLS verification. Installing the Skill does not establish MCP connectivity.

Personal MCP configuration for ${name}:
\`\`\`${format}
${config.trim()}
\`\`\`

Companion Skill install command:
\`\`\`sh
${installCommand.trim()}
\`\`\`

Return a brief result with completed steps and any action still needed. Keep all credentials redacted.`;

  return `请把当前 ${name} 客户端接入 ProofBridge（也叫 Codex Bridge），并安装配套的 proofbridge Skill。下面已包含我的完整 MCP 配置与安装命令，请直接完成接入和只读验证。

1. 先检查当前客户端支持的 MCP 配置方式或命令行帮助。把 proofbridge 服务器合并到我的个人客户端配置中，保留已有的其他服务器、登录信息、模型设置和项目文件。配置中的 MCP Token 是个人凭据，只保存到个人配置中；命令输出、日志和最终报告均须隐去它。
2. 执行下方 Skill 安装命令。安装脚本和全部 Skill 文件均来自官方 Atingaii/ProofBridge GitHub 仓库的同一固定版本，不从 Hub 下载。不要向 GitHub 发送 MCP Token。阅读安装后的 proofbridge Skill。遇到非受管目录、符号链接或配置冲突时，报告具体问题，保留安装器的安全检查。
3. 按客户端支持的方式加载 MCP 和 Skill。若必须重启客户端或开启新会话，且当前无法执行，请说明剩余操作，并把联通验证标记为待完成。
4. 工具可用后，只调用 proofbridge_get_context 和 proofbridge_list_endpoints 两项只读工具。分别回显 MCP 是否连通、Skill 是否安装及加载、当前账户和可用执行端点。仅在观察到加载证据后，才报告 Skill 已加载。
5. 完成上述检查后停止。接入过程不发起证明任务、不修改模型或审批设置、不编辑项目。某步失败时最多进行一次诊断性重试，然后报告阻碍与下一步。Skill 下载失败时检查 GitHub 访问。MCP 仍连接配置中的 Hub；Hub 不可达时检查网络或 VPN，保留服务器地址。若 HTTPS 证书不受信任（例如 curl 错误 60），说明客户端需要信任实验室 CA，或由管理员配置受信任证书。不要使用 -k/--insecure 或关闭 TLS 校验。Skill 安装成功不等于 MCP 已连通。

${name} 的个人 MCP 配置：
\`\`\`${format}
${config.trim()}
\`\`\`

配套 Skill 安装命令：
\`\`\`sh
${installCommand.trim()}
\`\`\`

最后用简短结果说明已完成的步骤和仍需执行的操作，回显时隐去所有凭据。`;
}
