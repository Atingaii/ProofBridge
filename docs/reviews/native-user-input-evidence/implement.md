> 公开脱敏历史记录。原始运行资料保留本机；此处保留当时的验证范围和未验收项，当前契约以[原生结构化问答](../../features/native-structured-user-input.md)为准。

# 实施与验证清单

最新阶段：用户已批准按[运行时交互补全计划](../../features/native-structured-user-input-runtime-plan.md)实施。Q0 已从 Codex 官方源码确认异步问题和答案契约；后端、前端实施正在进行，尚未完成完整链路验收或部署。任务维持 in_progress。下方历史记录不代表 Q0–Q6 已全部执行。

## 当前实施清单

权威清单为运行时交互补全计划，按 Q0–Q6 推进：

- [ ] Q0：核实真实同步／异步工具、事件、启动能力和生存期，定位实际失败的第一层；不根据截图断言根因。
- [ ] Q1：先让 Codex 默认模式＋自动执行通过真实浏览器闭环，再完成 Claude；只修经证据确认的阻断点。
- [ ] Q2：补全原生阻塞／非阻塞等待、异步待答寿命和唯一 reader 归属，验证任务图与原会话继续。
- [ ] Q3：实现主动问答面板、多请求队列、选项与自定义输入；复用卡片及状态源，验证键盘、手机和长日志。
- [ ] Q4：明确实际运行的能力状态，补足不含问题／答案的最小阶段诊断。
- [ ] Q5：按最新 PRD AC1–AC17 完成定向、原功能矩阵和同次真实浏览器验收，严格区分模拟与 live 结果。
- [ ] Q6：审查与文档同步，列出各组件版本和更新要求，授权部署后在目标机器复验并清理本次临时产物。

负载纪律：单个测试／构建／浏览器／原生探针串行执行，Go 使用 GOMAXPROCS=2、-p 1；低内存时停止启动重任务，不停止用户服务。本轮只做文档检查。

## 历史清单：二次审查修复

权威清单：[二次审查修复与原功能回归计划](../../features/native-structured-user-input-regression-plan.md)，按 S0–S6 串行推进：

- [x] S0：记录当前工作区与失败基线，复现 R1–R6，冻结 writer/outbox/v3 契约和测试隔离方案。
- [x] S1：隔离新集成测试的公网刷新与全局目录副作用，不改变生产统计逻辑。
- [x] S2：修正常 resolved 误杀竞态，再实现拥堵后无需重连的有界问答补发。
- [x] S3：实现 submission 权威核对和旧 ID 封禁，完成 Hub/Bridge/UI v3 协商及旧组件普通功能回归。
- [x] S4：处理 queued、多标签及终态断线恢复；未知结果可查询、不自动重放答案。
- [ ] S5：执行原功能矩阵、定向 race、前端测试／构建、同次完整 Go 测试、二进制构建和真实 CLI 到实际浏览器验收。
- [ ] S6：独立审查、文档与规格同步、临时产物清理；全部门禁通过前不发布。

执行纪律：一次只运行一个测试／构建／CLI 探针，GOMAXPROCS=2、Go -p 1；不能以定向重跑替代全量通过，不能仅以模型目录污染解释所有历史失败。

状态：六项加固代码已实施、最终验收中。用户已批准按此方案实施；下方早期清单和“实施记录”“审查后补充测试与文档”保留当时状态，最新结果以文末“六项加固实施记录”为准。实际 CLI 验证结果和未通过的发布门禁须如实记录。

## 0. 规划门禁

- [x] 创建任务得到用户同意。
- [x] 完成事实调研，记录版本与未实测限制。
- [x] 用户选择 A：遵循原生等待语义。
- [x] 形成 PRD、design、implement、ADR 和 feature 文档。
- [x] 用户在看到最终方案后明确批准实施，并要求控制机器负载。
- [x] 批准后启动任务；Bridge 文件交由独立实现者负责，前端、Hub 与协议由主代理负责，验证采用串行低并发执行。

## 1. 原生能力验证（阻塞后续上线）

