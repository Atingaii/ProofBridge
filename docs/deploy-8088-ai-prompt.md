# 复现本机 8088 的 AI 部署提示词

复制下面整段到目标服务器的 AI 客户端：

```text
请在这台服务器部署 https://github.com/Atingaii/ProofBridge 的
 proofbridge-8088-20261111 版本分支，不使用 main/latest。
先读 AGENTS.md、docs/deployment-8088.md 与 docs/features/reproducible-8088-deployment.md。
记录当前完整 commit，后续复现固定此 commit。
按文档安装兼容的 Go、Node、Caddy，npm ci 后运行 make frontend-8088，
构建当前服务器架构的 Hub 与端点下载文件。部署到 /opt/proofbridge，使用
configs/replica.yaml.example、configs/replica.env.example、deploy/systemd-hub-8088.service。
用 deploy/Caddyfile 让入口监听 8088，上游仅监听回环 18089。
将 PROOFBRIDGE_SITE_ADDRESS、PROOFBRIDGE_UPSTREAM 配置在 Caddy 服务环境，
将 AUTOMATION_PUBLIC_URL 设置为客户端真正可达的本站 HTTPS 地址（含实际端口）。
内网证书需要让浏览器、Codex、Claude 信任 CA，不关闭 TLS 校验。
缺域名时先检查现有证书/VPN，再向我确认域名或内网 CA 方案，不擅自改成客户端回环地址。
生成独立 JWT 与管理员密码，凭据写私有文件。不要复制源机数据库、账号或 CLI 登录。
保持 GitHub Skill 的 proofbridge-skill-v1.1.0 固定版本。
本机 Bridge 只在工作目录与现有 CLI 登录明确时配置；否则说明端点接入步骤。
检查健康、登录、页面资源、动态编排、官方 Codex 模型选择、原生问答、
Bridge 下载与安装脚本、MCP 配置及提示词、结果链接均正确。
实际调用 MCP 的 get_context 和 list_endpoints；模型任务需要真实 Bridge 和可用模型，
未通过的项明确列为待完成，不把工具连通说成模型执行成功。
验证新服务后再切流量；升级前备份数据库、JWT、配置和二进制并给出回滚步骤。
最后报告完整 commit、访问地址、凭据文件位置、systemd 命令与逐项验收结果，隐去密钥。
```

步骤与命令见[部署手册](deployment-8088.md)。这是功能复现版本；目标服务器的账户、端点、模型目录和历史由自身配置决定。
