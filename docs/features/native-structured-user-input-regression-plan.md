# 原生结构化问答：二次审查修复与原功能回归计划

状态：**R1–R6 已实施，S0–S4 和自动化回归有历史通过记录；真实模型至 v3 实际浏览器验收尚待完成**。日期：2026-09-24。本轮修复结束时未部署；用户后续报告已更新目标机器，实际组件版本尚未核实。任务沿用 `.trellis/tasks/09-24-native-user-interaction`。检查结果和未通过门禁记录在[任务实施清单](../reviews/native-user-input-evidence/implement.md)。

后续阶段：[运行时交互补全计划](native-structured-user-input-runtime-plan.md)承接用户再次确认的排队式原生问答体验；目前仅规划、待审核。本文的 v3 可靠性约束和原功能回归矩阵继续有效。

本文是[第一轮加固方案](native-structured-user-input-hardening.md)的后续修复计划，保留原有设计和验证历史。原需求见[功能设计](native-structured-user-input.md)、[ADR 024](../adr/024-native-structured-user-input.md)。发生冲突时，本文仅作为下一轮实施目标；不把尚未实现的设计描述为当前行为。

## 1. 目标与范围

修复二次审查发现的六个问题，并用可重复的回归证据证明：新增问答不会改变普通聊天、执行审批、原生会话连续性、编排终态、统计和公开分享的既有契约。无法仅凭计划承诺绝对零回归；未通过本计划门禁，不宣布功能完成或可发布。

保留用户选择 A：遵循原生阻塞／非阻塞语义。只在实验站原生问答链路内修复，不扩展普通聊天问答、不新增数据库、全局 CLI 配置或新的模型供应商依赖，不升级 CLI，不维护 Android，不重构通用消息总线或价格系统。

本轮不得以调整测试期望、放宽短超时、关闭原有测试、增加自动重试次数掩盖失败；确有原测试缺陷时独立说明证据与最小改动。

## 2. 六项问题与证据

| 编号 | 级别与问题 | 已有证据／源码入口 | 修复完成判据 |
| --- | --- | --- | --- |
| R1 | P1：瞬时出站拥堵后问答帧滞留 | `internal/bridge/user_input.go:sendUserInputFrame` 只写 pending；`internal/bridge/orchestration.go:AttachOut` 在重连时补发。静态路径确认 | 不断开连接，队列恢复后自动送出问题／状态／快照；reader、心跳和原审批继续工作 |
| R2 | P1：正常 resolved 可能误杀 Codex | `internal/bridge/appserver_runner.go:respondUserInput` 将请求取消绑定进程终止；`internal/bridge/user_input.go:closeUserInputLocked` 会取消 deliveryCtx。静态竞态，尚未做进程级确定性复现 | 完整写出与 resolved 竞争仍为 closed+delivered，原 CLI 存活并继续同 turn；实际通信故障仍有界收敛 |
| R3 | P2：未到 Bridge 的答案一直 confirming | `frontend/src/app/lib/userInput.ts:receiveUserInputSnapshot` 保留 confirming；内存内执行实际 reducer 已复现 | Bridge 权威核实未接收并封禁旧 submission 后可重新编辑；迟到旧答案永不执行 |
| R4 | P2：queued 被前端忽略 | `frontend/src/app/lib/userInput.ts:receiveUserInputStatus` 缺 queued 分支；实时和刷新快照均复现为 pending | 所有标签页及刷新后均禁用排队／写入中卡片，最终收敛到胜出答案结果 |
| R5 | P2：断线期间终态后不再重连 | `frontend/src/app/pages/OrchestrationWorkspace.tsx:connectRun` 使用连接建立时的 readTerminalInput；终态 effect 仅处理 OPEN 连接。静态路径确认 | active→断线→terminal 能重连只读查询，恢复 receipt；禁止回答和重启执行 |
| R6 | P2：新集成测试污染价格目录 | `internal/bridge/claude_hub_user_input_test.go:TestClaudeAskUserQuestionMCPThroughHubAndBrowser` 调用 Hub Run；`internal/hub/server.go:Server.Run` 启动全局目录刷新 | 新测试不访问公网、不改模型目录全局状态；与原价格测试组合和乱序运行均通过 |

