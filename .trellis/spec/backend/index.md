# 后端规范入口

本仓库为 Go 单模块、单二进制的 Hub/Bridge 项目。规范的唯一来源仍是
[AGENTS.md](../../../AGENTS.md)、[架构](../../../docs/architecture.md)、
[代码地图](../../../docs/code-map.md)和[变更影响表](../../../docs/change-impact.md)。

## Pre-Development Checklist

- 阅读上述入口和当前任务对应的 ADR / feature 文档。
- 非平凡变更先完成仓库要求的设计文档，保留既有未提交改动。
- 按任务范围阅读下表；涉及前端时同时阅读[前端规范](../frontend/index.md)。

| 规范 | 内容 |
| --- | --- |
| [目录](directory-structure.md) | 模块边界与定位 |
| [数据库](database-guidelines.md) | SQLite、迁移与查询 |
| [错误](error-handling.md) | Go 与 HTTP 错误处理 |
| [日志](logging-guidelines.md) | slog 与敏感信息 |
| [质量](quality-guidelines.md) | 验证与提交约束 |

## Quality Check

按变更范围执行定向验证和仓库退出门禁；详见[质量规范](quality-guidelines.md)。
会话连续性、反向连接、Hub 独占持久化均为必须保留的边界。
