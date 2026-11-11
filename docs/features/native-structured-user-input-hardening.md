# 原生结构化问答：审查缺陷与 Claude 实测修复方案

> 后续状态：二次审查的 R1–R6 已按[二次审查修复计划](native-structured-user-input-regression-plan.md)实施，同次完整 Go 测试已通过；真实模型到 v3 实际浏览器的端到端验收仍待完成。本文保留第一轮设计和验证历史，具体进度见[实施清单](../reviews/native-user-input-evidence/implement.md)。

状态：**代码已实施、最终验收中**。日期：2026-09-24。本文保留审查时的证据和目标契约；实施状态以[任务清单](../reviews/native-user-input-evidence/implement.md)及文末验证记录为准。下文“必须”“拟”描述原定验收目标，不代表仅凭代码改动就已满足。

本方案补充[原功能设计](native-structured-user-input.md)与 [ADR 024](../adr/024-native-structured-user-input.md)，保留用户选择 A：遵循原生阻塞／非阻塞语义。环境配置、架构及联动规则分别以[开发流程](../dev-workflow.md)、[架构](../architecture.md)、[变更影响表](../change-impact.md)为权威，不在此复制全表。任务要求与实施记录见[任务 PRD](../reviews/native-user-input-evidence/prd.md)和[实施清单](../reviews/native-user-input-evidence/implement.md)。

## 目标、范围与证据等级

目标是消除空快照崩溃、控制通道阻塞、待答问题不可见、终态复活和送达确认丢失，使浏览器在乱序、断线、关闭竞争下仍准确表达原生调用状态。

本期不新增问答历史数据库、桌面推送、独立“取消问题”语义或普通聊天支持；不改执行审批政策，不强制暂停所有 worker。不得将答案变成新 prompt，不得新建或替换 run/session/turn 来补答关闭请求。原生进程死亡后不恢复旧调用，不承诺跨进程 exactly-once。

下表保留**修复前的源码静态核实**，用于复核修复目标；当前实现与验证结果见文末“实施与验收记录”。P1 表示阻断核心流程或生命周期正确性；P2 表示可发现性或状态恢复问题。修复前的测试通过记录不能替代新增行为验收。

| 编号／级别 | 当前事实与锚点 | 触发与影响 |
| --- | --- | --- |
| F1／P1 | `internal/bridge/user_input.go:UserInputSync` 未初始化 Requests；`internal/protocol/envelope.go:UserInputSnapshotPayload` 的 requests 不含 omitempty；`frontend/src/app/lib/userInput.ts:receiveUserInputSnapshot` 直接调用 requests.map/filter | 无有效请求时 Go nil slice 编码为 null；刷新、初次同步或全部关闭后的普通空快照可触发 TypeError。React updater 内异常不应依赖外层 WS try/catch 兜底 |
| F2／P1 | `internal/bridge/client.go:connectOnce` 唯一 ReadJSON 循环同步调用 handleEnvelope；`internal/bridge/client.go:handleEnvelope` 同步调用 UserInputResponse；`internal/bridge/user_input.go:UserInputResponse` 持 deliveryMu 调用原生回调；`internal/bridge/appserver_runner.go:respond` 持 writeMu 执行 stdin.Write | 原生不读 stdin、写锁拥堵或 Claude socket 写阻塞时，后续取消、同步、其他回答及 Pong 的读取均可能停顿；不是仅单个问题变慢 |
| F3／P2 | `frontend/src/app/pages/OrchestrationWorkspace.tsx:OrchestrationWorkspace` 在 scrollRef 容器内先渲染 visibleUserInputs，随后渲染 timelineGroups；scrollTimelineToBottom 跳至日志底部，问题未参与 timelineItems | 长日志底部阅读时，新问题位于视口上方，缺少独立待答提示／入口；阻塞问题可能让用户误认为运行停滞，非阻塞问题可能在被发现前关闭 |
| F4／P2 | `internal/hub/user_input.go:sendUserInputRejected` 生成 revision 默认 0 的 rejected；`frontend/src/app/lib/userInput.ts:receiveUserInputStatus` 对 revision 0 绕过版本比较，rejected 保护只涵盖 delivered／不同 submission | closed 后收到同 submission 或无 submission 的 Hub rejected，可以把卡片设回 pending；closed tombstone 虽仍存在，但此状态分支未使用它，UI 可再次提交已失效问题 |
| F5／P2 | `internal/bridge/user_input.go:CloseUserInput` 覆盖 delivered 为 closed；UserInputResponse 只有 state==delivered 才重发送达；UserInputSync 排除 closed；`frontend/src/app/pages/OrchestrationWorkspace.tsx:connectRun` 断线把 submitting 改回 pending | CLI 已收到答案、送达帧丢失、原生随后关闭时，同 submission 重试仅得到 closed，快照又没有终态凭据；用户无法知道上次回答是否写出。当前机制仍阻止再次原生写入，不应误报为已证明重复投递 |