- [x] 在任务专用临时目录与 CLI runtime home 中记录版本、schema、实际模式；未读取或打印凭据，未改用户全局配置。
- [ ] Codex：0.156.1 在 auto-execute／review-required × Default 非阻塞／Plan 阻塞四种组合均已真实完成请求→回答→resolved→同 turn 完成；取消、自定义答案与完整浏览器链路仍待验收。
- [ ] Claude：历史 2.1.229 基线失败；最新 2.1.281 + gpt-6-sol 的临时格式包装器在 default／bypassPermissions 下均完成真实三类答案往返。当前项目 helper 格式仍需修复，能力保持不可用；详见 [Claude 实测](claude-capability-verification.md)。
- [ ] 验证失败先定位版本／接法；如 MCP 路线不支持，提交 stdio 备选影响说明。不得先做完整 UI 后以文本模拟交差，也不升级用户全局 CLI。
- [ ] 记录实际支持版本、具体测试和失败原因；技术变更影响已审核范围时更新文档并重新审核。

## 2. 协议与 Bridge（依赖步骤 1）

- [ ] 实现独立 request/response/status/sync/snapshot 类型和能力版本，增加 round-trip 测试。
- [ ] 注册唯一请求身份、submission 幂等、revision/执行代次、终态有界缓存、取消与回写互斥。
- [ ] Codex reader 不同步等用户，保持 resolved/terminal 可处理；所有写入使用既有串行机制。
- [ ] Claude host 安装与权限模式解耦；用户等待不沿用无差别 10 分钟超时，依原生生命周期清理。
- [ ] 阻塞等待不被误判为无输出卡死；显式运行命令 timeout 不受影响。
- [ ] 原生失效后不新建 turn/run 代答；敏感题目答案不进普通日志。

## 3. Hub 与能力 rollout（依赖步骤 2）

- [ ] 校验登录用户、run 所有权、Bridge 身份及当前执行代次；Bridge 最终验证请求可回答性。
- [ ] 接入转发、刷新同步、断线重连；验证快照与终态竞争。
- [ ] 注册 native-user-input，建议 all，后端交互边界检查，向前端暴露 CLI 支持状态和原因；兼容缺失能力的旧组件。
- [ ] 按 docs/change-impact.md 同步 rollout 默认值／配置示例／说明；本期不做 SQLite 迁移。

## 4. 前端（依赖步骤 2–3）

- [ ] 实验站问题卡：发起者、问题、选项说明、单选或多选、自定义输入、多题整组提交。
- [ ] 区分阻塞等待／原生可继续、提交中、已送达、关闭／失效；不自动确认预选项。
- [ ] 多标签页同步、刷新恢复、提交失败与请求关闭；不把本地发送成功当原生处理成功。
- [ ] 键盘与移动端验证，接入 i18n，保留既有停止运行行为。

## 5. 验收与文档（依赖全部实现）

| 验证 | 对应 PRD |
| --- | --- |
| 两种 CLI × 两种权限模式真实往返 | AC1–AC3、AC7 |
| 并发同原生 ID、重复提交、跨 run 与旧 attempt | AC4 |
| 刷新、事件/快照竞争、取消、进程退出、迟到答案 | AC5 |
| 阻塞/非阻塞、reader 存活、停止、显式命令超时 | AC6 |
| 单选/多选能力、敏感内容、公开分享、普通审批回归 | AC3、AC8 |

- [ ] 添加能验证上述行为的 Go 集成／状态机测试和前端交互测试；不要只以源码字符串检查代替行为测试。
- [ ] 先定向 Go 测试，再运行受影响并发包 race 测试；测试使用本次唯一临时目录。
- [ ] `npm test` 与 `npm run build`（工作目录 frontend），保留生成的 internal/web/static 交付资源。
- [ ] `/usr/local/go/bin/go test ./...`。
- [ ] `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o <本次临时目录>/codex-bridge .`；避免覆盖已有 bin 中用户产物。
- [ ] R7 顺序核查 AGENTS、architecture、code-map、dev-workflow、ADR/feature、change-impact，以及设置变化涉及的 README / README.zh-CN。
- [ ] `make doc-lint`；区分本任务引入与已有警告。
- [ ] Trellis check 审核实现与契约，修复实际问题后才宣告通过。不运行 Android 门禁。
- [ ] 清理本次创建的探针、临时 runtime home、截图和验证二进制；保留测试证据、文档与交付资源。

