# 动态编排与运行中追加指令

用户于 2026-09-29 在 grilling 后确认实施。

## 需求和验收
- test 新运行无前台轮次选项、无后台数值轮次上限，每轮继续按完成/停滞判定；保留停止按钮。
- 常驻对话输入框，默认排队、显式立即发送。保存原 runID，当前工作器接收且后续工作器继承新指令。
- 展示队列、原生接收、失败状态；重载不丢消息，不把 HTTP 成功等同模型处理。
- 原生结构化问答复用；已接受的追加内容未交付时不得误报任务完成。
- test 领先前端、proofbridge 旧前端，共用实验Hub与原数据库/JWTSecret。旧请求和旧运行保持原轮次语义。
- 网站构建与部署，不生成四平台下载包。功能依赖的本机Bridge更新需明确报告，保留机器身份/配置。
- DNS通过用户授权的Cloudflare OAuth配置。部署前在线备份，不覆盖用户模型库/机器/历史数据。

# 技术设计

主设计见 [ADR-025](../adr/025-dynamic-orchestration-input.md)。

执行模式 executionMode: dynamic|bounded，缺省 bounded，持久化到运行和graph payload。旧运行不因新域名访问而转换。
运行中 POST /api/orchestrations/{runID}/prompts 复用连续性端点；携带 messageId（幂等）、deliveryMode queue|immediate、prompt。GET 同一路径读取追加指令列表。
消息持久化：queued -> sending -> delivered|failed。delivered仅原生协议接收/完整写出，不声称模型已执行。明确不确定的传输失败，不能无声重复投递。
Bridge capability orchestrationLiveInput 与版本字段匹配。原生Codex用turn/steer和turn/start；排队等待当前原生turn结束。Claude复用stream-json持久stdin，队列使用原生处理，立即路径按能力实际验证。不得杀进程伪造成功。
Hub将追加消息文本纳入后续task上下文；有未处理输入时撤销旧完成/停滞判定，继续处理新增要求。消息API校验owner、状态、大小、幂等ID。
新UI由构建变量VITE_LEADING=true启用新建dynamic模式；旧前端静态完整备份独立部署。API/WS始终指向同一个8090 Hub；不运行两个调度器共享SQLite。

# 执行计划

1. 写ADR与feature，保留既有未提交源码。
2. Store/Hub/protocol新增动态模式与回归（主agent）。
3. 原生Bridge输入+Hub持久化消息API和定向测试（实现agent）。
4. 前端输入状态与动态界面、浏览器验证（实现agent）。
5. 主agent联调冲突、全Go门禁、前端构建、doc-lint、真实原生输入探针。
6. Online SQLite备份、旧前端快照、更新Hub及必要本机Bridge，部署test静态入口；DNS与TLS验证。
7. 验证用户/机器/配置/历史仍存在、旧站可用、两站共用API；清理本次临时产物，保留回滚备份。

## 非目标
不新做结构化问答系统，不自动转换旧运行，不新增第二套账户库，不发布跨平台安装包。

## Reviewer Q&A
- 为什么不能只隐藏轮次？后台仍会到限停止，因此需要持久化运行模式与调度判定。
- 为什么不复制数据库？复制后实时数据分叉，无法保留同一机器连接与配置更新。
- 投递失败会不会偷偷重建会话？不允许；必须在原run/原生会话路径显示失败或明确恢复。
- 如何判断接收？以原生回执/写出事实为准，不以HTTP请求成功冒充模型处理。

## 状态
已于 2026-09-29 部署至 https://test.sparkon.cn，版本 `v0.3.81-leading-dynamic-20260929`。旧实验站保留原前端，两站共享原 Hub/SQLite。用户明确授权停止旧运行并更新 test1 Bridge，原运行已取消且历史保留。

## 验证记录（2026-09-29）

