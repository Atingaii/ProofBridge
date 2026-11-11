# ProofBridge MCP、验证 Skill 与任务邮件通知

> 状态：已在功能分支实现，正在完成本地验收；未部署。日期：2026-11-06。
>
> 基线：`feat/isolated-cli-profiles-verifier-harness` @ `21f1a0b`；实现分支：`feat/mcp-skill-task-notifications`。
> 用户已授权实现和本地测试。下面保留设计契约；实际验证与部署边界见本文末尾。
> 架构决策见 [ADR-029](../adr/029-hub-mcp-and-submission-notifications.md)，
> 源码证据及历史文档差异见[调研报告](../reviews/proofbridge-mcp-current-state.md)。

## Goal

用户在网站注册并连接自己的执行机器后，从个人中心复制已填好的 MCP 配置，
安装一次验证 Skill，即可在 Codex 或 Claude Code 中用自然语言调用 ProofBridge。
系统发现用户已有端点和模型配置，应用个人偏好，在选中的用户机器上运行证明，
保存可追溯的结果，并由 Hub 在任务结束后主动发邮件。提交端不需要一直等待。

为老师的大规模实验提供相同的结构化工具接口：脚本可直接调用 MCP，按配置批量提交、
查询和收集结果，无须让另一个模型替脚本决定每次调用。自然语言是交互入口，
Hub 的实际配置、状态、验证证据才是实验记录。

## Non-Goals

- 本轮不部署、推送或升级运行中的 Hub/Bridge，不发布端点安装包。
- 首版不新增证明引擎，不在 Hub 执行用户项目，不迁移用户工作区。
- 不重做既有单聊、原生 resume、task graph、Agent 验证策略或权限配置。
- 不开放管理员工具、任意 Shell/文件读写、公共分享、模型密钥导出或模型凭据导入工具。
- 不将端点安装、重启或权限升级变成 Skill 每次提交任务的隐藏步骤。
- 不新增 Redis、第二个数据库、第二个调度器、外部 agent 框架或定时轮询自动化服务。
- 邮件首版覆盖编排任务；不把每条 1:1 聊天消息或每个内部节点都当通知任务。
- 不要求云端 Agent 能访问用户电脑的 VPN，不对外开放实验室内网服务。
- 不承诺任意自然语言必然触发 Skill，也不把运行 completed 翻译成证明通过。

## Pre-Implementation Baseline

现有机制应复用，事实依据见调研报告的源码锚点：

| 领域 | 已有 | 本次缺口 |
| --- | --- | --- |
| 账号 | 可配置自助注册、登录 JWT、用户隔离 | 个人 MCP Token、接入模板、收件邮箱 |
| 执行端 | 用户运行安装/连接命令，Bridge 反向连接 Hub，上报目录和能力 | MCP 的明确目标解析与诊断，不再依赖“首个在线端点”兜底 |
| 模型 | 账户模型库、三种 workerPair、槽位独立模型/effort、官方登录能力 | 面向 MCP 的可用选项投影、可命名执行方案、正式默认偏好 |
| 编排 | create/continue/cancel、bounded/dynamic、事件、统计、结论 | 顶层提交幂等、提交身份、有界查询工具、统一结果结构 |
| 交互 | WebSocket 审批；原生问答 v3 快照和回执 | MCP 共享路由；审批仍缺权威快照/回执，需要增量 Bridge 能力 |
| 通知 | Hub 已保存任务终态和结论 | 邮件设置、可靠终态通知 outbox、重试和投递可观察性 |
| Skill | 现有项目开发 Skill | 面向最终用户的 ProofBridge 操作 Skill 及安装入口 |

`internal/hub/orchestration.go:resolveAgentID` 的现有默认选择不理解用户当前目录。
`internal/hub/orchestration.go:resolveWorkerProfiles` 仍允许无绑定兼容路径；
网页要求完整绑定不等于所有 HTTP 调用都已强制如此。
`profile=default|formal-proof` 是领域契约，不是本文新建的个人执行方案。

官方登录、保存的 API key 预设和公共模型目录不能混用。官方 Codex 的显式 model/effort
受账户 rollout、ConfigSwitcher v3 和目标 Bridge 原生目录限制；当前不能假定官方 Claude
有同样的显式模型覆盖能力。以 `internal/hub/official_cli_models.go` 的校验为准。
Hub 的账户模型 vault 会接收并加密保存用户主动录入的 provider key；不能沿用旧简述，
宣称 Hub 从不接收任何模型凭据。

## Design

### 用户流程与部署关系