Claude 修复前实测见[能力验证记录](../reviews/native-user-input-evidence/claude-capability-verification.md)。本机 2.1.281、用户后续指定模型 gpt-6-sol，在 Base URL 移除末尾 /v1 后基础调用成功。default 模式触发真实 AskUserQuestion，但旧 helper 的结果格式被 CLI 拒绝。只在临时包装器中移除 structuredContent、保留单个 content 文本块后，单选、多选和自定义答案成功返回同次 CLI 并完成。该对照证据促成了项目 helper 的修复；同样的包装器在 bypassPermissions 下也完成三类答案的真实往返，未切换权限模式。私有端点与凭据不写入文档。

| 编号／级别 | 当前事实与锚点 | 触发与影响 |
| --- | --- | --- |
| F6／P1 | 修复前 `internal/bridge/claude_approval_mcp.go:handleClaudeApprovalMCPToolCall` 同时返回 content 和 structuredContent；CLI 2.1.281 的真实 permission callback 拒绝此结果 | 修复前 default AskUserQuestion 已到达 socket，但答案无法作为成功原生工具结果被接纳；临时仅去掉 structuredContent 的对照完成原生往返。当前实现结果见文末记录 |

HTTP 基础请求成功、CLI 输出文字、mock 回调通过均不能单独证明 AskUserQuestion 原调用往返通过。本次缺陷不包含 Claude 多选数组编码问题，临时包装器的两个权限模式均实际验证数组答案可用；实际编码验收以对应版本原生工具契约和真实往返为准。

## 必须保持的契约

1. Bridge 是原生请求存活与投递结果的权威。requestId 绑定 run、execution/attempt、worker、turn、原生进程实例；浏览器不能指定新的原生目标。所有排队任务在实际写入前再次验证这一绑定。
2. 原生 request 一旦关闭不可恢复 pending；更高 revision、延迟 rejected 或晚到 delivered 也不能重开。新问题必须是新的 requestId，不能复用旧 ID。
3. “已送达”仅指对应原生响应完整写出成功；不表示模型已处理。“已关闭”描述请求不可回答，与此前是否送达是两个维度。不能仅因 resolved、run 完成或空快照推断用户已经回答。
4. revision 只由 Bridge 在其状态域生成。Hub 的本地路由错误只影响匹配的本次提交提示，不能产生原生生命周期状态。断开连接不是确认投递失败，也不是允许换一份答案的证据。
5. 等待用户没有新加的自动回答时限。原生阻塞保持阻塞，非阻塞仍允许原生独立工作继续；通信写入超时属于传输失败，不是用户思考超时。原有显式命令 timeout 保持不变。
6. 任何同步、查询、重试均不自动写入替代进程。旧 attempt 的投递凭据只能作为只读结果查询，不能授权新 attempt 接受其答案。

## F1：规范空集合，并在一个边界解码

拟在 `internal/bridge/user_input.go:UserInputSync` 构造时初始化 Requests 为空 slice；v1 保证 requests 始终编码为数组，无请求为 `[]`。所有新增集合字段遵循相同规则。

前端在 `frontend/src/app/lib/userInput.ts:receiveUserInputSnapshot` 所属模块建立统一的 unknown → snapshot 解码／规范化入口，供 `frontend/src/app/pages/OrchestrationWorkspace.tsx:connectRun` 调用。兼容已部署旧 Bridge 的 requests:null／缺字段为 `[]`，statuses:null／缺字段也按空数组处理；非数组对象、字符串、非法元素、非法 revision、跨 run 内容则拒绝整帧，不能归一为空快照后误删现有问题。规范化后 reducer 内只消费类型安全的数组，不在多个组件分散补丁。

有效空快照仍须应用水位：删除不晚于快照且已不存活的 active 项，保留更新的实时项及独立终态凭据。只匹配当前连接、run、syncId 的快照；过时 sync 不清理新状态。F5 引入新字段时复用同一解码入口。

## F2：有界异步投递与关闭竞争

### 调度责任

把 `internal/bridge/client.go:handleEnvelope` 的问答处理拆为短暂校验／提交调度与异步投递。WS reader 不能等待原生 I/O、deliveryMu 或队列空位；也不能因“队列已满”的错误通知同步堵在输出队列。相关入队操作必须立即返回 accepted、already-known、busy 或 unavailable 之一。

推荐在 OrchestrationManager 生命周期内维护一个有界调度器，跨 WS 重连复用，避免每次连接创建新的一组 worker。初始内部限额建议全局 8 个执行槽、128 个排队项、每原生 client 最多 16 个排队项、每请求最多一个在途 submission；同一原生 client 的写入继续串行。限额是待实现并压测的内部常量，不新增用户配置。按原生 client 选择可执行工作，不能让等待同一个 writeMu 的任务占满全部执行槽；入队与排队时间均有界，busy 明确可重试、未预留投递权。

