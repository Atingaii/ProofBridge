# 历史分页与首屏契约

## 1. Scope / Trigger

修改编排首屏、手动历史、实时补页或管理员内容分页时适用。设计理由见
[加载性能](../../../docs/features/site-loading-performance.md)。事件条数不是完整消息条数。

## 2. Signatures

- `GET /api/orchestrations/{runID}/events?limit=&beforeSeq=|afterSeq=`。
- `GET /api/admin/users/{userID}/conversations/{kind}/{conversationID}?limit=`。
- Store：`internal/store/admin_content.go:AdminConversationContent`。

## 3. Contracts

- events 默认200、最多1000源行；lookahead不出现在响应中，分别返回
  `hasMoreBefore` / `hasMoreAfter`。Workspace首屏100、手动旧页300。
- HTTP实时追页使用自身已同步seq和当前页末seq；WS收到的最大seq不能推进
  HTTP游标。单选择只有一条追赶链，换run取消整条链。
- 管理员同样默认200/max1000，先限制源行再安全投影及页内合并；
  编排 `beforeSeq` / `nextBeforeSeq`，聊天
  `beforeCreatedAt`+`beforeId` / `nextBeforeCreatedAt`+`nextBeforeId`。
  timestamp=0是有效游标；更多状态由源记录决定，不以可见items长度推断。
- 最近页缺turn.start但有完整turn.end时不能只展示尾部delta；完整答案去重
  显示，补齐start后恢复正常流式归约。未完成轮次提示较早历史可按需加载。
- 最新页全部隐藏时仍提供加载更多。手动请求失败保留已有内容，重试相同游标。

## 4. Validation & Error Matrix

| 条件 | 结果 |
| --- | --- |
| beforeSeq与afterSeq同时提供 | 400 BAD_QUERY |
| 非法/负seq或timestamp | 400 BAD_QUERY |
| 管理员聊天游标只给timestamp或id之一、kind游标不匹配 | 400 BAD_QUERY |
| 非管理员 / 会话不属于路径账户 | 403 ADMIN_ONLY / 404 NOT_FOUND |
| 管理员内容超时 | 错误响应，UI保留旧页并允许重试 |

## 5. Good / Base / Bad Cases

- Good：WS先到seq1000，HTTP仍按100→101→102补齐，随后去重合并。
- Base：进入只读最近小页，按钮加载更早，滚顶本身不触发请求。
- Bad：内部事件构成空展示页就关闭更多按钮；会让历史永久不可达。

## 6. Tests Required

`frontend/history-pagination-browser-check.mjs`：并发首屏、详情一次、手动页、
滚动锚点、WS抢跑、取消追页、隐藏事件首屏、空管理员页与失败重试。
`frontend/visible-events-check.mjs`：截断轮次完整答案及补start后的真实reducer。
`internal/hub/history_pagination_test.go`：页上限、探测行、终态、错误游标。
`internal/store/admin_content_test.go`：同秒聊天、源空页、隐私与归属。

## 7. Wrong vs Correct

Wrong：`afterSeq = maxSeqSeenByWebSocket`，或 `hasMore = items.length > 0`。
Correct：使用HTTP连续游标和后端源页 `hasMoreBefore/hasMoreAfter`，任何时候
都先验证响应属于当前选择。完整统计和模型原生上下文不按页面历史裁剪。


## Temporary authentication outages

The `/api/me` bootstrap may temporarily fail during a shared-Hub restart. Keep the original URL and pending local input, retain an actionable failure card, and retry with capped backoff only while visible and online. Manual/online/visibility recovery must share one in-flight request; 401 ends automatic recovery. Do not resend model tasks or answers as part of authentication recovery. Regression: `frontend/auth-recovery-browser-check.mjs`.

## 渐进统计总览

总览使用 `GET /api/usage/overview?limit=1`，串行自动拉取下一页，单页独立超时；
首批显示后必须标明统计未完成，不能把部分总额、空源页或暂未发现的价格当成最终结论。
失败保留累计结果并重试相同游标，按游标与run ID去重，切换范围取消整条链。
机器数从累计项目去重，趋势按日期累加；空源页的未知价格标志不能污染已有总额。
完整契约与测试见[统计总览](../../../docs/features/orchestration-usage-overview.md)。
