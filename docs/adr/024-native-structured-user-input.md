# ADR 024: 原生结构化用户问答透传

Status: Accepted — 用户已批准实施；适配器经真实往返验证，版本范围按官方最低协议要求开放。

## Context

实验站已有权限审批传输，但原生 CLI 的需求澄清和方案选择需要问题与答案结构，不能由 accept/decline/cancel 表达。用户已选择保持原生阻塞／非阻塞语义，并希望信任模式仍可提问。

当前入口：`internal/bridge/appserver_runner.go:readEvents`、`internal/bridge/orchestration_claude.go:prepareClaudeApprovalServer`、`internal/hub/orchestration.go:handleOrchestrationWS`。

## Decision

1. 在现有 Envelope 与反向 WebSocket 连接中加入独立的用户问答协议；执行审批维持现有规则。
2. Bridge 是原生调用存活与答案投递的权威，Hub 执行身份／归属检查及路由，浏览器呈现原生问题。使用全局唯一交互 ID 绑定执行实例与原生请求。
3. 保留原生等待语义，不强制暂停整个 run。阻塞式 `item/tool/requestUserInput` 的答案写回原 JSON-RPC 请求，并在同一 native turn 继续；Codex `request_user_input_async` 产生的 `agentMessage(delivery=async, questions)` 已完成原工具调用，答案须按原生格式携带 `questionItemId`，在同一 thread 的活动 turn 使用 `turn/steer`、空闲时使用 `turn/start`。这是 Codex 原生异步回答机制，不得新建实验站 run、换 thread 或把答案作为普通编排任务广播。事件读循环不得同步等待用户答案。
4. 信任模式保留问答通道；Claude 通道安装与 permission mode 分离；Codex 仅在验证支持的实验站进程上按需配置默认模式提问，不更改用户全局配置或强制切换 Plan 模式。
5. 首期使用活跃进程中的临时状态与重连同步，不增加 SQLite 表，不承诺原生进程死亡后的挂起调用恢复。
6. 对每种 CLI 适配器和权限模式验证原生往返；已验证适配器按官方接口最低版本及以上开放，后续版本不逐个锁定，依据见功能设计的版本范围修复；Claude 优先现有 MCP permission host。若需改用 stdio 控制协议，先记录验证证据并复核设计与审批兼容性，不能悄悄替换。没有真实往返证据的适配器保持能力不可用。
7. 问题生命周期与答案投递凭据分别记录。原生请求关闭不可逆；完整写入只证明答案已送到 CLI 通信通道，不证明模型已处理。关闭与确认乱序时，Bridge 在有界内存窗口保留无正文的 terminal/receipt，支持原提交查询与终态 run 的只读同步。
8. Bridge 的 WebSocket 读取循环只调度答复，不同步执行原生 I/O。每个原生 client 串行写入，调度队列和写入预算有界。正常问题关闭撤销尚未开始的写资格，不终止健康 CLI；已开始的写入由独立传输预算管理，完整写出后记录 delivered。部分写入或故障为 unknown，不能自动重投。只有 Bridge 原子确认旧 submission 未到且封禁其 ID、请求仍可回答时，才允许用新 ID 明确改答。
9. v3 同时协商 Hub、Bridge 和浏览器能力。浏览器创建／继续请求携带版本；Hub 在 start payload 写入已协商版本；Bridge 缺少明确 v3 时不安装问答 host。浏览器只在 session storage 保存未决提交身份，刷新后由 Bridge 核对并封禁未收到的旧 ID，不保存答案。

## Alternatives and consequences

- 扩展审批 decision 字段：丢失多题和自定义答案语义，不采用。
- 解析普通文本为问题并发新 prompt：不保持原工具结果与会话执行语义，不采用。
- 把异步答案当成同步 JSON-RPC 回调：原工具已返回 `accepted`，回调不存在，不采用。
- 强制所有问题阻塞：与用户选择 A 不符，不采用。
- 持久化完整问题历史与进程恢复：超出本期目标，延期；刷新仍须恢复存活调用。

对旧 Bridge／不支持的 CLI 明确显示能力限制，禁止静默吞掉请求。版本兼容策略采用最低版本，不以历史实测版本作精确白名单；真实往返验证用于适配器发布验收，不声称所有兼容版本均已实测。

这使一次用户提交在浏览器断线后可能暂时处于“待确认”，直到 Bridge 返回权威凭据或缓存窗口结束；不为了降低等待感而自动重发。真正的原生 stdin 故障可能要求终止对应 CLI 进程以解除阻塞；正常问题 resolved 不触发该路径。兼容与具体窗口见[功能设计](../features/native-structured-user-input.md)、[加固方案](../features/native-structured-user-input-hardening.md)和[二次修复方案](../features/native-structured-user-input-regression-plan.md)。

## Exit gates

两种 CLI × 审批／信任模式通过问答往返；并发隔离、原生等待语义、刷新恢复、取消／失效、重复提交与现有审批回归通过。完整流程、契约、当前验证矩阵和验收见 [功能设计](../features/native-structured-user-input.md)。


## Addendum: correlated sync rejection (2026-10-03)

`internal/protocol/envelope.go:UserInputStatusPayload` gains an optional `syncId`
for Hub-to-browser query rejection only. This additive v3 repair keeps the
existing frame type: `source=hub`, `phase=route`, `status=rejected`, `revision=0`,
empty `requestId`, matching `runId/syncId`, and no receipt. The browser decodes
this separately from request status and never treats it as delivery evidence.
Regular request statuses still require a request ID; Bridge adapters and answer
payloads do not change. This avoids fabricating a request identity merely to pass
validation. Full error reporting requires the repaired Hub and frontend together;
an older frontend conservatively ignores the new query rejection.

See the [query recovery contract](../features/native-structured-user-input.md#查询故障与送达提示修复2026-10-03)
for user-visible behavior and regression gates.
