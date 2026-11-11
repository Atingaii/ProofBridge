# 邮件投递方式与本地预览

## Goals

用户必须能区分真实 SMTP 外发与本地收件预览。使用 Mailpit 等不转发的收件器时，即使测试邮件的收件地址是真实邮箱，SMTP 接受也只表示本地预览成功，不表示外部收件箱已收到。

## Non-Goals

不自动注册外部邮件账号、不借用 Dot 或客户端内部凭据、不改用户邮箱或通知偏好；不根据 loopback 地址猜测投递类型，因为本地 SMTP relay 也可能真实外发。测试邮件仍只使用本人已保存邮箱及固定正文。

## Data And Protocol Impact

- 增加显式 `SMTP_DELIVERY_MODE` / `automation.smtp.delivery_mode`：默认 `smtp` 保持兼容；`preview` 用于 Mailpit 等不外发的收件器。未知值使邮件服务不可用，不静默回退。
- 保留 `mailReady` 的传输就绪语义及现有 outbox、SMTP 接受回执；新增只读 `mailDeliveryMode=disabled|smtp|preview` 到个人接入配置和 MCP `get_context`。
- `POST /api/automation/mail/test` 成功响应增加 `deliveryMode`。`preview` 返回明确的本地预览结果，不声称真实邮箱收件；普通 SMTP 仍只确认接受，不保证收件箱送达。
- HTTP/MCP 通知列表增加当前 `mailDeliveryMode`、`deliveryModeScope=current-transport` 与 `sentStatusMeaning=smtp-accepted`。旧通知不存历史投递模式，不用当前配置猜测它；`sent` 始终只表示当时的 SMTP 服务接受。
- 不增加数据库、任意收件人接口或共享预览收件箱链接。客户端必须按服务端模式显示关键投递状态，缺少新增字段时兼容旧 Hub。

## Implementation Steps

1. 读取脱敏的当前配置、个人偏好与 Mailpit 收件元数据，确认实际故障边界。
2. 增加显式投递模式和安全只读投影，复用同一状态判断。
3. 更新固定测试邮件结果与前端状态，保留归属、限速和错误脱敏。
4. 同步环境变量模板、部署说明和测试；本机部署时由协调者标记当前 Mailpit 为 `preview`。

## Exit Gates

- 默认/显式 SMTP、本地预览、禁用和非法模式有回归；不将 localhost 自动视为预览。
- 本人测试邮件在真实本地 SMTP 被接受，响应明确 `preview`，且外人/任意正文/限速/失败门禁保持。
- 接入配置与 MCP 状态模式一致，不返回 SMTP 主机、用户名、密码或其他账户地址。
- 当前 Mailpit 测试邮件实收与外部收件能力分开报告。没有真实外发配置时不得宣称已修好外部收件。

## Reviewer Q&A

为什么仍保留本地预览？本机和自动化验证需要不向外界发信的可观察收件器；它不能冒充生产外发服务。

怎样启用真实邮件？由部署管理员填写标准 SMTP 主机、认证及已验证发件人，选择 `smtp` 后重启 Hub，再由用户测试自己的邮箱。完整变量见 [开发流程](../dev-workflow.md#environment-variables)。

## Deployment Portability

同一仓库提交必须能够在另一台服务器构建并运行相同的 MCP、个人配置页、接入提示词、Skill 安装器和 SMTP outbox；这些能力不能依赖本机未入库的启动脚本、桌面路径或当前聊天的插件。部署使用现有单 Hub 二进制，保留默认仅部署网站的范围，不增加 Bridge 安装包构建要求。

`configs/hub.env.example` 提供生产环境的完整启动占位模板；`deploy/systemd-hub.service` 可选读取固定的私有 `configs/hub.env`，缺少该文件时保留旧 YAML/进程环境行为。Docker 显式用 `--env-file` 注入相同变量。MCP 配置、Skill 安装地址和结果链接由 `AUTOMATION_PUBLIC_URL` 生成，换主机后不用修改源码。完整操作只维护在[部署指南](../deployment.md#repository-deployment-parity)。

代码与构建产物可以入库；环境文件中的 SMTP 凭据、JWT secret 和运行数据库不入库。原地升级或迁移账户时必须保留 SQLite 与原 JWT secret，因为个人 MCP token 和模型 vault 都依赖该密钥。新增服务器首次运行使用新的私有配置与数据库；复制代码不会自动复制旧账号、已连接机器或任务历史。

Docker 运行保持 distroless nonroot。镜像内的持久化 `data/` 预先归 UID/GID 65532，首次挂载全新 named volume 时继承可写目录，而不是要求用户手动进入容器修权限。首次初始化用同一镜像的 `user` 子命令读取同一私有 env 和数据卷；Hub 启动本身不创建账号。更新复用原卷和 JWT，不能再次运行首次建用户命令来隐式改密码。绑定宿主机已有目录时，目录权限仍由宿主管理员提供。构建上下文排除填实的 `configs/*.env`。

退出条件：用不含本机 `data/`、私有配置和 helper 的干净仓库副本构建 Hub，在独立端口/全新测试库中确认嵌入新页面与 Skill 资产可用、两个客户端配置和安装命令均引用配置的 HTTPS Hub、MCP 能读取账户上下文，环境重载后 token 和账户保持可用。SMTP 外部收件与 Linux systemd 实际运行仍须在目标环境单独验收，不能由本机隔离测试代替。

Docker 另以全新 named volume 验证 nonroot 下首次建用户、Hub 健康、MCP 和重启后 SQLite/PAT 连续性；仅检查 Dockerfile 文本不能代替容器运行。测试仅操作独立命名的容器、卷、端口和镜像，不复用生产资源，不发外部邮件。