R6 的刷新成功路径可以解释此前 official-catalog/models.dev 差异，但不能据此解释其余三个 Bridge 测试失败。其余失败仍要分别定位。第一轮已通过的定向测试、race 和 CLI helper 探针属于历史证据，不能直接充当本轮修复后的通过记录。

## 3. 必须保护的原功能契约

| 原功能 | 隔离要求 | 必须保留的行为 |
| --- | --- | --- |
| 普通聊天 | 不修改聊天 Runner／WS 分发／session 创建逻辑 | 发送、取消、重连及 resume 沿用原 sid、remote_thread_id |
| 执行审批 | 不复用问答 status、receipt、submission 作审批决策 | review-required 仍审批，auto-execute 不新增执行审批；接受、拒绝、取消含义不变 |
| Codex 普通 RPC | 问答 writer 仍使用同一串行写锁，不整体替换 request/respond/respondError | initialize、turn、审批和普通 RPC 不交叉写帧；无问答的运行不触发新增终止路径 |
| Claude | 保留已验证 MCP helper 格式及两种权限模式 | AskUserQuestion 回到原调用；其他工具走既有权限策略，不添加通用自动批准 |
| 编排连续性 | 保留原 run/worker/turn/execution 身份 | follow-up 仍经原 prompts 端点；问答不创建新 run/turn，不暂停无关 worker |
| 编排停止及结束 | 显式停止沿原 cancellation 路径执行 | 用户停止、进程退出和明确命令 timeout 可收敛；正常问题 resolved 不等于停止 worker |
| 共享连接 | 新增重试器有界，不阻塞原读循环，不接管普通事件缓存 | heartbeat、approval_response、cancel、run.end/run.error 不被问答饿死或重排 |
| 用量、模型目录 | 测试隔离在测试边界；不更改生产价格选择优先级 | 原有 token、cost source、目录刷新与统计保持原契约 |
| 授权及隐私 | 原问题和答案不进普通事件、SQLite、日志和 share | run 所有权、agent 归属、执行代次、终态只读校验继续成立 |
| 功能关闭／不兼容 | 只关闭新问答入口，不阻断普通编排 | 不支持的 CLI／旧组件显示原因，不伪造问题，不静默等待不可见回答 |

基线必须是**本轮修复前的当前工作区**，不是简单 HEAD：仓库有大量先前未提交工作，不能把已有改动移除以制造通过。S0 记录受影响文件差异、测试结果、运行负载及准确失败信息，不复制任何凭据。不能排除的原功能回归继续阻断发布。

## 4. R1：有界问答发送与持续补发

实现入口：`internal/bridge/user_input.go:sendUserInputFrame`、`internal/bridge/orchestration.go:AttachOut`／`DetachOut`；必要时新增问答专用 outbox 文件，不更改 `internal/bridge/orchestration_events.go:send` 的普通事件语义。

1. 在 OrchestrationManager 生命周期内建立一个问答发送器，最多一个 drain worker；注册、关闭、receipt 更新和 sync 处理只更新有界待发状态并唤醒，不在原生 reader 或 WS reader 内等待网络。
2. 出站有空间立即投递；瞬时满载后持续等待空间或受控重试，无需重连。AttachOut、DetachOut 唤醒发送器，旧连接失效不会丢失待发状态。发送不持有 manager 状态锁，禁止 sendMu/mu/outbox 锁反向获取。
3. 不简单另建无界 FIFO：活动请求预留待发槽，终态和 receipt 以请求／submission 的 revision 合并；closed 优先于未发出的旧 request，但不能吞掉成功 receipt。至少保留当前身份、最高状态和需要恢复的回执。
4. snapshot 按 runId+syncId 关联，不能将不同标签页的 syncId 随意合并成一个。快照队列饱和须返回可重试结果或由客户端有界超时重查；不能静默丢弃后假装同步成功。
5. 初始预算：问答待发活动身份全局 256、每 run 64；独立待发 sync 64；沿用终态 256 全局／64 每 run／10 分钟上限。注册前预留容量，超限明确拒绝注册并按现有原生错误路径返回，不留下 CLI 等待但没有待发记录的半成功请求。最终预算在 S0 的受控测试里确认，调整须记录。
6. 公平发送，不能无限抢占共享出站队列；例如每批最多 16 帧后让出调度。新增任务只在有问答待发时运行，manager 关闭后退出；断线缓冲和普通 pending 的所有权明确，禁止同一帧被两条刷新路径无序重复消费。

