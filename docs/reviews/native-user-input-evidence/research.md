> 公开脱敏历史记录。原始运行资料保留本机；此处保留当时的验证范围和未验收项，当前契约以[原生结构化问答](../../features/native-structured-user-input.md)为准。

# 原生用户问答调研与候选计划

日期：2026-09-24。以下为最初规划时的历史调研，彼时未授权实施；后续用户已批准实施。当前状态以 [implement.md](implement.md) 为准，最新 Claude 2.1.281 实测见[能力记录](claude-capability-verification.md)，缺陷处理见[加固方案](../../features/native-structured-user-input-hardening.md)。下面“代码事实”描述改造前状态，不代表当前源码。

## 证据

- Codex [App Server](https://learn.chatgpt.com/docs/app-server) 确认 `item/tool/requestUserInput` 和 `serverRequest/resolved`；工具名和传输方法名不同。
- 本机 codex-cli 0.156.1 的 experimental schema 含 isBlocking；autoResolutionMs 已弃用。响应为 answers[questionId].answers 字符串数组，问题含 isOther/isSecret/可空 options。临时 schema 目录已删除。
- 本机 `default_mode_request_user_input` 为 under development / false；不能假定仅接通协议就允许默认模式调用工具。
- Claude [用户输入](https://code.claude.com/docs/en/agent-sdk/user-input) 确认 AskUserQuestion、canUseTool 回调、questions/answers、单选/多选/自定义文字。
- Claude [权限](https://code.claude.com/docs/en/agent-sdk/permissions) 区分 bypassPermissions（仍有用户交互）与 dontAsk。
- Claude [headless](https://code.claude.com/docs/en/headless) 与 [CLI 参数](https://code.claude.com/docs/en/cli-reference) 确认 SDK callback 和 MCP permission host；仅 stream-json 输出不构成回传接口。
- 本机 Claude 2.1.229 不能直接使用较新文档中的全部参数。优先验证现有 MCP host 能否完成 AskUserQuestion 往返；SDK 示例不能代替该路径实测。若不可用，再评估官方 SDK 使用的 stdio control_request/control_response。

## 代码事实

- `internal/bridge/appserver_runner.go:readEvents` 仅处理审批请求，未处理 requestUserInput。`handleApproval` 回答原始 JSON-RPC ID。
- `internal/bridge/orchestration_claude.go:shouldBridgeClaudeApproval` 在无审批模式关闭 host；withClaudeStreamApprovalArgs / withClaudeApprovalArgs 强加 default 模式，不能直接照搬给信任模式问答。
- `internal/bridge/claude_approval_mcp.go:handleClaudeApprovalMCPToolCall` 允许时 updatedInput 为空，未携带答案。
- `internal/bridge/orchestration.go:orchestrationApprovalRequester.RequestApproval` 注册内存 pending channel，发送并重发 approval_request。
- `internal/hub/ws_bridge.go:handleBridgeEnvelope` 向编排浏览器转发；`internal/hub/orchestration.go:handleOrchestrationWS` 校验并回传 Bridge。
- `frontend/src/app/pages/OrchestrationWorkspace.tsx:connectRun` 接收，respondOrchestrationApproval 返回；`frontend/src/app/components/chat/ApprovalCard.tsx:ApprovalCard` 只有审批决定。
- Codex 常规 NativeSession 路径在信任模式仍用 app-server；`internal/bridge/orchestration_codex.go:shouldRunCodexAppServer` 仅影响 fallback。

## 候选计划

1. 验证两种 CLI × 两种审批模式的真实请求/答案/原调用恢复；明确 Codex 默认模式能力与等待语义、Claude MCP host 可行性。
2. 设计独立 user_input_request / user_input_response / user_input_resolved；保留原生题目与响应编码，复用 Bridge—Hub—浏览器传输。
3. Bridge 生成唯一交互 ID，绑定 run/attempt/worker/turn/native request ID；处理回答、撤销与失效，不创建新上下文。
4. Hub 校验请求归属与有效性；幂等提交、多标签页同步、浏览器重连补发。是否增加持久化表取决于确认后的恢复范围。
5. 前端问题卡保留问题、选项说明、原生单选/多选、自由输入；显式提交，收到服务端确认后才完成；显示失效状态。
6. 测试信任模式、并发 worker、重复提交、刷新重连、取消、迟到回答、会话连续性；按 docs/change-impact.md 同步 feature/必要 ADR、architecture/code-map。
7. 定向测试后运行前端 build、Go 测试/构建及 make doc-lint；不做 Android 维护。

## 风险与约束

- 现审批仅以原生 request ID 为 pending map 键，跨进程可能碰撞；新交互不能照抄。
- 现前端提交即乐观完成；问答需服务端回执，重复响应幂等、首次有效提交获胜。
- Claude MCP socket 固定 10 分钟 deadline；用户等待与执行卡死超时需区分，不能静默默认回答。
- Codex NativeSession 锁持有至 turn 完成；验证等待不会堵住取消与清理。
- 浏览器刷新恢复不等于 CLI 进程死亡后恢复；失效调用不得继续作答。
- isSecret 内容遵循敏感字段边界，不进入普通日志或公开分享。

## 已决策

用户于本轮回复 A：遵循原生 blocking/non-blocking。最终契约见 design.md 与仓库 feature 文档；原生自动关闭不得标成用户已回答，客户端不得选择默认答案冒充用户决定。
