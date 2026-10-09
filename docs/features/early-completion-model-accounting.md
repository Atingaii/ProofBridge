# 提前完成与按模型用量修复

## Goals

- 中间审查轮后，独立验证器只根据用户原始任务检查完成情况；通过后停止后续轮次。
- 协作与辩论保留职责差异，审查者可以如实报告整个任务已完成，不受最后一轮限制。
- 识别原生用量别名及实际模型；多个模型逐项计价、合计，未知价格保留 token 和已知费用。
- 验证器消耗也计入统计，包括无效判定产生的消耗。控制验证和测试并发。

## Non-goals

- 不改变当前两验证器至少一方通过的策略，不把模型口头完成声明当作独立验证。
- 不改数据库、编排拓扑或续跑接口，不重建已缺失的历史用量，不调用真实模型测试。
- 不把目录价格估算当作账单，不猜测未知模型或第三方渠道费率。

## Data / protocol impact

复用既有 `turn.usage` 字段，同一次 turn 可按实际模型产生多个用量事件；汇总值不再重复发出。
`TaskPrompt` 已保存原始用户任务，验证器优先使用它，旧请求回退 `Prompt`。
`verified-early`、`max-turns` 及已有续跑语义保持兼容，无 schema 或 frame 变更。
有验证器快照或供应商费用时，不能被只覆盖工作节点的本地日志账本替换。
供应商只给总量或总费用、缺少模型分摊时，以 `unattributed` 分项保留差额；它不增加调用次数。
已有总账单但缺少分模型费用时使用 `provider-unallocated` 来源，禁止 Hub 再加目录估算而重复计费。

## Implementation steps

1. 修正 `internal/bridge/orchestration_verifier.go:composeAgentVerifierPrompt` 的任务输入，串行执行独立验证，保留两个结果及取消语义。
2. 对齐 `internal/bridge/orchestration_relay.go:composeRelayPromptWithTaskScope` 与 `internal/hub/task_graph.go:orchestrationTaskSpecs` 的中间审查和模式指令。
3. 在 `internal/bridge/orchestration_usage.go:normalizeNativeUsage` 保留实际模型分项，识别原生字段；在 `internal/bridge/orchestration_relay.go:emitTurnUsage` 分模型发出。
4. 修正 `internal/usagepricing/catalog.go:Resolve` 的明确版本费率，在 `internal/hub/orchestration.go:shouldUseOrchestrationUsageLedger` 保留完整计价快照，前端显示已知费用小计与未知状态。

## Exit gates

- 使用含内部节点指令的真实派发形状回归验证器任务隔离；覆盖两模式中间完成、Hub 不继续派发及取消。
- 覆盖不同模型分项、缓存、未知模型、用量字段别名、验证器无效输出消耗，以及账本刷新不丢失已知费用。
- 使用假 CLI；`GOMAXPROCS=2`、Go `-p 1 -parallel 1`，串行前端构建，观察内存和负载；通过针对性及完整 Go 检查、前端检查、构建和 doc lint。
- 保留本轮开始前的改动；删除仅为本轮创建的临时验证产物，保留嵌入 UI。

## Reviewer Q&A

**为何原有早停仍不生效？** 验证器错误地将附加内部节点指令的 `Prompt` 当成原始任务，其中含“只有最终轮可结束”的要求；改用独立保存的 `TaskPrompt`。

**两个模型如何计费？** 每个实际模型保留自己的输入、输出、缓存和费用来源，再求和。优先使用供应商报告费用；缺失时才按明确型号目录估算。

**价格依据？** Claude 明确版本及五分钟缓存写入目录价取自 [官方价格表](https://platform.claude.com/docs/en/about-claude/pricing)，核对于 2026-09-08；其他渠道、套餐、长上下文与特殊服务等级不代表实际账单。

**轮数和上下文是否改变？** 最大轮数仍是预算；独立验证通过后结束。后续用户任务仍使用原 run 和 prompts 接口。

## Verification record

2026-09-08 本地验证：针对性回归与完整 Go 测试通过；前端全部检查、静态构建和 Go 二进制构建通过。
浏览器以模拟 API 验证 1365px 与 390px 的运行统计、总览、分模型金额和未知费用小计，无脚本错误或页面横向溢出。
Go 使用 `GOMAXPROCS=2`、`-p 1 -parallel 1`；构建串行、降低进程优先级，未调用真实模型。
文档检查为 0 错误；既有 `internal/hub/server.go:Server.Run` 方法锚点仍触发扫描器警告，方法实际存在。
本轮未提交或部署运行中的服务，保留现有工作区改动及嵌入 UI 交付物。

## Suggested commit message

```text
fix(bridge): correct early completion and per-model usage

Change summary:
- internal/bridge/orchestration_verifier.go: verify the original task and preserve billed usage on verifier failure
- internal/bridge/orchestration_relay.go: clarify mode contracts, intermediate completion, and model usage events
- internal/bridge/orchestration_usage.go: normalize native model breakdowns and preserve unallocated totals
- internal/hub/orchestration.go: retain verifier/provider accounting and prevent duplicate cost estimates
- internal/usagepricing/catalog.go: match explicit Claude versions and preserve unknown prices
- frontend/src/app/pages/OrchestrationStatsPage.tsx: show cache writes, model amounts, and known-cost subtotals

Exit gate:
- [x] Serial Go suite and binary build
- [x] Frontend checks, embedded build, and desktop/mobile browser smoke
- [x] Doc lint: zero errors; existing method-anchor scanner warning

Doc-Impact: docs/architecture.md, docs/change-impact.md, docs/code-map.md, docs/plan.md, docs/features/agent-verifier-round-continuation.md, docs/features/configured-round-completion-and-usage.md, docs/features/orchestration-collaboration-and-debate-contracts.md, docs/features/orchestration-usage-overview.md, docs/features/early-completion-model-accounting.md
```
