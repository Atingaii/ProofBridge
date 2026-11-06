# ProofBridge MCP 接入：实施前基线核对

> 核对日期：2026-11-06。基线：`feat/isolated-cli-profiles-verifier-harness`，提交 `21f1a0b38d0842bd76e3b3cd6fe2c8dd96926907`。
>
> 本文保存实施前基线；后续实现与本地验证见[功能文档](../features/mcp-skill-task-notifications.md)。本文是源码与文档调研，不是实现或运行验收报告。未启动 Hub、Bridge、真实 CLI、邮件服务或 VPN 联调；所有“当前行为”均指本提交的源码检查结果。本文不修改现有功能。

## 1. 核心判断

1. ProofBridge 已有账号隔离、用户接入自己的 Bridge、工作目录发现、账户模型库、多 CLI 编排、任务事件与结论。新增 MCP 应复用这些机制。
2. Hub 部署位置不决定证明执行位置。用户选中的 Bridge 在自己的机器和工作目录中调用 CLI；Hub 不需要持有该项目工作区。依据：`internal/hub/ws_bridge.go:handleBridgeWS`、`internal/bridge/orchestration.go:cwd`。
3. 新用户要自行注册，需要部署方启用现有注册配置。默认示例关闭注册，注册请求目前仅接收 username、password、turnstileToken。账户没有 email 字段。依据：`internal/hub/registration.go:registrationReady`、`internal/hub/registration.go:handleRegister`、`internal/store/store.go:User`。
4. 当前没有面向最终用户的个人 MCP Token、个人中心 MCP 模板、ProofBridge 操作 MCP、跨客户端执行方案/默认偏好或邮件通知基础设施。当前内部 MCP 是 Claude 审批 helper。依据：`internal/hub/server.go:NewServer` 的路由注册、`internal/bridge/claude_approval_mcp.go:handleClaudeApprovalMCPRequest`、`internal/config/config.go:Config`。
5. “已完成运行”不等于“验证通过”。MCP 和邮件应分别报告运行生命周期、验证结论、原因与证据。依据：`internal/hub/task_graph.go:handleTaskGraphEvent`、`internal/protocol/envelope.go:RunConclusion`。
6. MCP 调用重试需要新增顶层提交幂等性。现有运行中的追加指令有 messageId 幂等；新建与终态继续没有对应的外部请求幂等键。依据：`internal/hub/orchestration.go:orchestrationCreateRequest`、`internal/hub/orchestration.go:handleCreateOrchestration`、`internal/hub/orchestration_messages.go:handleActiveOrchestrationMessage`。
7. 邮件不能仅监听任意 Bridge run.end。durable graph 的单个节点也发送此事件；应在 Hub 判定整个提交最终结束并持久化结论后触发。现有写入没有与邮件 outbox 联合的事务。依据：`internal/bridge/orchestration_events.go:emitConclusionIfNeeded`、`internal/hub/task_graph.go:handleTaskGraphEvent`。

## 2. 账号、端点、工作目录与连接

| 对象 | 当前实现 | MCP 设计影响 |
| --- | --- | --- |
| 用户 | `internal/store/store.go:User`，含 id、username、createdAt、管理员及功能字段 | 沿用既有用户，不另建账号系统；收件邮箱需要新增或关联持久化 |
| Bridge 归属 | `internal/hub/ws_bridge.go:handleBridgeWS` 消费 enroll token，按 token 的 UserID 绑定机器；`internal/store/store.go:UpsertAgentForUser` 防止跨用户重新归属 | MCP Token 只决定调用者，不能凭客户端传入 userId 访问他人机器 |
| 连接命令 | `internal/hub/server.go:handleCreateBridgeToken` 与 `docs/features/background-connect-command.md` 已提供 setupCommand 及两步 install/connect 命令 | 保留既有连接流程；无需为了 MCP 迁移工作区或把证明工具安装到 Hub |
| 目录与能力 | `internal/bridge/client.go:connectOnce` 上报 RegisterPayload.WorkingDirs 与 Capabilities；`internal/hub/server.go:handleAgents` 返回所有权过滤后的在线信息 | 机器发现工具可复用；“当前项目”需要明确选中自己已绑定的端点与目录 |
| 实际工作区 | `internal/bridge/orchestration.go:cwd` 先使用锁定的 RunCWD，再使用任务 CWD 或 Bridge.CWD，解析发生在 Bridge 机器 | 本地路径在对应 Bridge 上解释，不在 Hub 解释 |
| 同任务继续 | `internal/hub/orchestration.go:handleContinueOrchestration` 保持 runID，要求相同 agentId，恢复原生上下文与配置绑定 | MCP 的继续工具不得隐式新建运行，也不能在继续时切到另一机器 |