~~~mermaid
flowchart TD
    A["首次：网站注册、绑定邮箱"] --> B["执行机器：运行现有安装/连接命令"]
    B --> C["个人中心：复制 MCP 配置、安装 Skill"]
    C --> D["Codex / Claude Code：自然语言提交"]
    D <-->|"MCP / HTTPS，外部经 VPN"| H["实验室 Hub：身份、配置、调度、记录"]
    W["用户自己的 Bridge 机器"] <-->|"Bridge 主动建立反向 WS"| H
    W <--> P["该机器的项目、CLI、证明检查工具"]
    H --> N["最终提交结算 → 通知 outbox → 邮件服务"]
    N --> E["用户邮箱"]
~~~

- Hub 只增加一个受鉴权的 `/mcp` 入口，保持单 Go 模块、单二进制和 Hub 唯一 SQLite 写入权威。
- 证明运行在已连接的用户 Bridge；同机使用时“当前项目”就是该端点对应的本机项目。
  发起端与执行端也可不同，必须明确选中目标端点，不能把发起端路径交给 Hub 当本机路径解释。
- 目录发现不授予新权限。实际路径、符号链接、strict-workspace 和原生 CLI 权限继续由 Bridge 校验。
- 外部发起端和外部执行端都必须能经 VPN 到达内网 Hub；无需在用户机器开放入站端口。
- 退出发起会话不取消已接受任务；执行机器、后台 Bridge 及其 Hub 连通性仍需保持。
  长时间 Bridge 失联受原有恢复窗口约束，不能承诺无限断线恢复。
- MCP PAT、Bridge enroll token、模型 API key 是三种独立凭据。
- 网页和 MCP 调用同一套 Hub 归属、配置解析、运行状态与交互逻辑，不复刻第二套调度器。

### 一次接入

1. 保留既有注册机制，在实验室部署中明确启用自助注册；不绕过原有注册和用户隔离。
2. 注册成功或旧账户首次打开个人中心时，为该用户建立个人 MCP 接入记录。
3. 个人中心生成含规范 Hub 地址、`/mcp` 地址、个人令牌的 Codex TOML /
   Claude Code JSON 模板。用户选择客户端并复制，不手填主机名、端口或占位符。
4. 支持重新复制、撤销、重新生成；令牌重显要求见 ADR。变更后明确告知旧配置是否失效。
5. 提供一条 Skill 安装命令，默认用户级安装。安装器只安装操作 Skill，
   不覆盖现有 CLI 登录、模型配置、其他 MCP、全局 agent 指令或项目源码。
   安装脚本与文件均来自官方 GitHub 的固定 Skill 版本，校验与 TLS 边界见
   [分发契约](github-skill-distribution.md)；MCP 继续连接原 Hub。
6. 个人中心展示执行端在线状态、邮箱/邮件服务准备状态和清楚的故障排查入口。

以下为模板形状；个人中心会生成已填入实际地址和个人令牌的完整配置。
默认生成完整模板；高级用户可以自行改为环境变量取令牌。

~~~toml
[mcp_servers.proofbridge]
url = "https://<实验室Hub地址>/mcp"
http_headers = { Authorization = "Bearer <用户MCP令牌>" }
~~~

~~~json
{
  "mcpServers": {
    "proofbridge": {
      "type": "http",
      "url": "https://<实验室Hub地址>/mcp",
      "headers": { "Authorization": "Bearer <用户MCP令牌>" }
    }
  }
}
~~~

首版针对两客户端已配置静态 Bearer 的私有集成，不宣称完整实现 OAuth discovery。
令牌不能进入 Skill 正文、模型工具结果、项目仓库或日志。模板接口仅供登录用户访问、
禁止缓存；管理令牌只走网站登录会话，不能让 MCP 令牌签发更多令牌。

### 默认偏好和可命名执行方案

新增“执行方案 execution preset”，避免与已有 CLI model preset、proof-domain profile 混淆。
方案保存 workerPair、每槽模型预设/官方选项、受支持 effort、mode、profile、
verificationMode、executionMode、轮次参数及通知选择；保存配置 ID 和非秘密参数，不复制模型 key。

正式偏好保存在 Hub，Codex 与 Claude Code 共用；Skill 可以保存本地使用约定和缓存，
并通过工具读取/更新正式偏好。优先级为：

`本次明确要求 > 端点/工作目录默认 > 账户默认 > 明示的系统兜底`。

- 无歧义才自动解析；多个端点无默认、模型重名或用户要求不支持时，列出候选并询问。
- 默认轮次、模型等不能只写死在 Skill。缺少可用默认时不能随意选择“第一台机器/第一个模型”。
- 查询公共模型目录不代表用户拥有调用权限；必须合并账户预设、目标 Bridge 能力和 rollout。
- 本次明确指定的模型、验证方式或权限不可因不可用而静默替换。
- 启动前解析为完整、安全的配置快照，保存来源和方案版本。修改默认值不改变已接受任务。
- 继续已有 run 时优先使用该 run 的已保存绑定；不因用户后来修改默认而悄悄换机器、模型或上下文。
- 支持“以后这个项目按这套配置运行”。只有用户表达持久化意图，Skill 才更新偏好。
- “设为默认执行方案”与“改写机器的全局 Codex/Claude 配置”是不同操作；首版不提供后者。