验收：实际 Client 读循环 + 可控满队列，先发 question，再释放队列，不发生断线仍送达；拥堵期间混入取消、心跳、审批及 run.end；容量和关闭测试检查 goroutine、持有引用及敏感内容清理。不能只用 manager 直调并发测试替代 reader 故障测试。

## 5. R2：区分问题关闭与原生传输故障

实现入口：`internal/bridge/appserver_runner.go:respondUserInput`／`abortUserInputTransport`、`internal/bridge/user_input.go:UserInputResponse`／`closeUserInputLocked`。

推荐保留当前原生通道与串行写锁，仅收紧问答写入状态机，不替换 CLI 通信架构。

| 阶段／事件 | 原生写入和生命周期 | receipt／进程行为 |
| --- | --- | --- |
| queued／等写锁时问题关闭 | 撤销尚未开始的写资格，不等写锁，零次写入 | 可确认 not_sent；问题 closed；不终止健康 CLI |
| 已开始写入，问题正常 resolved | 问题立即 closed，禁止新提交；已有写入自行在传输预算内结束 | 不因问题关闭直接 kill；按写结果独立记录 receipt |
| 全部字节写出，随后／同时 resolved | 写完成事实不可被较晚 context cancellation 覆盖 | closed+delivered；同原生进程继续工作 |
| 短写、部分写后失败 | 不尝试再次补写整个答案 | unknown；禁止用户改答重发；诊断不记录答案 |
| 真正传输超时／进程退出 | 按原生 client 故障路径有界清理 | 仅影响故障 client；unknown，不复活原调用 |
| 用户显式停止整个 run | 保留现有 run cancellation 和进程清理 | 所有相关问题关闭；不得为保护问答阻碍停止 |

具体实施要求：

- 区分 request lease 的取消信号与 native transport 的故障／10 秒写预算。正常 CloseUserInput 不再无条件触发 abortUserInputTransport。
- 等写锁、获得写资格、开始 Write 的边界必须可确定性测试；写资格在短锁下仲裁，close 已获胜时绝不开始写。已获写资格的 I/O 不占状态锁。
- 开始 I/O 后，正常问题取消不立即杀进程；保留独立的有界通信 deadline，只有实际故障／期限届满或原 run 停止才进入 transport 终止路径。不能用遗弃写 goroutine 获得表面上的可取消。
- `n == len(frame)` 且返回成功后记录 delivered；必须处理短写。timeout callback、成功完成、原生 resolved 共享明确的完成仲裁，防止已成功后残留 watchdog 再杀进程。
- 首先用 fake pipe/barrier 复现当前问题，再用可控实际 pipe+子进程验证“完整收到答案并发 resolved，进程仍能接收下一条原有 RPC”。取消测试不能继续以“所有请求取消都应杀进程”为通过条件。
- 真正不可恢复的 stdin 堵塞可能仍需关闭故障 client；这是受限的故障处理，不承诺故障进程能继续。普通完成与健康进程不应进入这一路径。

## 6. R3：未确认提交的权威核对

仅把 confirming 改回 pending 不安全：原答案可能仍在途，会与用户新答案竞争。因此采用**查询并封禁尚未接收的旧 submission**，不自动重发答案。

### 协议目标（v3 实施契约）

