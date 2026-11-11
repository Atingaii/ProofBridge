# MCP 运行入口、名称别名与邮件接入

状态：代码已提交并在本机运行；ArraySet 独立证明审计与同 run 后续提交 completed / Agent pass 已确认，取消和成功通知均在本地 Mailpit 实收，临时偏好与保活进程已恢复/清理（2026-11-07）。Skill 加载严格验收仍未通过；外部邮件、VPN 与自启未验。详细证据见[真实 ArraySet 验收](../reviews/proofbridge-mcp-real-arrayset-validation.md)。扩展既有[MCP功能](mcp-skill-task-notifications.md)，不改变任务调度权威；通用检查见[本地验收](../reviews/proofbridge-mcp-local-validation.md)。

## Goals

- 点击历史卡片正文、标题或状态均打开同一运行；操作按钮保留独立行为。
- 详情页通过现有 GET 结果的 executionConfiguration/submissionId 安全投影显示该次实际执行配置；不得用新建表单的默认值冒充历史配置。
- ProofBridge、Codex Bridge、CodexBridge 的自然语言请求使用同一个用户 Skill；静态别名支持与真实加载分别验收。
- 部署方通过标准 SMTP 环境变量或显式环境文件接入 Resend/Brevo 等服务；个人用户只填收件邮箱。
- 个人中心可向本人已保存邮箱发送固定测试邮件；成功提示 SMTP 接受，用户仍需检查收件箱。本地试验使用独立收件箱，保持与外部邮箱验收的区别。

## Non-Goals

- 不重命名 proofbridge MCP 服务及20个工具，不让受管工作器递归调用外层入口。
- 不依赖 Dot/当前聊天在线才能发送任务通知，不抓取或复制其内部邮件凭据。
- 不自动创建外部服务账号、不把邮箱测试变成任意收件人发送接口。
- 不改变现有 run、native resume、模型权限、验证判定或数据库结构。

## Data And Protocol Impact

现有 `GET /api/orchestrations/{runID}` 在检查归属后增加 `executionConfiguration` 和 `submissionId`。`internal/hub/orchestration_configuration.go:orchestrationExecutionConfiguration` 白名单投影接受时的 submission 快照；旧运行使用持久化 run 和 worker binding。凭据、原生 resume 身份和任务正文不进入配置卡；未知模型或原生默认值不伪造为固定选择。

复用账户邮箱/通知偏好及现有 outbox，不新增 SQLite 表或 WebSocket 帧。新增 `POST /api/automation/mail/test`，由 `internal/hub/automation_mail_test_send.go:handleAutomationMailTest` 处理。它只接受浏览器登录用户，只使用服务端保存的本人邮箱和固定文案，拒绝自定义收件人/正文及 MCP PAT。每用户每分钟可尝试一次，最长发送 15 秒，错误不回显 SMTP 凭据。该接口不启动证明、不修改运行，也不新增 MCP 工具。

配置增加显式 `CODEX_BRIDGE_ENV_FILE`，入口为 `internal/config/env_file.go:configurationEnvironment`；现有进程环境包含空值时仍优先，文件只接受环境键值语法，不执行 shell 或变量求值，错误不回显秘密值。默认不自动加载工作目录下的未知文件。
完整环境变量规范仍维护在[开发流程](../dev-workflow.md)。

用户级 Claude Skill 安装须与 CLI 的配置根一致：`internal/automationassets/install.sh` 使用 `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/skills`，未设置或空值时回退到原路径，并正确处理含空格的目录。项目级安装仍写入当前项目的 `.claude/skills`；Codex 路径不变。沿用校验和、受管标记与拒绝覆盖非受管目录/符号链接的机制，不改认证、模型或 MCP 配置。本轮原生探针已将 `CLAUDE_CONFIG_DIR` 设为同一个 `$HOME/.claude`，因此该兼容性修复不能作为 Skill load trace 失败已解决的证据。

独立验证期间，`internal/bridge/orchestration_verifier.go:composeAgentVerifierPrompt` 将临时审计脚本、报告和日志限制到工作目录的 `.codex-bridge/verifier-audit/`，或直接使用 stdin/stdout；Coq assumptions 审计优先向 `coqtop` 的 stdin 发送查询。源文件、规格、配置、README 与该目录外的普通日志/报告均只读；正常编译产物仍按原权限允许。复用 `internal/bridge/orchestration_completion.go:completionSourceDigest` 已有运行时目录边界，不新增扩展名或验证目录豁免，不改变执行权限、checker、quorum 或源变更拒绝规则，避免验证器自己的审计文件被当作源变更。

