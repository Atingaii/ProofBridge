# 参与者身份与逐题问答

## Goals

- 时间线、轮次标题、提问卡及待答队列优先显示实际参与者：Codex A / B、Claude A / B。
- 角色作为辅助信息；模型名称继续单独展示，不能用 GPT 代替 Codex 身份。
- 原生多题请求逐题填写、可返回修改，整组显式提交；选项、自定义回答和未填写状态清晰可见。

## Non-goals

本次不扩展普通聊天，不修改原生问答协议、能力门禁或原生 CLI 版本，不把普通文字猜测为结构化问题，不将答案持久化。

## Data and protocol

复用事件的 `task.workerSlot` / `turnStartData.workerSlot` 及问答 `workerId`。同一 run/turn 的开始事件可补齐后续消息身份；缺失实例时只展示 CLI，不能从候选方案 A/B 猜测参与者 A/B。`verifier:` 前缀继续标识独立验证实例。原有 v3 请求、答案、回执及 run 连续性保持兼容。

## Implementation

1. `frontend/src/app/lib/utils.ts:orchestrationParticipantLabel` 统一参与者显示；事件 reducer 补齐同 turn 身份。
2. `frontend/src/app/components/OrchestrationComponents.tsx:OrchestrationTimelineGroupItem` 和消息卡将身份置于角色之前。
3. `frontend/src/app/components/UserInputCard.tsx:UserInputCard` 提供题号导航、上/下一题、回答进度、单选/多选提示、自定义输入焦点和明确校验。遵循原生 allowCustom；不预选答案、不自动提交；多选的空自定义输入阻止完成。
4. `frontend/src/app/pages/OrchestrationWorkspace.tsx` 的请求切换使用同一身份名称，保持已挂载卡片与草稿。
   答题面板展开期间隐藏时间线的悬浮跳转按钮，避免手机端遮挡输入。
5. `internal/bridge/orchestration_claude.go:prepareClaudeApprovalServer` 接收真实 worker slot；`updateTurn` 只更新 turn 和角色，保留绑定到会话的 worker，并在写入新提示词前调用，避免快速提问携带上一回合身份。增加原生 socket 回归。

沿用产品的 primary / muted / border 色彩、现有字体与 Lucide 图标、圆角与 focus-visible 反馈；移动端采用换行标题、纵向选项、可滚动题号。

## Exit gates

- 身份格式和同 turn 元数据回归覆盖 Codex/Claude A/B、角色轮换、缺失 slot、验证器和同名 turn 跨 run 隔离。
- Playwright 桌面/手机覆盖逐题返回、单选与自定义互斥、多选加自定义、空白校验、答案 ID 原样回传、队列草稿、回执恢复、键盘操作和无横向溢出。
- 前端测试、嵌入式构建、Go 测试/构建和 doc-lint。

## Reviewer Q&A

- 为什么不从候选方案推断 A/B？候选任务与 CLI 实例不是同一维度，角色可轮换。
- 为什么逐题填写仍整组提交？原生工具按原始问题 ID 接收一组答案，保持既有契约。
- 推荐选项是否默认选择？不默认选择；只显示模型本来给出的标签，提交始终由用户触发。
- 刷新是否恢复答案？仍只恢复待确认提交身份，不存储答案内容；收起、切换请求与前后题在内存中保留草稿。
- 浏览器模拟验证是否代表真实模型验收？否；既有真实模型到部署浏览器的验收门禁独立保留。

## Verification (2026-09-29)

- `npm test`：通过，包含参与者身份、同回合元数据补齐、问答解码和回执恢复。
- `npm run test:user-input-browser`：1280px / 390px 通过。使用 API / WebSocket fixtures 驱动真实页面，断言四个参与者、逐题草稿、选项与自定义互斥/组合、空白校验、答案原样发送、键盘焦点、断线恢复与无横向溢出；已人工检查两种宽度的截图。
- 最终遮挡修复后，两种宽度的浏览器回归再次通过，并复查手机截图；`npm run build` 已刷新 Go 嵌入页面。
- `CGO_ENABLED=0 GOMAXPROCS=2 go build -p 1 -ldflags '-s -w'`：通过。
- `make doc-lint`：0 errors；既有 `internal/hub/server.go:Server.Run` 锚点产生 1 条 warning。
- Claude 能力门禁和 MCP manager round-trip 的定向 `go test -race`：通过；覆盖角色变化后保留 worker identity。
- 初次较宽的 race 选择还包含已有 Hub/browser 集成测试；该次在高负载下登录请求超时，没有报告 data race。后续专项竞态验证使用完整测试名精确选择，不把它等同于全仓库 race 通过。
- 全量 Go 检查的第一次运行被 SIGTERM 中断后已重新执行。重跑时已有 `TestDurableTaskTerminalReleasesCodexWriterBeforeDelivery` 触发 5 秒超时；相同测试单独复测通过（约 4.2 秒），未修改测试阈值或无关实现。Hub、跨模块 integration、store 等检查通过；全量检查不能表述为全绿。
- 清理了本轮临时截图、测试二进制、构建前备份和 43 个中间构建资源，停止临时 Vite 服务。核对 121 个原有静态文件仍在，最终 manifest 引用的 47 个资源均存在；保留源码、回归脚本与最终嵌入页面。
- 上述实现验证阶段未部署，也未触发外部模型；真实 Codex / Claude 到部署浏览器的验收状态不因上述模拟测试而改变。

## Deployment (2026-09-29)