### MCP 工具清单

首版注册 20 个工具，统一 `proofbridge_` 前缀；清单对应 `internal/hub/automation_mcp.go:automationTools`。
`R` 为读取；`W` 为会产生持久化或执行副作用。工具描述写用户意图、前置条件和失败影响。
读写注解按实际行为填写，不能把会修改用户项目的任务启动标为只读。

| 工具 | R/W | 主要输入 | 返回与责任 |
| --- | --- | --- | --- |
| `proofbridge_get_context` | R | 无 | 当前账号、安全的能力/部署状态、协议版本、默认引用、邮箱和通知准备状态；不返回令牌 |
| `proofbridge_list_endpoints` | R | 在线/CLI 过滤 | 本人端点 ID、目录、在线状态、CLI/权限/交互能力 |
| `proofbridge_list_model_options` | R | endpointId、可选 cli | 本人可用预设、官方登录选项、支持的 model/effort、合法 workerPair；返回约束，不返回密钥 |
| `proofbridge_list_execution_presets` | R | 可选作用域 | 可命名方案及版本，标注默认和不可用引用 |
| `proofbridge_save_execution_preset` | W | 完整方案、可选 id、expectedVersion | 创建/更新本人方案；校验槽位和模型能力，返回版本，不启动任务 |
| `proofbridge_delete_execution_preset` | W | id、expectedVersion | 删除本人方案；已启动任务的快照不变，默认引用须明确清理 |
| `proofbridge_get_preferences` | R | 可选 endpointId/cwd | 账户与项目偏好、优先级、版本、通知开关 |
| `proofbridge_update_preferences` | W | 允许字段、作用域、expectedVersion | 更新正式默认及通知开关；不能修改管理员/权限策略或任意收件人 |
| `proofbridge_preview_run` | R | prompt、目标、方案引用/显式覆盖 | 不启动证明/业务 turn，返回解析后的安全快照、限制和待确认项；不要求用户手动再点确认 |
| `proofbridge_start_run` | W | clientRequestId、prompt、明确目标/方案、monitoringMode | 快速接受任务并返回 runId、submissionId、配置快照、内网结果链接；不等整个证明 |
| `proofbridge_continue_run` | W | runId、明确 operation、文本、请求身份 | 在同一 run 后续提交或追加动态指令，保留各自身份与投递语义，见下文 |
| `proofbridge_list_runs` | R | 端点/状态/query 过滤、游标 | 本人历史任务摘要和分页信息，不返回全量长日志 |
| `proofbridge_get_run` | R | runId、可选 submissionId | 当前状态或指定提交的冻结结果、验证结论和证据；交互与通知通过专用工具查询 |
| `proofbridge_get_run_events` | R | runId、afterSeq、limit | 有界事件页、下一游标和截断标记；敏感交互不从日志重建 |
| `proofbridge_wait_run` | R | runId、afterSeq、可选 submissionId、timeoutMs | 至多 20 秒的观察，返回变化/结束/无变化；取消等待不取消任务 |
| `proofbridge_cancel_run` | W | runId、clientRequestId | 复用取消状态机，返回取消中或已取消；不声称进程已立即退出 |
| `proofbridge_get_run_interactions` | R | runId、只读查询身份 | 从活着的 Bridge 查询审批和原生问题、版本与已有回执；不附 PendingSubmissions，不执行封禁对账 |
| `proofbridge_respond_run_interaction` | W | operation、kind、runId、request/instance/execution 身份、decisionId 或答案提交身份 | operation=answer/decide 回答原请求；operation=reconcile 显式对账；返回可证明的投递/封禁状态 |
| `proofbridge_get_run_statistics` | R | runId、可选提交边界 | 用量、缓存、用时、估价来源及不完整标记；估价不是账单 |
| `proofbridge_get_notifications` | R | 可选 runId/submissionId、limit | 本人通知的 queued/sending/sent/retry/failed、发送尝试和安全故障原因 |

`continue_run.operation` 必须明确：
- `follow_up`：只对可继续的终态 run，形成新 submission，复用 runId、agentId、上下文。
- `live_instruction`：只对支持的活动 dynamic run，明确 deliveryMode，复用原有 messageId/回执。
  它不是新一轮顶层提交；失败不能自动改成 follow_up 或新建 run。
- 回答审批/问题必须使用交互工具，不能转成普通 prompt。

交互读取与对账不能混为一谈：现有 `internal/bridge/user_input.go:UserInputSync`
在带 PendingSubmissions 时可能写 not_sent receipt 并封禁旧 ID，因此 reconcile 属于 W。
只读工具只能使用不含待核对提交的快照请求，返回未知时保持未知，不隐式写状态。

