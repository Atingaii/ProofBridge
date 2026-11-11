# ArraySet-Coq 真实证明与 MCP 交付验收

更新日期：2026-11-07。实现分支：`feat/mcp-skill-task-notifications`。
设计契约见[运行入口与邮件接入](../features/mcp-onboarding-and-mail-setup.md)，通用代码与客户端检查见[本地验收](proofbridge-mcp-local-validation.md)。

ArraySet 主定理的独立编译、内核与无新增公理审计已通过；同 run 后续提交于 2026-11-07 08:53:36+08 达到 completed / Agent pass，成功通知已在本地 Mailpit 实收。本次本地证明、MCP 冻结结果和取消/成功两类通知链路已闭环。旧提交仍冻结为取消；Skill 自动加载、外部邮箱与实验室部署保留各自未验收边界。

## 验收结论

| 层次 | 已确认的结果 | 边界 |
| --- | --- | --- |
| 代码门禁 | 最终全量 `go test ./...` 通过；定向 race、vet、doc-lint 通过 | 默认测试不调用模型，不覆盖真实 Skill 加载 |
| 证明与内核 | Coq 8.18.0 clean build、五模块 `coqchk`、原主定理类型检查均通过 | 独立检查在无活跃任务时完成，不能代替新提交的产品终态 |
| 原始规格与公理 | 四个原模型定义不变，仅增加三条构造器 `Arguments` 兼容声明；主定理类型不变；源码空洞扫描通过，两项 `Print Assumptions` 均为 closed | 不引入 axiom、不弱化模型或主定理 |
| 首次提交 | execution=`canceled`，Agent verification=`unknown`，取消超时未收到端点确认 | 不是产品 pass，也不以该取消判定证明错误 |
| 同 run 后续提交 | completed，terminalReason=`verified-early`；Agent pass / satisfied，unmetObligations=[] | Codex verifier pass；Claude verifier 模型调用失败/continue，按现有至少 1/2 规则通过，不能写成双通过 |
| 本地任务邮件 | 取消和成功两封均在 Mailpit 实收，各自 run/submission、状态和链接正确 | 没有外部邮箱投递 |
| 自然语言入口 | Claude 自然别名与显式请求的两项只读 MCP 调用成功 | Skill trace=false，严格 FAIL；Codex 隔离推理超时 SKIP，不能计为通过 |

## 证明产物与输入约束

工作目录为 `/Users/florian/Desktop/Tencent/Github/Test/ArraySet-Coq`，原始压缩包保留。目标是 `ArraySetLinearizability.v` 中的 `Fixed_Domain_Lock_Free_Set_is_linearizable`，保留原类型和模型，使用 Rocq/Coq 8.18.0。

主代理在任务空闲时独立执行 clean、完整 make、五模块内核检查，以及主定理与 `forward_simulation_holds` 的 assumptions 查询；各命令退出码均为 0，两次结果均为 `Closed under the global context`。审计前后五个 `.v` 源码哈希稳定。

最终提交没有 source-integrity 失败，153 个冻结文件指纹一致。主代理在终态后再次核对五个生产 `.v` 的 SHA256，与 `independent-final-audit.json` 完全一致；原模型、原主定理与零新增公理的审计仍适用于最终交付源码。

相对该工作目录的证据为：

- `.proofbridge-validation/independent-final-audit.json`：`status=pass`、`originalStatementTypechecked=true`、`closedAssumptions=2`、`sourceStable=true`，并索引各命令日志。
- `.proofbridge-validation/independent-source-check.json`：对照原 zip 的四个模型定义与构建配置不变，主定理类型不变；仅 `ArraySetConcrete.v` 两条与 `ArraySetAbstract.v` 一条 `Arguments` 兼容声明，源码空洞扫描通过。
- `.proofbridge-validation/scope-note.md`：目录约束纠正记录。一次实现曾将两个临时探针写到 `/tmp`，协调者核对后迁回并清理相关临时产物；后续通过同 run 指令纠正。不能声称全过程从未发生目录外写入。最终源码、证明和验证记录位于 ArraySet-Coq 内。

## 同 run 的两次提交

两次提交保留同一个 run：`orc_ed3af20a59e135a9553f55620c9f4aab`，同一工作目录与端点 `agt_27675c168fdd8454fdf9b3fdf9a09924`。

| 提交 | 配置与用途 | 当前冻结或运行状态 |
| --- | --- | --- |
| `asub_5491de3677bba64eb20b6441925c0199` | Claude + Codex，bounded 6 轮，formal-proof / Agent，background-mail；Codex 官方 `gpt-6.1-sol/high`，Claude 官方默认 | 2026-11-07 00:18:54 canceled / unknown；取消超时，历史结果保留 |
| `asub_809339596cdfd5634a0f1c3e2ec66d5e` | `follow_up`，promptSeq=5550；原 run 上 Codex 首轮、high、2 轮、background-mail，沿用所选工作器绑定 | 2026-11-07 08:53:36+08 completed / pass / satisfied，verified-early，unmetObligations=[]；旧 submission 不变 |