- 已部署至独立实验站 `https://proofbridge.sparkon.cn`，版本 `v0.3.78-exp-identity-input-20260929`，构建时间 `2026-09-29T07:41:11Z`。
- 四个平台安装包从同一工作区构建；基线 commit 为 `3726844974043ccc730130f986384eccd9bbdd9c`，包含未提交修改（`vcs.modified=true`）。
- 独立站 Hub 与本机连接该站的两个 Bridge 均运行新版二进制，进程 SHA256 一致，两个 Bridge 已重连。主站 Hub 与其 Bridge 的 PID 保持不变。
- 公网健康检查返回新版；页面入口、编排页面 JS/CSS 以及 Linux/macOS 的 amd64/arm64 四个下载包均完成 SHA256 比对。
- 在数据库副本上完成迁移、启动与健康检查；正式切换前已停止服务并备份数据库、旧二进制、配置与端点启动脚本。回滚备份及发布验证记录保留在部署目录的 `backups/20260929T074111Z-identity-input/`。
- 浏览器刷新后加载新页面；其他机器上的 Bridge 仍需通过站点提供的安装命令升级。本次未触发真实模型到浏览器的端到端验收。

## Manual question-flow check

1. 刷新站点并进入多 CLI 编排页面，选择在线运行机器。在侧栏“结构化提问能力”确认所选组合可用；这是能力提示，不是需要手动打开的开关。当前最低版本与权限门禁见[原生结构化用户问答](native-structured-user-input.md#版本范围修复2026-09-29)。普通聊天不属于此功能范围。
2. 使用一个新的测试运行，选择协作模式，粘贴下面的提示词。先使用可用的 Codex 组合验证单选和自定义输入；Claude 组合可追加多选测试。

```text
这是一项结构化问答交互测试，不需要读写文件或执行命令。
请当前负责澄清的参与者实际调用可用的原生提问工具，一次提出以下两个问题。
Codex 使用可用的 request_user_input 或 request_user_input_async，Claude 使用 AskUserQuestion。
每题提供选项并允许我自行输入：
1. 页面主题：浅色、深色、跟随系统。
2. 内容密度：紧凑、标准、宽松。
不要只在普通消息里列出问题，不要替我选择答案。
收到我的回答后，逐项复述实际收到的答案，然后结束测试。
若工具不可用，请说明原因。
```

3. 检查面板显示实际 Codex A/B 或 Claude A/B。第一题选择选项，第二题输入自定义文字（例如“测试-0929，自适应密度”）。使用上一题/下一题返回修改，确认未提交内容保留；收起再展开也应保留。
4. 整组提交后，确认卡片进入已送达或原生确认状态，并核对模型复述与最终填写内容一致。只看到发送状态不代表模型已处理；不应新建另一条运行来接收答案。
5. Claude 多选追加提示：“请使用 AskUserQuestion 发起一道允许多选的问题：需要哪些功能？选项为搜索、导出、通知，并允许自定义。”勾选两个选项并填写补充内容后提交，核对完整回显。不要要求 Codex 展示未支持的多选。
6. 活跃问题可另外测试刷新后恢复，但未提交草稿不跨刷新保存。模型若只输出文字，则本次未触发原生工具；若侧栏显示能力不可用，先处理其报告的版本、权限或连接原因。

## 问答导航与完成状态修复（2026-09-29）

- 范围：`OrchestrationWorkspace` 的独立请求队列导航、`UserInputCard` 的逐题导航与反馈，及对应浏览器回归。复用 v3 回执，不修改 Bridge、原生答案编码或持久化。
- Codex 异步提问可能拆成多个单题请求。当前请求从提交中变为已送达后，自动选择下一条 pending 请求；尚在发送或确认时不提前切换，不将“填写完毕”标为“已送达”。已送达请求仍可手动查看。
- 多题卡切换后将题目标题滚入可见区域；已填写题号使用绿色勾选和文字，当前题保留独立焦点标识；选项和自定义输入增强选中反馈。送达后清除草稿并保留成功提示。
- 验收：桌面及手机覆盖预先到达的三条异步请求逐条提交、回执前后状态、已送达回看、下一题及自定义回答草稿、空白校验、实际发送内容。仅部署独立站网站。
- Q：为什么等回执才自动跳转？避免把网络中尚未确认的提交展示为完成；草稿与网络送达是不同状态。

验证结果：新增三条异步请求逐条提交、等待回执再跳转、已送达回看及自定义答案原样传输的浏览器回归；1280px 与 390px 均通过，已检查截图。`npm test`、`npm run build` 和 Go 静态资源／健康接口定向测试通过；`make doc-lint` 为 0 errors、1 条既有 `Server.Run` 锚点 warning。实际用户运行的 Codex 原生会话已确认收到三项答案并据此制定计划；此证据用于确认原有问答链路，新增导航交互使用浏览器 fixtures 验证。

本次网站修复已部署为 `v0.3.80-exp-question-ux-20260929`。公网健康检查以及首页、编排页面 JS/CSS 的 SHA256 与构建产物一致；本机 Bridge 和主站 Hub 未重启。回滚备份保留在独立站部署目录 `backups/20260929T090150Z-question-ux/`。

## 问答摘要与完成收起（2026-09-30）

test 领先站的界面整理见[对话界面优化](dynamic-orchestration-input.md#对话界面优化2026-09-30)。
确认所有请求已送达后自动收起，摘要显示送达题数并保留历史入口；有新问题时重新展开。
待确认不会被算为已完成。请求导航保留序号、参与者、题数与文字状态，回看不显示已清除的答案草稿。
切题只滚动问题面板，避免带动整个页面；原有显式提交及回执后跳转规则继续有效。
