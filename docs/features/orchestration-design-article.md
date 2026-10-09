# Public orchestration design article

## Goals

Publish a professional Chinese engineering essay at `/blog/orchestration-design`
(and the trailing-slash alias) on the existing Hub. Explain cycle versus turn,
actual model versus CLI participant, role-based workflow, origins, costs,
quality trade-offs and durable recovery. Compare a full collaboration cycle
with alternating one-model turns fairly. Include four original Ian Xiaohei
illustrations, accessible diagrams, an interactive stage-handoff demonstration,
source links and a readable mobile/print layout.

## Non-goals

No runner, scheduling, pricing, auth policy, private conversation, or database
changes. No claims of measured accuracy gains or universal superiority. No
private run identifiers, credentials, prompts, logs, or live statistics requests.
No external framework dependency; this is an editorial page in the existing UI.

## Data / protocol impact

A public lazy-loaded frontend route skips auth bootstrap like the existing help
page. The existing static handler serves its fallback and embedded local images.
Images use `/article-assets/orchestration-design/`, separate from the page URL:
a physical directory at the route itself would trigger Go's directory server
instead of the SPA fallback.
No API or WebSocket changes. The article is a dated implementation explanation,
not an alternative scheduler setting. New tasks plan once; later internal rounds
reuse the plan in current code. Older recorded runs can include repeated plans.

## Editorial and visual design

Audience: developers choosing and operating a two-participant CLI workflow.
Chinese long-form engineering prose, explicit evidence boundaries, five core primary
source references. Existing ProofBridge icon and native frontend delivery.
Reading-layout reference supplied by the user:
https://openai.com/zh-Hans-CN/index/introducing-chatgpt-images-2-5/.
White background, near-black sans-serif headings and prose, centered publication
date/title/deck, generous vertical whitespace, 8px spacing scale, thin dividers,
no shadows or decorative animation. Blue/orange is reserved for the illustrations
and small participant marks. Main column 720px; wider figures and tables;
compact desktop contents rail, mobile disclosure navigation. Focusable controls,
44px touch targets, reduced-motion support and print styles.

### Revision: cycle rationale, typography and reading position

Lead with why a cycle schedules responsibilities around one acceptance contract:
A implements, B improves, A integrates, and B reviews the integrated workspace.
Explain concrete benefits (review coverage, evidence handoffs, integration
ownership and actionable continuation), using an explicitly illustrative bug-fix
example. Keep the numbering equivalence as a short boundary: alternation with
the same contracts can implement the same workflow. Do not imply every finished
cycle means the task passed verification; independent early completion may skip
remaining stages when its conditions are met.

Use locally served OpenAI Sans Regular/Medium/Semibold from OpenAI's official
font CDN, with system sans-serif fallback for Chinese glyphs. Official font
source: `https://cdn.openai.com/common/fonts/openai-sans/v2/`. The reference
article's HTML/CSS is challenge-protected in direct browser access; the typeface
is confirmed by `https://openai.com/brand/`, but exact Chinese rendered-font
parity across operating systems is not claimed. Scope fonts to this article.

Track the last section heading above a reading line near the viewport top.
Update on scroll, resize, hash navigation and article layout changes; handle
long sections, upward scrolling, restored anchors and the document bottom.
Mark exactly one contents link with `aria-current="location"`, darker text,
medium weight and an inset indicator without shifting the rail layout.

### Revision: verified design evidence

Use five primary sources, checked on 2026-09-11, with claims tied to the
actual method sections. Keep later explanatory comparisons distinct from the
project's documented historical inspirations (AutoGen, LangChain/LangGraph,
CrewAI and Swarm). Do not transfer published benchmark gains to ProofBridge.