`monitoringMode=background-mail|interactive` 是新增的观察/通知选择，
与已有 bounded/dynamic 的 executionMode、collaboration/debate 的 mode 分开。
background-mail 明确包括完成邮件和需要人工处理的 attention 邮件；Skill 可快速返回。
interactive 由用户留在终端查看并处理交互，不承诺退出后的邮件提醒。

上表覆盖用户日常发现、配置、执行、交互和查结果。模型库增删/密钥测试、
机器安装/修复、账号邮箱地址修改、令牌管理继续由网站承担；首版不暴露万能管理工具。
后续新增工具需有具体用户操作和端到端用例，不能机械镜像所有内部 HTTP handler。

### 工具契约、提交幂等与结果

- 所有调用从令牌解析 uid，逐层检查端点、模型预设、run、提交和通知归属；不接受客户端指定 uid。
- 使用 schema 校验与结构化结果，并提供兼容的简短文本摘要。每次结果包含协议/结构版本。
- 限制返回条数和正文长度，长日志分页；返回 afterSeq、hasMore 等真实边界，不把截断当完整记录。
- `preview_run` 可检查配置和已报告能力，必要时调用现有只读模型 metadata 探针，
  不执行用户项目命令，也不能假装读取 Hub 不拥有的用户文件系统。
  真实目录/命令门禁在 Bridge 启动时执行；启动前重新验证过期能力和方案版本。
- `start_run` / 终态 `follow_up` 使用持久化 clientRequestId 与规范化请求摘要。
  相同用户、操作、键、摘要返回原接受结果；同键不同内容返回 REQUEST_CONFLICT。
- 摘要针对用户本次显式输入和文本；已接受请求先查幂等记录，不重新读取后来改变的默认值
  来计算冲突。第一次解析的实际配置另存快照，重试返回该快照，不启动第二份任务。
- accepted 表示 run、该次 user.message/PromptSeq、submission 关联及可恢复派发责任已经持久化。
  若创建分步失败，不得把半成品冒充接受成功；补偿/恢复保持原请求身份，不另建 run。
- 接受状态至少区分 validating、accepted、dispatch_unconfirmed、terminal。
  网络超时后用原键查询，不自动创建新 run；不确定的远端执行不盲目重放。
- 新增稳定 submissionId，将其映射到 runId + 已持久化 user.message 的 PromptSeq。
  不是沿用某个内部 taskId，也不是复用原生问题 answer submissionId。
- runId 负责上下文连续性；submissionId 负责每次顶层要求、配置和通知边界。
- MCP 请求 context 只约束本次调用。已接受工作由现有 Hub/Bridge 生命周期持有；
  wait 超时、SSE 断开、退出 Codex 都不等同 cancel_run。
- 身份失败返回 401，权限不足返回 403；业务错误提供稳定 code、可理解说明和下一步。
  证明没通过属于任务结果，不能伪装成 MCP 通信错误。
- 关键错误包括 TARGET_AMBIGUOUS、ENDPOINT_OFFLINE、MODEL_UNAVAILABLE、
  REASONING_UNSUPPORTED、CAPABILITY_UNAVAILABLE、REQUEST_CONFLICT、
  DISPATCH_UNCONFIRMED、INTERACTION_UNAVAILABLE、DELIVERY_UNKNOWN、MAIL_NOT_CONFIGURED。

结果契约如下；实际结构对应 `internal/store/automation.go:AutomationResult`：

~~~json
{
  "schemaVersion": 1,
  "runId": "<run>",
  "submissionId": "<top-level-submission>",
  "execution": { "status": "completed", "terminalReason": "stalled" },
  "verification": {
    "authority": "agent",
    "status": "continue",
    "summary": "指定目标仍有未完成证明义务",
    "checks": [],
    "unmetObligations": ["<target>"]
  },
  "configuration": { "endpointId": "<owned-endpoint>", "workerPair": "codex-codex" },
  "finishedAt": 0
}
~~~

get_run 在 result 字段返回该冻结结果，外层附当前 run、submission 与 resultUrl。
notification 状态通过 get_notifications 读取；待答状态通过 get_run_interactions 获取权威快照。
execution、verification 和 notification 是三个独立维度。Agent 审核结论保留来源；
MCP 不重新投票、不把编译成功扩大成语义证明、不新增“无条件成功”判断。

### 人工审批、问答与内部 worker 隔离

- 原生问题复用现有 v3 sync/receipt/reconcile。将 Hub 的归属和协商逻辑从 BrowserConn
  路由中抽取共享处理，MCP 不绕过 nativeUserInputProtocolVersion 和 CLI 能力门禁。
- 当前审批只有 Bridge 内存请求、重发和 requestId/decision，没有权威快照/ACK。
  完整 MCP 审批需增加 Bridge 报告的自动化交互能力、带运行/执行代次/实例身份的快照和回执。
  只缓存 Hub 曾收到的审批帧不足以证明请求仍待决。
