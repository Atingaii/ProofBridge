> 公开脱敏历史记录。原始运行资料保留本机；此处保留当时的验证范围和未验收项，当前契约以[原生结构化问答](../../features/native-structured-user-input.md)为准。

# Claude Code 原生问答能力实测

日期：2026-09-24。用途：记录外部接口、CLI 与 MCP helper 的真实证据，支持[加固方案](../../features/native-structured-user-input-hardening.md)。下文前半部分保留修复前的原始观察，最新修复后结果见文末。

## 环境与边界

- 本机 Claude Code 为 **2.1.281**；此前 2.1.229 的记录属于历史，不覆盖本版本。本轮未升级全局 CLI。
- 用户先提供模型 `claude-opus-5`，随后改为 `gpt-6-sol`。二者均是请求的模型标识；兼容网关背后实际服务的模型身份未独立核验。
- 端点与密钥仅在隔离探针使用，仓库不保存私有端点或密钥。使用独立 `CLAUDE_CONFIG_DIR`、空工作目录、`--no-session-persistence`、`--setting-sources ''` 与 `--strict-mcp-config`。
- 只开放 `AskUserQuestion`，没有给予 Bash、文件修改等工具能力。基线探针关闭所有工具。模型请求串行执行；设置请求超时及进程总期限，超时回收本次进程组。
- 当前源码的 `claude-approval-mcp` 用 `GOMAXPROCS=2`、`go build -p 1`、`nice -n 10` 构建到专用临时目录，没有覆盖部署二进制。
- 实际测试路径为 **Claude CLI → 当前项目 MCP helper → 临时 Unix socket 答案接收器 → helper → 原 CLI**。接收器用固定测试答案替代真人，不经过实际 OrchestrationManager、Hub 或浏览器。故即使该路径成功，也不能宣称网页卡片端到端成功。

## 结果矩阵

| 配置／试验 | 观察 | 可得结论 |
| --- | --- | --- |
| 第一组凭据，原始 HTTP Messages 基线 | HTTP 403，响应含 `error code: 1010` | HTTP 客户端未成功；不能据此断言密钥无效 |
| 第一组凭据，`claude-opus-5` CLI 基线 | exit 1、is_error=true，网关 HTTP 502／origin_bad_gateway | 尚未获得可用模型回复 |
| 第一组凭据，default／bypassPermissions，早期 `--bare` MCP 探针 | MCP connected、tools=[]、502、零问题回调 | 没有问答证据；不能用 bare 的空工具列表判断正常模式 |
| 第一组凭据，移除 bare 的 default MCP 探针 | MCP connected、tools=[AskUserQuestion]；502、零回调 | 已确认正常启动会暴露工具，但接口故障挡住调用 |
| 第二组凭据，`gpt-6-sol`，Base URL 原样带 `/v1` | 基线与 default MCP 探针均报告模型不存在或无权限；exit 1、is_error=true | 此配置不可用；单凭错误文案不能确定是模型、权限还是路径问题 |
| 第二组凭据，移除 Base URL 末尾 `/v1`，其余基线参数不变 | exit 0、is_error=false，精确返回 `PROBE_OK` | 本次 Claude CLI 基础 Messages 调用成功；后续沿用此 Base URL 形状 |
| 第二组凭据，归一 Base URL，当前 helper，default | 真实 AskUserQuestion 含三题，socket 收到一次回调并返回单选、多选、自定义答案；原 CLI tool_result 报返回格式非法；随后模型请求超时，exit 1 | **当前 helper 的本次真实往返失败**，已超越单纯接口连通性问题 |
| 第二组凭据，归一 Base URL，当前 helper，bypassPermissions | MCP connected、工具暴露；本次模型请求超时，零问题回调、exit 1 | 本次未进入原生提问；后续包装器试验单独记账 |
| 第二组凭据，归一 Base URL，临时仅移除 structuredContent 包装器，default | 一次真实工具调用、一次 socket 回调、原工具成功、三类答案回显；exit 0、is_error=false | 候选格式修复下原生往返通过；未修项目代码 |
| 同上，bypassPermissions | 保持 bypassPermissions，仍触发 AskUserQuestion 回调并成功回显三类答案；exit 0、is_error=false | 本环境免审批模式原生问答可行；未验项目完整链路 |