## 交付与停止点

本任务不自动提交、部署或升级全局 CLI。交付说明列出支持版本、两端真实往返结果和已知限制，并提供 R6 格式提交消息及 Doc-Impact。

若版本能力不足、原生答案通道不能满足契约、或兼容行为需要产品范围变化，回到设计审核。不得将未通过的真实集成标为完成。

## 2026-09-24 实施记录

- Codex 0.156.1 的 auto-execute／review-required × Default／Plan 四种真实 CLI 探针均完成原生提问、答复、resolved 与同 turn 继续；仅向这些已验证版本与权限模式声明能力。
- Bridge、Hub、协议、rollout、实验站卡片与快照恢复已接通；Claude MCP 适配通过模拟测试，但本机 Claude 2.1.229 在进入提问前反复重试模型 API，真实往返未通过，因此能力保持关闭。本任务仍为 in_progress。
- 定向 Bridge／Hub 测试通过；首次低并发全量 Go 测试只有旧 Isabelle 集成用例在 15 秒固定等待窗口失败。将该状态等待延长至 25 秒后，单项和完整 `internal/integration` 包均通过。尚未重新运行单次完整 `go test ./...`。
- `npm test`、新快照竞态定向回归、`npm run build`、最终静态资源的 `CGO_ENABLED=0` Go 构建、`git diff --check` 均通过。`make doc-lint` 为 0 错误、1 个既有 `internal/hub/server.go:Server.Run` 锚点警告。
- 尚待 Claude 真实 CLI 往返、浏览器人工验收及适当的 race 检查；为控制本机负载未并行运行重型验证。本次临时目录中的构建二进制和五份测试／构建日志已逐项确认并清理；生成的 `internal/web/static/` 属于交付资源，已保留。

建议提交消息（仅限本功能相关文件，不包含工作区中其他未提交改动）：

```text
feat(orchestration): bridge native structured user questions

Change summary:
- internal/bridge/: route validated native questions and answers within the active CLI turn
- internal/hub/ and internal/protocol/: authorize and forward question, answer, status, and snapshot frames
- frontend/: render structured questions and recover live requests after reconnect

Exit gate:
- [x] Codex 0.156.1 four permission/mode probes and focused Bridge/Hub checks
- [x] frontend tests/build, integration package, Go build, and doc-lint
- [ ] Claude live AskUserQuestion round trip and browser manual acceptance

Doc-Impact: docs/adr/024-native-structured-user-input.md, docs/features/native-structured-user-input.md, docs/architecture.md, docs/code-map.md, docs/change-impact.md, docs/dev-workflow.md, docs/plan.md, README.md, README.zh-CN.md
```

## 2026-09-24 审查后补充测试与文档（未修功能）

- 用户授权使用新提供的兼容接口凭据验证 Claude，并将修复规划落成详细文档；本次未修改功能代码、静态 UI、部署或提交。
- Claude Code 2.1.281：新组凭据的 Base URL 移除末尾 /v1 后基础调用成功。当前 helper 的 default AskUserQuestion 返回格式被拒绝；临时仅移除 structuredContent 后 default 与 bypassPermissions 均完成单选、多选和自定义回答。具体证据及限制见 [能力记录](claude-capability-verification.md) 和 [事件 JSON](claude-probe-evidence.json)。
- 原五项缺陷加新增 Claude MCP 结果格式问题形成[六项加固方案](../../features/native-structured-user-input-hardening.md)。明确修复顺序、生命周期／提交凭据、真实 reader 故障测试、UI 可见性、发布门禁与回滚；所有修复仍待实施。
- 文档复核澄清 queued 不能改答，只有最终 not_sent 且撤销 lease／释放预留才可新提交；rollout 禁用保留授权窗口内 receipt 只读查询。F4/F5 保持原审查 P2。
- 本次只做低并发探针所需的临时 Go 构建与文档验证，不重复运行全量工程测试；不能将包装器通过计为项目正式 helper、网页或取消验收通过。
- 收尾：独立只读复核确认文档与两模式事件证据一致；make doc-lint 为 0 错误、1 个既有 Server.Run 锚点警告。脱敏检查未发现密钥／私有端点，任务临时目录及关联探针进程已清理。