`internal/hub/orchestration.go:resolveAgentID` 在未提供 agentId 时会选择可见的首个在线机器。该现有兜底不能被描述成“理解了用户当前目录”。多端点的自然语言入口应先解析明确目标或正式默认偏好，歧义时才询问。

Bridge 任务生命周期由 `internal/bridge/orchestration.go:Start` 中的 manager lifetime 控制，不依赖提交任务的浏览器 WebSocket。普通客户端断开时，`internal/hub/orchestration.go:handleOrchestrationWS` 只移除客户端连接，不自动取消执行任务。

但 Bridge 到 Hub 的连接有另一套恢复边界：`internal/hub/ws_bridge.go:scheduleAgentRunFailure` 在恢复窗口后可能把任务标记失败；`internal/hub/server.go:recoverInterruptedRuns` 会处理重启后仍未重连的端点。用户可退出发起任务的 CLI，不代表可以关闭执行机器、停止 Bridge 或无限期断开其 VPN。

## 3. Token 的三种用途

| 凭据 | 当前情况 | 不变量 |
| --- | --- | --- |
| 浏览器登录 JWT | `internal/hub/server.go:writeAuthSession` 写 HttpOnly cookie；`internal/hub/server.go:userIDFromRequest` 也接受同一 signer 的 Bearer JWT | 现有 Bearer 支持不代表已经具备个人 MCP Token 的签发、展示、撤销或范围控制 |
| Bridge enroll token | `internal/hub/ws_bridge.go:handleBridgeWS`、`internal/store/store.go:ConsumeEnrollTokenInfo` 用于机器注册与绑定后重连 | 不能拿它当用户操作 Hub 的 MCP Token |
| 个人 MCP Token | 当前没有相应路由、表、配置展示与鉴权路径 | 新功能必须绑定既有用户，并保持机器、模型库、任务的相同所有权规则 |

`internal/store/store.go:CreateCLIConfigPreset` 的 `NewID("mcp")` 是“模型配置预设 ID”前缀，不能把这个搜索结果当成已实现 MCP Token 的证据。

个人中心长期提供可再次复制的完整配置模板，与 token 只保存不可恢复摘要属于不同存储选择。具体令牌重显、加密保存、轮换策略应由新 ADR 明确，不能在文档中同时承诺互相矛盾的行为。

## 4. 模型库、组合、profile 与默认偏好

### 4.1 当前已有模型能力

