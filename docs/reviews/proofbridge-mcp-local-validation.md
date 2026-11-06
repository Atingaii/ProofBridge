# ProofBridge MCP 本地验收

日期：2026-11-06。实现分支：`feat/mcp-skill-task-notifications`。
设计契约：[MCP、Skill 与任务通知](../features/mcp-skill-task-notifications.md)、[运行详情、名称与邮件接入](../features/mcp-onboarding-and-mail-setup.md)。
代码已实现并在本机 8088 运行。本文区分代码检查、真实 MCP 调用、Skill 加载、证明与邮件；不代表实验室部署已经完成。

## 环境与门禁

| 项目 | 本地结果 |
| --- | --- |
| Go 全量测试 / vet / 本机构建 | 全部通过；全量测试含 Hub、Bridge、集成与 Store |
| 文档与引用 | doc-lint 为 0 errors / 8 warnings；相对链接目标检查通过，diff-check 通过 |
| Go 1.26.6 / macOS arm64 | 使用 `TMPDIR=/private/tmp` 避免 macOS Unix socket 路径长度与路径别名影响 |
| MCP Go SDK | 锁定官方 `github.com/modelcontextprotocol/go-sdk v1.8.0` |
| Codex CLI 0.162.1 | 真实 app-server 连接 Hub，`mcpServerStatus/list` 返回全部 20 工具 |
| Claude Code 2.1.287 | 真实 `claude mcp list` 报告 Connected；最新自然别名和显式探针均成功调用两项只读 MCP 工具并中文回显，但 Skill load trace=false，严格 FAIL |
| Codex 自然别名 | MCP 协议连接成功；推理 60 秒超时、工具 calls=[]，子测试 SKIP；不能将顶层 PASS 计为自然语言或 Skill 验收通过 |
| Skill 资产与安装 | ProofBridge / Codex Bridge / CodexBridge 静态资产回归通过；两客户端实际用户 Skill 已由安装器 checksum 更新；安装不等于请求加载 |
| 前端 | `npm test`、生产构建、设置页依赖 TypeScript 检查通过；embedded UI 已重建 |
| 实际浏览器 | Chrome 检查复制配置、令牌生命周期、偏好冲突、剪贴板错误、SMTP 门禁与手机布局通过；新网站详情夹具在 8088 通过，仅模拟 GET，无真实数据变更 |
| 个人凭据与资源隔离 | HTTP/SDK 鉴权、每请求撤销、首次撤销 tombstone、错误凭据拒绝、资源归属和安全投影通过 |
| 幂等与恢复 | 同键并发、同键变更冲突、默认改变后重试、同 run 继续、接受前重启补偿及未知派发不重放通过 |
| 人工交互 | 权威快照、回执、重复决定、失效身份、只读查询与显式对账、问答 v3 回归通过 |
| 受管 worker | 精确禁用外层入口，保留其他 MCP 和 Claude 审批 helper；真实 Codex metadata 探针通过 |
| SMTP | 实际 localhost TCP 收件；首次 DATA 返回 451，重试返回 250，同 Message-ID；没有外层客户端仍发送 |
| 环境文件与测试邮件 | 显式 env 语法/进程环境优先/错误脱敏通过；本人固定邮件、按用户限速、有限超时和浏览器/PAT 权限测试通过；SMTP accepted 不代表收件箱已收到 |
| 定向并发检查 | Store、Hub、Bridge、SMTP、安装资产的相关 `-race` 测试通过 |

## 本轮真实客户端与运行

本轮复跑覆盖移除旧可选验证器后的全量 Go、vet、前端回归和干净生产构建。新 Hub 已在 `http://127.0.0.1:8088` 健康运行，原 Bridge 在线且未重启。部署后的详情浏览器夹具覆盖标题、卡片边缘、详情按钮、实际配置、深链刷新、管理按钮隔离、键盘与手机布局。

原文“Claude 自然语言自动加载 Skill 已通过”的结论不再作为当前验收依据。最新严格探针如下；普通 `go test ./...` 默认不调用模型，不能覆盖这些未通过项。

| 探针 | 实际观察 | 结论与本地证据 |
| --- | --- | --- |
| Claude + `Codex Bridge` 自然语言 | `proofbridge_get_context`、`proofbridge_list_endpoints` 均成功，无越界工具，中文回显；Skill load trace=false | 严格 FAIL；`native-codex-bridge-alias-discovery.log` |
| Claude + `/proofbridge` 显式调用 | 同上两项 MCP 查询成功；Skill load trace=false | 严格 FAIL；`native-codex-bridge-explicit.log` |
| Codex + `Codex Bridge` 自然语言 | MCP 协议连接成功；60 秒推理超时，业务工具 calls=[] | SKIP，不能计为通过；`native-codex-alias.log` |

本地日志位于 `.trellis/tasks/11-06-mcp-run-detail-selection/validation/`。当前解析器没有观察到成功加载，已保存日志不足以确认是否存在未覆盖的加载路径；不得据此改判测试误报或放宽断言。两项 MCP 操作成功、客户端安装成功与 Skill 自动加载成功分别报告。