## 2026-09-24 六项加固实施记录

- F1/F3/F4/F5 的浏览器侧采用统一帧解码、实例／revision 水位、终态 tombstone 与独立 receipt 视图；问题入口固定在时间线滚动区域之外，关闭后只显示终态摘要。浏览器通过 `user_input_sync.clientVersion=2` 声明能够理解两维状态。
- 项目自带 Claude MCP helper 已去掉不被当前 CLI 接纳的 `structuredContent` 回包字段。Claude Code 2.1.281 + gpt-6-sol 在隔离 runtime home 下，default 与 bypassPermissions 两种模式均完成一次实际 `AskUserQuestion`、三题 socket 回调和同次 CLI 答案回显。凭据未落盘；此探针只覆盖 helper 路径，不能代替完整网页链路。
- 前端 `npm test`、状态机回归与桌面／390px 浏览器用例均通过；浏览器用例覆盖 48 条长日志、固定入口、键盘聚焦、secret 输入和 v2 同步声明。临时 Vite 服务已停止。后端、整包构建、race 与文档检查的最终结果待补记。
- Bridge／Hub 定向行为测试通过，包含项目 Claude MCP helper 经真实 Hub WebSocket、Bridge manager、模拟浏览器的请求、答案、关闭与 receipt 同步。Bridge 最新定向 race 通过；Hub 定向 race 对此前代码通过，最新 Hub v2 测试仅做非 race 验证。真实 CLI 使用修复后的项目 helper 在 Claude Code 2.1.281 的 default／bypassPermissions 两种模式均完成三题同次往返；这与模拟浏览器跨层测试为两份独立证据，尚无部署后真实模型到实际浏览器的人工证据。
- 本轮 `frontend/npm run build` 通过，新的嵌入式资源已保留；构建前已有的资源逐项恢复保留。构建专用临时备份目录已在成功后删除。完整 Go 测试、临时二进制构建与 doc-lint 正串行收尾。
- 第二轮全量 Go 测试采用 `GOMAXPROCS=2 go test -p 1 ./...`：Hub、integration、store 及其余包通过；Bridge 包失败四项：`TestOrchestrationFinalTurnEndAndRunEndDoNotWaitForVisibleCompaction`、`TestRelayCLIErrorIsFrontendVisibleAndRedacted`、`TestDurableTaskTerminalReleasesCodexWriterBeforeDelivery`、`TestNormalizeNativeUsageCodex`。四项同组定向重跑均通过，不能把全量结果写成通过。首轮额外的 Isabelle 集成超时在双核正常优先级下单独通过，第二轮完整 integration 包亦通过；低优先级调度会放大既有短时限测试波动。Hub 原“完成运行拒绝 WebSocket”测试与新终态只读查询冲突，已改为实际 WebSocket 握手验证，定向和第二轮完整 Hub 包均通过。
- 最新 Hub v2 相关定向 race 通过，Bridge 定向 race 此前通过。`CGO_ENABLED=0`、双核单包 Go 构建通过，输出到任务唯一临时目录并已删除。`make doc-lint` 0 错误、1 个既有 `internal/hub/server.go:Server.Run` 锚点警告；Go 格式与 `git diff --check` 通过。本轮源码／文档及前端相关文件未检出用户提供的密钥标记或私有端点。构建临时目录、先前 UI 备份和 Claude 探针临时目录均已核对不存在；保留嵌入式静态 UI、脱敏证据与文档。未提交、未部署、未升级全局 CLI。

候选提交消息（仅纳入本功能文件，且全量 Go 门禁与人工浏览器验收未完成前不自动提交）：