- 扩展 `UserInputSyncPayload`，增加 `pendingSubmissions`：最多 64 项，每项包含 requestId、submissionId、executionId；含查询列表时 sync 的 instanceId 必须精确匹配，字段长度及重复项在 Hub 和 Bridge 校验。列表不带问题或答案。首次仅发现实例／取得快照的 bootstrap sync 可省略 instanceId，但不能在实例未知时封禁提交或解锁未确认答案。
- 快照仍以 syncId 返回，复用 statuses、closed、receipts 表达 queued／delivered／not_sent／unknown；无法匹配实例、已过缓存窗口或容量不足的查询，返回明确的不可确认结果，绝不冒充 not_sent。
- 一次查询的仲裁与 dispatch admission 使用同一 manager 锁。已知 receipt 原样返回；已排队／写入中的提交返回 queued；同实例且请求仍 open、该 ID 从未接收时，原子记录该 ID 的 not_sent 拒绝凭据，之后迟到的同 ID 只能取该凭据，不能取得写资格。
- 若同一请求已有另一 submission 排队或已送达，返回胜出者状态，不能因为被查询 ID 未发送就使整个请求可编辑。只有请求 open、无其他有效预留且查得本次 not_sent 才允许新 ID。
- 未知／过期／跨实例请求关闭本地旧卡片，展示不可确认，不允许补答。封禁凭据受现有有界缓存限制；容量不足时不给可编辑结论，不能为了接受查询淘汰仍 open 请求的幂等凭据。
- terminal／rollout 关闭时允许授权只读核对：不得创建 native 写任务、恢复 run 或放行答案。对终态不凭“未见过 ID”推断未投递。

### 兼容性门禁

现有 v2 不理解“未接收 submission 的权威封禁”，不能静默当作支持。拟将完整修复能力升为 v3，Hub 暴露自身支持版本，浏览器同时检查 Hub、Bridge 和已验证 CLI 模式。只凭 Bridge 的 Version>=3 不能证明旧 Hub 支持新契约。

浏览器 `user_input_sync.clientVersion=3`；创建／继续运行的 HTTP 请求携带可选 `nativeUserInputClientVersion=3`，Hub 只有三方支持且 rollout 允许才派发 NativeUserInput=true。缺字段按旧客户端处理，普通创建／继续运行仍有效。活动问答写入要求 v3；旧页面给刷新提示，不能默默显示可回答却不接收。已存在旧版本活跃问答应在升级前结束，不在其运行中切换协议。

Bridge 必须独立验证协商，不能只依赖新 Hub：在 `OrchestrationStartPayload` 增加 `nativeUserInputProtocolVersion`，新 Hub 核验客户端／Bridge 后写入 3；新 Bridge 只有同时收到 NativeUserInput=true 与明确支持的协商版本 3，才安装问答 host、启用 Codex 功能开关或注入问答提示。缺失、旧版本及未知版本一律关闭新问答，普通运行继续。该约束覆盖创建、继续运行及 durable 派发。旧 Hub 可能因 Version>=2 误认新 Bridge，但其旧 true-only payload 将被新 Bridge 安全降级。使用真实旧 wire fixture 验证，不只 mock capability 判断。

本变更只升级 native-user-input 能力，不升级所有 Envelope 或否定旧 Bridge 的聊天／编排能力。具体字段需同步 ADR 024、功能文档、Hub handlers、Bridge payload、前端 caller 和兼容测试；新旧组合必须验证，不能仅把数字 2 全局替换为 3。

### 前端行为

首次发送前，将 user/run/request/submission/execution/instance 身份及时间写入有界 sessionStorage（当前标签页、最多 64 条未决项），不保存题目、选项、答案、cookie 或任何凭据。不能只放 React ref：刷新必须先载入未决身份并禁用相应卡片，核对完成后才解锁。存储写入失败／满额时不发送新答案，显示可恢复的错误，不能无记录发送；不得为腾空间静默删掉仍 open 的未决身份。注销／换用户清理对应范围；终态及确定结果后按回执规则清理。不承诺浏览器数据被用户主动清除后仍能恢复该标签的未决提交。

路由失败、断线恢复或用户主动“重新查询”只发送上述核对请求。收到权威 not_sent 后释放旧 ID，保留当前页面内可编辑草稿，下一次明确提交用新 ID；刷新不恢复答案文本。delivered 清空答案；closed 卸载草稿。unknown 保持不可编辑但提供原因及重新查询入口，不能因本地记录年龄较大就推定未发送。每个新标签／其他设备的独立提交仍遵循 Bridge 首个有效预留胜出的原子规则，不声称跨浏览器存储的全局草稿同步。

同一 run 最多一个自动核对在途；使用有上限的退避、页面卸载取消和请求超时。Bridge 离线不密集轮询，重新在线触发核对。重试预算耗尽显示可手动查询的状态，禁止无限“加载中”。限制查询只影响问答，不改变现有事件轮询频率。