不采用每个帧 `go UserInputResponse(...)`：这会允许无限 goroutine 等锁，不能处理重复提交、资源上限或关闭回收。用户长期等待不占投递执行槽；槽位只服务已明确提交的答案。重复同 submission 返回其已有排队／在途／结果信息，不追加原生写任务；不同 submission 仅首个有效预留者获胜。

沿用 `internal/bridge/orchestration_events.go:send` 的输出管理边界，但为问答确认／忙碌状态安排有界 outbox 或可合并的待同步状态，reader 不调用可能持 sendMu 等待满队列的路径。输出拥堵不丢失 Bridge 内部 receipt；下一次 sync 必须能恢复结果。不要为此改动普通编排事件的可靠发送语义。

### 原生 writer 与取消

Codex 的 `internal/bridge/appserver_runner.go:respond`、request、respondError 共用串行写边界；修复不得引入另一把仅保护问答的锁。拟使 writer 支持排队取消、实际写入前校验与可中断写入。等待 writer 锁也必须可取消，不能只有锁后 ctx 检查。

Claude 的 `internal/bridge/orchestration_claude.go:handleClaudeUserInputSocketConn` 拟在用户提交后才设置写 deadline，并验证关闭 conn 能中断 Encode；不得把该 deadline 提前施加在等待用户阶段。Codex 必须验证实际 stdin 类型能否使用 deadline；如不支持，应由拥有原生进程的 client 关闭原 stdin／终止故障进程解除阻塞，不能启动一条孤儿 goroutine 后仅返回 timeout。通信失败只影响相关原生进程及其请求，不能拖垮整个 Bridge 或假装正常继续其旧调用。

建议原生写入及等待写通道共用最长 10 秒传输预算，测试使用可注入时钟／更短期限。预算不包含用户等待。若这会改变共享 client 的失败恢复，应在实施前补充 ADR 024 的后果说明，并验证现有 request／审批响应未回归；只有能证明中断、退出和资源回收，F2 才算修复。

### 明确线性化点

拟用短锁保护请求阶段及不可复用的投递 lease；不要让 CloseUserInput 或原生 readEvents 等待一次无界 native Write。writer 获取串行写资格后，在短锁内最后检查 ctx、request、execution 和 close 标记，然后从 queued 转为 writing，获得一次投递权。I/O 在状态锁之外执行。

| 竞争顺序 | 必须结果 |
| --- | --- |
| 关闭先于取得写入权 | 标记 closed，取消／清空 queued，原生写次数为 0；晚到任务不能获得新 lease |
| 写入权已取得，随后关闭／取消 | 立即禁止新写入，发起中断；不等待 I/O 才处理其他原生事件。已经开始的字节无法撤回，不能承诺“关闭后零字节”；按完整写成功或不确定记录 receipt，生命周期保持 closed |
| 完整写成功，然后原生 resolved | 保留 delivered receipt，再关闭请求；最终 UI 同时知道“已送达、请求已关闭” |
| 部分写、超时或结果无法证明 | receipt 为 unknown，关闭请求，不能回退 pending；不自动重投，不标记已送达 |
| 关闭后写回调迟到成功 | 只补充合法 lease 的 delivered receipt，不重开生命周期；没有对应 lease 的迟到回调忽略 |

因缓冲写入成功与原生逻辑关闭可以交错，完整写成功仍不证明 CLI 使用了答案。保留 F5 的两维状态可准确表达该限制。取消与原生终态的 reader 必须能在写被挂住时继续消费；最终回收阶段等待 writer 退出时采用明确期限。

## F4 与 F5：生命周期与提交凭据分离

### 推荐状态模型（拟议）

现有 `internal/bridge/user_input.go:pendingUserInput` 使用一个 state 混合 pending、delivering、delivered、closed。拟拆成以下独立部分：

| 部分 | 内容与责任 |
| --- | --- |
| 原生生命周期 | open／closed、closeReason、lifecycleRevision；closed 单向不可逆 |
| 投递进度 | idle／queued／writing／settled；内部调度使用，不将“已入队”显示为已送达 |
| 提交凭据 receipt | requestId、runId、executionId、submissionId、outcome（delivered／not_sent／unknown）、receiptRevision、无敏感内容的原因码；可晚于 closed 到达 |
| 浏览器提交状态 | idle／submitting／confirming／error，绑定 submissionId；断线后的 confirming 与待填写问题区分 |

确认某次回答只认 receipt；问题能否接受新答案只认生命周期和投递预留。相同 submission 重试应查已有 receipt，不能先因 state==closed 返回笼统失败；查得 delivered 时仍回复 delivered 事实及 closed 生命周期，不新增写入。不同 submission 在已预留、已投递或已关闭时均不能覆盖首次回答。

