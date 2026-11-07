# Completion Email Summary

## Goal

完成邮件只回答任务是否通过、执行失败、取消或尚未完成验证，给出 Token 与费用摘要，并提供一个打开网站详情的入口。收件人不需要在邮件里阅读完整对话、证明日志或模型生成的长结论。

持久化与依赖边界见 [ADR-031](../adr/031-completion-mail-usage-snapshot.md)。

## Non-Goals

不改变任务生命周期、验证 quorum、模型调用、价格目录、登录或 VPN 边界；不推断反例、不将 provider 错误当作命题失败；不补发旧通知或修改真实运行。待处理提醒沿用固定短正文。

## Current State And Visual References

旧邮件仅有纯文本，含最多 3,000 字的模型结论及多项未完成项。新版入口为 `internal/notify/completion.go:FromNotification`。通知原先只保存提交结果，缺少冻结的统计摘要。

2026-11-07 查看了以下 21st primary 页面及其公开预览图片：

- [Stats Grid](https://21st.dev/@shadcnui-blocks/components/stats-04)：页面声明 MIT，参考其浅色边框和分隔指标单元格。
- [Stats cards with links](https://21st.dev/@ephraimduncan/components/stats-cards-with-links)：参考卡片底部的单个详情入口。页面未列明 license，因此只借鉴公开可见布局，不复制源码。

实现重新编写为邮件可用的 HTML 表格、内联 CSS 和系统字体。不下载登录后源码，不引入 React、Tailwind、外部字体、图片、脚本或跟踪资源。公开预览的本地只读留证位于当前任务 validation 的 `email-reference/`。

## Design

### Result And Content

顶部依次显示 ProofBridge、醒目结果状态、任务名和一至两句固定说明。仅执行完成且权威 verification 为 pass 时显示验证通过；执行错误、取消、unknown/continue 分别明确说明，绝不把执行错误称为反例。动态模型 summary、未完成项正文、对话、原始错误和配置不进入邮件。

下方为总 Token、费用估算、输入（含缓存）和输出四个指标。输入显示现有统计的普通输入加缓存，缓存为其中子集；总量沿用网站统计，不能再次加缓存。费用直接使用现有 Hub 聚合，标明美元、估算而非账单；部分价格只显示已知部分估算，全部未知或旧快照显示未记录/未知，不将未知写成零。

只有一个“查看详情”按钮，指向现有本人运行路由。底部简短说明需要登录及适用时的实验室 VPN，并说明统计快照范围。

### Data And Snapshot Boundary

`AutomationResult` 增加真实终态事件的 `usageCutoffSeq`。恢复时没有真实终态事件、旧快照或无可信截止标记时，不追溯猜测用量。

通知 worker 在 claim 后、SMTP 发送前读取截至该 seq 的统计输入，复用 `buildOrchestrationRunStatsWithLedger` 及现有模型/验证器 enrichment。范围为**整个运行累计，截至本次提交结束**，与详情页的运行累计口径相同，但邮件冻结后不会随后续任务或价格目录更新。

读方法使用一个只读事务，复用 Store 的 query helper，不在 SQLite 单连接事务里调用另一个 Store 方法。模型价格只使用既有内存聚合，禁止新增网络、模型目录刷新或另一套计费。

只从截止事件内观察到的 native session/worker identity 接受 ledger 行，并在聚合前排除截止后的数据。原 ledger 无可靠逐 turn/task identity，时间精度为秒；如果发送前已经出现后续用户提交而不能证明归属，则保守显示未记录。不能用当前运行累计冒充旧提交。

首次投影以通知 id、claim token、原 snapshot 做 CAS，将小型 `usageSummary` 冻结进 outbox JSON；重试复用同一值，迟到的结论不得覆盖它。不新增数据库列，不保存整事件、模型凭据或原生 session identity 到邮件摘要。

### MIME And Safety

完成邮件使用 `multipart/alternative`：先 `text/plain`，再 `text/html`，各自 UTF-8 quoted-printable。纯文本退化包含同样结果和指标。HTML 使用 Go `html/template` 的上下文转义；任务名截断并去除换行，URL 由受信任的站点 base 和转义后的 run id 生成。旧纯文本测试邮件和提醒继续可发送。

## Implementation Steps

1. 增加结果分类、短摘要、HTML/text 共用视图及实际 MIME 编码。
2. 增加 cutoff、小 usage 类型和发送前读投影/CAS；保留既有终态与通知幂等性。
3. 复用原 Hub 聚合，将明确范围的数值冻结到当前 outbox。
4. 用合成数据由实际 renderer 生成可打开的 HTML 及截图，验证窄屏与长标题。
5. 执行结果矩阵、未知/部分费用、注入、MIME 和真实本地 SMTP 测试，再交独立复核。

## Exit Gates

- pass、未通过/未完成、取消、执行错误和旧快照的主题与正文均正确；长模型结论和日志完全不出现。
- 总 Token 与网站聚合一致，缓存不重复；已知、部分与未知费用有清晰区别，估算不声称账单。
- 同一运行续问、同秒截止、迟到结论、重试和旧 cutoff 不串用量；CAS 并发及单连接无死锁通过。
- HTML 注入与 URL 注入被转义或拒绝；无外部请求资源；text 退化可读。
- 实际本地 TCP SMTP 收到合法两部分 MIME，原测试邮件、待处理提醒和接受边界回归通过。
- 合成预览明确不是实际实验用量。未更新 live Hub/Bridge、真实 DB/prefs 或证明目录；外发新版测试最多一封且由协调者先知情。

## Reviewer Q&A

**为什么不用模型 summary？** 它可能很长、含日志或原始提示，且不能保证简洁。邮件用确定性的结果描述，完整证据留在登录后的详情。

**为何统计可能“未记录”？** 旧快照或后续提交可能使原始 ledger 的逐提交归属无法证明。缺值比把后续任务费用算给旧邮件更准确。

**邮件与稍后打开网站是否永远相等？** 口径一致，时间点不同。邮件是截至本次提交结束的运行累计冻结值；网站可能已有后续提交、补充 ledger 或新目录价格。

**是否复制了 React 组件？** 没有。只查看公开样式参考，使用自行编写且适合邮件的表格和内联样式。

**是否影响验证与执行？** 不影响。邮件投影是旁路读取与 outbox 元数据，不改变模型、digest、权限或 quorum。
