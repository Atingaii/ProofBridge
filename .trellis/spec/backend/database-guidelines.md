# 数据库规范

架构约束见[架构文档](../../../docs/architecture.md)，联动更新见
[变更影响表](../../../docs/change-impact.md)。

- SQLite 仅位于 Hub。使用 `database/sql` 和 `modernc.org/sqlite`，不是 ORM。
- `internal/store/store.go:Open` 限制一个打开连接，配置 WAL、外键和 busy timeout。
- `internal/store/store.go:Migrate` 管理迁移；遵循现有迁移方式并更新 Store 测试。
- 查询与写入在 Store 内，使用参数绑定及 context；不要把用户输入拼进 SQL。
- 单连接下注意事务/Rows 生命周期，避免持有连接时重新从 db 请求连接。

实际例子：`internal/store/store.go:CreateUser`、
`internal/store/store.go:CreateOrchestrationRun`。新增表/列必须同步结构体、CRUD、
测试和架构/代码地图，具体清单以变更影响表为准。

历史筛选必须在 SQL 分页之前执行；游标绑定账户及筛选条件。归档不等于删除，
继续运行时必须与状态 claim 一起恢复可见，且统计包含归档记录。具体 API、
迁移、错误矩阵与正反例见[桌面修复契约](../../../docs/features/desktop-audit-fixes.md)
及 [ADR 026](../../../docs/adr/026-orchestration-history-metadata.md)。

事件部分索引的 WHERE 必须与消费查询匹配；不能假定 SQLite 会推导不同
JSON/OR 谓词之间的蕴含关系。修改时用 EXPLAIN QUERY PLAN 与长历史等价
测试验证，不能靠截断历史减少成本。见 [ADR 027](../../../docs/adr/027-sparse-orchestration-read-indexes.md)。

管理员内容先在索引源记录上LIMIT，再进行安全投影和页内分组；不能先全文
GROUP_CONCAT再截响应。空展示页仍推进源游标，同秒消息用(created_at,id)
稳定键集。接口字段及断言见[历史分页契约](../frontend/history-pagination.md)。


## Heavy read fairness with the single SQLite connection

Usage timeline reads must not hold the sole connection while decoding large JSON payloads. `internal/store/orchestration_usage_timeline.go:readOrchestrationUsageTimeline` captures an initial sequence high-water mark, reads bounded raw batches, closes rows, then decodes outside the connection. Preserve the complete prefix, legacy/typed payload semantics and cancellation errors; never return a successful partial total. If a run is deleted during the read, its high-water event must be checked before publishing. A single oversized event may exceed the soft byte bound; do not describe it as an absolute latency guarantee.

Authentication database failures must not be classified as invalid credentials: only a missing user returns 401; transient failures return 503, deadlines 504, without clearing the session cookie. See `internal/hub/server.go:writeAuthUserReadError`.

统计总览分页只在显式提供limit时启用；旧客户端仍收到完整响应，不能静默改变计费含义。
以(created_at,id)遍历含归档的源run，不能用创建日期排除仍在产生近期用量的旧run。
游标绑定账户、范围、时区及首次cutoff；空统计页仍推进源游标，每个run完整计费，
错误不作为成功部分页返回。此遍历不是跨请求强一致快照，见
[统计总览](../../../docs/features/orchestration-usage-overview.md)。