同 ID 重发是不可变的同次操作，不接受修改答案来更新该提交。Bridge 对同 submission 直接返回此前状态且不重新使用携带的 answers；前端禁止在结果未知时编辑并换 ID 发送。敏感答案不为幂等缓存额外复制或持久化。

### F4 的过渡兼容规则

在 v1 上可以先做单独修复：`frontend/src/app/lib/userInput.ts:receiveUserInputStatus` 首先检查 closed 项／tombstone；任何 rejected 都不能把它改回 pending，delivered 也不能使其重新可答。Hub revision 0 rejected 仅能作用于当前活跃请求、当前本地 submitting 且 submissionId 精确匹配的提交；无 submissionId 的 Hub 错误作为连接级提示，不修改单个问题生命周期。

不要笼统丢弃所有 revision 0：合法的 Hub 路由失败仍需给当前用户反馈。建议给新版状态显式携带来源／阶段（如 source=hub、phase=route）和稳定原因码；旧版缺字段时，仅用已知 v1 revision 0 + rejected 规则兼容，绝不让浏览器指定来源后被 Hub 原样冒充 Bridge。

Hub 拒绝或成功转发都不证明更早一次相同 submission 没有送达。路由失败可提示“本次未转发”，但未知的前次结果仍保持 confirming，通过 sync 查询；只有 Bridge 权威返回最终 not_sent、原队列任务及 lease 已撤销、预留已释放且仍 open，才允许重新编辑答案。queued 即使尚未写出也不满足条件；改答作为新操作使用新 submissionId，已知旧 ID 仅返回原结果。

### F5 的同步协议扩展（拟议 v2）

只在当前状态字段上保留 delivered 会丢失关闭事实；只在 closed 上附加一句 reason 又不能可靠表达身份与幂等。推荐可选字段扩展现有帧、版本化协商 v2，并保留 v1 解码兼容；落地前在 ADR 024 和原功能设计中更新正式 wire 契约。以下字段为建议名称，尚未存在：

| 帧／模型 | 拟增加／规范化内容 |
| --- | --- |
| status | 独立 lifecycle/receipt，带 executionId、Bridge instance 身份及各自 revision；Hub 路由错误独立 source/phase，不生成权威 revision |
| sync | 有界 knownSubmissions 查询（requestId、executionId、submissionId），只查结果、不带 answers；每帧最多 64 项，超量显式拒绝或分批 |
| snapshot | requests 始终为数组；增加终态 lifecycle 摘要、receipts、查询中 unknown/expired 的明确结果；独立总量上限，不沿用当前 statuses 仅 delivered 且最多 64 的假设 |
| 浏览器 collection | open items、closed tombstone 与 receipts 分开投影；快照不包含 active request 不能删除其已确认 receipt；无题目正文的终态可显示最小状态条 |

同进程断线恢复流程：浏览器保存未确认的 submission 元数据 → 重连先 sync → Bridge 返回仍存活的问题和有界凭据 → delivered 则清空答案并显示送达，closed+delivered 显示“已送达，问题已关闭”，closed+unknown 显示“问题已关闭，上次送达未确认” → 仅确认 open、最终 not_sent 且原任务／lease 已撤销和预留已释放时，允许用户以新 submissionId 明确重试；旧 ID 始终只查询原结果。浏览器不在重连时自动重放答案。

页面完整刷新后不要求恢复未提交草稿，也不把答案存 localStorage/sessionStorage。Bridge 快照提供缓存窗口内的终态摘要，便于刷新识别已完成交互；同一标签页可在 sessionStorage 保存纯 request/execution/submission ID 以查询本次结果，若采用必须带 Bridge 实例、run 作用域和过期时间，不能存题目、选项、答案或凭据。不需要该存储也能通过 Bridge 返回的有界摘要恢复当前窗口内的结果；默认优先这一较小方案。

`internal/hub/user_input.go:userInputRun` 当前仅接受 active run，`handleUserInputFromBridge` 仅接受 delivered snapshot status。扩展 receipt 恢复必须同时修改两侧校验：写入口继续要求 active run、rollout 开启、归属与能力通过；**只读 receipt sync** 可在授权窗口内查询已结束 run，但只路由到原归属 Bridge，校验用户所有权、Bridge instance 与 execution，不返回新问题或发起执行。浏览器 WS 订阅 `internal/hub/orchestration.go:handleOrchestrationWS` 也须验证终态 run 的这条只读路径。run 已删除、用户权限已撤销、Bridge 不能确认时返回 unavailable，不泄露其他用户的 request 是否存在。

新 Bridge 进程的 revision 会重置，不能和旧进程水位直接比较。协商或快照必须明确 Bridge instance／状态域；切换状态域后将旧请求失效，隔离旧 receipt，不能因新 revision 低而无限忽略新问题，也不能因新 revision 高而复活旧问题。复用已有实例信息前先核对协议来源，不能只依赖缺少原生进程随机性的 executionId。