## 7. R4／R5：前端状态和断线终态恢复

### 排队与多标签页

`frontend/src/app/lib/userInput.ts:receiveUserInputStatus` 显式处理 queued：从实时帧和 snapshot 恢复相同不可编辑状态；保存获胜 submissionId 及 revision。UI 展示“正在送达”，不能表示模型已处理。

request/lifecycle/receipt 是不同维度：旧 not_sent 不覆盖更新的 queued／delivered；另一标签页胜出后本标签也禁用，不允许通过旧提交拒绝复活卡片。单个请求的 receipt 保持按 submission 区分，需要改变当前按 requestId 只保留一个 receipt 的简化模型时同步类型和回归测试。业务派生状态优先使用当前有效预留，而非任意 receipt 遍历顺序。

卡片、顶部数量与键盘聚焦使用同一派生状态：待回答、排队／送达中、不可确认、已送达、已关闭；secret 字段仍不进日志，关闭／送达后清除内存答案。

### 终态恢复

重连根据**当前 run 状态和当前问答恢复需求**决策，不依赖旧闭包的 readTerminalInput。active→断线→completed/cancelled/failed 后进入终态只读查询；已关闭 socket 的 terminal effect 可建立只读连接，不能只尝试向 OPEN socket 发帧。

区分连接模式 live 与 receipt-only。后者只允许 heartbeat/sync，服务端继续拒绝答案／审批／执行操作。恢复到明确回执或不可确认结果后，停止自动问答重试，避免为完成运行新增永久轮询；若关闭只读 socket，使用主动关闭标记防止 onclose 自行重连。

切换 run、卸载页面、再次续跑均以 runId+连接代次清理定时器和旧回调。续跑重新使用既有 run/prompts 契约，不因前一次 terminal 连接标记丢失后续实时事件。

## 8. R6：隔离集成测试副作用

优先将跨层测试放到独立集成测试边界，通过子进程运行 Hub；或复用已有测试 HTTP handler，不启动目录刷新生命周期。S0 检查现有测试设施后选择最小方案，不为此给产品添加“测试模式”配置。

要求：

1. 保留真实 Hub 路由、鉴权、WebSocket、Bridge manager 和正式 Claude helper 的证据，不能退回 mock 跳过关键链路。
2. 测试目录刷新改用本地固定 fixture 或完全不启动；禁止该测试访问公网模型目录。进程隔离本身不足以满足“不联网”，必须同时隔离刷新来源／生命周期。
3. 不在并行测试中临时替换全局 Default 再恢复：后台刷新 goroutine 可能在恢复后继续写。确需改测试辅助设施时，必须保证背景任务停止并等待退出；生产价格来源优先级不变。
4. 将 native-input 集成测试与 `TestNormalizeNativeUsageCodex` 组合运行，至少检查两种顺序；再对受影响小集合做固定 seed 的 shuffle。断网和本地 fixture 条件下结果一致。
5. 其余原失败项（终态不等待 compaction、CLI 错误脱敏、durable writer 释放）分别保存完整失败信息，以 barrier/可控 fake 排除调度偶然性；无法证明与改动无关则继续阻断门禁。

## 9. 实施顺序及每阶段退出条件

| 阶段 | 工作及边界 | 完成条件 |
| --- | --- | --- |
| S0 基线与设计冻结 | 记录当前脏工作区；定向复现 R1–R6；冻结 writer 状态、outbox 容量、v3 字段和兼容策略；选定测试隔离方式 | 六项有对应回归用例或明确静态证据；重型验证无并行；无原功能文件被意外清理 |
| S1 测试隔离 | 先修 R6；只改集成测试与必要测试辅助入口 | 不联网、不污染价格目录；组合顺序验证通过 |
| S2 Bridge 生命周期 | 修 R2，再修 R1；保留通用 RPC/审批/事件路径 | writer barrier、真实 pipe、拥堵恢复、reader 活性及 scoped race 通过 |
| S3 协议核对 | 实现 R3 的 v3 query/fence、授权与容量限制，补兼容门禁 | 查询与迟到答复两种先后顺序均只会产生零次或一次正确写入；不兼容组件普通功能可用 |
| S4 前端收敛 | 实现 R4、R5 和 R3 UI；同步 types/i18n | reducer、浏览器断线／多标签页／移动端用例通过；无自动重放答案 |
| S5 原功能回归 | 执行下表；重新生成 embedded UI，Go 构建；真实 CLI+实际浏览器验证 | 所有发布门禁通过；历史 helper／模拟浏览器记录不替代本次端到端证据 |
| S6 审核与交付 | 独立 trellis-check 审查，主会话同步 specs/文档；逐项清理临时物 | 六项闭环、所有剩余风险公开；用户授权前不部署，不自动提交 |