| Source and location | Supported claim | Mapping and limit |
| --- | --- | --- |
| [Anthropic, Building Effective Agents](https://www.anthropic.com/engineering/building-effective-agents), Evaluator-optimizer | Generation and evaluation form a feedback loop; clear criteria and useful refinement are applicability conditions | Supports the feedback-cycle abstraction, not mandatory Round naming |
| [Google ADK, Sequential agents](https://adk.dev/agents/workflow-agents/sequential-agents/), Code Development Pipeline | Writer, Reviewer and Refactorer execute in order and share InvocationContext/session state | Supports stage dependencies and handoffs; sequence alone is not an iteration or acceptance guarantee |
| [AutoGen, Reflection](https://microsoft.github.io/autogen/dev/user-guide/core-user-guide/design-patterns/reflection.html), CodeReviewResult and CoderAgent | Coder and Reviewer exchange code/review until approval or stopping conditions | Supports explicit feedback and termination; model approval is not executable proof |
| [Self-Refine, NeurIPS 2023](https://proceedings.neurips.cc/paper_files/paper/2023/file/91edff07232fb1b55a505a9e9f6c0ff3-Paper-Conference.pdf), section 2 / Algorithm 1 | Initial generation precedes repeated feedback and refinement using one model with different prompts | Supports role-based iterative improvement; its loop boundary is Feedback then Refine, not a prescribed A/B pair |
| [MetaGPT, ICLR 2024](https://proceedings.iclr.cc/paper_files/paper/2024/hash/6507b115562bb0a305f1958ccc87355a-Abstract-Conference.html); [author manuscript v7](https://arxiv.org/html/2308.00352v7), sections 3.1–3.3 | SOP roles, structured intermediate outputs, dependency-driven activation and executable feedback | Supports responsibility and artifact contracts; its roles/topology differ from this project's four stages |

Introduce `R_t = <E_t, V_t, D_t>` only as this article's conceptual notation:
E groups candidate work/integration, V groups review/applicable independent
verification, D is scheduling based on evidence and limits. It is not a quoted
paper formula, wire enum, transaction guarantee or literal scheduler algorithm.
Explain that budget exhaustion is not acceptance; early completion can skip
remaining stages. Preserve the equivalence when alternation has identical
responsibilities, evidence, dependencies and stopping rules.

Shot list (Ian Xiaohei Illustrations):
1. Opening/collaboration: two creatures bind a folded task sheet into one coherent
   stitched booklet; one stitches, one examines the seam. Labels: 交接 / 复核.
2. Shared workspace: one creature works through a giant keyhole on a shared bench;
   the other physically receives the single oversized key. Labels: 同一工作台 / 顺序交接.
3. Review trade-off: one creature holds a hand-built chair while another measures
   a crooked leg, with a small wind-up clock nearby. Labels: 发现偏差 / 额外时间.
4. Accounting: one creature folds a long receipt into a booklet, another
   checks the same receipt stretched out. Labels: 实际工作 / 一轮协作.
All are original 16:9 white-background pen illustrations; exact workflow and
billing semantics stay in editable HTML. Attribution links to Ian and the skill.

## Implementation steps

1. Verify source code and official references; write the article and image prompts.
2. Generate/QA each image with the requested skill and built-in image tool.
3. Implement the public route, scoped styles and local assets, including fonts;
   no runtime external font requests.
4. Verify public access, no API reads, navigation, stage-handoff demo, images,
   mobile/desktop/print and existing protected route behavior.
5. Rebuild embedded assets, run focused Go checks and doc lint; build/deploy the
   isolated Hub after checking active runs, with rollback backup and HTTPS smoke.
   If work is active, first verify existing disconnect/startup-grace tests and
   recent endpoint liveness; preserve Bridge processes and check attempt identity
   plus endpoint reconnection after the Hub restart.

## Exit gates

Public direct route and trailing slash; actual generated images load; no page
horizontal overflow at 390/768/1440px; keyboard controls and local anchors work;
interactive demo exposes each stage's handoff and acceptance status; contents
highlight follows scrolling, anchor navigation, resize and page end; font files
load and typography uses the article font family; article makes no fake benchmark
claims; frontend suite/build and Go embedded/static tests pass. Verify production
HTML/assets and release health. Remove task-only temporary files after delivery.

Browser verification: after `npm run build` and starting Vite preview, run
`node frontend/orchestration-design-check.mjs http://127.0.0.1:4197`.
Optional second argument stores screenshots in a task-owned directory.

## Reviewer Q&A

- Is alternating A/B inherently unreliable? No. Durability and review are separate
  choices; a turn-based scheduler can implement both.
- Does every round contain planning? Current code plans in the first round of a
  new user task; historical runs may follow older six-node generations.
- Are extra reviews guaranteed to improve quality? No. They create opportunities
  to catch defects, and can also add cost, anchoring, or conflicting edits.
- Are illustrations measured data? No; they are explanatory metaphors.
- Why no offline single-HTML generator? Delivery is a public page integrated with
  the project's existing React/Vite and embedded-UI release boundary.

References: `frontend/src/app/pages/OrchestrationDesignPage.tsx:OrchestrationDesignPage`,
`internal/hub/task_graph.go:orchestrationTaskSpecs`,
`internal/store/task_graph.go:ClaimReadyTask`,
`internal/hub/server.go:staticHandler`.

## Revision verification

The reading-position regression check covers all eight sections downward and
upward, long-section interiors, a clicked anchor, mobile resize, the document
bottom and direct loading with a section fragment. The fragment case exposed
lazy-route mounting after native anchor navigation; the page now restores the
initial section position after mounting. Browser checks also load all three
OpenAI Sans faces, exercise stage selection by keyboard, retain four images,
check 320/390/768/1440px widths and print, and keep statistics behind login.

Frontend tests and full Go tests passed. Documentation lint reports zero errors
and the existing method-anchor scanner warning in the startup-reconnect design.

## Final editorial and online acceptance

The existing article was polished using the beautiful-article editorial/review
criteria while retaining the user's chosen integrated React delivery, layout
and four illustrations. Five core references were selected from the supplied
material; primary-source mechanisms and this project's engineering inferences
remain distinct. Independent editorial review is recorded in
[the final review](../reviews/orchestration-design-article-final.md).

Production HTTPS browser checks passed for all four widths, real font loading,
images, stage interaction, contents tracking, print and protected statistics.
The regression checker waits for the active locator directly so it works under
the production Content Security Policy without allowing unsafe evaluation.
After the Hub-only release, the previously active run completed and its active
attempt succeeded; the Bridge process retained its identity. Previous live
JavaScript/CSS dependencies remain served for already-open application tabs.
