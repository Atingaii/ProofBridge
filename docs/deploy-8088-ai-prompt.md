# 8088 内网部署的 AI 提示词

复制下面整段到目标服务器的 AI；本提示词部署网页 IP:8088 + MCP 127.0.0.1:8088。

```text
请部署 https://github.com/Atingaii/ProofBridge 的 main 分支，
记录完整 commit，不使用未固定的 latest 产物。先读 AGENTS.md 与 docs/deployment-8088.md。
目标是复现当前内网方式：网页 http://目标服务器内网IP:8088，
MCP http://127.0.0.1:8088/mcp，Skill 固定 GitHub proofbridge-skill-v1.1.0。
检查服务器实际内网/VPN IP；不默认要求域名、HTTPS 或证书。
若 8088 已有服务，先识别并保留，不覆盖或重启既有服务；用独立目录和候选端口验证。
按文档安装 Go、Node、Caddy，npm ci 后 make frontend-8088，构建 Hub 与需要的平台下载包。
安装到 /opt/proofbridge，使用 replica 配置和 systemd-hub-8088.service。
生成独立 JWT 与管理员密码，写权限600的私有配置，不回显、不复制源机数据库与凭据。
Hub 监听127.0.0.1:18089，Caddy进程配置
PROOFBRIDGE_SITE_ADDRESS=http://:8088、PROOFBRIDGE_UPSTREAM=127.0.0.1:18089。
Hub 配置 HUB_COOKIE_SECURE=false、AUTOMATION_PUBLIC_URL=http://实际内网IP:8088、
AUTOMATION_MCP_URL=http://127.0.0.1:8088；两项均填写基址，不加/mcp。
网页结果链接保留内网IP，复制的Codex/Claude MCP配置使用127.0.0.1，
不通过代理重写JSON，不修改GitHub Skill固定下载源。
如果接入的Codex/Claude在其他电脑，解释并提供SSH本地端口转发，
不能把那台电脑自己的127.0.0.1说成服务器；同机运行则无需转发。
在新站创建端点令牌、接入Bridge和明确的工作目录，复用目标端已有CLI登录。
检查登录、健康、动态编排、官方模型选择、原生问答、安装/下载与结果链接，
复制新站MCP提示词并实际调用get_context和list_endpoints，不发起无关任务。
真实模型任务是否通过、Skill是否真正加载、邮件是否实际发送分别报告。
最终给出完整commit、网页访问地址、MCP配置、凭据文件位置、服务命令、验收及回滚步骤。
不要影响源服务器原本正在使用的8088。
```

配置和端口转发详见[部署手册](deployment-8088.md)。