- 账户级 API key 模型库：`internal/hub/cli_config.go:handleListUserCLIConfigPresets` 与 `internal/store/store.go:CLIConfigPreset`。返回 metadata，VaultSecret 与 Secret 不进入 JSON。
- 新模型凭据由 Hub 接收并加密保存在 vault，再按任务选中的 Bridge 包装凭据。依据：`internal/hub/cli_config.go:handleCreateUserCLIConfigPreset`、`internal/hub/cli_config_vault.go:sealCLIConfigVaultSecret`、`internal/hub/cli_config_vault.go:materializeCLIConfigPresetCredential`。不能笼统声称“Hub 从不接收任何模型密钥”。
- Worker 对支持 `claude-codex`、`codex-codex`、`claude-claude`，活动槽位依次对应 claude/codex、codex-a/codex-b、claude-a/claude-b。依据：`internal/hub/orchestration.go:resolveWorkerProfiles`。
- 每个槽位可绑定独立 preset 和 reasoning effort，Hub 解析为绑定快照；Bridge 将其物化到该运行/槽位私有 CLI 配置。依据：`internal/bridge/orchestration_worker_profiles.go:workerRuntime`。
- 官方登录以 `__official__` 绑定表示，不等同于保存的 API key preset。Codex 官方 model/effort 的显式选择还受账户 rollout、ConfigSwitcher v3 和目标 Bridge 返回的原生目录约束；不能把公共模型目录当成每台机器的官方模型权限。依据：`internal/hub/official_cli_models.go:authorizeOfficialWorkerRequest`、`internal/hub/official_cli_models.go:loadOfficialCLIModels`、`internal/bridge/cli_config_official_models.go:officialCodexModels`。
- 官方 Claude 绑定可沿用本机登录，但当前显式官方 model 覆盖校验仅允许 Codex 槽位。不要为两个 CLI 编造对称能力。依据：`internal/hub/official_cli_models.go:normalizeOfficialWorkerModels`。
- reasoning effort 必须来自已审定模型能力，或目标机器官方目录；不能假定每个模型都支持相同枚举。依据：`internal/hub/orchestration.go:resolveWorkerProfiles`、`internal/hub/model_catalog.go`。

### 4.2 必须保持区分的概念

| 名称 | 当前语义 |
| --- | --- |
| CLI model preset | 某个用户保存的 provider/model/effort 等模型预设 |
| Worker binding | 一次运行中每个槽位实际选择的预设、模型、effort 与私有凭据快照 |
| workerPair | 三种 CLI 双工作器组合 |
| mode | collaboration 或 debate |
| profile | default 或 formal-proof；这是证明领域契约，不是用户的“默认配置方案” |
| verificationMode | agent 或 tbci；tbci 仅适用于 formal-proof 且要求目标 Bridge 能力可用 |
| executionMode | bounded 或 dynamic；动态模式没有总轮数预算，但仍受完成/停滞和取消机制控制 |
| 用户可命名的编排方案、账户/项目默认偏好 | 本次需要新增的产品能力，不应与上述 profile 或机器级 active preset 混用 |

字段与规范化依据：`internal/hub/orchestration.go:orchestrationCreateRequest`、`internal/hub/orchestration.go:normalizeOrchestrationStart`、`internal/hub/orchestration.go:normalizeOrchestrationProfile`、`internal/hub/orchestration.go:validateVerificationCapability`。

`internal/hub/orchestration.go:resolveWorkerProfiles` 的空 requested map 会返回 nil，保留已有无绑定调用路径；只有传入绑定后才要求完整匹配所有槽位。`docs/features/orchestration-worker-profiles-and-early-verdicts.md` 的“新运行每个槽位必须显式选择 saved preset”描述了网页产品约束，不能当作当前 HTTP 全局硬约束。

## 5. 任务提交、继续、追加指令与幂等

### 5.1 顶层提交身份

`internal/hub/orchestration.go:startOrchestration` 先持久化一条 `user.message` 事件，然后将该事件的 Seq 写入 `OrchestrationStartPayload.PromptSeq`。durable graph 的 payload JSON、run.start 和统计边界沿用它。依据：`internal/protocol/envelope.go:OrchestrationStartPayload`、`internal/hub/task_graph.go:groupOrchestrationProgressGraphs`、`internal/hub/orchestration.go:orchestrationTaskBoundaries`。

同一个 runID 可以先后包含多次终态继续提交。通知去重不能永久只使用 runID；至少需要区分当前提交边界，例如 runID + promptSeq。现有 `OrchestrationRun` 没有独立的 currentPromptSeq 字段，新接入应明确如何可靠获取并冻结这个身份，不能在邮件发送时重新读取可能已被后续提交改写的当前状态。

### 5.2 三种操作不是同一件事

