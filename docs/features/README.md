# Feature Docs

Feature-level designs live here. Add one before implementing any non-trivial
user-visible workflow, endpoint, WebSocket frame, runner behavior, or UI flow.

Use this shape:

```markdown
# <Feature Name>

## Goal
## Non-Goals
## Current State
## Design
## Implementation Steps
## Exit Gates
## Reviewer Q&A
```

Private tickets, scan reports, and operational notes belong in `docs/private/`,
not in this directory.

Public engineering article: [Orchestration design](orchestration-design-article.md).

Native structured questions: [feature contract](native-structured-user-input.md), [v3 regression baseline](native-structured-user-input-regression-plan.md), and [runtime interaction plan awaiting review](native-structured-user-input-runtime-plan.md).

Participant identity and question navigation: [A/B labels and question flow](participant-identity-and-question-flow.md).

Desktop reliability and history: [drafts, request recovery and history management](desktop-audit-fixes.md).

Conversation and statistics performance: [sparse queries and cancellable loading](site-loading-performance.md).

Official Codex A/B model and reasoning selectors: [feature contract](official-codex-worker-models.md).

Android and phone presentation: [shared feature contract](android-mobile-redesign.md) and [lightweight host ADR](../adr/028-lightweight-android-webview.md).