首次运行已产出并多次编译验证证明，但独立验证期间出现 `source changed during independent verification`。旧提示允许普通审计文件，源完整性摘要也覆盖普通日志、JSON 和 Markdown，存在输出边界冲突；协调者的观察文件也可能参与摘要，尚未逐一归因所有变化。不能将局部 verifier pass 或证明编译通过改写成整次提交已 pass。

修复 `ab77767` 仅要求独立 verifier 使用 stdin/stdout，或将临时审计脚本、报告和日志放在原有运行时目录 `.codex-bridge/verifier-audit/` 下；其他源码、规格、配置和普通报告保持只读。入口为 `internal/bridge/orchestration_verifier.go:composeAgentVerifierPrompt`，复用 `internal/bridge/orchestration_completion.go:completionSourceDigest` 的既有边界，没有增加扩展名豁免、放宽 source guard、checker、权限或 quorum。

协调者为升级有序取消旧提交，随后在空闲期完成独立审计并更新 Hub 与同 machine Bridge，再用同 run 的 `follow_up` 发起最终验收。新提交运行期间，协调者和观察者不写 ArraySet 目录，避免审计本身影响完整性判断。

最终独立 verifier 结果为 Codex pass，Claude 模型调用失败并返回 continue。现有判定规则允许至少一位独立 Agent 通过后提前结束，系统据此发布 completed / verified-early 与 Agent pass；这不表示两位 verifier 均通过。冻结结果绑定新 submission 与 promptSeq=5550，未覆盖旧取消结果。

## 本机版本与邮件

已提交代码：`b72918f`（详情配置、SMTP 和验证入口清理）、`b05013f`（Claude 自定义配置根安装）、`ab77767`（独立 verifier 审计输出边界）。最后全量测试证据为本地任务 `validation/final-go.log` 与 `final-go.exit=0`，其中 Bridge 67.538 秒、Hub 64.318 秒、Integration 33.311 秒。

本机 Hub/Bridge 均更新到 `ab77767-local`，保留 machine/agent ID、密钥、cwd 和环境，没有清空用户数据。`http://127.0.0.1:8088` 健康，Bridge 在线。本次 launchd 启动新二进制停在 dyld open，当前使用受控后台进程运行；健康检查不代表已验收机器重启后的自动恢复。

私有运行管理器的 status、start-noop、进程身份/UID/PID 复用模拟检查已通过，当前 Hub PID=34499 保持运行；未对真实服务测试 stop，也未验证开机自启。

本地验收使用临时邮箱 `zy1@local.test`、loopback SMTP 和 Mailpit `http://127.0.0.1:8025`，没有外发。收件箱共两封，分别绑定两个 submission：

| 通知 | SMTP 与本地收件结果 |
| --- | --- |
| 旧取消 | notification=`ant_fcadeb03a33faa3bc545c4f9c3a54092`，sent、attempts=1；Mailpit=`0MeVLve2USHvwWgmtZNM5u`，正文 canceled / unknown，保留取消超时义务 |
| 新成功 | notification=`ant_0c7f6f806a6b04149d678c27158880e3`，2026-11-07 08:53:37+08 sent、attempts=1；Mailpit=`1ur4gqfZr8dyxZ8t0qz8Gc`，标题“验证通过”，正文对应新 submission、completed / verified-early / Agent pass，详情链接正确 |

上述结果验证了真实任务事件→Hub 通知→SMTP→本机收件箱的路径；证据为本地任务 `validation/arrayset-live.md`。终态后，主代理通过 CAS expectedVersion=1 将原偏好恢复到 version=2：无收件邮箱、interactive、attention/completion=true。当前 context 为 emailConfigured=false、mailReady=true。后续要使用邮件通知，需先保存收件邮箱；临时测试邮箱未遗留为用户默认值。

## 运行环境

本机 `pmset` 日志确认 Mac 整夜多次进入 Maintenance Sleep / DeepIdle，2026-11-07 08:37 最后唤醒；期间 native Codex 响应流断开并重连。08:47 同一 session 恢复 handoff，08:49 集成检查通过后进入 Codex reviewer，08:53:36 完成；本轮没有 source-integrity 失败。协调者于 08:46 临时运行 `caffeinate -i -t 1800`，终态后核对 PID=42758 的 argv 并停止，移除 pidfile；未更改永久电源或显示设置。本次墙钟耗时包含睡眠和网络等待，不可作为模型或证明性能 benchmark；执行机须在线并保持唤醒，实验室常在线机器仍需单独验证。

## 终态与清理核对

本地最终核对结果：

| 核对项目 | 状态 |
| --- | --- |
| 新 submission 的 execution / verification / outcome 与冻结结果 | completed / Agent pass / satisfied，verified-early，unmetObligations=[] |
| 新 submission 的完成通知、SMTP receipt 和 Mailpit 正文身份/结果一致性 | 已核对，成功邮件实收；旧取消邮件与历史结果仍独立保留 |
| 临时测试邮箱及相关通知设置恢复 | 已通过 CAS 恢复原偏好到 version=2，无邮箱，interactive，attention/completion=true |
| 临时保活进程与 pidfile | 已核对并停止/移除，永久电源与显示设置未更改 |

真实 Skill 成功加载、实验室 VPN、目标部署和外部邮箱收件仍未验收。不得使用代码测试、MCP 只读成功、独立 Coq 检查或本地 Mailpit 收件替代这些证据。
