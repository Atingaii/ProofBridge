# 原生 Skill 探针诊断边界

2026-11-07。仅测试夹具改动，入口为
`internal/hub/automation_native_skill_test.go:TestAutomationNativeSkillReadOnly`。
没有改变产品 MCP 工具、运行权限、全局 CLI 配置或自动加载通过门槛。

此前探针从安装准备前启动同一个60秒上下文，推理出错时又先 SKIP，
因而无法知道超时前是否已有可观察加载事件或终端回复。错误分类优先判断
deadline 也会覆盖已经返回的认证或限流证据。JSON-RPC 方法未留证时，
一个约59秒的请求不能区分正常订阅长连接与工具调用，不能据其耗时认定 MCP 死锁。

现在安装准备使用独立30秒窗口；推理在准备完成后才开始独立120秒窗口，
子进程输出管道收尾另有2秒 WaitDelay。仍然每客户端只有一次请求，
没有新增配置项或无界重试。正常订阅如 `subscriptions/listen` 只记录其方法名、
耗时与是否结束，等待本身不是卡死证据。

退出或超时时先解析已有 JSON 事件，再报告安全诊断。日志只包含客户端/模式、
已知路由、阶段耗时、RPC方法与工具名、事件类型、加载/终端回复是否观察到，
不记录终端正文、raw JSON、请求参数、HTTP头、Token或邮箱。
认证、限流等已知错误先于deadline分类，但超时或非零退出依旧是 SKIP，
已有局部证据不会把 SKIP 改成通过。
结构化NDJSON只从明确error事件/标志的错误字段分类；普通usage、identifier、
agent正文和成功result不参与。HTTP 401/403还须有HTTP/status语境，
不会因为token计数或UID片段出现401/403就误判认证。

Codex 使用 `official-isolated` 路由，只复制已确认的 ChatGPT OAuth，
临时根配置固定 `openai / gpt-6.1-sol / low`，不复制用户的 custom provider 配置。
它测的是这条隔离官方路径，不能当成当前用户 custom 模型配置的结果。
Claude 保留进程作用域的 provider 认证和模型别名；缺少文件式OAuth不等于缺少认证。

通过仍必须同时具备：两项只读工具返回成功、非空且成功的终端回复、
既有解析器能够观察到的 Skill 加载事件，以及成功的客户端退出。
自述、提示词中的 `$proofbridge` 或 `/proofbridge`、安装成功、普通 MCP 调用均不能替代加载事件。
初始修复没有加入新加载事件通道，也没有将 CLI 自带预展开推测成已加载。
自然语言与显式调用始终单独报告。

确定性回归见
`internal/hub/automation_native_skill_diagnostics_test.go:TestAutomationNativeSkillDiagnosticEvidenceAndStrictGate`，
覆盖局部事件保留、无加载/无回复/无工具成功的失败、超时即使证据完整仍SKIP、
错误分类、长订阅的进行中快照、只读门禁及诊断内容最小化。
这些测试不请求模型，不能替代 opt-in 原生客户端验收；旧60秒历史结果不追溯改判。

## 显式展开的组合证据（仅测试夹具）

后续只读研究确认：[官方 UserPromptExpansion 文档](https://code.claude.com/docs/en/hooks#userpromptexpansion)
覆盖直接 `/skill` 展开，而 `PreToolUse(Skill)` 不覆盖这条路径。该hook在正文展开前执行，
其触发本身不能代替加载。当前CLI 2.1.287的公开artifact还确认：个人来源值为
`userSettings`，Skill正文按实际目录加前缀后进入synthetic user消息。
来源值也可能用于旧custom command，事件没有 `skill_path`，所以只看名称和source不够。

`internal/hub/automation_native_skill_expansion_test.go:nativeSkillExpansionFixture` 仅在
Claude explicit的临时配置中增加静默command hook。它将最小nonce/source/文件与路径SHA256
写入私有0600、只创建一次的receipt；不记录prompt、sessionID、正文或凭据，不增加模型工具。
全局配置、生产Skill及安装器保持原样。

接受这个新通道必须同时满足：

1. 已安装文件为真实普通文件，字节等于分发asset，无同名legacy command或其他用户Skill造成歧义。
2. receipt事件、来源、名称、fixture nonce、路径hash和文件hash全部匹配，且安装文件仍未改变。
3. 受信CLI输出的非replay、非tool-result、非subagent的synthetic user消息，含实际baseDir及
   **完整**渲染正文的精确SHA256。发现列表、原始输入、assistant自述、短前缀或hook本身均不够。

正文去frontmatter的规则来自该CLI版本的实际解析器；没有通配删除正文、内部空白归一化或
忽略内容变更。当前asset没有动态上下文替换；以后增加动态表达式时该通道先停止验收。
`nativeSkillCorrelateExpansion` 只在receipt与正文两项同时成立时补充Loaded，原有两工具成功、
终端回复和成功退出条件继续保留。观察不到可信正文时，该新通道不会置Loaded；既有Skill工具通道保留。

防伪、无hook/无正文、wrongsource/别名、旧回执、文件变化、真实路径、symlink及权限测试均为
确定性测试；静默helper也通过独立子进程执行测试。后续真实模型验证需要主会话单独授权，
当前此前Claude explicit FAIL保持原结论。
