# 日志规范

使用现有 `log/slog` 和结构化键值。例子：

- `internal/bridge/client.go:connectOnce` 记录连接与 agent 标识。
- `internal/bridge/client.go:Run` 记录断开原因和重试等待。
- `internal/hub/server.go:writeAuthSession` 将内部失败映射为稳定的客户端错误。

连接等正常状态使用 Info，可恢复异常使用 Warn，无法完成的操作使用 Error，
遵循相邻代码的命名与前缀。绝不输出密码、token、API key 或原始凭据内容。
安全联动规则以[变更影响表](../../../docs/change-impact.md#security)为准。