### 缓存、清理与保密

当前 `internal/bridge/user_input.go:pruneUserInputsLocked` 只在总量超过 256 时裁剪 closed 项，closed 对象仍可能保留 questions、ctx 和 deliver closure。拟在关闭／投递完成后尽快释放题目、答案、原生 ID、回调及 transport 引用，终态只留下最小 receipt/tombstone；仍在写入的对象先保留必需引用，回调结束立即释放。

建议终态元数据全局最多 256 项、每 run 最多 64 项、关闭后最多 10 分钟，按先到的限制裁剪；这是**有界确认恢复窗口**，不是可回答期限。执行结束立即清理敏感正文与活跃回调，但最小 receipt 可存活至该窗口结束；这一点需要修订原设计“随执行结束释放”的表述。可用惰性过期配合现有生命周期清理，避免每条 receipt 一个永久计时 goroutine。缓存被清理或 Bridge 重启后明确返回 expired/unavailable，UI 只说未能确认，不声称未送达或已送达。

前端不只删除 `userInputSubmissionsRef`：`frontend/src/app/components/UserInputCard.tsx:UserInputCard` 的 custom/selected、闭包中的提交副本也应在 delivered、closed、切换 run、卸载时清理。confirming 期间若要保留用户明确重试所需答案，仅留内存、只限原调用仍有效；关闭或明确 delivered 后立即删除。JavaScript 清引用不等于保证内存物理擦除，验收只要求应用不可再访问、无持久化和日志泄露。公共分享、普通事件、SQLite、错误消息和指标均不含问题／答案正文；可记录计数、耗时及固定原因码。

## F3：持久可见的待答入口

推荐保持完整问题卡只有一份，在日志滚动容器外、实验站当前 run 区域内增加固定可见的紧凑提示入口，显示“等待你回答 N 项／可回答 N 项／待确认 N 项”。这比复制整张卡或自动滚动更小，也避免两份表单草稿不同步。入口不得随旧日志一起滚出视口，移动端不依赖桌面侧栏。

点击或键盘激活入口时，滚动到第一个相关卡片并聚焦其标题／首个字段；多项按稳定顺序可达。新问题通过 aria-live=polite 宣告数量与等待性质，不自动抢焦点、不自动跳离用户正在读的日志、不朗读 secret 或题目正文。卡片关闭时更新计数，失效的按钮不能继续指向不存在的表单。没有待答／待确认项时入口消失，已送达终态仅保留轻量结果展示。

状态展示必须依 F4/F5 reducer 的可答性计算，不能仅看 request.blocking 或收到新 request 就累计。阻塞文案描述相应 CLI／worker 在等待；非阻塞文案保持“可回答，任务仍可继续”，不把整个实验站显示成暂停。保留现有停止运行与日志滚动按钮，入口不遮挡二者。通过 `frontend/src/app/lib/i18n.ts:1` 维护中英文文案，复用既有 button、focus 和响应式组件。

## F6：Claude permission MCP 的结果格式兼容

拟让 permission host 的 `tools/call` 结果按经本机验证的单个 content 文本块返回，text 为 permission decision JSON；保留 questions 与 answers，不借修复修改 permissionMode。临时对照包装器不是部署依赖，修复应落在现有 helper 边界，不新增永久代理进程。

新增回归必须使用实际 MCP JSON-RPC 输出验证 content 恰有一个 text 块、其文本可解析为 decision；不能仅断言 Go 内部 decision 对象字段。分别覆盖普通工具 accept／deny、AskUserQuestion 单选／多选／自由输入、取消与非法答案。移除 structuredContent 是否影响其他调用方须通过当前消费者核对及普通审批回归证明。

default 与 bypassPermissions 分别用**修复后的真实项目 helper**完成原生往返；包装器成功只证明候选修复方向，不计作修复后通过。本次临时包装器已证明 2.1.281 的 bypassPermissions 仍进入 permission host；正式修复必须保留这一行为，不能通过改成 default 冒充通过。若其他版本不支持，按版本关闭能力并补充证据，不能泛化本次成功。真实能力验收还需经过实际 manager、Hub、浏览器，取消不应新建 turn。

## 实施顺序与依赖

以下均为未来实施步骤，尚未打勾。F1 和 F4 的 v1 防御修复可先独立落地；完整功能发布要求全部阶段通过。