- Store/Hub 定向测试：模式迁移、旧轮次兼容、超过第12轮继续、完成/停滞退出、追加验收要求使旧结论失效；消息幂等、owner隔离、旧回执拒绝与终态重试。
- Bridge 定向测试：Codex同thread queue/steer、Claude中断回执成功/失败、首个result不能提前结束待处理输入。
- 浏览器1280/390：排队/立即、刷新恢复、网络不确定结果查询与幂等重试、能力提示、旧run继续；原有结构化问答完整回归通过。
- npm test、领先版Vite构建、doc-lint通过（文档工具已有Server.Run符号warning，无error）。
- Codex 0.158.0真实隔离会话：turn/steer收到后续指令；同thread下一turn验证上下文仍在。已经输出的内容不会被撤回。
- Claude 2.1.281仅完成真实协议探针：UUID触发queued/started，错误result先于lifecycle cancelled。本机未登录Claude，未声称完成真实模型端到端验证。
- 全量 Go 测试中 Hub、Store、集成及其余包通过；Bridge 四个短时限测试在高负载下超时，随后分别复跑全部通过。全量命令本身并非一次通过。Claude 中断缺少回执时的 scanner 收尾回归另行通过。
- 公网两站健康、TLS、首页及入口资源与各自静态快照一致；真实浏览器访问同一旧 run 无页面异常，test 有运行对话且新建页无轮次选项，proofbridge 保持旧页面。
- 部署前后比对：4 个用户、11 个模型预设、20 条预设凭据内容完全一致；33 台机器、55 次运行的原 ID 全保留，机器身份与工作目录配置不变。test1 在线且 `orchestrationLiveInput=1`，Codex/Claude 适配能力均已上报。
- SQLite online backup 已提交并可独立读取关键表；未将耗时的完整 quick_check 宣称为已通过。回滚备份及验收报告位于 `/opt/proofbridge-isolated/backups/20260929-dynamic-input/`。

## 快速手动验证

在test新建Codex+Codex运行，任务可用：

> 在当前工作目录进行一次只读检查：先说明检查计划，再执行一个20秒的等待命令，之后列出顶层文件并说明项目结构。不要修改或删除文件。

运行时发送“排队”：

> 最终答复增加一个‘仍需确认’小节，明确区分已看到的事实和推测。

运行时发送“立即”：

> 修改方向：不再展开项目结构，只报告是否存在README及其文件名；不要修改文件。

确认URL runID不变、消息状态变为已送达CLI，刷新仍保留记录；查看后续输出是否采纳追加要求。旧run保持原bounded语义，验证无上限需新建领先版运行。

## 对话界面优化（2026-09-30）

目标：减少已完成问答占用的空间，让正在回答的问题与主动追加指令有清楚的视觉层级。

- 设计沿用现有中性灰、主文字色与 emerald/amber/destructive 状态色；使用现有字体和图标，4px 间距单位、12px 外框圆角、轻边框与 150ms 颜色过渡。
- 原生问题入口在无待答时显示已完成摘要，最后一个问题确认送达后自动收起，仍可展开回看。新问题自动打开，切换或收起不能清除未提交草稿；不因纯刷新状态重新强制收起。
- 请求导航保留参与者、序号、题数及状态；减少满色块，送达使用小勾与文字。已完成卡片仅呈现一行回执。
- 追加指令以一体式输入框整合操作；排队为主要按钮、立即发送为次要按钮，快捷键及语义提示收敛为一行。状态记录支持折叠查看，待发送／失败清晰可见，刷新仅用带可访问名称的图标按钮。
- 手机端使用按内容排列的滚动布局，限制输出区高度，避免展开问答后配置区与输入框重叠；桌面保留双栏。
- 非目标：不修改 API、数据模型、原生投递或停止行为；不升级 Bridge，不发布跨平台包。
- 验收：桌面／手机、浅色／深色截图检查；问答自动跳转、完成收起与重开、草稿保持；追加消息发送／刷新／失败／幂等与继续旧运行的现有浏览器回归；构建及静态资源验证后部署 test 前端，保留旧站快照和共享数据。
- Q：收起后能否核对状态？摘要保留完成数量，点击可展开原请求；自动收起仅发生于全部待处理请求确认送达时。

发布记录：前端发布 `20260930-conversation-ui` 已切换至 test.sparkon.cn；`proofbridge.sparkon.cn` 继续使用旧快照。此次仅切换 Caddy 静态资源，Hub/Bridge 未重启，数据库未操作，健康接口仍显示原后台版本 `v0.3.81-leading-dynamic-20260929`。回滚目录 `/opt/proofbridge-isolated/backups/20260930-conversation-ui/` 保留原 Caddy 配置、旧静态路径及新资源 SHA256 验收记录。

验证：npm test、问答与追加指令浏览器回归（1280/390）、浅深色截图、手机展开时不重叠断言、领先版生产构建、Go 静态资源测试及本机二进制构建通过；doc-lint 0 errors，保留既有 Server.Run 锚点 warning。公网首页及编排 JS/CSS 与部署产物一致，旧站首页未变。

## 输入入口说明（2026-09-30）

