# Main 与实验室命令行部署集成验收（2026-11-11）

## 集成范围

正常合并 main 的 d112176 与部署版 0080f53，不覆盖主线文章及 CI，不重写任一侧历史。
main 的新文章使用 /blog/orchestration-design；App 保留动态编排、原生问答与认证恢复。
网页、结果和邮件基址继续使用 AUTOMATION_PUBLIC_URL，个人 MCP 使用可选 AUTOMATION_MCP_URL。
README 英/中说明当前仓库能力、main 构建与命令行部署，不展示 AI 部署文档链接。
Caddy 新增独立环境模板与 systemd drop-in；部署手册包含 VPN/SSH、初始化、服务与升级命令。

## 已验证

- npm test 通过；两客户端/两语言提示词保留原配置、固定 GitHub 安装命令和两项只读检查。
- make frontend-8088 构建通过；CGO_ENABLED=0 Go 二进制构建通过。
- go test ./... 全量通过，包括独立 MCP URL/结果链接、SDK 集成、配置优先级与静态路由回归。
- 发现并修复真实资源目录与文章 SPA 路由冲突：无尾斜杠和尾斜杠都返回应用，SVG 保持静态图片。
- 实际候选 Hub 的浏览器夹具：动态指令 1280/390、官方模型、原生问答、认证恢复均通过。
- 个人设置夹具：两客户端与两语言、Token 生命周期、复制、邮件兼容、手机布局、回环网络提示通过。
- 主线文章：两种 URL、4 张图、1440/768/390/320 布局、键盘阶段切换、锚点及受保护统计页通过。
- 临时 Caddy/Hub/SQLite：真实 LAN HTTP 登录、回环 MCP 模板、两项 MCP JSON-RPC 只读调用通过；
  GitHub Skill 固定 tag，安装脚本为 LAN 地址，二进制下载 SHA 匹配。
- SSH -G 验证转发绑定、ExitOnForwardFailure 与保活参数；没有登录真实 VPN/SSH 或更改 SSH 信任。
- systemd-analyze verify 使用同内容临时 unit，仅将安装路径替换为现存候选路径；模板解析通过。
- make doc-lint：0 错误，8 个既有代码锚点警告；git diff --check 通过。

## 边界

浏览器 API/WS 业务使用已有夹具，注入断言仅在测试浏览器 bypassCSP；交付 Hub CSP 未放宽。
Caddy/SQLite/HTTP MCP 检查使用真实候选进程。没有提交真实模型任务、迁移用户数据或验证外部邮件。
远程用户仍需其 VPN/SSH 权限或受信 HTTPS；127.0.0.1 的作用域由 CLI 运行位置决定。
原 8088 入口进程 PID 不变，健康返回原版本；原 Hub/Bridge、配置和数据库未替换或重启。