| 阶段 | 交付物与依赖 | 完成判定 |
| --- | --- | --- |
| S0 契约冻结 | 明确 lifecycle/receipt、实例水位、关闭线性化点、容量和有效期；根据新 CLI 证据更新能力门禁，修订 ADR 024／原 feature | 评审通过，v1/v2 混合版本策略与失败语义无歧义 |
| S1 边界回归 | F1 空数组生产者+统一解码；F4 closed 单向与 Hub rejected 匹配；先增加失败用例 | null 快照无异常，所有终态／rejected 顺序均不可复活 |
| S1b Claude 兼容 | 依赖真实 CLI 证据；F6 MCP 返回格式、普通审批回归与分模式探针 | 项目 helper 原生往返通过；尚不支持模式保持关闭，不把临时包装器当交付 |
| S2 投递模型 | 依赖 S0；Bridge 两维状态、receipt 缓存、单次 lease、有界调度与原生可中断 writer | 同次最多一次写；取消、原生通知、WS reader、资源回收均通过故障注入 |
| S3 同步恢复 | 依赖 S1/S2；协议 v2、Hub 校验／只读终态查询、前端 reducer 与内存清理 | 丢确认→关闭→重连仍返回原结果；已退出原生进程不可补答 |
| S4 入口可见性 | 依赖 S1，最终接入 S3 状态；固定待答入口与唯一表单 | 长日志、手机、键盘、secret、多个问题通过浏览器验收 |
| S5 完整门禁 | 依赖全部；真实 CLI × 模式、集成、race、前端 build、Go 构建、文档联动 | 所有必须项有证据，无法验证的 CLI 模式保持关闭并准确标注 |

实施修改范围预计涉及协议、Bridge 状态机／client／两端 writer、Hub user_input／WS 生命周期、前端 types／reducer／Workspace／Card／i18n 和相关测试。按照[变更影响表](../change-impact.md)同步权威文档；本轮只更新设计、测试证据和任务／规范索引，不改这些功能代码或静态资源。

## 测试步骤与具体断言

测试以可控 barrier、fake writer、显式 frame 队列和可注入时钟建立顺序，避免靠长 sleep 猜竞争。现有 `internal/bridge/user_input_test.go:TestUserInputSlowNativeWriteDoesNotBlockOtherRequests` 直接并发调用 manager，不能证明唯一 WS reader 存活；必须增加经过实际 Client 分发循环的测试。现有前端 `frontend/user-input-check.mjs:1` 可承载 reducer 行为回归，视口可见性则用已有 Playwright 依赖启动真实页面验证，源码字符串断言不能替代。

| 用例 | 设置／步骤 | 必须断言 |
| --- | --- | --- |
| T1 空快照 | 无请求、全部 closed、ctx 已取消三种情况调用 UserInputSync，经过 JSON 编码后交给前端；另输入 null、缺字段、[]、对象和坏元素 | Go 输出 requests:[]；旧 null／缺字段兼容；无 TypeError；非法结构不清空当前状态；较旧空快照不删除较新 request |
| T2 reader 与真实入站控制 | 用假 CLI 阻塞 A 写入；按顺序经 WS 发 A response、B sync、B response、cancel、heartbeat/control | 未释放 A writer 时 B sync 与取消已被处理；可用执行槽下 B 可投递；WS 持续读；不靠直接并行调用 manager 冒充测试 |
| T3 有界饱和 | 填满执行槽、全局／每 client 队列，重复同 submission，再提交不同 submission；同时让出站队列拥堵 | 活跃 worker／队列保持配置上界，同次不多占槽；忙碌反馈可恢复；reader 不等待 outbox；解除拥堵后 sync 可恢复权威结果，退出后 goroutine／句柄回到基线 |
| T4 原生 reader 与关闭竞争 | 用 barrier 分别停在排队、取得 lease、部分写、完整写后；注入 resolved、turn.end、ctx cancel、进程退出 | 排队关闭零写；开始后关闭保持 closed；完整成功 receipt delivered；部分／不确定为 unknown；readEvents 能消费终态；所有任务最终回收，无新 turn |
| T5 native writer 回归 | Codex response 与 request／respondError 并发，Claude socket 对端停止读取；触发通信期限与 run 停止 | 原生帧无交错；deadline／关闭确实解除 I/O；普通审批正常；用户等待不被通信期限误杀；原显式命令 timeout 不变 |
| T6 terminal 单向 | request→submitting→closed(rev3)→Hub rejected(rev0)，分别同 ID、异 ID、无 ID；再加迟到 delivered 与 request | 始终不可回答，closed tombstone 不被清；迟到合法 delivered 只能增添 receipt；仍 open 的精确匹配路由失败能显示错误，异 submission 不污染 |
| T7 确认丢失 | 原生完整写成功，截断 delivered 帧；关闭原调用；同 submission 重试／重连 sync；再换不同 submission | 原生写计数始终 1；原 submission 获得 delivered+closed；不同 submission 不覆盖；页面不允许修改后补发，不显示模型已处理 |
| T8 空快照与终态乱序 | 对 request／receipt／closed／snapshot 建立多种交错，包括 closed 先于 request、旧 sync 后至、不同 Bridge instance | 水位不复活 request，receipt 不因 active 列表缺席而丢失；新实例仅建立新状态域，旧请求失效；重复帧幂等 |
| T9 终态 run 查询与权限 | run 正常结束后以 owner sync；再使用其他用户、其他 Bridge、跨 run／attempt、rollout 禁用、run 已删除查询或写答案 | owner 的有效窗口只读查询可恢复凭据；rollout 禁用拒写但保留该查询，权限撤销则读写均拒绝；跨用户／实例等非法路径不泄露结果；终态 run 写入拒绝，查询不启动执行 |
| T10 缓存与敏感数据 | 产生超过容量的 closed 请求，推进 TTL，模拟 Bridge 重启；使用特征明显的 secret 填写并关闭／切换 run | 缓存有界；过期结果明确不可确认；回调／题目／答案引用按约定释放；前端存储、普通事件、分享、SQLite、日志中无 secret |
| T11 多标签页与多 worker | 同原生数字 ID 的两个 worker；两标签对同 request 提交不同 submission，丢帧并重连 | worker 不串答；只一个有效写入；所有标签最终看到相同生命周期和胜出 receipt，失败标签不能复活卡片 |
| T12 长日志与可访问性 | 生成足以滚动的日志，停在底部／中部再注入阻塞与非阻塞问题；桌面及 390px 窄屏，键盘导航 | 不自动跳屏；待答入口始终在可视范围、数量正确、可键盘触达且定位唯一卡片；关闭移除计数；停止运行、日志底部按钮不被遮挡；无 secret 播报 |
| T13 原生真实往返 | 在隔离 runtime home 对两种 CLI、两种权限模式逐项验证，Codex 覆盖 Blocking／NonBlocking，Claude 包括选项／自由输入／多选 | 记录 CLI 版本、实际原生请求和答案路径、原调用继续、权限未改变；阻塞不等于 reader 停止，非阻塞无代答；每项独立通过才声明对应能力 |

