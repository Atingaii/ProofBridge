# 前端规范入口

## Pre-Development Checklist

- 先读 [AGENTS.md](../../../AGENTS.md)、[代码地图](../../../docs/code-map.md)
  和[变更影响表](../../../docs/change-impact.md)。
- React / TypeScript / Vite 源码位于 frontend；入口为 `frontend/src/app/App.tsx:1`。
- 复用已有页面、组件、类型和 i18n 组织方式，不手改生成的静态资源。
- 用户可见流程变更先更新对应 feature 文档。
- 编排参与者身份使用 workerSlot / workerId，角色和模型作为独立信息；不能根据 candidate-a/b 推断 CLI 实例。原生多题表单在内存保留草稿，整组显式提交，并遵循 allowCustom 和单选/多选语义。见[参与者与问答](../../../docs/features/participant-identity-and-question-flow.md)。
- 后续聊天沿用 sid，编排沿用 runID 和 prompts 端点；详见
  [会话连续性](../../../docs/features/orchestration-continuity.md)。

- 编排草稿按账户和目标保存，异步成功只清提交时的草稿；可切换的统计与历史
  请求必须约束迟到响应，认证 401 与服务故障分开处理。具体签名、错误矩阵、
  正反例和回归断言见[桌面修复契约](../../../docs/features/desktop-audit-fixes.md)。

- 历史页与实时补页的游标、空可见页和截断答案显示遵循
  [历史分页契约](history-pagination.md)，不能用WS最大seq跳过HTTP补页。

## Quality Check

- 根据变更执行现有 `frontend/package.json:1` 中相关检查。
- 在 frontend 运行 `npm run build`，刷新 `internal/web/static/`。
- 随后执行 AGENTS.md 中的 Go 测试和构建门禁。
- 涉及用户界面时验证实际操作、错误状态与响应式布局。
- 手机呈现与安卓宿主遵循[移动适配契约](../../../docs/features/android-mobile-redesign.md)：复用原业务状态/回调；APK独立构建，不复制网站bundle。常规网站部署仍不自动发布APK。

- 手机自定义弹层需要可见的 `role="dialog"` / `role="menu"` 和 Escape 关闭语义；验证嵌套弹层先关闭最上层，关闭不得提交业务动作。Android 系统返回复用此契约。触摸目标按实际 CSS px 测量，不能把根字体 14px 下的 Tailwind `h-12` 当作 48px，也不能依赖组件未输出的属性。
