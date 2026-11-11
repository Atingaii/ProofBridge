# 目录与模块边界

完整定位以[代码地图](../../../docs/code-map.md)为准；不要在这里复制完整文件表。

- 单 Go 模块、单二进制；入口见 `main.go:main`。
- HTTP/API 位于 Hub，例如 `internal/hub/server.go:handleLogin`。
- 私有机连接由 Bridge 发起，例如 `internal/bridge/client.go:connectOnce`。
- 数据库访问位于 Store，例如 `internal/store/store.go:Open`。
- 协议边界见 `internal/protocol/envelope.go:Envelope`。
- UI 源码在 frontend，静态产物由 Go 嵌入；Android 已归档。

新增端点、协议字段或持久化变更，先检查[变更影响表](../../../docs/change-impact.md)。
