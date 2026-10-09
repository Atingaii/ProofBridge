> 公开脱敏历史记录。原始运行资料保留本机；此处保留当时的验证范围和未验收项，当前契约以[原生结构化问答](../../../features/native-structured-user-input.md)为准。

# Research: Codex 原生异步结构化提问契约

- Query: `request_user_input_async` 和“Queued follow-up inputs”如何经 Codex app-server 传递、回答，并判断实验站现有断点。
- Scope: mixed（本机 CLI/schema/源码与 Codex 官方源码）
- Date: 2026-09-24

## Findings

### 版本与官方契约

- 本环境 `codex` 报 `codex-cli 0.156.1`；`claude` 报 `2.1.281 (Claude Code)`。`internal/bridge/client.go:440` 起的版本检查也锁定这两个版本。此处未检查目标远端机器 的实际版本。
- 本机 `codex app-server generate-json-schema --experimental` 导出的 0.156.1 schema 含 `item/tool/requestUserInput`（阻塞服务器请求）及其 `answers` JSON-RPC 响应；另含 `ThreadItem` 的 `agentMessage` 变体，字段 `delivery` 可为 `async`、`questions` 为 `{title,options?}[]`。`request_user_input_async` **不是** `item/tool/requestUserInput` 的别名。
- **版本已核实**：以下源码均可在与本机 CLI 对应的官方 [`rust-v0.156.1` tag](https://github.com/openai/codex/tree/rust-v0.156.1) 找到。其 [async handler](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/core/src/tools/handlers/request_user_input_async.rs#L22) `TOOL_NAME` 为 `request_user_input_async`；`RequestUserInputAsyncHandler::handle`（同文件第 129–132 行）用原工具 `call_id` 建立 `AgentMessageItem`，设 `delivery=Async` 与 `questions`，依次发 `turn_item_started`、`turn_item_completed`，随后立即返回 `{"accepted":true}`。对应 app-server 通知是 `item/started` 与 `item/completed` 的 `agentMessage` item，而非服务器请求/回调；官方 [协议类型](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/protocol/src/items.rs) 定义 `AgentMessageDelivery::Async` 与 `AsyncUserInputQuestion`。
- [0.156.1 工具注册](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/core/src/tools/spec_plan.rs#L1115-L1145)：同步 `request_user_input` 看 `experimental_request_user_input_enabled`；异步工具独立地要求 root agent 且 `model_info.experimental_supported_tools` 中有 `request_user_input_async` 或旧名 `send_user_message_async`。单开 `default_mode_request_user_input` 并不能保证模型拿到 async 工具；当前版本未找到可单独开启 async 的 app-server 参数。模型目录是否为本次会话启用了 async，须查模型实际 catalog/工具列表，不能从无警告推断。
- [Codex TUI 异步问题状态](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/tui/src/bottom_pane/async_questions/state.rs#L9-L19) 将问题 ID 编码为 JSON 数组 `["request_user_input_async", message_id, index]`，与用户截图里的 `questionItemId` 完全一致。它将回答渲染为含该 ID 的 `AnsweredQuestion` 文本，交给 `QuestionSubmission::Submit`（同文件第 135–143 行），不是返回先前的工具调用。`Queued follow-up inputs` 是 [TUI 展示标题](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/tui/src/bottom_pane/pending_input_preview.rs)，不是 app-server 方法。
- [0.156.1 回答文本格式](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/context-fragments/src/answered_question.rs#L39-L55)：`<send_user_message_question_reply>\n[{"answer":"选项或自定义答案","question":"原题标题","questionItemId":"[\"request_user_input_async\",\"call_id\",0]"}]\n</send_user_message_question_reply>`。外层 `questionItemId` 是字符串，内层是 JSON 数组的序列化字符串；原题仅保留前 512 个字符（换行改空格），ID 最长 512 字符，否则降级为纯文本。
- [0.156.1 TUI 提交](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/tui/src/chatwidget/questions.rs#L112-L155) 把回答当普通用户文本送入队列/提交。其 [app-server 路由](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/tui/src/app/thread_routing.rs#L751-L888) 在原 turn 活跃时调同线程 `turn/steer`；原 turn 已结束时调同线程 `turn/start`。本机 schema：`turn/steer` 必填 `threadId`, `expectedTurnId`, `input`；`turn/start` 必填 `threadId`, `input`；`input` 的文本项是 `{type:"text",text:"上述 envelope"}`，可省略 `text_elements`。两者均可带 `clientUserMessageId` 便于去重。
- [0.156.1 TUI 状态](https://github.com/openai/codex/blob/rust-v0.156.1/codex-rs/tui/src/bottom_pane/async_questions/state.rs#L147-L206) 根据用户消息中的 `questionItemId` 清理待答问题；未展开的问题有 30 秒自动处理计时（同文件第 14 行），展开后可取消该计时。**这是 TUI 自己的 UI 策略，不是 app-server 答案到期或原 turn 必须存活的协议约束**。独立 web UI 需定义清理策略；至少应在答复成功、显式取消、线程终止等时转移状态。

### 项目现状与确定断点

- `internal/bridge/appserver_runner.go:578` 只转发阻塞式 `item/tool/requestUserInput`；`internal/bridge/appserver_runner.go:647` 的 `item/completed` 分支只取 `agentMessage.text`，丢弃 `delivery/questions`。因此即使 CLI 发来 async 问题，目前也只能显示文字；不能形成结构化前端卡片。这是代码可直接证实的断点，并非上次运行已发出 async 事件的证据。
- `internal/bridge/appserver_runner.go:489` 在原生 turn 结束时关闭注册的阻塞式请求；async 问题从未注册到该集合，不能用该生命周期作 async 契约。
- `internal/bridge/appserver_runner.go:139` 仅当 `req.NativeUserInput` 时启用 `default_mode_request_user_input`；该开关针对阻塞式工具，不能据此推断 async 工具一定暴露。`internal/bridge/orchestration_codex.go:39`、`internal/hub/orchestration.go:1541` 的 v3/模式/功能旗标是现有产品路径的前置条件。`frontend/src/app/pages/OrchestrationWorkspace.tsx:243` 的提示只在特性可见时出现，故“无警告”不能证明当前运行已启用 v3。
- `internal/bridge/codex_user_input.go:82` 的提示仅提 `request_user_input`，并要求同一原生 turn 继续；这与 async 的事件形态不同，但不能单独证明上次模型为何只打印问题。

## 可实现建议

1. 在 app-server `item/completed` 的 `agentMessage` 分支识别 `delivery=async` 与 `questions`，把结构化问题向 Hub/UI 传送，保持 `message_id + index` 映射和选项/自定义输入。不要把它伪装成 `item/tool/requestUserInput` 或等待原 JSON-RPC 回调。
2. 对异步回答按官方 TUI 的 envelope 生成含 `questionItemId` 的用户文本项；同线程活跃 turn 走 `turn/steer`，已结束走 `turn/start`（必要时沿用当前产品连续运行的同 run/thread 保持语境）。此处不应套用同步请求的 `answers` 响应体。
3. 对同步工具与异步消息分别建状态机与测试；审查 v3 协议/持久化/前端能力开关以及 turn 结束后的待答保留。未拿到实际目标机事件日志前，保留纯文字回退。

## Files found / related specs

- `internal/bridge/appserver_runner.go:647`：现有 app-server agentMessage 仅文本化。
- `internal/bridge/codex_user_input.go:82`：现有同步工具提示与答复协议。
- `internal/bridge/orchestration_codex.go:39`：Bridge 能力闸门。
- `internal/hub/orchestration.go:1541`：Hub v3 payload 决策。
- `frontend/src/app/pages/OrchestrationWorkspace.tsx:243`：结构化能力警告可见性。
- `.trellis/spec/backend/index.md`、`.trellis/spec/frontend/index.md`、`.trellis/spec/guides/index.md`、`.trellis/spec/guides/cross-layer-thinking-guide.md`：跨层规范索引。
- `.trellis/tasks/09-24-native-user-interaction/{prd.md,design.md,implement.md}` 与 `docs/features/native-structured-user-input-runtime-plan.md`：本任务既有计划；需按已证实 async 契约校正同步式假设。

## Caveats / Not Found

- 未调用模型、未重放用户实验；无法确认上次 `codex-a` 运行是否真的执行了 `request_user_input_async`，或者只依照提示输出自然语言问题。输出时间线没有原始 app-server `item/completed` 参数。
- 官方 0.156.1 tag 与本机生成的 0.156.1 schema 契约一致；仍未检查目标远端机器是否同版本、实际模型目录是否向该会话暴露 async 工具。不可把普通 Responses API 的工具列表当成 Codex CLI 内置工具清单。
- `turn/start` 回答原 turn 已结束的问题是 TUI 的实际路径；跨 CLI 进程重启、Bridge 断线重连后的自动恢复与重复提交语义仍需结合产品持久化设计验证。TUI 的本地 `seen_ids`/`answered_ids` 不能直接等同于服务端持久化。
- Claude `AskUserQuestion` 对应路径本轮仅以现有 MCP helper 代码间接核对，未调用 Claude 模型，也未研究其实际工具暴露条件。
- 本轮生成的 `_codex_schema_probe` 仅为临时本机 0.156.1 schema 产物；已提取上述字段并清理该目录内 436 个 JSON 文件和空目录，未碰预存数据。
