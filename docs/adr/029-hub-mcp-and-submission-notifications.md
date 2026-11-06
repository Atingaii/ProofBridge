# ADR-029: Hub MCP 入口、个人令牌与提交级通知

## Status

Proposed，2026-11-06。用户已确认产品方向并要求本轮留下设计；技术方案待后续实施。
不表示功能已实现，也不改写历史运行验收记录。

完整用户流程、20 个工具及验收编号以[功能设计](../features/mcp-skill-task-notifications.md)为唯一来源。
当前源码证据见[核对报告](../reviews/proofbridge-mcp-current-state.md)。

## Context

现有网站控制用户自己的 Bridge 端点，Hub 持有账号、模型预设与任务历史。
用户希望在注册、连接机器后，复制个人 MCP 配置并安装 Skill，从自然语言发起同样的任务，
并在终端退出后收到任务结束邮件。计算位置、用户隔离、权限和同 run 连续性必须保持。

当前浏览器 JWT、机器 enroll token 都不是个人 MCP Token。任务新建/终态继续缺外部幂等键；
同一 run 可包含多次提交。审批只有重发，原生问答已有 v3 可靠回执。
任务终态存在多个写入入口，节点 run.end 不等于顶层任务结束。

## Decision Proposal

### 1. Hub 内增加 MCP Adapter

在现有 Hub 提供 `/mcp`，复用已有 Store、调度器、Bridge pool 和权限检查，
不用另一进程写同一 SQLite。协议传输、工具注册、鉴权与业务操作分开，
HTTP/MCP 均调用共享业务操作；不通过构造内部 HTTP 请求绕一圈复制 handler 逻辑。

采用支持实际客户端版本的 MCP SDK，版本和协议协商在实现前锁定并测试；
不把现有 Claude 审批 helper 的旧协议硬编码拿来充当完整公共操作服务器。
首版采用 Streamable HTTP，不依赖两客户端都支持某种后台任务/主动推送扩展。
应用层 runId/submissionId 是持久状态，不依赖 MCP 会话或长连接。

Hub 部署在实验室内网；接入模板使用管理员设置的规范访问地址，不从不可信请求 Host
拼出令牌发送目的地。外部发起端和外部 Bridge 都需到达该地址。
既有 public/leading 站点不在本轮部署目标，不自动修改其数据库和运行实例。

### 2. 个人 MCP PAT 与现有凭据分离

个人中心提供每账号的接入令牌与完整模板；注册后建立记录，旧用户首次访问时补建。
建议首版一个活跃令牌，支持撤销/重新生成；有效期受服务策略控制并在界面明确展示。
管理接口仅使用网站登录会话，MCP PAT 仅在 MCP 入口鉴权，不能扩大为管理员或令牌管理凭据。

- 令牌随机生成且不可猜测，绑定 uid 和允许的读/执行/偏好/交互操作。
- 每个工具再次检查资源归属、rollout、能力、权限配置；知道 runId 不等于获授权。
- 为满足用户“随时重新复制已填好模板”，数据库保留受保护的可恢复密文及鉴权摘要，
  而非同时承诺“仅存摘要”和“随时重显原文”。
- 复用既有 vault 的加密/密钥来源实现时保持用途隔离和格式版本，不混用模型预设记录。
  明确备份、密钥轮换、撤销及无法解密时重新生成的恢复行为。
- 模板接口 no-store、登录态访问；日志、MCP工具结果、Skill、事件和邮件均不返回令牌。
- 显式静态 Bearer 是本轮私有集成范围；不宣称完整 OAuth discovery/客户端注册。
  实际 401/撤销行为与两客户端兼容性必须测试。

### 3. 编排方案和正式偏好属于账户

新增 execution preset 与分作用域偏好，引用现有用户模型预设和端点，
不把 `profile=default|formal-proof` 改成个人配置，也不把机器 active preset 当成账户默认。

Skill 提供自然语言解析、查当前选项和保存偏好的流程；Hub 校验并冻结本次实际配置。
已有 run 继续保留原端点和上下文，后来的默认修改不覆盖旧绑定。
预览不执行证明/业务 turn；可调用既有只读模型 metadata 探针。启动重新验证版本/能力，
未知目录状态由 Bridge 真实门禁确认。

### 4. 顶层提交身份、幂等与同 run 连续性

新增持久化提交记录，关联 uid、clientRequestId、规范化摘要、runId、submissionId、
PromptSeq、接受/派发状态、配置与最终结果。创建/认领与请求记录应具备事务一致性。

`internal/hub/orchestration.go:startOrchestration` 现用 user.message 的 Seq 作为 PromptSeq。
本设计复用该提交边界并建立明确映射，不另外发明一套 worker/task attempt 身份。

同键同内容返回原结果，同键不同内容冲突；响应丢失后不新建 run。
幂等摘要依据用户显式输入；先查已接受记录，不能因默认偏好/能力后来改变而把同次重试
重新解析成不同请求。accepted 前应持久化 user.message/PromptSeq 与提交映射及派发责任，
半成品创建失败需按原请求身份恢复/补偿，不能另建第二份 run。
远端派发结果不确定时保留不确定状态，遵守现有 unknown 恢复，不自动重放有副作用的工作。