早期基线曾因 CLI 的可变长 `--tools` 参数吞入 prompt 而启动失败；加 `--` 分隔后重测，启动错误不计作远端能力失败。一次 socket 路径重复的探针在启动 CLI 前终止，也不计作远端结果。

第一组网关错误时间包含 05:18:41、05:20:04、05:20:07、05:21:26 UTC。这里仅保存脱敏摘要，不复制含端点的完整错误体。

## 当前 helper 的可复核失败

default 模式的真实事件顺序：

```text
init: model=gpt-6-sol, permissionMode=default
      tools=[AskUserQuestion], mcp_servers.codex_bridge=connected
assistant: tool_use(AskUserQuestion, 3 questions)
socket: toolName=AskUserQuestion, multiSelect=[false,true,false]
socket reply: decision=accept, updatedInput={questions, answers}
user: tool_result(is_error=true, permission result shape rejected)
assistant/result: Request timed out, exit=1, is_error=true
```

原生错误全文（不含秘密）：

```text
Error calling tool (AskUserQuestion): Permission prompt tool returned an invalid result. Expected a single text block param with type="text" and a string text value.
```

固定测试答案分别为 `Red`、`["Search", "Export"]` 和 `CUSTOM_CAPABILITY_729`。题目文本由真实模型产生，答案按原始题目文本作为 key；保留完整 questions。

修复前的相关实现为 `internal/bridge/claude_approval_mcp.go:handleClaudeApprovalMCPToolCall`：当时同时返回 content 文本块和 structuredContent。仅凭这条错误不能定位字段；后续对照实验只移除 structuredContent，单选、多选数组和自定义答案均成功。该结果支持修复 MCP 返回形状，不能把“多选数组”归因为缺陷。

CLI result 可能同时出现 `subtype=success` 与 `is_error=true`；验收必须合并检查退出码、is_error、工具错误和答案回显，不能仅看 subtype。`[claude-code:unrecognized_model]` 警告也不是失败的充分条件，本次相同模型标识的基础调用已经成功。

## 复测参数与通过标准

复测在隔离目录注入 `ANTHROPIC_API_KEY`、`ANTHROPIC_BASE_URL`、`CLAUDE_CONFIG_DIR`。不在命令行、日志或文档中粘贴密钥；不要修改全局配置。当前可用配置的 Base URL 形状为 `https://<compatible-host>`，不要把面向其他 API 的 `/v1` 前缀机械照搬。

CLI 的关键参数如下；MCP 配置指向临时构建的项目 helper 和临时 Unix socket：

```text
claude -p --model gpt-6-sol --no-session-persistence
  --setting-sources '' --strict-mcp-config --mcp-config <isolated-json>
  --permission-prompt-tool mcp__codex_bridge__browser_approval
  --permission-mode <default|bypassPermissions>
  --output-format stream-json --verbose --max-turns 3
  --tools AskUserQuestion -- <three-question-test-prompt>
```

每种模式必须分别确认：工具实际暴露 → 原生发起请求 → 对应模式的回调进入 → 原始问题无损回传 → 原工具成功 → 同次 CLI 执行继续并精确回显单选、多选、自定义答案。之后另验取消、迟到答案与实际 manager／Hub／浏览器链路。模型超时、只有 MCP connected 或 mock 通过均不算完整往返。