- 旧 Bridge 不支持可靠审批查询时明确返回能力限制/升级或网站处理入口；不能偷偷切换成免审批。
  正式“日常无需网页”验收必须使用支持该增量能力的 Bridge，Hub 更新不自动升级它。
- 题目/答案和原生回调仍以活着的 Bridge 为权威，不写入 SQLite 事件历史或邮件正文。
- queued、完整写出、原生关闭和模型处理分别表达。结果 unknown 时不重复批准/改答；
  只有权威 not_sent、旧提交已封禁且请求仍 open 时才能明确重试。
- 用户选择的权限配置保持不变。无人值守需要预先选择适用权限并提供完整任务输入，
  邮件和 MCP Token 不授予更高的执行权限。默认 review-required 端点可能持续等待审批，
  不能把“后台提交成功”当成“无需后续人工操作”的保证。
- 外层入口 Skill/MCP 必须在内部 Codex/Claude worker 中精确隔离，尤其官方登录复用配置路径。
  保留既有 Claude 审批 MCP；不能通过全局关闭 MCP 来实现。
  Skill 触发描述也应排除托管 worker，但只靠提示词不能作为隔离验收证据。

### 事件触发的邮件

个人中心新增收件邮箱、任务结束通知开关和投递状态。邮件服务由部署方一次配置。
缺少邮箱不阻断原有注册；用户明确要求邮件而邮箱/服务未准备好时，在提交前说明并给出设置入口，
不能接受后悄悄忽略。任务已接受后发生投递故障，不改变证明结果。

首版通知范围：
- 已启用用户的顶层编排提交结束：验证通过、未通过、取消、执行失败/超时均保留真实原因。
- 网页与 MCP 进入同一套结算流程；普通聊天和内部节点不发送结束报告。
- “需要人工处理”是非终态。background-mail 模式必须包含 attention 通知：
  任务产生审批或原生待答请求时，主动发待处理入口，不含问题、答案、命令正文。
  同一请求重发不重复入队，已关闭的请求在发送前取消待发提醒；它不能冒充任务完成。
- 若用户关闭 attention 通知，则不得仍以“退出终端也会获知待办”承诺 background-mail。
  提交前应说明限制并让用户选择启用提醒或使用 interactive，不能静默代答或升级权限。
  用户中途关闭通知时，个人中心同样明确提示对现有后台任务的影响。

结束通知从 Hub 整次提交的最终结算生成，包括旧 relay、durable graph、早结束/停滞、
取消确认/超时、Bridge 离线超时和 Hub 启动恢复。不能只 hook 原始 Bridge run.end。
同一事务冻结本次 execution/verification 结果和 outbox，邮件 I/O 不进入数据库事务。
既有多处结算需共享同一结算方法；迁移中如有历史非原子路径，必须有可靠补偿与重启补扫。

- 结束通知唯一键包含用户、run、submission、通知类型和 channel；attention 再区分
  原生请求/执行实例身份。继续同 run 能发新报告，下一待答请求能发新提醒；
  同一请求重发、节点重复事件、重连和重启不重复入队。
- outbox 保存本次结果与收件人快照；不能等发送时再读已被后续提交覆盖的当前 run。
- 后台发送器在 Hub 内运行，SQLite 保存 claim/lease、次数、下次尝试、状态和有界错误。
  bounded backoff 重试；永久失败可在个人中心和查询工具中发现，不要求终端在线。
- 发送前检查用户当前是否已关闭通知；取消不必要的未发通知。地址改变不静默重定向旧任务，
  按其已记录收件策略处理，并在界面清楚展示。
- SMTP sent 仅代表邮件服务接受。采用稳定 Message-ID 和应用去重尽量避免重复；
  SMTP 接受后 ACK 丢失仍可能重复投递，不承诺跨 SMTP/SQLite 的绝对 exactly-once。
- 邮件包括任务、端点名称、提交编号、执行状态、独立验证结论、用时、简要未完成项及本人站内链接。
  不附模型 key、MCP Token、原生问答/审批正文或默认整份项目/日志附件。
- 邮件主题明确区分“运行结束，验证未通过”和“验证通过”。实际收信可不经 VPN，
  邮件结果链接仍要求 VPN 和本人账号，不自动建立公开 share。
- 实验室部署须验证邮件服务出站连接；云端模型的出站连接也与用户访问 Hub 的 VPN 路由分别验收。

### Skill 的完整职责

首版使用同一套流程内容，按两个客户端支持的目录和配置安装，不维护两份不同产品规则。
个人中心给出可直接执行的安装命令；固定安装产物版本、可检查的来源及不破坏现有文件的更新方式。
具体目录按实现时两客户端版本实测，不把本项目开发用 Trellis Skill 当最终用户 Skill。