## Implementation Steps

1. 复现历史卡片点击问题并最小修复，覆盖标题/正文/状态与独立操作按钮及直接链接。
2. 补Skill发现描述、提示词和名称别名回归；保留原生MCP/Skill隔离。
3. 补SMTP模板、显式env加载、受限测试发送及个人中心反馈。
4. 本机部署后验证注册、真实历史、别名与邮件；再运行 ArraySet-Coq 证明。

## Exit Gates

- 浏览器点击所有非操作区域打开同一run；原生上下文及历史分页回归通过。
- 三个名称的静态资产与安装校验通过；真实模型请求须另外观察 Skill 加载及正确工具使用，未观察到加载则不计该项通过。
- 安装器覆盖 Claude 自定义配置根（含空格）、未设/空值回退、项目级不受配置根覆盖、受管更新及非受管/未知标记/符号链接拒绝；Codex 路径与现有 CLI 配置不变。
- env优先级/语法/错误脱敏测试通过；本人收件地址绑定和限速测试通过。
- 实际本地SMTP收件可查看；外部邮件没有凭据时明确未验证。
- 前端test/build、Go相关测试/构建、doc-lint通过。
- ArraySet最终定理保持原命题，Rocq/Coq8.18编译及内核检查通过，Print Assumptions无新增公理。

## Current Validation

| 层次 | 本轮结论 |
| --- | --- |
| 代码与网站 | Go 全量/vet、定向 race、前端回归与干净构建通过；本机 Hub/同 machine Bridge 已空闲更新到 ab77767-local，8088 健康、端点在线，身份/配置/用户数据保留 |
| 详情与配置 | 部署后浏览器夹具通过卡片各区域、详情按钮、配置、深链刷新、管理操作隔离、键盘与手机布局；夹具只模拟 GET，无真实数据变更 |
| 名称与安装 | 三个名称资产检查通过；Codex/Claude Code 实际用户 Skill 已由安装器 checksum 更新，不改变 MCP 名称或 20 个工具 |
| Claude 真实请求 | `Codex Bridge` 自然语言和显式 `/proofbridge` 均完成两项只读 MCP 查询并中文回显；Skill load trace=false，严格探针 FAIL，不能宣称已自动加载 |
| Codex 真实请求 | MCP 协议连接成功；自然别名请求推理 60 秒超时，工具 calls=[]，SKIP，不能计为通过 |
| 本地邮件 | 本人测试邮件/失败路径、限速、权限与 TCP SMTP 检查通过；真实 ArraySet 取消/成功通知均在 Mailpit 实收，临时邮箱已恢复，无外部投递 |
| 真实证明 | Test/1 与 ArraySet 后续提交均 completed/pass；ArraySet 原类型/模型、clean build、内核及 assumptions 独立审计通过。最终 Codex verifier pass、Claude verifier 模型失败/continue，不能写双通过 |

ArraySet 真实运行保持 `orc_ed3af20a59e135a9553f55620c9f4aab`，目录为 `/Users/florian/Desktop/Tencent/Github/Test/ArraySet-Coq`。旧 submission `asub_5491de3677bba64eb20b6441925c0199` 仍 canceled / unknown；同 run 后续 submission `asub_809339596cdfd5634a0f1c3e2ec66d5e`（Codex 首轮/high、2 轮、background-mail）于 2026-11-07 08:53:36+08 completed / verified-early，Agent pass / satisfied，unmetObligations=[]，成功邮件于下一秒发送并在本地 Mailpit 实收。本轮无 source-integrity 失败，153 个冻结文件一致；终态五生产源码 SHA 仍匹配独立审计。临时邮箱/通知偏好已恢复，当前无收件邮箱；后续要使用邮件须先保存邮箱。墙钟耗时包含本机睡眠和网络等待，不作性能 benchmark。外部邮箱、实验室 VPN 和自启仍需单独验收。

## Reviewer Q&A

### 配置与 AI 接入提示词（2026-11-07）

个人中心的接入路径收敛为选择终端客户端、复制完整 MCP 配置，或复制一段可直接交给 AI 执行的接入提示词。取消重复的地址/Token 输入框、单独安装命令区和接入说明小字；保留重新生成/撤销凭据、客户端切换和失败反馈。完整配置按用户要求直接显示，凭据仅来自当前用户的接入响应，不进入浏览器持久存储。

