# 质量与验证

以 [AGENTS.md](../../../AGENTS.md) 的 R1–R8 和
[开发流程](../../../docs/dev-workflow.md)为准。

- 非平凡变更先设计，编辑前后检查变更影响表；保护用户已有改动。
- Go 修改执行 gofmt，并先运行相关包的测试；共享行为改变时扩大验证范围。
- 后端门禁：`/usr/local/go/bin/go test ./...`，以及
  `CGO_ENABLED=0 /usr/local/go/bin/go build -ldflags "-s -w" -o bin/codex-bridge .`。
- 前端修改需重新生成 embedded UI，再执行 Go 测试/构建。
- 文档路径、锚点、环境变量或提交规则变化时执行 `make doc-lint`。
- Android 维护已恢复；安卓宿主或手机呈现变更遵循[移动适配契约](../../../docs/features/android-mobile-redesign.md)，APK单独发布。普通网站部署不自动构建安卓包。
- 提交使用 R6 格式及 Doc-Impact；Trellis 不自动提交日志/任务变更。

测试入口例子：`internal/store/store_test.go:1`、
`internal/integration/bridge_flow_test.go:1`。不要为可逆的小型文案/配置修改
机械新增测试；采用与风险相称的验证。

官方 Codex 工作器的恢复默认回归须保留原 thread ID，并检查
`internal/bridge/appserver_runner.go:threadResumeParams` 的显式官方 provider
覆盖。仅省略 model/effort 或重新启动进程不足以证明旧原生持久化设置已清除；
具体运行参数契约见[官方工作器模型](../../../docs/features/official-codex-worker-models.md)。

## 原生用户交互验证补充

原生问答边界与具体断言以[加固方案](../../../docs/features/native-structured-user-input-hardening.md)为准。空快照回归必须经过实际 Go JSON 编码再交给前端；reader 存活必须经过真实 Client 分发循环，不能用并发直调 manager 替代。终态不可复活，完整写出与模型处理是不同事实。跨 Bridge／Hub／模拟浏览器的测试与真实 CLI helper 探针是互补证据，不能把两者拼写成一次已验证的真实模型到浏览器往返。

Claude 的 MCP connected、工具暴露、CLI subtype=success 都不足以声明原生问答通过：必须检查退出码、is_error、原生 tool_result 及答案回显。临时包装器通过只能验证候选方向，正式能力门禁需要实际项目 helper 及完整链路证据。精确版本门禁和当前证据见[Claude 实测记录](../../../docs/reviews/native-user-input-evidence/claude-capability-verification.md)；未验证的新版本必须继续保持关闭。

v3 问答回归另见[二次修复计划](../../../docs/features/native-structured-user-input-regression-plan.md)：短时出站拥堵必须无需重连恢复；正常 resolved 不能把独立的原生写入故障预算当作取消信号；刷新后的未决 submission 必须由 Bridge 原子核对并封禁旧 ID，不能仅凭前端超时解锁。版本门禁同时覆盖 Hub、Bridge、浏览器及派发给 Bridge 的 start payload。跨层集成测试复用 Hub handler，不应启动模型目录刷新生命周期污染统计测试。