执行顺序：
1. 仅在用户明确希望使用 ProofBridge 或请求受支持的自动证明服务时匹配；保留显式调用方式。
2. get_context → 发现目标/模型/方案与偏好；可缓存 metadata，不能缓存授权结果。
3. 根据用户要求解析配置，只有影响目标的歧义才追问。读取 README 是目标 Bridge 的工作；
   同机发起端可辅助理解，但不能默认发起端和执行端文件相同。
4. preview 并校验 → 带稳定请求键 start，回显真实目标、模型/effort、验证方式、任务号与通知状态。
   preview 不是额外的人工审批环节。
5. 用户选择“提交并邮件通知”时使用 background-mail，确认完成和 attention 提醒均可用后
   快速返回。用户要求留在终端时使用 interactive、有界 wait 和分页事件；不额外循环空等。
6. 支持查历史、查证据、统计、取消、同 run 继续、动态追加和原生交互；正确保留每种身份。
7. 只有用户明确要记住配置时更新偏好。结果来自后端，不由 Skill 自行宣布证明通过。
8. 终端退出后的主动邮件由 Hub 承担。Skill 不创建 Codex 定时任务，不依赖无限轮询，
   也不能保证给已退出的终端自动回显。

## Data And Protocol Impact

新增持久化对象（实际表名以架构文档为准）：
- 用户个人 MCP 接入：用户、令牌 ID、摘要/受保护密文、状态、签发/撤销信息。
- 用户/端点目录偏好和执行方案：版本、作用域、配置引用；与已存在的模型预设表区分。
- 顶层自动化提交及幂等记录：uid、operation、clientRequestId、请求摘要、runId、
  submissionId、PromptSeq、接受/派发状态、安全配置快照和冻结结果。
- 用户邮箱/通知设置与通知 outbox：收件人、提交、类型、结果快照、lease/retry/delivery。
- 交互身份和回执的最小查询信息；不持久化原生问题/答案正文，不替代 Bridge 状态。

新 HTTP 入口为 `/mcp`；网站新增个人接入、偏好及通知设置管理入口，浏览器管理仍用原 JWT。
Bridge 协议仅为可靠审批的缺失快照/回执增加受能力门禁的增量；问答 v3 和任务原有帧继续复用。
首版不新增通用 Shell、文件系统 RPC 或新的执行调度协议。

## Implementation Steps

1. 先完成本设计、ADR 和源码核对；用户授权后在独立功能分支实施。
2. 提取可复用 Hub 操作：现有 HTTP 和 MCP 共用归属、配置解析、提交、继续、交互和最终结算。
3. 落地个人令牌、方案/偏好、提交身份/幂等和通知 schema；覆盖旧数据迁移与备份/回滚。
4. 接入 MCP transport、鉴权、schema、20 工具；先跑通发现 → 预览 → 提交 → 本机执行 → 结果。
5. 补可靠审批快照/回执能力，复用原生问答 v3，完成内部 worker 入口隔离。
6. 接入所有最终结算路径、邮件 outbox、发送器、邮箱与通知设置，证明终端退出不影响通知。
7. 个人中心生成两客户端模板及 Skill 安装命令；发布同一操作 Skill 的两端安装产物。
8. 完成统计/历史/继续/错误路径和脚本使用示例；执行分层验收，再单独安排实验室试部署。

实施文件范围和逐步验证清单保存在本地 Trellis 任务的 implement.md；跨层影响表见
[Change Impact](../change-impact.md)。不能把本设计的存在记为功能已完成。

## Exit Gates

以下为验收条件；本地测试与外部部署验收应分开记录，见末尾验证表。

| 编号 | 可观察完成条件 |
| --- | --- |
| AC01 | 新用户注册、接入本人端点后可直接复制有效客户端配置；旧用户无需重建账号或机器 |
| AC02 | Codex / Claude Code 各自用真实客户端发现工具、自然语言调用和显式调用成功 |
| AC03 | 两个账户交叉猜测端点/模型/方案/run/submission/通知/交互 ID 均无法越权 |
| AC04 | “当前项目”唯一绑定正确用户端点；多目标无默认时询问；Hub 不读取或执行该项目 |
| AC05 | 三种 workerPair、受支持 model/effort、官方登录和 Agent 验证门禁保持准确 |
| AC06 | 两客户端共用偏好；明确覆盖只影响本次；默认修改不改变旧运行或继续绑定 |
| AC07 | 同请求重复/并发/响应丢失/重启不重复启动；同键不同内容冲突；unknown 不盲重派发 |
| AC08 | follow_up 沿用 runId；live 指令保留 messageId/投递回执；问答不变成新 prompt |
| AC09 | 人工审批/多题问答在终端完成；旧能力明确拒绝；失效/乱序/unknown/双提交不重复决策；R快照不改变receipt，W对账才可封禁旧ID |
| AC10 | 托管 worker 不再递归启动 ProofBridge；既有 Claude 审批 helper 和原生 resume 仍可用 |
| AC11 | background-mail 下退出CLI后仍执行并发信；之后出现审批/待答会主动提醒，可恢复原请求；执行机器失联按原恢复语义结算 |
| AC12 | 普通/早结束/停滞/取消/失败/离线/Hub 恢复都生成正确整次提交报告，节点结束不误发 |
| AC13 | 同 run 第二次顶层提交有独立邮件；后续运行不改写旧邮件；SMTP 故障可重试且可见 |
| AC14 | completed + continue/unknown 不显示“证明通过”；邮件、网页、MCP 结果一致 |
| AC15 | VPN 开/关/重连分别测试发起端与执行端；内网链接需本人登录；无公网绕过入口 |
| AC16 | 结果分页/游标、用量不完整/估价标签准确；脚本至少重复运行两个任务并可核对实际配置 |
| AC17 | 真实小型证明任务、本机编译器/检查器证据、邮件实际收件和两客户端链路分别留证 |
| AC18 | 定向测试、共享 Go 测试/构建、前端测试/重建 embedded UI、文档检查按影响范围通过 |