原 Test/1 运行 `orc_c01cd9efef9d360db86e369ce3d11979` 已于 22:55:06 达到 completed/pass（verified-early，12 分 22 秒）。本机 Coq 8.18 对 ListRev 与 Audit 编译成功，三处 assumptions 输出为 closed；独立 Codex verifier 通过，Claude verifier 模型调用失败，未宣称双验证器都通过。证据为同目录 `run-monitor.md`。

真实 ArraySet 提交 `orc_ed3af20a59e135a9553f55620c9f4aab` / `asub_5491de3677bba64eb20b6441925c0199` 当前 running，工作目录 `/Users/florian/Desktop/Tencent/Github/Test/ArraySet-Coq`。它使用 6 轮 Claude + Codex Agent；Codex 为官方 `gpt-6.1-sol/high`，Claude 为官方原生默认。原始 zip 保留，所有证明改动仅限该目录；主定理、模型和无新增公理约束不变。尚未获得最终编译及 assumptions 审计，不能写成完成证明。

本机 mailReady=true，临时邮箱 `zy1@local.test` 使用 Mailpit 本地收件箱 8025，没有外发。ArraySet 完整任务通知需等该提交终态后核对，不能由邮件单测或 SMTP 就绪状态代替。

## 固定 CLI 夹具的证明与批量链路

`internal/hub/automation_proof_flow_test.go:TestAutomationProofFlowRealCoqAndLocalSMTP`
通过实际 HTTP MCP、反向 WebSocket Bridge、默认 durable task graph、SQLite 结算及
Hub 邮件 worker 跑两个顶层任务。原 MCP 客户端在检查开始前关闭，重连后读取各自冻结结果。

CLI app-server/exec 消息来自固定测试夹具；每次检查真实执行本机 Coq 8.18.0 的
`coqc Goal.v`，退出码决定结果。两次任务共运行 10 次检查，其中 4 次来自独立 verifier
进程，最后均为 execution=completed / verification=pass；两份本地邮件对应正确的提交。
错误 False 证明探针实际编译失败，夹具报告 continue。未调用模型提供方。

同键 SDK 重试和 `scripts/mcp-batch.py` 同一清单连续两遍均复用 run/submission，
没有额外证明进程或重复完成邮件。证明源与夹具保留在
`internal/hub/testdata/automation-proof/`。

## 重跑入口

~~~bash
TMPDIR=/private/tmp GOMAXPROCS=2 go test -p 1 -parallel 1 -timeout 10m ./...
go vet ./...
CGO_ENABLED=0 go build -ldflags '-s -w' -o bin/codex-bridge .
make doc-lint

TMPDIR=/private/tmp GOMAXPROCS=2 go test -race -p 1 ./internal/hub \
  -run 'TestAutomation|TestActiveOrchestrationMessages' -count=1
TMPDIR=/private/tmp PROOFBRIDGE_TEST_REAL_CLIENTS=1 go test ./internal/hub \
  -run '^TestAutomationNativeMCPClients$' -count=1 -v
TMPDIR=/private/tmp PROOFBRIDGE_TEST_REAL_CODEX=1 go test ./internal/bridge \
  -run '^TestManagedWorkerRealCodexMetadata$' -count=1 -v
~~~

原生探针需要本机已安装对应 CLI。证明链路需要 Python 3 与 coqc；缺少依赖时
测试明确 skip，不能把 skip 写成真实检查已经通过。

现有安装器与进程组测试修复了平台假设：查询本机 true 可执行文件，使用脚本保持
原有进程存活，使用独立 POSIX shell 获取子进程 PID。没有改变生产安装器和取消行为。

## 部署前仍需验收

实验室 VPN 的连接/断开/重连、真实部署域名、外部 SMTP 收件箱与目标机器权限，
均需在实际实验室环境验证。Codex 的真实客户端连接只证明 MCP 兼容；Claude 的最新探针证明两项自然语言只读 MCP 调用成功，Skill 加载仍未通过严格验收。固定 CLI 夹具证明产品链路
和实际证明器运行，不能扩大成真实模型自动理解自然语言并完成项目的验收。

SMTP sent 表示服务器接受，不等于收件箱已收到，也不承诺跨 SMTP/SQLite 绝对一次投递。
默认配置可保留原生 CLI 默认模型，实验记录不会伪造固定 model。

临时服务器、客户端进程、测试数据库、隔离 CLI home、编译中间文件及清单由测试清理；
保留源码、重建的网页静态资源、可复用依赖、构建二进制和验证记录。


自然语言 opt-in 测试：`PROOFBRIDGE_TEST_REAL_SKILL=1 PROOFBRIDGE_TEST_SKILL_ALIAS='Codex Bridge' go test ./internal/hub -run TestAutomationNativeSkillReadOnly -count=1 -v`。
它安装真实 Skill、使用隔离客户端配置，服务器只执行两项只读工具；默认测试不会调用模型。别名还可设为 ProofBridge 或 CodexBridge。
Codex 的超时是本次推理连接的观测，不足以归因于 MCP 或证明其自然语言路径可用。本轮已保留 natural/explicit 的独立结果，不再无依据重复调用模型。
