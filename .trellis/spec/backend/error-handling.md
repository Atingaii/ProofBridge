# 错误处理规范

- 底层返回 Go error，补充上下文并保留原因；例如 `internal/store/store.go:Open`
  使用 `fmt.Errorf("open sqlite: %w", err)`。
- 复用 Store 的 `ErrNotFound`、`ErrUnauthorized`、`ErrConflict` 等错误语义，
  见 `internal/store/store.go:ErrNotFound`。
- HTTP 层使用现有 `serverutil.WriteError`，稳定地返回 HTTP 状态和错误码；
  参考 `internal/hub/server.go:handleLogin`，不要向客户端泄露内部凭据。
- 取消、断线和任务完成状态不能混为一谈；连续性约束见
  [会话连续性设计](../../../docs/features/orchestration-continuity.md)。

行为修复应验证可观察的失败路径与恢复行为；不要仅测试与实现同构的细节。


结构化问答查询错误不能伪造成问题状态：Hub 返回带 `runId/syncId` 的
`source=hub, phase=route, status=rejected, revision=0` 查询拒绝，前端单独校验，
不能解除未决提交。具体字段、错误矩阵与回归门禁统一见
[查询恢复契约](../../../docs/features/native-structured-user-input.md#查询故障与送达提示修复2026-10-03)。