[官方用户输入文档](https://code.claude.com/docs/en/agent-sdk/user-input)支持保留 questions 并按问题文本构造 answers；多选可以使用标签数组或逗号分隔字符串，自定义回答使用字符串。这是 SDK 契约依据，不代替本项目 MCP host 对当前 CLI 版本的兼容验证。

## 修复前发布状态（历史记录）

审查当轮没有修改 `internal/bridge/orchestration_claude.go:claudeNativeUserInputValidated`，当时返回 false。该段保留修复前决策；当前能力门禁见下方“修复后的项目 helper 复测”和加固方案实施记录。

## 临时包装器对照：default 与 bypassPermissions 均成功

包装器在项目 helper 的 stdout JSON-RPC 结果中仅删除 `result.structuredContent`，保留原 `result.content` 及其 permission decision JSON。没有修改工作区功能文件，没有改问题／答案、多选编码、permission mode 或 CLI 版本。

观察到一次真实 AskUserQuestion 和一次对应 socket 回调，原生 tool_result 接受答案，随后 assistant 精确回复：

```text
Red
Search,Export
CUSTOM_CAPABILITY_729
```

两种权限模式分别得到 exit 0、is_error=false、num_turns=2；这两次模型迭代属于同次 CLI 执行，不是新建会话补答。CLI 将多选标签数组投影为 `Search,Export`，三个测试答案均保留。

default 与 bypassPermissions 的 init 均保留各自权限模式，均实际进入 permission host 回调；免审批没有取消需求问答。脱敏实际事件见 [claude-probe-evidence.json](claude-probe-evidence.json)。

该对照确认此环境存在可行的原生 MCP 答案回路，也复核了当前 helper 的返回形状兼容问题。仍需把候选修复落实到项目源码并重新测试真实 helper、普通审批、免审批、取消和网页链路后，才可开启能力。

## 收尾与保留物

保留本文、脱敏测试事件 JSON 与加固方案。任务专用临时目录中的隔离 CLI 配置、脚本、socket、包装器和构建二进制已统一清理；检查目录已不存在且没有该目录关联的探针进程；不清理既有依赖、其他任务缓存或部署产物。

## 修复后的项目 helper 复测

2026-09-24 的 F6 实现从 `internal/bridge/claude_approval_mcp.go:handleClaudeApprovalMCPToolCall` 的 JSON-RPC `tools/call` 结果移除 `structuredContent`，仅返回单个 `content` 文本块，其中是 permission decision JSON。`internal/bridge/claude_approval_mcp_user_input_test.go:TestClaudeApprovalMCPJSONRPCSingleTextContent` 逐类校验序列化后的真实 MCP 形状，并覆盖普通审批允许／拒绝、三类问答与取消。

用修复后的**项目源码**低并发构建临时 helper，再沿用本机 Claude Code 2.1.281、隔离 runtime home、兼容 Base URL 去掉 `/v1`、`gpt-6-sol`、真实 CLI 与临时 Unix socket 答案接收器重测；没有使用此前包装器，也没有更改全局 CLI 配置。两种模式分别观察到：

| permissionMode | AskUserQuestion | socket 回调 | 三题答案 | tool_result | CLI 退出／result |
| --- | --- | --- | --- | --- | --- |
| default | 1 次 | 1 次 | 单选 Red、多选 Search+Export、自定义标记精确回显 | 无错误 | exit 0，is_error=false |
| bypassPermissions | 1 次 | 1 次 | 单选 Red、多选 Search+Export、自定义标记精确回显 | 无错误 | exit 0，is_error=false |

首次修复后探针把第三题设为零选项，被 Claude CLI 的输入验证在回调前拒绝：原生工具要求至少两个选项。该失败属于测试题目无效；将第三题改为两个选项并允许自由输入后，两种模式均通过。测试进程只输出上述脱敏计数／状态；密钥未放入命令行、仓库、日志或脱敏证据。

该结果证明本机版本的 `CLI → 项目 helper → socket → 原 CLI` 答案回路，而不是 `浏览器 → Hub → Bridge manager → CLI` 完整链路。随后 `internal/bridge/claude_hub_user_input_test.go:TestClaudeAskUserQuestionMCPThroughHubAndBrowser` 使用实际项目 helper、Bridge、Hub 和模拟浏览器完成一次问答及送达凭据同步；这仍不等于已在部署环境由真实模型驱动实际浏览器完成手工验收。当前能力门禁按 Claude Code 精确版本和权限模式开放，细节见[加固方案](../../features/native-structured-user-input-hardening.md)。此前“当前 helper 返回格式仍需修复”的句子只描述上方修复前记录，不代表当前代码状态。