已有运行时，右侧原任务输入位置保留说明与“前往输入”按钮，聚焦对话区的同一个输入框。运行中说明可追加指令，停止／结束后说明可继续当前任务；首次新建仍使用右侧任务输入框。目标是让入口切换可理解，避免两个可编辑输入框造成歧义。沿用原 runID、草稿状态及 prompts API，不改变取消或恢复协议。验收覆盖桌面与手机的定位、草稿保留，以及取消后继续仍使用原运行。

- Q：为什么不恢复第二个输入框？已有运行统一从运行对话发送，右侧提供可发现的导航；不增加独立草稿或新建运行路径。

发布 `20260930-input-entry`：已部署 test 前端，备份 `/opt/proofbridge-isolated/backups/20260930-input-entry/`。1280/390 浏览器模拟取消、草稿保留、定位聚焦、沿用原 run 继续均通过；领先版构建、Go 静态资源测试及本机构建通过。Hub/Bridge 无需重启，配置及持久化数据不变。

## 送达状态自动更新（2026-09-30）

目标：CLI 回执到达后无需手动刷新；慢网络不能使有效查询结果一直被丢弃。前端接入 Hub 已有 `orchestration_input_status` 通知，校验 runID 后立即查询持久化状态；WebSocket 建立／恢复、运行状态变化和页面回到前台时同样查询。定时查询改为本次结束后再等待 3 秒，成功结果仅在已有更新结果应用时才丢弃；请求超时提示并自动重试，卸载时取消请求。查询使用 no-store，避免缓存旧状态。

范围：仅前端状态同步和提示，不改变 CLI 投递、API、持久化或消息幂等。仍以服务端回执为准，不把 HTTP 接受显示为已送达。无需升级 Bridge 或安装脚本。

验收：无需点击刷新即可处理成功／失败回执；无回执时轮询兜底；连续超过 3 秒的响应仍更新、乱序旧响应不回退；页面回到前台自动查询；保留发送失败、幂等重试、取消／继续原运行回归。

- Q：为什么保留轮询？断线或漏收回执时从持久化状态恢复；手动刷新只作为补充入口。

发布 `20260930-live-input-sync`：已部署 test 静态前端。1280/390 浏览器验证持续 4.5 秒查询、暂停定时器后的 WebSocket 更新、旧响应乱序不回退，以及原发送／幂等／继续运行回归通过。npm test、生产构建、Go 静态资源测试及本机构建通过；doc-lint 无错误（既有 Server.Run warning）。公网页面与 JS/CSS 字节匹配，旧实验站首页未变，Hub PID 未变。回滚记录位于 `/opt/proofbridge-isolated/backups/20260930-live-input-sync/`。

## 原生输入适配器边界

`internal/bridge/orchestration_live_input.go:dispatchLiveInput` 使用 executionID 隔离活动 attempt，并在同一 execution 内去重消息 ID。持久化层以 runID/messageID 唯一约束及 executionID 条件更新拒绝重复派送与旧回执；断线后的不确定派送只标记失败，不自动重试。

`internal/bridge/appserver_runner.go:readEvents` 对 Codex 排队消息在当前原生 turn 结束后调用同 thread 的 `turn/start`；立即消息调用 `turn/steer`。`internal/bridge/claude_live_input.go:interrupt` 对 Claude 立即消息发送官方 headless `control_request`/`interrupt`，收到成功回执后在同一 stdin/session 写入 user 消息；这会中断当前 turn，不杀 CLI 进程，也不承诺 TUI 的工具后台化体验。队列消息通过原生 `priority=next` 及 UUID 交付，stdout 的 `command_lifecycle` 防止首个旧 result 使任务提前结束。

能力字段 `orchestrationLiveInput=1` 表示协议版本；`orchestrationLiveInputCLIs` 按 CLI 声明适配器支持。Claude Code 2.1.281 是本适配器实证的协议支持下界，不声称它是上游首次引入版本；更高版本正常支持。该版本的无登录探针确认无需 initialize 即发 queued/started，认证失败时 result.is_error=true 先到，随后发 cancelled。推理中断行为仍需已登录环境验收。官方 headless interrupt 来源：[Claude Agent SDK Python](https://github.com/anthropics/claude-agent-sdk-python/blob/main/src/claude_agent_sdk/_internal/query.py)；TUI 行为来源：[Claude Code interactive mode](https://code.claude.com/docs/en/interactive-mode#queue-messages-while-claude-works)。