## Reviewer Q&A

**为什么不是把整个后端逐个包装为 MCP？**
用户需要发现、选择、提交、交互、收结果的一条路径。20 个工具覆盖这些动作，
把凭据维护、机器修复和管理员运维留在已有网站，避免暴露大量无关底层细节。

**用户必须把项目上传到实验室服务器吗？**
不用。选中的 Bridge 继续在用户接入的机器和工作区执行；上传机制只在用户明确需要时复用。

**为什么不让 Skill 自己永久记住所有模型？**
模型库、权限和能力会变化。Skill 负责流程，Hub 保存正式偏好，工具提供当前可用 metadata。
这样跨客户端一致，也能在实验记录中冻结实际配置。

**为什么“邮件只改 Hub”仍可能不够？**
邮件发送本身属于 Hub；完整终端交互还缺可靠审批快照/回执，且内部入口隔离涉及 Bridge。
应明确组件版本依赖，不假装一次网站部署已经升级所有用户端点。

**运行被标记 completed，可以发“证明成功”邮件吗？**
不能。保留本次独立验证状态、authority 和证据。停滞、轮次结束、通信失败和证明确有缺陷分别报告。

**客户端掉线后是否无条件持续运行？**
发起客户端退出不取消已接受任务；Bridge 或 CLI 死亡、执行端 VPN 长期断开仍受既有失败恢复约束。

**完成邮件是否足以替代用户守着终端？**
不足以覆盖中途需要人工输入的任务。background-mail 必须同时提供 attention 提醒和恢复入口；
用户也可明确选择 interactive。两个模式均不自动审批或猜答案。

**是否需要现在决定 SMTP 供应商或开放公网？**
不需要。本设计用可配置邮件发送实现，验收时验证实验室出站和真实收件。
Hub/MCP 保持实验室内网访问，外部客户端及外部 Bridge 使用 VPN。

## Source Reading And Validation Record

本次阅读了 README、架构、代码地图、变更影响表、开发/部署流程，以及注册隔离、
端点连接、模型库与官方登录、独立 worker、同 run 连续性、shared CWD、
durable graph、dynamic input、原生交互 v3、Agent 验证、完成结论等文档。
具体源码证据和过时表述以[调研报告](../reviews/proofbridge-mcp-current-state.md)为准。