| 操作 | 当前入口与语义 | MCP 设计影响 |
| --- | --- | --- |
| 新建任务 | `handleCreateOrchestration` 创建新 run，随后 startOrchestration | 客户端超时重试可能创建重复 run；需顶层 requestId 幂等 |
| 终态继续 | `handleContinueOrchestration` 通过 `ClaimOrchestrationRunForContinue` 原子 claim 同一 run，再写新 user.message | 并发 claim 可防同时双启，但不等同持久化请求重试幂等；仍需同次提交身份 |
| 运行中追加 | `internal/hub/orchestration_messages.go:handleActiveOrchestrationMessage`，要求 messageId、prompt、deliveryMode | 已有持久化消息与幂等键；需保留 queued/sending/delivered/failed 语义 |

运行中消息 `delivered` 只表明原生协议接收/完整写出，不能声称模型已完成执行；不确定的 sending 消息不能自动重放。依据：`internal/hub/orchestration_messages.go:dispatchOrchestrationMessages`、`internal/hub/orchestration_messages.go:finishOrchestrationMessages`。

`internal/hub/orchestration.go:handleContinueOrchestration` 同时承载后两种操作；MCP 可拆为语义清楚的工具，再复用相同的底层处理逻辑。终态继续还保持原始 executionMode 与 agentId；遗漏 worker bindings 时保留原快照，不应因默认偏好后来变化而静默覆盖老运行。

## 6. 运行结束、证明通过与邮件触发

### 6.1 验证的实际策略

当前 Agent Verifier 并非两个工作器互相确认的同义词。它使用两次独立检查，当前源码串行调用；必须两次结果都返回，至少一个 pass，且不能存在带具体 target/evidence 的 missing-target、proof-incomplete、build-failed、spec-mismatch、trust-violation 冲突。依据：`internal/bridge/orchestration_verifier.go:evaluateAgentVerifierQuorum`、`internal/bridge/orchestration_verifier.go:aggregateAgentVerifierQuorum`。

durable reviewer 会触发检查，worker/integrator 的 resolved handoff 也可以触发独立检查候选；最终 reviewer 没有“免验证”通道。依据：`internal/bridge/orchestration.go:shouldRunAgentVerifier`、`internal/bridge/orchestration.go:run`、`internal/bridge/orchestration_completion.go:completionCandidate`。

TBCI 复用单独的验证模式与能力门禁，不能在 TBCI 不可用时悄悄切到 Agent 模式。当前执行分支依据：`internal/bridge/orchestration.go:run`，能力校验依据：`internal/hub/orchestration.go:validateVerificationCapability`。

达到轮次上限或停滞可以得到运行 completed，但结论可能是 blocked/unknown/continue，不能转译成“证明成功”。依据：`internal/hub/task_graph.go:handleTaskGraphEvent`、`internal/bridge/orchestration_completion.go:completedExecutionConclusion`。

### 6.2 通知应接入最终投影，而非原始节点事件

- 旧 relay 的终态在 `internal/hub/orchestration.go:handleOrchestrationEventFromAgent` 处理。
- durable 节点的 run.end/run.error 被携带 TaskAttemptRef 处理；`internal/bridge/orchestration_events.go:emitConclusionIfNeeded` 明确不为节点单独发送公开结论。
- Hub 在 `internal/hub/task_graph.go:handleTaskGraphEvent` 完成图推进、最终 barrier 和结论投影后，才写公开 run.end/run.error/run.cancelled。
- Hub 重启恢复、Bridge 离线超过窗口、取消超时也有独立终态写入路径。依据：`internal/hub/server.go:recoverInterruptedRuns`、`internal/hub/ws_bridge.go:scheduleAgentRunFailure`、`internal/store/store.go:CancelOrchestrationRunIfStillCanceling`。

因此，仅在 WebSocket 收到 run.end 或仅在一个 handler 中同步 sendMail 都不完整。

当前 `UpdateOrchestrationRunStatus`、`UpdateOrchestrationRunStatusIfAllowed` 与 `AddOrchestrationEvent` 是分开的持久化操作；`AddOrchestrationEvent` 自身有事务，但没有邮件 outbox。新增可靠通知需要覆盖上述所有最终结算入口，在同一提交边界冻结运行状态、验证结论和通知任务，或给出等价的持久化补偿/重启补扫方案。

