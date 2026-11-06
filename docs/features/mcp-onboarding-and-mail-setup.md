# MCP 运行入口、名称别名与邮件接入

状态：代码已实现，本机运行中；真实证明尚未完成，Skill 加载严格验收尚未通过（2026-11-06）。扩展既有[MCP功能](mcp-skill-task-notifications.md)，不改变任务调度权威。实际证据见[本地验收](../reviews/proofbridge-mcp-local-validation.md)。

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

## Implementation Steps

1. 复现历史卡片点击问题并最小修复，覆盖标题/正文/状态与独立操作按钮及直接链接。
2. 补Skill发现描述、提示词和名称别名回归；保留原生MCP/Skill隔离。
3. 补SMTP模板、显式env加载、受限测试发送及个人中心反馈。
4. 本机部署后验证注册、真实历史、别名与邮件；再运行 ArraySet-Coq 证明。

## Exit Gates

- 浏览器点击所有非操作区域打开同一run；原生上下文及历史分页回归通过。
- 三个名称的静态资产与安装校验通过；真实模型请求须另外观察 Skill 加载及正确工具使用，未观察到加载则不计该项通过。
- env优先级/语法/错误脱敏测试通过；本人收件地址绑定和限速测试通过。
- 实际本地SMTP收件可查看；外部邮件没有凭据时明确未验证。
- 前端test/build、Go相关测试/构建、doc-lint通过。
- ArraySet最终定理保持原命题，Rocq/Coq8.18编译及内核检查通过，Print Assumptions无新增公理。

## Current Validation

| 层次 | 本轮结论 |
| --- | --- |
| 代码与网站 | Go 全量/vet、定向 race、前端回归与干净构建通过；本机 8088 新 Hub 健康，原 Bridge 在线且未重启 |
| 详情与配置 | 部署后浏览器夹具通过卡片各区域、详情按钮、配置、深链刷新、管理操作隔离、键盘与手机布局；夹具只模拟 GET，无真实数据变更 |
| 名称与安装 | 三个名称资产检查通过；Codex/Claude Code 实际用户 Skill 已由安装器 checksum 更新，不改变 MCP 名称或 20 个工具 |
| Claude 真实请求 | `Codex Bridge` 自然语言和显式 `/proofbridge` 均完成两项只读 MCP 查询并中文回显；Skill load trace=false，严格探针 FAIL，不能宣称已自动加载 |
| Codex 真实请求 | MCP 协议连接成功；自然别名请求推理 60 秒超时，工具 calls=[]，SKIP，不能计为通过 |
| 本地邮件 | 本人测试邮件/失败路径、限速、权限与 TCP SMTP 检查通过；本机 Mailpit 已接入，mailReady=true，不代表外部邮箱投递 |
| 真实证明 | Test/1 已 completed/pass，Codex verifier 通过、Claude verifier 模型失败；新的 ArraySet run 当前 running，尚不能判为证明完成 |

ArraySet 真实运行是 `orc_ed3af20a59e135a9553f55620c9f4aab` / `asub_5491de3677bba64eb20b6441925c0199`，目录为 `/Users/florian/Desktop/Tencent/Github/Test/ArraySet-Coq`。采用 6 轮 Claude + Codex Agent，Codex 官方 `gpt-6.1-sol/high`、Claude 官方原生默认。临时地址 `zy1@local.test` 仅投递到本地 Mailpit 8025；完整完成通知须在该提交终态后另行核对。外部邮箱与实验室 VPN 仍需真实部署验收。

## Reviewer Q&A

为什么不直接调用当前聊天的邮件工具？后台任务必须在终端退出后仍能投递，部署的Hub应独立持有明确的SMTP配置，不能依赖外层会话或复制平台内部凭据。

免费服务需要改代码吗？不需要，标准SMTP已覆盖。先验证发件身份、取得服务商凭据，再注入环境变量。免费额度及实际网络可达性属于部署条件。

证明失败能改弱命题吗？不能。保留原始模型与主定理；若发现模型问题，报告具体障碍，不通过axiom、Admitted或弱化定义宣称成功。

MCP 能调用为何 Skill 还未通过？客户端可能直接选择 MCP 工具。当前已保存探针只证明两项查询成功，没有观察到 Skill 加载 trace。严格失败保留；安装成功、静态别名或模型自述不能补足加载证据。