采用串行、小范围实施：每阶段一个实现者完成后再启动检查者，不能后台同时跑构建或 race。若修复需要改通用会话／权限／存储语义，停止该扩展并更新设计，不以“顺手优化”合并进来。

## 10. 回归与故障注入矩阵

| 检查组 | 必测场景 | 断言 |
| --- | --- | --- |
| R1 | 队列瞬时满→恢复、持续满、换连接、关闭、容量超限；混合审批/心跳/停止 | 无重连也可见；reader 不阻塞；缓冲有界，无原事件饥饿 |
| R2 | queued、等锁、写资格、部分写、完整写、回调返回各 barrier 注入 resolved/cancel/exit | 完整成功不误杀、receipt 准确、未获资格零写、故障可清理 |
| R3 | 答案未到、已入队、已写出、丢确认；query 先于／晚于迟到答案；重复 query；另一标签先胜出；发送 A→Hub 暂扣→刷新→核对封禁→提交 B→释放迟到 A | fence 原子、不能串答/改投；not_sent 可新提交；unknown 不被当成未发送；刷新不绕过核对，存储失败不无记录发送 |
| R3 容量 | 重复/过量 ID、缓存到期、Bridge 实例替换、执行代次变化、授权失败 | 无无限 map/后台任务；不从无记录推断未发送；不给其他 run 的数据 |
| R4 | 实时 queued、刷新 snapshot、A not_sent→B queued→B delivered、乱序 receipt、双标签 | 排队禁用；旧结果不覆盖新预留；全部标签看到胜出结果 |
| R5 | active→掉线→终态、终态后 Bridge 暂离线、终态 receipt 丢失、切 run、终态后续跑 | 自动只读恢复；预算耗尽可手动查；旧 timer 不作用于新 run |
| 审批 | 两种 CLI×review-required/auto-execute，问答与执行审批交错，接受/拒绝/取消 | 原审批次数/决策不变，免审批不替用户答题；停止可用 |
| 连续性 | 普通聊天 follow-up/resume；实验站 follow-up；同 CLI 双 worker | sid/thread/run/worker 身份不变，无新上下文补答 |
| 无问答与关闭 | 功能关闭、CLI 不支持、功能开启但没有问题 | 无问答专用额外进程/持续定时器；原编排输出、停止和终态不变 |
| 版本混合 | 新 Hub/旧 Bridge、新 Bridge/旧 Hub、旧页面/新 Hub、全部 v3 | 能力不误报；新问答安全关闭或提示升级；普通运行仍可用 |
| 编排共享路径 | durable writer release、compaction 非阻塞终态、CLI 错误脱敏、显式命令 timeout、Isabelle 既有集成 | 原断言通过；不能靠延长等待/吞错获得通过 |
| 隐私与所有权 | secret/custom、多用户跨 run、错误 agent、公开分享、普通事件/DB/日志检查 | 无答案泄露，无未授权查询/提交 |
| R6/统计 | 新测试与原价格测试顺序组合，固定 fixture，无公网 | cost source 不受测试顺序影响，原统计路径通过 |
| 真正端到端 | Codex 0.156.1：两权限模式×Default非阻塞/Plan阻塞；Claude 2.1.281：两权限模式；实际浏览器选择/自定义，Claude 多选 | 原工具提问→实际卡片→原调用回显→同 turn 继续；另做取消/刷新；版本变化保持关闭直至另行验证 |