针对 T7 的关键事件轨迹应保留脱敏 ID／状态证据：`write-success → delivered-frame-dropped → native-close → reconnect → receipt(delivered, closed)`。只有“答案曾发送”日志不能证明投递确认恢复。

实施后的验证按低并发串行执行：先相关 Go 定向测试与 frontend reducer／浏览器用例，再受影响 bridge/hub 包的 race 检查，然后 `frontend/package.json:1` 中 npm test 与 npm run build，随后全量 Go test、CGO_ENABLED=0 Go build 和 make doc-lint。构建二进制放本次唯一临时目录，不覆盖用户已有 bin 产物。保留刷新后的 embedded UI 作为交付物；不运行 Android 门禁。

## 发布退出门禁与回滚

- [ ] F1–F6 均有失败前证据和修复后行为断言；T1–T12 必须通过，没有仅凭源码匹配给出的“通过”。
- [ ] v2 两维状态与 receipt 查询契约完成跨层评审；新旧组件不匹配时显示限制并禁用不可靠新交互。旧浏览器不理解 v2 时不能静默接收会丢确认的交互；必须用现有协商扩展明确其支持，或要求页面重载并重新协商后再开放。
- [ ] 真实 CLI 矩阵 T13 分项有证据。Claude 必须重测修复后的项目 helper，临时包装器结果不足以开启能力；验证未通过时保持对应能力关闭；不能把“加固代码通过 mock”宣称为完整双 CLI 功能已完成。
- [ ] 确认终态后只读查询不绕过 run 所有权、Bridge 归属和权限撤销；公开分享与敏感信息边界通过。
- [ ] 取消、普通执行审批、会话连续性、非阻塞独立工作、显式命令 timeout 均无回归；无遗留 writer goroutine／连接／未清答案。
- [ ] 前后端相关测试、race、全量 Go 测试、前端生成资源、Go 构建及 doc-lint 完成；按 AGENTS R7 顺序完成文档核查，既有警告单列。
- [ ] 审核交付 diff，保留其他任务改动；不自动提交或部署。仅在用户授权后发布一致的 Hub/Bridge/静态 UI 组合，避免半套协议上线。

发布采用先兼容解码、再部署完整状态契约、最后按能力启用的顺序。v1 的 F1/F4 修复可以先发布防御补丁，但不能借此宣布 F2/F5 已解决；具体启用／禁用行为沿用现有 rollout，不能只隐藏 UI 却允许 CLI 发出无人可答的问题。

回滚时先关闭新原生问答入口，再让现有 in-flight 投递按原 lease 收敛，取消未开始的写任务并把有效请求同步关闭；不得自动默认回答。若原生等待只能通过取消其 turn 才能结束，应走已有停止／取消语义并明确反馈，不把原生阻塞悬挂在隐藏卡片后。只要同一 Bridge 仍存活且权限有效，最小 receipt 的只读查询保留至窗口结束，避免回滚吞掉已成功的确认。