通知记录应保存该次提交的结果快照、收件人、结论和投递状态；不能等到发送时再读 run 当前结果，否则用户继续同 run 后，旧邮件可能报告新提交的数据。重试应避免重复创建通知；SMTP 接受与最终进入邮箱是不同的可观察事实。

## 7. 人工审批和原生问题

当前命令/文件审批走 `approval_request` / `approval_response` WebSocket；`internal/bridge/orchestration.go:RequestApproval` 在 Bridge 内存保留待决请求并重复发送，Hub 通过 `internal/hub/ws_bridge.go:handleBridgeEnvelope` 广播，浏览器经 `handleOrchestrationWS` 回复。审批卡片不是持久化事件历史；不能仅靠 get events 实现 MCP 的“所有待处理交互”。

进一步核对，当前审批没有 pending snapshot、权威 closed 状态或决策送达 ACK。`internal/protocol/envelope.go:ApprovalResponsePayload` 仅含 requestId 和 decision；`internal/bridge/orchestration.go:ApprovalResponse` 按 requestId 从内存 map 取出并删除请求。Hub 的最后一条重发缓存只能证明“曾收到审批”，不能证明它现在仍然有效；MCP 返回 HTTP 成功也不能冒充原生审批已接受。

最小增量建议是：为审批查询和回答添加受能力门禁控制的 Bridge 快照/回执契约，带足够的 run、execution、Bridge instance 和版本身份；Bridge 保持事实来源。旧 Bridge 未声明该能力时，MCP 明确报告审批查询/回答不可用，保留网站现有路径。具体新 frame 名称由主设计确定。不要仅因新增 MCP HTTP 路由就声称支持完整人工交互。

原生结构化问答是独立通道。Bridge 拥有问题、原生回调及 receipt，Hub 不把问答正文写入 SQLite；浏览器通过 v3 sync 查询快照并提交带 requestId、submissionId、executionId、instanceId 等身份的答案。依据：`internal/hub/user_input.go:handleUserInputFromBrowser`、`internal/bridge/user_input.go:UserInputSync`、`internal/protocol/envelope.go:UserInputResponsePayload`、`internal/protocol/envelope.go:UserInputSyncPayload`。

要让终端完全替代日常网页，MCP 需要适配这两类交互，而不是新增一条普通 prompt 去伪造回答。必须保留账户、run、Bridge、原生执行身份、v3 协商、终态拒绝和 receipt 对账规则。仅返回“running”会掩盖等待用户的任务；退出外层 CLI 后需要在通知/任务详情中明确等待状态及恢复入口，不能自动批准或猜答案。

原生问题的 v3 快照/对账已经存在，不需要另建一套问答协议。但当前 Hub 路由依赖 BrowserConn 和连接内的 nativeUserInputVersion；接入 MCP 应抽取共享的鉴权/协商/路由逻辑，维持同等校验，再把快照投影给 MCP 的有界查询。无法联系 Bridge 时返回 unavailable/unknown，不能从历史事件重造“待答问题”。

## 8. 新入口的递归调用风险

这是针对拟新增能力的设计推断，不是已复现的当前缺陷。

现有绑定预设通常使用私有 CODEX_HOME / CLAUDE_CONFIG_DIR；但官方登录路径会沿用本机身份，项目级/用户级 Skill 也可能被内部 CLI 发现。当前没有明确的“禁用 ProofBridge 外层入口 MCP/Skill”策略。依据：`internal/bridge/orchestration_worker_profiles.go:workerRuntime`。

如果外层 Skill 在“完成验证”这种宽泛请求下自动调用 ProofBridge，而内部 worker 也装载同一个 Skill/MCP，就可能递归提交编排任务。需要在内部 worker 启动配置与 Skill 触发条件中隔离外层入口，并保留既有 Claude 审批 MCP。当前 Claude 会注入临时 `codex_bridge/browser_approval` helper，不能通过禁用所有 MCP 来粗暴解决。依据：`internal/bridge/orchestration_claude.go:prepareClaudeApprovalServer`、`internal/bridge/orchestration_claude.go:withClaudeStreamApprovalArgs`。