上述是场景集合，不要求对所有维度做昂贵笛卡尔积。竞争／故障主要用确定性 fake 和本地子进程覆盖；真实模型仅做最小必要矩阵，一次一个会话。真实模型可能费用较高，不为重复证明同一内部状态机大量调用。

## 11. 低负载验证与发布门禁

实施时先查看 `uptime`、可用内存和现有进程，不停止用户服务。默认 `GOMAXPROCS=2`、Go `-p 1`；一次只运行一个测试／构建／CLI 探针，前端 build、Go race 和 CLI 实测不并行。内存余量不足 1 GiB、持续 swap 或现有服务响应恶化时停止启动新的重型任务，先报告负载；不可为通过时间断言提高全机并发。

按以下顺序执行，已通过的检查仅在相关代码又变化或出现新证据时重跑：

1. 定向新回归 + 相邻原审批／连续性测试；对原失败保存完整输出并定位。
2. 前端 reducer 与浏览器场景；桌面和 390px，键盘、长日志、secret、多标签与断线。
3. 受影响 Bridge/Hub 并发测试的定向 `-race`，串行执行；使用 barrier 而非极短 wall-clock 断言。
4. `npm test`、`npm run build`（frontend），保留生成的 `internal/web/static/`；不得删除构建前已有的未提交静态资源。
5. `GOMAXPROCS=2 /usr/local/go/bin/go test -p 1 ./...`，**要求同一次完整执行通过**。定向重跑通过不能替代全量门禁。
6. `GOMAXPROCS=2 CGO_ENABLED=0 /usr/local/go/bin/go build -p 1 -ldflags "-s -w" -o <唯一临时目录>/codex-bridge .`；不覆盖已有 bin。
7. 按 R7 核查 AGENTS、architecture、code-map、dev-workflow、ADR/feature、change-impact、README；`make doc-lint`、`git diff --check`，区分已有警告。
8. 修复后的真实 CLI 与实际浏览器往返、无关 worker 同时继续工作；未部署前可用本地隔离 Hub/Bridge/浏览器，发布后再做最小 smoke。

发布条件：R1–R6 全部关闭；全量测试与构建通过；原功能矩阵无未解释回归；兼容门禁可用；实际端到端证据完整。任一 P1、原功能回归、无限等待／泄露或全量失败未闭环，均不发布。

清理只针对本轮明确创建的临时目录、测试子进程、浏览器服务、构建二进制和截图；保留交付静态资源、必要脱敏证据和已有依赖缓存。文档不得包含密钥或私有端点。

## 12. 升级、回滚与审核问答

配套升级 Hub、Bridge、前端，并要求页面刷新，CLI 版本保持已验证集合。升级前先完成活跃问答；不能让同一原生调用跨 v2/v3 切换。未完成配套升级时不打开新问答能力，普通功能继续使用原兼容路径。

回滚前停止接纳新的问答运行，尽可能完成在途回答和回执同步；必须中止时由用户明确停止相关 run。优先保留当前 Bridge 到回执窗口结束，不承诺重启／回滚 Bridge 后还能恢复内存 receipt。仅回退本任务文件，不 reset/clean 整个工作区；普通审批配置、数据库及模型设置不作回滚联动。运行中直接关闭 rollout 不是“零影响”的回滚方式：既有阻塞问题必须先结束或明确取消，不可只隐藏卡片。

**怎样证明不影响原功能？** 通过修复前基线、上述跨路径断言、同次全量门禁及真实往返共同证明已覆盖的行为；不声称数学意义的绝对零缺陷。

**为何需要改问答协议？** 旧快照只能说当前问题仍活跃，不能证明浏览器那次答案从未进入队列。新增权威核对和旧 ID 封禁才能安全恢复编辑；只改 UI 会引入迟到答案竞争。

**为什么不在路由失败后直接重发？** 用户选择的同原生调用连续性和防重复要求优先；查询不带答案，只有最终 not_sent 才让用户用新 ID 明确提交。

**会扩大到普通聊天或改变信任模式吗？** 不会；修复限定实验站问答。免执行审批仍需要真实用户回答，原执行审批政策不变。

**现在是否开始修复或上线？** 用户已授权按本计划修复；上线仍须满足发布门禁并另行确认部署范围。文档状态不能代替测试和实际浏览器验收。