`frontend/src/app/lib/automationSetup.ts:automationSetupPrompt` 按所选客户端与界面语言生成提示词。它嵌入该用户完整配置和 Hub 提供的 Skill 安装命令，要求合并个人客户端配置、保留已有服务器、安装并读取配套 Skill，最后只调用 `proofbridge_get_context` 和 `proofbridge_list_endpoints`。安装命令的两层下载均来自官方 GitHub 的固定 Skill 版本，MCP 配置仍指向实际 Hub；详见[分发契约](github-skill-distribution.md)。安装、Skill 加载和 MCP 连通分别回显；需要重启或遇到网络/权限/配置冲突时报告具体阻碍。GitHub 网络失败与 Hub 私有 CA 信任失败分别诊断，不跳过 TLS 校验。接入过程不启动证明、不改变模型、不修改项目，也不无限重试。日志和最终回显隐去个人凭据；用户复制的提示词本身包含授权给其个人客户端的 MCP Token。

配置和提示词都由当前接入响应即时生成；切换客户端、重新生成或撤销 Token 后同步更新，不保留旧凭据。复制失败仍能选中文字手动复制。此功能不增加 API、数据库或 MCP 工具；现有认证、任务连续性和模型选择规则保持有效。

邮件区域保留必要的可见状态。接入响应可选的 `mailDeliveryMode=preview` 显示“仅本地邮件预览，真实邮箱不会收到”，测试结果同样区分本地收件与 SMTP 接受。旧 Hub 未返回该字段时沿用通用反馈。具体邮件协议见[邮件投递状态](mail-delivery-status.md)。

实施顺序：先加入纯提示词生成函数，再替换接入区域，最后覆盖两客户端/两语言、凭据轮换和撤销、剪贴板拒绝、邮件模式兼容以及 390px 布局。退出条件为上述真实浏览器控件回归、前端测试/类型检查和文档检查通过；本面板测试使用隔离 API 夹具，不将其作为真实 MCP、Skill 加载或外部邮箱验收。

评审问题：为何直接显示凭据？这是用户明确选择的个人配置展示方式；不再维护一个易误复制的遮罩版本。为何接入提示词不直接发任务？安装与只读连通是可独立验证的完成条件，证明仍由用户后续明确的任务请求发起。

### 长运行历史切换修复（2026-11-07）

用户在已完成的长、短运行之间切换时报告卡顿和无法切换。此次先以两个运行的最近页大小、请求次数和浏览器主线程记录复现，再修改导致阻塞或过期选择的最小边界；不更改任务、证明源、接口或原生上下文。测试夹具只允许读取，覆盖与真实最近页相同规模的正文，不能用单条短消息的选择成功替代长历史验收。

可观察完成条件：点击另一运行立即更新选中与加载反馈；长短双向和快速连续选择均以最后一次选择为准；过期响应不改变当前运行；同一选择不重复加载或订阅。现有手动旧页、HTTP连续游标、滚动锚点、直接链接、管理按钮和手机入口保持原行为。修复前的失败和修复后的操作延迟、请求/订阅数量分别留证。

已在真实部署复现：管理按钮整行的 `relative z-10` 位于标题按钮的整卡命中伪元素上方，按钮右侧及按钮间隙命中的是没有选择处理器的行容器。点击后 URL 和详情请求均不变，而标题、底部详情及完整历史切换在只读实测中正常。最小修复让管理行空白不参与命中，四个直接管理按钮显式恢复命中；沿用原生标题按钮的整卡选择与键盘语义，不增加容器点击处理器、请求重试或新的选择状态。`frontend/mcp-run-detail-browser-check.mjs` 以两个运行覆盖桌面和手机的两类空白命中、一次点击一次详情请求及管理动作隔离。

为什么不直接调用当前聊天的邮件工具？后台任务必须在终端退出后仍能投递，部署的Hub应独立持有明确的SMTP配置，不能依赖外层会话或复制平台内部凭据。

免费服务需要改代码吗？不需要，标准SMTP已覆盖。先验证发件身份、取得服务商凭据，再注入环境变量。免费额度及实际网络可达性属于部署条件。

证明失败能改弱命题吗？不能。保留原始模型与主定理；若发现模型问题，报告具体障碍，不通过axiom、Admitted或弱化定义宣称成功。

MCP 能调用为何 Skill 还未通过？客户端可能直接选择 MCP 工具。当前已保存探针只证明两项查询成功，没有观察到 Skill 加载 trace。严格失败保留；安装成功、静态别名或模型自述不能补足加载证据。

内网网页和 MCP 地址分离：个人配置优先读取 automation.mcp_url，为空才继承 public_url；
结果/邮件链接始终使用 public_url。HTTP 回环接入和其他电脑的本地转发见[8088 部署](../deployment-8088.md)。