## 9. 文档与当前源码不一致之处

| 文档表述 | 当前源码或更具体文档 | 规划时的处理 |
| --- | --- | --- |
| AGENTS Positioning 中 Hub 仅传输/鉴权/静态 UI，模型凭据归 Bridge 的简述 | `internal/hub/cli_config.go:handleCreateUserCLIConfigPreset` 接收模型 key；`internal/hub/cli_config_vault.go:sealCLIConfigVaultSecret` 加密存 Hub；README Provider presets 已明确 Hub operator 信任边界 | 区分原生登录凭据与用户主动保存的 provider key，不复制旧简述为新架构事实 |
| `docs/architecture.md:477` 概括“每个 run 一条 Codex 和一条 Claude 长会话、无 verifier” | 同文件较前段已区分双 Codex/双 Claude 与 durable attempt；`orchestrationExecutionKey` 按 run:attempt 分隔；`evaluateAgentVerifierQuorum` 有独立验证器 | 用当前实际 task graph/worker pair/attempt 结构解释；该旧概括不应成为新 MCP 的约束 |
| `docs/features/orchestration-worker-profiles-and-early-verdicts.md` 声称所有新运行必须带完整预设 | `resolveWorkerProfiles` 允许空绑定；网页要求完整槽位选择 | 将 UI 产品规则和后端兼容路径分开 |
| 同一 feature Q&A 写“concurrent sessions” | `evaluateAgentVerifierQuorum` 顺序调用，且 `agentVerificationGate` 串行重检查 | 不声称当前并发验证 |
| `docs/features/tbci-formal-proof-verification.md` 的步骤仍写最终 reviewer 跳过检查、预算结束成功 | 当前 `shouldRunAgentVerifier` 不排除最终 reviewer；`handleTaskGraphEvent` 保持生命周期与验证结论分离 | 新结果契约以当前源码与 `docs/features/independent-early-run-completion.md` 为准 |
| 旧设计可能写双方一致 pass 或纯 OR 判定 | `aggregateAgentVerifierQuorum` 两结果齐备、至少一 pass，同时拒绝具体证据冲突 | MCP 展示真实 verdict 与检查证据，不发明更强验证保证 |

本文仅记录冲突，不批量重写历史设计。新设计应引用具体 owning source；需要修正文档时按 `AGENTS.md` R5/R7 保留历史与状态，避免把旧验收记录改写成当前真实运行结果。

## 10. 推荐由后续设计明确的接口缺口

1. 个人 MCP Token 的签发/重显/撤销/轮换，以及它与既有 JWT、机器 enroll token 的隔离。
2. 账户级默认偏好和可命名的编排方案；避免与 proof-domain profile、模型 preset、机器 active preset 混淆。
3. 统一的模型可用选项投影：账户模型库 + 目标 Bridge 官方登录能力 + 允许的 worker pair + reviewed effort；只返回非秘密 metadata。
4. 顶层提交 requestId 幂等与每次提交的稳定身份；保留 runID 继续和 live instruction messageId 独立语义。
5. 面向 MCP 的有界等待、分页事件、最终状态/验证结论/证据投影，不能把截断历史当完整结果。
6. 面向 MCP 的审批与原生问答适配，不改变既有权限配置，不绕过版本和所有权门禁。
7. 覆盖全部最终结算路径的持久化邮件 outbox、邮箱与偏好、重试、去重、提交结果快照和发送失败可观察性。
8. 内部 worker 的外层入口 MCP/Skill 隔离，尤其官方登录和项目级 Skill 的路径。
9. VPN 部署的 Hub/MCP 地址、反向 WebSocket、客户端和执行端网络检查；提交客户端断开与 Bridge 本身失联分别处理。
10. 端到端验收分别证明：真实客户端触发、同机项目执行、权限交互、退出提交会话后邮件、VPN 恢复、失败分类与跨用户隔离。本文尚未运行这些验收。

