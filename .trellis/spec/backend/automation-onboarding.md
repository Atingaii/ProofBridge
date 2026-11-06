# 自动化接入与验收契约

## 1. Scope / Trigger

修改运行详情配置、个人测试邮件、环境文件、验证方式或原生 Skill 探针时适用。
产品流程见[接入设计](../../../docs/features/mcp-onboarding-and-mail-setup.md)，
调度、通知和受管 worker 隔离见[MCP 功能](../../../docs/features/mcp-skill-task-notifications.md)，
验证方式兼容见[ADR-030](../../../docs/adr/030-agent-only-verification.md)。
保留 Hub 统一鉴权与持久化、反向 Bridge、同 run/session 连续性，不增加第二套调度器。

## 2. Signatures

- `GET /api/orchestrations/{runID}` → `{run, executionConfiguration, submissionId}`；
  入口 `internal/hub/orchestration.go:handleGetOrchestration`，投影
  `internal/hub/orchestration_configuration.go:orchestrationExecutionConfiguration`。
- `POST /api/automation/mail/test`，调用 body 为 `{}` →
  `{status:"accepted", smtpAccepted:true, message}`；入口
  `internal/hub/automation_mail_test_send.go:handleAutomationMailTest`。
- `CODEX_BRIDGE_ENV_FILE=/absolute/private/mail.env ./bin/codex-bridge hub`；
  解析 `internal/config/env_file.go:configurationEnvironment`，应用
  `internal/config/load.go:Load`。完整变量和模板只维护在
  [开发流程](../../../docs/dev-workflow.md#environment-variables)及
  [SMTP 设置](../../../docs/dev-workflow.md#smtp-setup-and-a-personal-delivery-test)。
- 新建/继续请求的 `verificationMode` 省略或 `agent` 为支持值；
  `internal/protocol/envelope.go:NormalizeVerificationMode` 保留其他值，供 Hub/Bridge 拒绝。

## 3. Contracts

- **配置投影**：先以 uid 查询 run，再读取本人最新提交的已保存快照；无快照时从运行元数据和 worker 绑定安全重建。
  顶层只返回执行选择字段，worker 只返回 `cli/nativeDefault/presetId/name/officialLogin/model/reasoningEffort`。
  禁止返回凭据、原生 resume 身份、任务文本或整个原始快照。旧运行无绑定时标记
  `nativeDefault:true`，不猜测实际模型或思考强度，也不用新建表单默认值覆盖历史。
- **个人测试邮件**：浏览器登录和 Origin 校验后，仅向当前账户在 Hub 保存的有效邮箱发送固定文案。
  不接受收件人、主题或正文参数；不启动任务，不授权个人 MCP token 管理该接口。
  每 uid 每分钟最多尝试一次，不能用更换 IP 绕过；失败尝试同样计入。
  超时取已配置 SMTP timeout 与 15 秒的较小有效值，未设有效值时为 15 秒。
  `smtpAccepted` 只表示 SMTP 接受，不能写成外部收件箱已收到。
- **环境文件**：仅显式选择才读取，不隐式加载工作目录 `.env`。进程环境优先，包含显式空值；
  文件值不写入 `os.Environ`，不执行 shell、命令替换或变量展开。只解析合法键值/引号/注释；
  文件最多 1 MiB，错误只返回位置和安全原因，不回显值。
- **历史验证方式**：保留数据库原值和历史事件可读性。继续旧运行必须显式选择受支持方式；
  不能把未知值归一化成 `agent`。Hub HTTP/MCP 和 Bridge 收到 payload 时都校验，拒绝后不创建或派发新工作。
- **原生验收**：ProofBridge、Codex Bridge、CodexBridge 使用同一个外层 Skill；保留受管 worker 防递归隔离。
  安装、发现、Skill 加载、MCP 成功调用、终端回显分别留证。自然语言和显式模式分别验收，
  MCP 两项只读工具成功不能代替 Skill 加载 trace。默认测试跳过 opt-in 模型调用；
  超时或推理不可用产生的 skip 不计为通过，缺少加载证据的严格失败不能放宽。

## 4. Validation & Error Matrix

| 条件 | 可观察结果 |
| --- | --- |
| 未登录；个人 MCP token 调用浏览器管理接口 | 401，不发送 |
| run 不属于调用者或不存在 | 404 `NOT_FOUND`，不返回配置 |
| run/配置查询失败 | 500 `STORE_ERROR`，不返回原始错误或秘密 |
| 自动化关闭；测试邮件 Origin 不允许 | 503 `AUTOMATION_DISABLED`；403 `ORIGIN_DENIED` |
| 测试邮件 JSON 非法或带自定义字段 | 400 `BAD_REQUEST`，不发送 |
| SMTP 未就绪；本人邮箱缺失或无效 | 503 `MAIL_NOT_CONFIGURED`；409 `EMAIL_REQUIRED` |
| uid 一分钟内重复测试 | 429 `MAIL_TEST_RATE_LIMIT`，`Retry-After: 60` |
| SMTP 拒绝、连接失败或超时 | 502 `MAIL_TEST_FAILED`；不返回 SMTP 凭据或原始错误 |
| 显式 env 文件缺失、超限、语法非法 | 配置加载失败；错误不包含值 |
| 新请求显式选择不支持的验证方式 | Hub HTTP/MCP 返回 `BAD_VERIFICATION_MODE`，无派发；Bridge 拒绝执行 |
| MCP 读取成功但 Skill trace 缺失 | 原生 Skill 严格测试 FAIL，不能用普通 Go 测试通过覆盖 |

## 5. Good / Base / Bad Cases

- Good：本人详情保留已接受的固定 model/effort；测试邮件使用已保存邮箱；进程中的空 `SMTP_HOST` 覆盖文件，不能意外启用发送。
- Base：旧运行没有 worker 绑定，返回 `nativeDefault:true`；页面明确原生默认未固定，历史仍可阅读。
- Bad：直接返回完整快照、从请求接收 `to`、执行 env 文件、用表单默认值填历史，或静默替换历史验证方式。

## 6. Tests Required

- `internal/hub/orchestration_configuration_test.go`：本人快照的 model/effort 不变，秘密字段不出现；外人 404；旧无绑定为 native default。
- `internal/hub/automation_mail_test_send_test.go`：真实本地 SMTP、两账户地址隔离、固定文案、uid 限速与 IP 绕过、任意字段拒绝、失败非成功、匿名/PAT/Origin 拒绝、无新 run。
- `internal/config/env_file_test.go`：显式加载、进程空值优先、无全局污染、命令文本不求值、语法错误脱敏、无隐式 `.env`。
- `internal/protocol/verification_test.go`、`internal/hub/automation_verification_test.go` 和既有 Hub/Bridge Agent 回归：未知值保留后拒绝，拒绝不派发，旧数据不重写。
- `frontend/mcp-run-detail-browser-check.mjs`、`frontend/automation-settings-browser-check.mjs`：同 run 选择/深链接、独立操作按钮、安全配置、未保存邮箱禁用和发送结果语义。
- `internal/hub/automation_native_skill_test.go:TestAutomationNativeSkillReadOnly`：opt-in 时使用真实客户端，确认必需只读 MCP 成功、无越界调用、终端回复与 Skill trace；分别记录 natural/explicit 的 pass/fail/skip。
  不保存凭据或完整工具正文，不将 provider 超时、skip 或自述加载当作成功证据。

## 7. Wrong vs Correct

```go
// Wrong: raw snapshot may contain credentials or native session identities.
response["executionConfiguration"] = submission.Configuration

// Correct: ownership is checked first; return the explicit execution allowlist.
safe, submissionID, err := s.orchestrationExecutionConfiguration(ctx, uid, ownedRun)
```

Wrong：`tools/call` 成功就宣称自然语言自动加载 Skill；未知 authority 默认为 `agent`。
Correct：分别报告 MCP、Skill trace 和客户端终态；未知 authority 保留后显式拒绝，历史继续要求用户选择支持值。