```text
feat(orchestration): harden native structured user input

Change summary:
- internal/bridge/: bound answer dispatch, preserve delivery receipts, and validate Claude MCP responses
- internal/hub/ and internal/protocol/: negotiate v2 and authorize terminal read-only receipt sync
- frontend/ and internal/web/static/: keep native questions visible and recover terminal states

Exit gate:
- [x] focused Bridge/Hub tests and native user input race checks
- [x] frontend tests, browser interaction test, embedded UI and Go builds, doc-lint
- [ ] full Go suite clean and deployed real-model browser acceptance

Doc-Impact: docs/adr/024-native-structured-user-input.md, docs/features/native-structured-user-input.md, docs/features/native-structured-user-input-hardening.md, docs/architecture.md, docs/code-map.md, docs/change-impact.md, docs/dev-workflow.md, docs/plan.md, README.md, README.zh-CN.md
```

## 2026-09-24 v3 修复实施记录（当前）

- R1：Bridge 新增有界问答 outbox，在瞬时出站拥堵恢复后持续补发；实际 Client reader、关闭清理与容量测试通过。R2：Codex 答案写入的 lease 与传输 watchdog 分离，正常 resolved 不再杀健康进程，短写及阻塞写有确定性测试。
- R3：协议升级为 v3，Bridge 在与答案入队同一锁下核对并封禁未到达的 submission；Hub、Bridge、页面三方能力协商，旧组件只关闭问答入口，普通编排继续。R4/R5：前端恢复 queued 胜出状态、刷新后持久身份核对、终态只读 receipt 查询；答案内容不写 sessionStorage，也不自动重发。
- R6：Claude 跨层测试复用真实 Hub 路由而不启动目录刷新；与价格测试两种顺序及乱序组合通过。此前全量失败的最终压缩测试改为先确认压缩已启动、再用释放屏障验证 run.end 已返回；改动仅限测试，连续 5 次定向及完整 Bridge 包通过。
- 验证：`npm test`、`node frontend/user-input-check.mjs`、桌面／390px Playwright 浏览器场景、`npm run build`、Bridge/Hub 定向 `-race`、`GOMAXPROCS=2 /usr/local/go/bin/go test -p 1 ./... -count=1` **同一次全通过**、`CGO_ENABLED=0` 单包 Go 构建、`git diff --check`、`gofmt -l` 通过。`make doc-lint` 为 0 错误，保留既有 `internal/hub/server.go:Server.Run` 锚点警告。
- 剩余发布门禁：本轮尚无“真实模型 → 当前 v3 本地或部署 Hub/Bridge → 实际浏览器卡片 → 原生调用继续”的单条端到端证据，也未做发布后 smoke；此前真实 CLI helper 探针与模拟浏览器跨层测试是独立证据，不能合并冒充这一条。任务保持 in_progress，未提交、未部署、未升级全局 CLI。
- 清理：本轮临时 Vite 服务、静态资源备份和 Go 构建二进制／目录均已停止或移除；生成的 `internal/web/static/` 是交付资源，保留，工作区原有修改和依赖缓存未触碰。

建议提交消息（仅选择本功能相关文件；发布门禁完成前不自动提交）：

```text
fix(orchestration): recover native user input safely across delivery gaps

Change summary:
- internal/bridge/: add bounded question outbox and separate answer-write lease from transport failure
- internal/hub/ and internal/protocol/: negotiate v3 and fence missing submissions atomically
- frontend/ and internal/web/static/: restore queued state and terminal receipt-only recovery

Exit gate:
- [x] focused/race checks, frontend tests/browser scenario/build, full Go suite and Go build
- [ ] real model through v3 Hub/Bridge to actual browser, then deployment smoke

Doc-Impact: docs/adr/024-native-structured-user-input.md, docs/features/native-structured-user-input.md, docs/features/native-structured-user-input-regression-plan.md, docs/architecture.md, docs/code-map.md, docs/change-impact.md, docs/dev-workflow.md, docs/plan.md, README.md, README.zh-CN.md
```