如果回滚必须重启 Bridge，则旧原生请求关闭且缓存失效，浏览器显示送达未确认的限制，不重试到替代进程。回退成套二进制与对应静态 UI，无 SQLite 迁移需要回滚；只恢复本功能明确文件，禁止全仓 reset/clean。临时探针、测试目录、下载工具和验证二进制逐路径确认后清理，保留必要脱敏证据及交付资源。

## 实施与验收记录

此段保留历史实现；版本白名单已由[2026-09-29 最低版本策略](native-structured-user-input.md#版本范围修复2026-09-29)替代。

2026-09-24 已将 F1–F6 的修复落入源码。`internal/bridge/user_input.go` 初始化空集合，并以有界队列、按原生 client 调度提交；原生写入可取消且有期限，Bridge 的 WS reader 不等待原生 I/O。Bridge 的 request lifecycle 与 submission receipt 分离；原生请求关闭后，同一 submission 在缓存窗口内仍能查询送达结果。Hub 校验 Bridge v2 能力、浏览器 WebSocket 的 `user_input_sync.clientVersion=2`、run 归属与只读终态查询。前端集中解码空／旧格式快照，使用 instance/revision 水位及 closed tombstone，固定待答入口位于可滚动时间线之外。Claude MCP 仅返回单个文本内容块；能力按 Claude Code 2.1.281 精确版本和已验证权限模式开放。

定向行为测试覆盖空快照、队列饱和和不同原生 client 公平调度、慢／阻塞 writer、关闭与送达竞态、v2 协商、run 归属和终态只读同步。`internal/bridge/claude_hub_user_input_test.go:TestClaudeAskUserQuestionMCPThroughHubAndBrowser` 用实际项目 MCP helper 经 Bridge、Hub 与模拟浏览器完成请求、答复、关闭和 receipt 同步；真实 Claude CLI 在 default 与 bypassPermissions 下也分别通过修复后 helper 的三类问答往返。这两项证据独立，尚不构成部署环境的真实模型至实际浏览器人工验收。前端状态机、完整 npm test、生产构建与桌面／390px 浏览器用例通过。Bridge 与最新 Hub v2 定向 race 均通过；CGO 关闭的 Go 构建通过；`make doc-lint` 为 0 错误、1 个既有 `internal/hub/server.go:Server.Run` 锚点警告。第二轮全量 Go 测试中 Hub、integration、store 及其它包通过，Bridge 的四个既有测试失败，但同组定向复跑全部通过；全量门禁因此仍记为未通过。详细结果记入[任务清单](../reviews/native-user-input-evidence/implement.md)。

## Reviewer Q&A 与待定项

**为什么不只给 UserInputResponse 加 go？** 唯一 reader 会恢复，但重复帧可制造无限等待，原生 reader 仍可能卡在 CloseUserInput 等 deliveryMu；必须同时有有界调度、可中断 writer 和无 I/O 长锁的关闭处理。

**closed 后再显示 delivered 是否违反终态不可逆？** 不违反。closed 保持不可回答，delivered 是该 submission 已完整写出的历史事实。两者并存，不把状态改回 open/pending，也不声称模型已处理。

**断线后再点提交能换答案吗？** 在上次结果未确认时不能。先查询同一 submission；最终 not_sent、原队列任务及 lease 撤销、预留释放且请求仍 open 后，才能以新 submissionId 更改／重试。queued 不允许改答；旧 ID 只返回旧结果，不自动重播到新 turn。

**有界缓存是否保证任意时间刷新都能知道结果？** 不保证。仅承诺同 Bridge 生命周期且缓存窗口内确认；超出窗口明确 unknown/expired。这是无历史数据库设计的限制，不是“未投递”的证据。

**为何终态 run 还允许查询？** 原生写成功与 run 结束可能紧邻，而当前 Hub active-only 校验会挡掉丢失确认的恢复。只读路径仍严格授权，不能被用于回答、恢复执行或查看其他用户的结果。

**固定提示条会改变原生非阻塞语义吗？** 不会。它只改善发现入口，不暂停 worker、不强制滚动、不自动作答；等待性质仍由原生 blocking 字段表达。

**Claude 端点连通是否足够开放功能？** 不够。必须证明当前 CLI、权限模式与实际 AskUserQuestion 回答通道能继续同次原生调用；当前项目 helper 已通过这项真实 CLI 复测，并另有项目 helper 到 Hub 与模拟浏览器的跨层测试。真实模型到实际浏览器的人工验收仍应在部署后进行。

实施前需要在 S0 冻结三项技术决策：其一，v2 字段和浏览器能力协商的具体形状；其二，Codex stdin deadline 可行性及写故障是否需关闭整个原生 client；其三，8／128 调度容量、10 秒通信预算和 64／256／10 分钟 receipt 窗口能否通过故障与容量测试。上述推荐值不是当前实现事实；调整不得削弱有界性、终态不可逆和原生调用连续性。若需要换 Claude 原生通道、增加持久化或放宽这些不变量，先更新设计审核范围。
