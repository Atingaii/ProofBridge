# 实验站原生结构化用户问答

状态：v3 加固代码已进入工作区，用户已批准[运行时交互补全计划](native-structured-user-input-runtime-plan.md)的实施；排队式原生提问正在补全，完整真实浏览器验收仍缺失。协议决策见 [ADR 024](../adr/024-native-structured-user-input.md)，历史修复见[详细加固方案](native-structured-user-input-hardening.md)与[v3 回归计划](native-structured-user-input-regression-plan.md)。能力按官方接口的最低 CLI 版本与权限模式开放；后续版本默认兼容，不再锁定历史实测版本。最低版本调研见下节。

## 版本范围修复（2026-09-29）

目标：修复 Codex 0.158.0 因不等于历史测试版 0.156.1 被误报不可用的问题。
范围：仅修改 CLI 版本判断、提示文案和测试；不改变问答 v3、权限模式、审批或数据库。
方案：按数字比较最低版本及以上，不设上限；版本命令失败、输出无法解析或已知过旧时报告最低要求。
能力报告和实际启动适配器使用相同判断；新版本不被标为“已实测”。真实协议错误仍由运行时反馈。

官方版本证据（比较相邻正式 tag，非按本机安装版本推断）：

| 能力 | 首个正式版本 | 依据 |
| --- | --- | --- |
| Codex 原生 request_user_input | 0.88.0 | [引入提交 #9472](https://github.com/openai/codex/pull/9472)；0.87.0 无 handler，0.88.0 有 |
| Codex Default 模式提问开关 | 0.106.0 | [#12735](https://github.com/openai/codex/pull/12735)；0.105.0 无 `default_mode_request_user_input`，0.106.0 有 |
| Codex 显式阻塞语义（当前适配最低要求） | 0.147.0 | [0.147.0 wire 类型](https://github.com/openai/codex/blob/rust-v0.147.0/codex-rs/app-server-protocol/schema/typescript/v2/ToolRequestUserInputParams.ts) 有 `isBlocking`，[0.146.1](https://github.com/openai/codex/blob/rust-v0.146.1/codex-rs/app-server-protocol/schema/typescript/v2/ToolRequestUserInputParams.ts) 没有 |
| Codex 原生异步 request_user_input_async | 0.153.0 | [#42178](https://github.com/openai/codex/pull/42178)；0.152.1 无 handler，0.153.0 有 |
| Claude 交互式 AskUserQuestion | 2.0.21 | [官方 changelog](https://code.claude.com/docs/en/changelog#2021)；此版本仅交互式启用，不能直接视为本项目 `--print` 最低版本 |
| Claude 无需额外实验开关的 `--print` 提问（当前适配最低要求） | 2.0.71 | 官方 npm [2.0.70](https://registry.npmjs.org/@anthropic-ai/claude-code/2.0.70) 与 [2.0.71](https://registry.npmjs.org/@anthropic-ai/claude-code/2.0.71) 发布包 `package/cli.js`：AskUserQuestion 的 `isEnabled` 从 interactive／环境变量条件改为恒真 |

当前最低版本为 **Codex >= 0.147.0，Claude Code >= 2.0.71**，无最高版本上限。
Claude 2.0.71 同时声明 `requiresUserInteraction`，权限判断在 bypassPermissions 自动放行前返回交互请求；MCP permission prompt 接受 `updatedInput`，AskUserQuestion 将 answers 返回原工具调用。
2.0.50、2.0.60、2.0.65、2.0.70 的无界面提问仍依赖 `CLAUDE_CODE_ENABLE_ASK_USER_QUESTION_TOOL`，本修复不添加这一旧实验开关。2.1.85 新增的是 PreToolUse hook 代答路径，并非本项目 MCP 路径的首次支持。
以上最低版本来自官方包与接口源码审查；真实模型往返仍只按下方历史记录声明。

实施：核对 CLI 源码后更新最低版本；补充边界、未来版本、无效输出、权限与实际启动门禁测试；重建嵌入页面及本机二进制，更新独立站与受影响 Bridge，不构建四个平台安装包。
验收：0.158.0 能力可用；低于协议要求仍拒绝；浏览器 v3 与权限门禁保留；已有问答回归通过。

问：为什么不直接从 Codex 0.88.0 开放？答：那是工具首次出现，早期应答格式不同且没有当前适配必需的 `isBlocking`；当前 Bridge 从 0.147.0 开放同步问答，异步仅在 CLI 实际发出相应事件时处理。
问：是否声称所有后续版本都实测过？答：否。采用向前兼容策略，保留具体原生错误与模型实际调用边界；历史真实探针证据仍单独列出。

### 本次验证与部署

- 最低版本／相邻旧版／未来 minor、major／预发行／构建后缀／非法输出／失败命令及实际运行门禁回归通过；Codex 同步问答与 Claude 经 Hub/browser 的集成回归通过。
- 前端 `user-input-check.mjs`、`participant-identity-check.mjs` 通过；前端构建、本机 Go 构建通过；doc-lint 为 0 error，保留原有 `Server.Run` 锚点 warning。
- 独立站已部署 `v0.3.79-exp-input-compat-20260929`，构建时间 `2026-09-29T08:34:30Z`；对应 Hub、系统 Bridge、lucentCbat 和 testCC/test1 的本机 Bridge 使用新二进制。test1 启动器改指向独立站二进制，未覆盖共享 `~/.local/bin/codex-bridge`。
- 实际 `/api/agents` 确认 test1、lucentCbat 的 Codex／Claude `nativeUserInput.available=true`；本机 Codex 为 0.158.0。公网健康、HTML、入口引用的最新 workspace 资源与构建一致。历史资源因缓存兼容保留。
- 没有重新进行外部模型驱动的人工浏览器往返，本次不将能力报告等同于模型已提问。刷新页面后，在新运行明确要求 CLI 先调用原生提问工具即可验证实际出题。
- 仅构建本机二进制；已有 3 个跨平台下载文件哈希未变化。主站 Hub／Bridge 进程未变。回滚备份：`/opt/proofbridge-isolated/backups/20260929T083545Z-input-compat`（旧二进制、test1 启动器及验证记录）。

## Goal

2026-09-29 前端增量修复：参与者优先显示真实 Codex／Claude A/B；多题请求逐题填写、返回修改并整组提交；自定义输入有明确提示及空白校验。Claude 原生提问绑定 worker slot，角色切换时保留身份并在写入提示词前更新 turn。设计与验收见[参与者身份与逐题问答](participant-identity-and-question-flow.md)。

实验站 Codex／Claude Code 原生提出问题时，浏览器显示选项与自定义输入，答案回到原调用，继续原上下文。审批与信任模式均支持。用户选择保持原生阻塞／非阻塞行为。

最新澄清要求覆盖类似 `Queued follow-up inputs` 的待答队列：新问题主动可见，可选择或填写后交回原生交互；收起不取消，非阻塞请求不额外暂停独立工作。Codex 0.156.1 的异步事件与同 thread 回答方式已由[原生契约调研](../reviews/native-user-input-evidence/research/codex-async-native-contract.md)确认，但尚无当前产品的真实浏览器链路证据。主动面板、队列体验与验收详见运行时交互补全计划。

## Non-Goals

首期不扩展普通聊天、任意 MCP 表单、通知推送、问答历史数据库或 CLI 进程死亡恢复；不把普通文本问题猜测为工具请求。既有权限、会话连续性与 CLI 角色边界保持不变。

## 改造前的缺口

- Codex `internal/bridge/appserver_runner.go:readEvents` 处理审批请求，未处理 requestUserInput。
- Claude `internal/bridge/orchestration_claude.go:prepareClaudeApprovalServer` 按审批模式安装 MCP host；无审批时关闭。相关参数构造会强制 default，需要与通道安装解耦。
- `internal/bridge/claude_approval_mcp.go:handleClaudeApprovalMCPToolCall` 只有允许／拒绝，允许时没有答案 updatedInput。
- 现有路由在 `internal/hub/ws_bridge.go:handleBridgeEnvelope` 和 `internal/hub/orchestration.go:handleOrchestrationWS`；前端入口为 `frontend/src/app/pages/OrchestrationWorkspace.tsx:connectRun`。
- 本机调研版本为 codex-cli 0.156.1 / Claude Code 2.1.229。Codex schema 有 isBlocking，默认模式提问 feature 尚属开发中。Claude 官方 SDK 支持 AskUserQuestion，但本项目 MCP host 的实际往返需要验证。

## 当前 CLI 验证（2026-09-24）

以下保留历史真实探针及当时门禁；2026-09-29 起版本策略由上方最低版本范围替代。

| CLI 与权限 | 原生探针结果 | 能力门禁 |
| --- | --- | --- |
| Codex 0.156.1，auto-execute，Default | `item/tool/requestUserInput` 返回 `isBlocking:false`；按原始 JSON-RPC ID 回答后收到 `serverRequest/resolved` 和同 turn 完成 | 开放已验证的非阻塞问答 |
| Codex 0.156.1，auto-execute，Plan | `isBlocking:true`；回答后收到 `serverRequest/resolved` 和同 turn 完成 | 开放已验证的阻塞问答 |
| Codex 0.156.1，review-required（`on-request` + `workspace-write`），Default | 同样完成非阻塞请求、回答、resolved 与同 turn 完成 | 开放已验证的非阻塞问答 |
| Codex 0.156.1，review-required，Plan | `isBlocking:true`；回答后同一原生 turn 完成 | 开放已验证的阻塞问答 |
| Claude Code 2.1.229，default／bypassPermissions | 基线 `claude -p` 持续模型 API 重试，未进入可测试的 `AskUserQuestion`；MCP 适配仅有本地模拟测试 | 两种模式均声明不可用，待真实原生往返 |
| Claude Code 2.1.281，gpt-6-sol，default | 修复后的项目 helper 完成一次真实 `AskUserQuestion`、三题 socket 回调，单选／多选／自定义答案在同次 CLI 中回显；exit 0、tool_result 无错误 | 精确 CLI 版本且 Bridge 为 review-required 时开放；其它版本或探测失败关闭 |
| Claude Code 2.1.281，gpt-6-sol，bypassPermissions | 同一项目 helper 在免审批模式完成相同三题原生往返，不强制切回 default | 精确 CLI 版本且 Bridge 为 auto-execute 时开放；strict-workspace 尚未实测，保持关闭 |

最新环境隔离、端点路径对照和脱敏事件见[Claude 实测记录](../reviews/native-user-input-evidence/claude-capability-verification.md)。第一组端点／模型 502 未完成问答；新组配置基础调用成功，不能把两组结果混为一项。此前临时包装器和修复后的项目 helper 分别记录，不能混为同一次验收。

这些探针验证**同步 `item/tool/requestUserInput`** 的 CLI 原生应答契约；`internal/bridge/claude_hub_user_input_test.go:TestClaudeAskUserQuestionMCPThroughHubAndBrowser` 另用真实 Hub WebSocket、Bridge manager 与项目 MCP helper 验证同一条浏览器问答和终态 receipt 恢复链路，不调用外部模型。部署后的真实浏览器人工验收仍需单独进行。历史探针不等于其他版本已实测；当前能力按上方最低版本开放，`strict-workspace` 仍不在支持范围。Codex `request_user_input_async` 属于另一种消息和答案契约；0.156.1 源码证明原生机制存在，但不能把上面的同步探针当成异步适配或真实产品验收通过。

## Design

### 原生适配

Codex 同步工具处理 `item/tool/requestUserInput`，按原生 request ID 回写 `answers[questionId].answers`；同时监听 `serverRequest/resolved` 和 turn/进程终止。保留原生 ID 的类型，不能把 JSON-RPC 数字 ID 随意改成字符串。异步工具 `request_user_input_async` 发出 `agentMessage(delivery=async, questions)`，以原消息 ID 和题序生成 `questionItemId`；答案按原生 `<send_user_message_question_reply>` 格式提交到同一 thread：活动 turn 用 `turn/steer`，空闲时用 `turn/start`。这条路径不能借用同步 JSON-RPC 回调。Codex 0.156.1 仅在 root agent 且模型目录声明相应 experimental tool 时暴露异步工具；Bridge 协议支持不等于当前模型已调用。

优先使用 isBlocking；旧版只在明确验证其字段语义后兼容，不能把缺字段默认为非阻塞。当前适配最低版本来自上方官方接口调研。默认模式能力仅在实验站专用进程中按需启用，不编辑全局配置，不改变 permission policy 或 collaboration mode。非阻塞请求的呈现、送达及关闭遵循原生生命周期，不添加自动默认答案；不能把“非阻塞”理解为 Bridge 可以丢弃或伪造工具返回值。

Claude 优先复用 MCP permission host，识别 AskUserQuestion，将完整原始 questions 与用户 answers 放入对应原生响应。其他工具继续走既有审批规则，保持 default/bypassPermissions 原始选择。不能仅因为安装了 host 就强制切回 default。原生题目文字、选项、多选编码保持一致；重复题目文字等无法无损回传的情况明确拒绝，不静默覆盖答案。

两端都在工具实际可用时提供简短指引：需要用户补充信息或决策时优先原生结构化提问，提供合理选项并保留自定义回答。工具是否可用由能力判断决定，不能仅靠提示词保证每次都调用工具。Claude 若 MCP 路径失败，再审查 stdio control_request/control_response 备选。

### 传输契约（v1 基础，v2 加固，v3 核对）

继续使用 `internal/protocol/envelope.go:Envelope`。不增加 HTTP 写端点。

| 消息 | 方向 | 主要内容 |
| --- | --- | --- |
| user_input_request | Bridge → Hub → 浏览器 | requestId、runId、executionId、workerId、turnId、cli、blocking、questions |
| user_input_response | 浏览器 → Hub → Bridge | requestId、runId、submissionId、按题 ID 编码的答案 |
| user_input_status | Bridge → Hub → 浏览器 | requestId、runId、revision、状态、原因、可选 submissionId |
| user_input_sync | 浏览器／Hub → Bridge | runId、同步关联 ID，要求当前快照 |
| user_input_snapshot | Bridge → Hub → 浏览器 | 同步关联 ID、执行代次、revision、水位对应的活跃请求／终态摘要 |

统一状态消息承载送达、关闭和失败，替代草案中只能表达结束的 user_input_resolved。UI 的提交中状态为本地状态，不冒充服务端确认。

加固后的 v2 字段由 `internal/protocol/envelope.go:UserInputRequestPayload` 等定义：Bridge 发出的 request/status/snapshot 带 `instanceId` 状态域，request/status 带 `executionId`；status 以 `lifecycle=open|closed` 和 `receipt` 分别说明请求存活及一次提交结果，`receipt` 按 request/run/execution/instance/submission 绑定，outcome 为 `delivered`、`not_sent` 或 `unknown`。Hub 路由拒绝带 `source=hub,phase=route,revision=0`，不能冒充 Bridge 的生命周期确认；Bridge 明确未写入的校验或队列拒绝带 `source=bridge,phase=validate|queue` 和 `not_sent`。snapshot 中 `requests` 恒为数组，另有有界 `closed` 和 `receipts`；终态 run 只允许原所有者查询摘要，不可再次作答。旧版 `null`/缺失数组由前端统一解码为空数组，非法非数组快照整帧拒绝。

v3 浏览器在 `user_input_sync` 中声明 `clientVersion=3`，在创建／继续请求中携带 `nativeUserInputClientVersion=3`，同时检查 Hub 的 `/api/me.nativeUserInputProtocolVersion=3` 与 Bridge 的 v3 capability。Hub 仅向已声明 v3 的浏览器连接转发活跃问答帧，并在三方条件满足时派发 `nativeUserInput=true` 和 `nativeUserInputProtocolVersion=3`；Bridge 独立拒绝旧 Hub 的 true-only start payload。旧页面、旧 Hub、旧 Bridge 均不能开启新的原生问答，普通编排继续。终态 run 保留授权只读快照，不允许答复。

`user_input_sync.pendingSubmissions` 只包含 requestId、submissionId 和 executionId，不包含题目或答案；含查询时 instanceId 必须精确匹配。Bridge 在同一状态锁下核对既有 receipt、排队中的胜出提交，或为仍 open 且未收到的 ID 建立 `not_sent` 封禁凭据。`user_input_snapshot.reconciliations` 返回逐项结果；无法确认时以 `reconcileError` 阻止前端解锁。浏览器先持久化最多 64 个未决提交身份到当前标签的 session storage，再发送答案；刷新只查询，不自动重发原答案。queued 禁止改答，只有可核实的 `not_sent` 且请求仍 open 时才能用新的 submissionId 再提交；unknown 保持只读并可手动重查。

题目结构含稳定题 ID、header、question、options（选项 ID／label／description）、selectionMode、allowCustom、secret。选项选择与自定义文字分别编码，由适配器还原原生答案。Codex 响应使用数组不代表问题支持多选；只展示经能力确认的选择方式。不提供原生不支持的伪多选或伪跳过操作。

原生 request ID、问题原始字段与回调仅在 Bridge 保存，浏览器不能指定路由目的或注入任意原生响应。执行代次包含 attempt／原生进程生命周期，可复用现有 attempt 身份，兼容无任务图的执行路径。

### 一致性、等待与恢复

- Bridge 注册待答项并异步处理答案；继续读取原生通知，不在 readEvents 内同步阻塞等待。写入由原生 client 的串行写通道执行，取消与回写在请求状态机中仲裁。
- 每个新问题使用唯一交互 ID。Hub 以登录用户和 WebSocket 所属 run 为准，验证 Bridge 归属；Bridge 再验证当前 execution/worker/turn 和请求是否仍有效。
- 同一 submission 查询返回原有投递结果与当前生命周期；不同提交并发时仅首个有效提交可投递。问题已关闭不能转成下一轮 prompt。Hub 路由错误或传输断线不能证明前一份答案未送达，浏览器保持待确认并通过同步查询；只有 Bridge 明确给出 `not_sent` 且请求仍 open 时才能编辑并新建 submission。
- 原生响应成功写出后可显示“已送达”，不谎称模型已处理。Codex 同步工具以 native resolved 确认关闭；异步工具以同 thread 接受回答、相关问题终结及后续原生事件确认进展；Claude 以可观察的控制响应完成为边界，不能声称获得不存在的原生 ack。写入结果不确定时显示未确认，不自动重放到新进程。
- 原生关闭且未收到用户答案，显示“原生请求已关闭”，不能显示“用户已回答”。结束／取消／进程丢失清理全部关联等待。
- 阻塞请求等待用户时，不触发仅由没有模型输出造成的卡死判定；显式 shell 命令超时仍按原命令规则执行。Claude 现有 10 分钟 socket deadline 不直接套给用户等待；等待受原生取消、执行 context 和连接存活管理约束，不新增代答计时器。
- 非阻塞问题保持原生继续工作的能力；不冻结其他 worker，也不拦截原生完成／撤销通知。
- 浏览器刷新或重新订阅后请求快照；以 Bridge instance、执行代次和 revision 处理与实时事件的竞争，防止旧快照复活已关闭问题。Bridge 与 Hub 短暂断开、原生进程仍存活时重发有效状态；同实例有界 terminal/receipt 窗口可补回丢失的送达确认。
- Bridge／CLI 已重启时旧调用不可恢复；旧卡片失效。Hub 重启后的行为受既有运行恢复规则约束，只恢复仍经 Bridge 确认存活的调用。结束时立即清理题目／回调与敏感正文，最小 terminal/receipt 在有界窗口内保留；不承诺跨进程 exactly-once。

### 前端体验与数据边界

在实验站时间线显示问题卡，含发起 CLI／worker、问题与选项说明、自定义输入和明确提交按钮。时间线外保留可见的待答／待确认入口，长日志与手机屏幕下仍可跳转到唯一问题卡。显示“等待你回答”或“可回答，任务仍在继续”。多题整组提交，避免半组回答丢失；不自动提交预选项。原生提供推荐说明时才展示推荐。

保留已有停止运行操作；本期不增加原生语义不明确的独立取消问题按钮。刷新保证问题恢复，未提交草稿无需跨刷新持久化。支持键盘、字段标签、提交中防重和移动端布局。

问题与回答不进入新增数据库表，也不直接写入普通 orchestration 事件或公开分享。secret 按敏感输入呈现，日志不打印题目／答案原文；原生 CLI 自己的会话记录仍遵循其自身行为。

### 能力与兼容

在 Bridge CLI capability 中声明版本化问答能力，区分 CLI、模式与不支持原因；不将 protocol 类型存在等同于模型工具可用。Hub 和浏览器在不支持的端点显示原因。新旧组件不匹配时禁止启用该问答路径，不能让模型发出无人处理的问题。

作为用户可见功能按现有 rollout 机制注册独立 key（拟 `native-user-input`），同时在后端交互边界和前端消费能力；建议默认 all，与现有实验站计划工作区一致，但必须同时通过 CLI 能力门禁。验收覆盖允许／禁用。回滚关闭新问题入口并撤销有效交互，保留执行审批，不把待答项自动允许。

## Implementation Steps

1. 完成适配器真实 CLI 契约探针，记录模式、实测版本和失败原因；结合官方接口最低版本开放兼容范围，不要求每个后续发行版重新加入白名单。
2. 实现协议、能力与 Bridge 请求状态机，然后接入 Codex／Claude 适配。
3. 实现 Hub 归属校验、实时路由与快照同步，再接入前端卡片。
4. 验证生命周期、权限回归和上下文连续性；同步架构／代码地图及必要设置说明。
5. 通过前端测试与 build、Go 测试／构建及 doc-lint 后交付。Android 不在范围内。

## Exit Gates

- 两种 CLI × 两种权限模式：问题显示、选择／自由回答、原调用继续；Claude 原生多选完整往返。
- Codex 阻塞／非阻塞行为分别验证，不阻塞 reader 或取消。
- 并发 worker 原生 ID 相同不串答，多标签页首次有效提交获胜，重复提交幂等。
- 页面刷新、快照与关闭通知交错、断线、终止、旧 attempt 与迟到回答正确处理。
- 不改变现有审批策略，不开新实验站 run／新原生 thread 来回答问题；Codex 异步答案在原 turn 空闲时按原生协议于同 thread 发起后续 turn。
- 能力不支持／rollout 禁用可见且不挂死；敏感字段和公开分享边界通过检查。

## Reviewer Q&A

**为什么免审批还提问？** 执行授权不能提供需求答案，两条通道语义独立。

**是否所有 worker 都停下来？** 否，用户选择 A，只遵循原生调用本身的等待行为。

**刷新能恢复，为什么不存数据库？** 活跃 Bridge 持有原生请求并可同步；数据库记录不能复活死亡的 CLI 调用。

**官方支持就能直接上线吗？** 不能。默认模式能力与本机 Claude MCP 路径先通过真实往返；失败时说明限制、修订设计，不模拟成功。

**是否保证模型每次澄清都出卡片？** 不能保证。只在实际原生工具调用时显示卡片，通过能力支持和提示引导增加使用；普通文本仍是普通文本。


## 查询故障与送达提示修复（2026-10-03）

目标：修复无待确认提交时的送达误报、Hub 查询拒绝原因丢失、恢复后旧警告残留。
非目标：不自动重发答案、不变更 CLI 适配、不迁移数据库、不推广实验站专属功能。

契约：`internal/protocol/envelope.go:UserInputStatusPayload` 增加可选 `syncId`，
仅 Hub 的查询拒绝使用：`source=hub`、`phase=route`、`status=rejected`、
`revision=0`、空 `requestId`，关联当前 `runId/syncId`。该消息仅描述路由错误，
不携带投递 receipt，不改变问题生命周期，不解除未决提交。
`internal/hub/user_input.go:handleUserInputFromBrowser` 区分查询拒绝与答案拒绝，
先识别端点离线，再核验在线端点问答能力。无需更新 Bridge 或升级 v3 协议。

前端使用独立同步错误 decoder，普通问题 status 继续要求非空 requestId。
查询提示按当前 run 的未决提交区分问题列表同步与答案送达核对；恢复成功清理相应
过期查询提示，真实未知送达和本地存储故障仍保留。旧 Hub 不提供 syncId 时维持
保守超时；完整具体错误提示需要配套更新 Hub 与网页。

实施顺序：Hub/协议和前端修复 → 回归 → 嵌入资源构建 → 两站部署边界核验。
退出门禁包括空 pending 超时、离线/协议拒绝、错误关联隔离、晚到成功、unknown、
终态只读及重新查询不重发的浏览器/Go 回归，前端与 Go 门禁和文档检查。

评审问答：
- 为什么不只更新 Bridge？问题位于 Hub/前端确认链路，Bridge 无需新增行为。
- 为什么不超时后重发？超时不是未送达证明，自动重发可能重复执行。
- 为什么保留严格状态校验？同步错误不应伪造成某个问题的投递结论。
- 是否将 test 全部功能部署到主站？本次不以修复为由更改实验功能边界。


### 查询恢复验证矩阵

| 条件 | 页面行为 | 投递状态 |
| --- | --- | --- |
| 无未决提交，问题列表同步超时 | 提示问题状态同步失败，可重新查询 | 不声称有答案未送达 |
| 当前查询收到端点离线/能力不足拒绝 | 立即显示对应原因，允许只读重查 | 不把 Hub 拒绝当 not_sent |
| 错误属于其它 run 或旧 syncId | 忽略 | 不改变当前状态 |
| 超时后收到有效 delivered receipt | 清理相应过期查询警告 | 只按既有身份/revision规则确认 |
| 权威 receipt 为 unknown | 保留未确认及只读重查 | 不重发、不自动解锁 |
| 本地未决身份存储失败 | 保留本地存储故障 | 不假称已送达 |

正确基础例：打开没有提交过答案的 run，查询失败显示问题列表不可用。
正确恢复例：同一 submission 的权威送达凭据补回后清除旧查询告警。
错误例：为了通过 status 校验补造 requestId，或放宽所有 status 的 ID 校验；
正确做法是单独解析受 run/syncId 关联约束的查询拒绝。测试必须断言查询和重连
不会增加 `user_input_response` 帧数量，不能仅检查静态文案存在。