2026-11-06 设计阶段查阅的一手协议/客户端资料；实测版本见本地验收报告：
- [MCP transport overview](https://modelcontextprotocol.io/specification/2026-07-28/basic/transports)：stdio 与 Streamable HTTP；本设计选择内网 HTTP。
- [MCP tools](https://modelcontextprotocol.io/specification/2026-07-28/server/tools)：工具 schema、结构化结果、显式 handle；应用任务身份独立于连接。
- [MCP authorization](https://modelcontextprotocol.io/specification/2026-07-28/basic/authorization)：HTTP 鉴权规范；静态 PAT 首版不冒充完整 OAuth 实现。
- [Codex MCP](https://learn.chatgpt.com/docs/extend/mcp?surface=cli)、[Claude Code MCP](https://code.claude.com/docs/en/mcp)：两客户端 HTTP/Bearer 配置支持。
- [Codex skills](https://learn.chatgpt.com/docs/build-skills)、[Claude Code skills](https://code.claude.com/docs/en/skills)：自然匹配和显式调用需分别测试。

以下仅保留实施前的文档检查历史；本地实现和运行证据见后续模块与验收记录。

设计阶段文档检查记录（历史）：
- `make doc-lint`：0 errors，保留 8 个既有 warnings，未新增 warning。
- `git diff --check`、新增/修改文档的相对链接与空白检查通过；20 个唯一工具、18 个验收条目和规划引用检查通过。
- 独立复核的“对账并非只读”和“后台待答不能静默等待”两项已修正，并再次复核通过。
- 工作区产品树仅有文档变化；没有生成 Python 缓存、启动临时服务或创建需删除的构建产物。


## Implemented Modules And Local Acceptance

| 层 | Owning source |
| --- | --- |
| 官方 MCP Go SDK v1.8.0、20 工具及每请求鉴权 | `internal/hub/automation_mcp.go:newAutomationMCPHandler` |
| 个人 Token、完整模板、偏好和版本校验 | `internal/hub/automation_access.go:handleAutomationAccess` |
| 安全配置解析、幂等提交和同 run 继续 | `internal/hub/automation_runs.go:resolveAutomationRun`、`internal/hub/automation_operations.go:createOrchestrationOperation` |
| 有界事件与等待、安全结果、已有统计 | `internal/hub/automation_observation.go:automationRunResult` |
| 审批和问答共享路由 | `internal/hub/automation_interactions.go:automationGetInteractions`、`internal/bridge/automation_approvals.go` |
| 配置/提交/结果/outbox | `internal/store/automation.go`、`internal/store/automation_notifications.go` |
| 邮件消费与 SMTP | `internal/hub/automation_notifications.go:runAutomationNotifications`、`internal/notify/smtp.go` |
| 个人中心及 Skill 安装 | `frontend/src/app/components/AutomationSettings.tsx`、`internal/automationassets/assets.go:Handler` |
| 无模型批量实验入口 | `scripts/mcp-batch.py` |

MCP 新提交及 follow_up 要求 Bridge capability `automationInteractions >= 1`。
当前该能力包同时包含受管 worker 的外层入口隔离；旧 Bridge 可继续原有网站用法，
MCP 会明确要求升级，而不会在旧自动执行端点中冒险递归。审批模式还须满足已有原生问答 v3
和账户 rollout 门禁。native-default 模型可用，但快照标记实际使用原生默认，不能伪造固定模型。

`/mcp` 采用无状态 Streamable HTTP 与 JSON 响应。GET/DELETE 由 SDK 返回不支持，
客户端不应假定事件流推送；业务状态由 SQLite 中的 run/submission 保存。
偏好账户作用域返回完整缺省值；未设置的 workspace 作用域返回空配置，不覆盖账户默认。
read 工具不回传原生 resume 命令、thread/session ID 或模型凭据。
结果分页最多 100 事件、软 128 KiB 响应预算，单事件投影截断带序号标记；
nextSeq 仅推进实际返回的事件。统计不能可靠归属某一提交时返回 incomplete，不能把 run 总量
冒充该次提交用量。通知查询提供有界最近记录，首版不支持游标。

### Batch Example

在清单中保留稳定 clientRequestId；重试同一清单会返回原提交。Token 通过环境变量
`PROOFBRIDGE_MCP_TOKEN` 提供，不写入清单或命令参数；URL从个人中心复制。

~~~json
[
  {"clientRequestId":"experiment-001","endpointId":"<本人端点ID>","cwd":"<该机器的项目路径>","prompt":"请阅读 README 的要求，完成项目的验证","profile":"formal-proof","workerPair":"codex-codex"},
  {"clientRequestId":"experiment-002","endpointId":"<本人端点ID>","cwd":"<该机器的项目路径>","prompt":"按 README 独立重复验证并报告证据","profile":"formal-proof","workerPair":"codex-codex"}
]
~~~

~~~bash
python3 scripts/mcp-batch.py --url 'https://<实验室Hub地址>/mcp' --manifest experiments.json
~~~

脚本先输出每次接受的 ID，再汇总配置快照与冻结结果；退出码 0 表示报告完整，不代表每个证明均通过。`--submit-only` 立即返回，
`--max-wait-seconds` 限制观察总时间。超时不取消任务。相同目录的多个运行会修改同一工作区，
实验对比应由用户准备独立目录或顺序执行；脚本不隐式复制项目、清理结果或切换原生全局配置。

### Validation Boundary

实际命令、版本与证据边界见[本地验收报告](../reviews/proofbridge-mcp-local-validation.md)。

本地证据区分：协议与 Store/Hub/Bridge 测试、真实 Chrome 设置页、两种真实原生客户端
的 MCP 连接、固定 CLI transport fixture 调用实际 Coq、本地 SMTP TCP 收件。
客户端连接成功不代表模型已自然语言路由，也不等于整个原生证明会话已经实测。
实验室 VPN 开关/重连、部署地址和实际外部邮箱投递需使用部署环境继续验收；本次不修改在线实例。

内网回环接入可用 automation.mcp_url 独立配置 MCP 基址，automation.public_url 保留浏览器结果与邮件地址。
MCP override 仅接受 HTTPS 或 HTTP 回环；显式回环 override 下，浏览器结果地址可为 HTTP 私有 IP。
默认未配置 override 的部署保持原 HTTPS/回环校验，见[内网部署](../deployment-8088.md)。
