# 8088 可复现版本验收（2026-11-11）

发布版本分支：proofbridge-8088-20261111（下载后记录完整 commit 并据此固定版本）。部署与 AI 提示词见[部署手册](../deployment-8088.md)。

## 内网回环方案修订验收

后续修订按用户现用方式设置：网页 HTTP 私有 IP:8088、MCP HTTP 127.0.0.1:8088/mcp。
新增 AUTOMATION_MCP_URL 独立配置；下面的 HTTPS 记录是初版验收，当前默认步骤以[部署手册](../deployment-8088.md)为准。

- Go 全量测试与候选二进制构建通过；新增测试覆盖 env 文件/进程优先级、两客户端配置、
  LAN 结果链接、默认 HTTPS 兼容、非法 override 与地址边界。
- Go MCP SDK 通过真实 loopback httptest listener 调用 get_context/list_endpoints，未执行模型任务。
- Caddy + 独立 Hub/SQLite 在临时 14289/14488 端口实测：真实 LAN IP HTTP 登录，
  同一入口的回环 MCP 配置与两项 JSON-RPC 只读调用成功；安装脚本生成 LAN 下载地址，下载 SHA 匹配。
- 原 8088 入口 PID 保持不变，健康正常；未修改其配置、二进制、数据库或运行中的 Bridge。
- 文档检查 0 错误、8 个既有锚点警告；前端未变更，沿用下列已通过的浏览器回归。
- 未验收其他电脑的实际 SSH 转发或真实模型/外部邮件；文档提供转发步骤并说明同机条件。

## 初版来源与范围

源机后端源码基线为 e375f57748b0292c647646189e6b080671fbc157；该源码工作树无额外后端补丁。
发布基于 e3d6efd0bc7b7cf25c772e2db02a096c0e309af9，保留其 GitHub Skill 分发修复，
并收录原动态前端的简短提示词、HTTP 可用 UUID 与焦点回归同步。
固定 VITE_LEADING=true 与 VITE_OFFICIAL_CODEX_MODELS=true 后重建嵌入 UI。
代理改用 Caddy，无源机 IP、私人静态路径或 MCP JSON 重写。

## 已通过

- frontend npm test，包括两客户端/两语言接入提示词、UUID 格式与 4096 次随机生成检查。
- make frontend-8088，Go 全量 go test ./... 与 CGO_ENABLED=0 二进制构建。
- 动态追加指令浏览器夹具：1280px 和 390px，排队/立即发送、模糊响应重试、连续性与手机焦点。
- 官方模型选择浏览器夹具：独立槽位、目录隔离、默认值、续跑、旧 Bridge 与迟到响应。
- 原生问答浏览器夹具：参与者身份、多题、自定义答案、草稿、回执恢复、键盘及手机布局。
- make doc-lint：0 错误、8 个已有代码锚点警告；git diff --check 通过。
- Caddy 实际模板 validate；临时 Hub、独立 SQLite 与临时 Caddy 通过 HTTPS 健康和登录。
- HTTPS 客户端信任测试 CA，并保留证书校验；MCP 模板均指向测试站点 /mcp。
- Skill 安装命令均指向 GitHub proofbridge-skill-v1.1.0；安装脚本中的下载链接指向测试站点。
- Linux amd64 下载内容与候选 Hub 二进制 SHA-256 相同；页面保留 CSP。
- 通过实际 HTTPS /mcp 发送 JSON-RPC tools/call，get_context 和 list_endpoints 均返回成功。

## 证据边界

浏览器业务 API/WS 使用现有夹具；因断言使用 eval，仅夹具浏览器上下文设置 bypassCSP，
候选 Hub 与交付页面的 CSP 没有放宽。HTTPS 登录和 MCP 检查使用真实候选 Hub/SQLite，
不使用夹具，且独立检查 CSP 存在。
临时 Caddy 关闭 80 端口自动重定向与系统信任库安装，避免测试机器的低端口权限限制；
交付配置保持正常自动 HTTPS，生产 Caddy 服务需有对应端口权限与证书条件。

未在此次验收启动真实模型任务、迁移源机数据或验证外部邮件送达。
本次 MCP HTTP 检查不等于真实 Codex/Claude 原生客户端完整加载 Skill，也不等于证明任务通过。
目标服务器仍需用实际 Bridge、CLI 登录和模型供应商完成最终验收。
只验收当前 Linux amd64 下载；其他平台按目标需求构建并分别检查。
源机正在运行的入口、Hub 和 Bridge 均未替换或重启。