新 run、终态 follow_up、活动 live_instruction 和原生问题回答是四类不同操作。
继续任务沿用 runId；动态消息沿用 messageId 和投递回执；原生答案回到原请求。
旧网页调用可由 Hub 补内部提交 ID，并在事务中建立 PromptSeq 映射，
无需要求旧客户端提供新幂等字段；这不代表旧网页请求已经获得客户端重试幂等保证。

### 5. 最终结算与通知 outbox 共同提交

收敛旧 relay、durable graph、早结束/停滞、取消、离线和启动恢复的最终结算方法。
在同一 Store 事务冻结当前 submission 的结果、最终事件引用和通知 outbox。
既有已确认 graph/attempt barrier 保持原子语义，新增通知不能复活任务或解锁后继节点。

发送器在 Hub 内异步消费 SQLite outbox，网络 I/O 不持有数据库事务。
唯一键按 user + run + submission + notification type + channel，
使同 run 后续提交可以有新邮件，节点重发和进程重启不重复入队。

保存本次收件人和结果快照，避免发送时读到新提交结果。使用 lease、bounded retry、
稳定 Message-ID 与可查询状态。SMTP 和 SQLite 之间不能绝对原子，故承诺入队去重和尽力避免重复，
不承诺邮件绝对 exactly-once 或 SMTP sent 等于用户已经收信。

发送故障不改变验证结果；没有邮箱/配置且用户明确要邮件时，提交前给出可执行设置提示。
attention 是独立非终态事件，不能冒充完成邮件。background-mail 必须包含这类提醒，
以覆盖用户退出终端后才出现的审批/问题；未准备好时不得默认为用户已经有人处理待办。
用户明确不收 attention 时应改选 interactive 或明确开启提醒，不自动降低执行权限。
attention 去重还包含原生请求/执行实例身份，不能把一个 run 的后续问题全部去掉。

### 6. 可靠交互与内部入口隔离

原生问答复用 v3，不保存问题/答案正文；共享现有身份、版本、终态和 receipt 对账规则。
纯快照查询不带 PendingSubmissions；带 pending 的 reconcile 会封禁旧 ID/写 receipt，
须放在 W 交互工具，不能标为 readOnly。工具总数保持不变。
审批新增受能力门禁控制的 Bridge 权威快照/回执，包含运行和执行实例身份；
只在 Hub 缓存重发审批不满足可靠查询要求。旧端点明确限制，不自动提升权限。

外层 ProofBridge MCP/Skill 不应在受管 worker 内再次发起同类编排。
实现时按 CLI 配置机制精确禁用外层入口，覆盖官方登录和项目级 Skill 情况，
保留 Claude 审批 helper、其他用户 MCP 和原生上下文。Skill 条件只作补充，不是唯一防线。

## Alternatives And Trade-offs

| 方案 | 取舍 |
| --- | --- |
| 单独 MCP 服务持有第二任务库/调度器 | 增加身份和状态分叉；不采用。部署代理可以存在，但业务权威仍只有 Hub |
| 用浏览器短期 JWT 或 enroll token 直接充当个人令牌 | 生命周期和权限含义不符；不采用 |
| 只保存 PAT 摘要 | 无法满足重复复制原令牌的体验；除非用户以后改变目标，否则采用受保护密文与摘要 |
| 把偏好和模型列表全部写死在 Skill | 跨客户端不一致、引用易过期；Skill 读取正式偏好和当前能力 |
| 一个工具等待数小时直到证明结束 | 受客户端超时/退出影响；使用快速接受、显式身份、有界查询和后台邮件 |
| 在收到任意 run.end 时同步发邮件 | 节点误报、事务间隙丢失、无法恢复；采用最终提交结算和 outbox |
| 用通用 prompt 回答问题/审批 | 丢失原生请求与投递身份；复用交互协议 |
| 全局关闭内部 worker 的 MCP | 会破坏已有 Claude 审批；精确隔离外层入口 |

## Compatibility, Rollout And Recovery

- 保持一个 Go 二进制、Hub/Bridge 两运行模式；不引入新的证明运行服务。
- 保留现有用户、模型 vault、机器身份、注册规则、HTTP/WS 调用和自然语言以外的网页用法。
- 所有新行为加能力/功能开关，旧端点保留原网站审批路径；完整 MCP 交互须新版 Bridge。
- 新增表/字段可回填，先建立可回退备份。回滚功能开关只停止新入口/新通知，
  不删除已开始运行、幂等记录、旧提交结果或现有模型凭据。
- Hub-only 更新不自动更新/重启用户 Bridge；端点发布按需要的平台单独安排。
- 真实 VPN、实际邮件服务与真实客户端验收属于后续部署验证，本轮未执行。

## Exit Gates

以功能设计 AC01–AC18 为准。至少证明两客户端完整路径、归属隔离、请求重放保护、
同 run 第二次提交、所有最终结算入口、MCP 审批/问答、worker 非递归、
退出发起会话后实际收件以及 VPN/Bridge 失联的正确差异。

## References

- [架构](../architecture.md)、[变更影响](../change-impact.md)、[代码地图](../code-map.md)。
- [连续性](../features/orchestration-continuity.md)、[dynamic input](../features/dynamic-orchestration-input.md)。
- [用户模型库](../features/user-scoped-cli-provider-presets.md)、[原生问答 ADR](024-native-structured-user-input.md)。
- [独立完成语义](../features/independent-early-run-completion.md)、[durable graph](008-durable-orchestration-task-graph.md)。
