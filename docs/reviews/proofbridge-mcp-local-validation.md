# ProofBridge MCP 本地验收

日期：2026-11-06。实现分支：`feat/mcp-skill-task-notifications`。
设计契约：[MCP、Skill 与任务通知](../features/mcp-skill-task-notifications.md)。
本文记录本地实际验证，不代表实验室部署已经完成。

## 环境与门禁

| 项目 | 本地结果 |
| --- | --- |
| Go 全量测试 / vet / 本机构建 | 全部通过；全量测试含 Hub、Bridge、集成与 Store |
| 文档与引用 | doc-lint 为 0 errors / 8 warnings；相对链接目标检查通过，diff-check 通过 |
| Go 1.26.6 / macOS arm64 | 使用 `TMPDIR=/private/tmp` 避免 macOS Unix socket 路径长度与路径别名影响 |
| MCP Go SDK | 锁定官方 `github.com/modelcontextprotocol/go-sdk v1.8.0` |
| Codex CLI 0.162.1 | 真实 app-server 连接 Hub，`mcpServerStatus/list` 返回全部 20 工具 |
| Claude Code 2.1.287 | 真实 `claude mcp list` 报告 Connected；自然语言自动加载 Skill，调用两项只读工具并终端回显通过 |
| Codex 自然语言 Skill | MCP 发现和 thread/turn 启动成功，但推理请求在 60 秒内持续超时；未观察到业务工具调用，不计为自然语言验收通过 |
| 前端 | `npm test`、生产构建、设置页依赖 TypeScript 检查通过；embedded UI 已重建 |
| 实际浏览器 | Chrome 检查复制配置、令牌生命周期、偏好冲突、剪贴板错误、SMTP 门禁与手机布局通过 |
| 个人凭据与资源隔离 | HTTP/SDK 鉴权、每请求撤销、首次撤销 tombstone、错误凭据拒绝、资源归属和安全投影通过 |
| 幂等与恢复 | 同键并发、同键变更冲突、默认改变后重试、同 run 继续、接受前重启补偿及未知派发不重放通过 |
| 人工交互 | 权威快照、回执、重复决定、失效身份、只读查询与显式对账、问答 v3 回归通过 |
| 受管 worker | 精确禁用外层入口，保留其他 MCP 和 Claude 审批 helper；真实 Codex metadata 探针通过 |
| SMTP | 实际 localhost TCP 收件；首次 DATA 返回 451，重试返回 250，同 Message-ID；没有外层客户端仍发送 |
| 定向并发检查 | Store、Hub、Bridge、SMTP、安装资产的相关 `-race` 测试通过 |

## 完整证明与批量实验链路

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
测试明确 skip，不能把 skip 写成真实检查已经通过。managed TBCI 的 Linux amd64
真实二进制执行测试在本机 macOS 明确 skip，其余 TBCI 单元及能力门禁仍参与检查。

现有安装器与进程组测试修复了平台假设：查询本机 true 可执行文件，使用脚本保持
原有进程存活，使用独立 POSIX shell 获取子进程 PID。没有改变生产安装器和取消行为。

## 部署前仍需验收

实验室 VPN 的连接/断开/重连、真实部署域名、外部 SMTP 收件箱与目标机器权限，
均需在实际实验室环境验证。Codex 的真实客户端连接只证明 MCP 兼容；Claude 已另行通过自然语言只读调用。固定 CLI 夹具证明产品链路
和实际证明器运行，不能扩大成真实模型自动理解自然语言并完成项目的验收。

SMTP sent 表示服务器接受，不等于收件箱已收到，也不承诺跨 SMTP/SQLite 绝对一次投递。
默认配置可保留原生 CLI 默认模型，实验记录不会伪造固定 model。

临时服务器、客户端进程、测试数据库、隔离 CLI home、编译中间文件及清单由测试清理；
保留源码、重建的网页静态资源、可复用依赖、构建二进制和验证记录。


自然语言 opt-in 测试：`PROOFBRIDGE_TEST_REAL_SKILL=1 go test ./internal/hub -run TestAutomationNativeSkillReadOnly -count=1 -v`。
它安装真实 Skill、使用隔离客户端配置，服务器只执行两项只读工具，默认测试不会调用模型。
Codex 的超时是本次推理连接的观测，不足以归因于 MCP 或证明其自然语言路径可用；本轮不再反复调用模型。
