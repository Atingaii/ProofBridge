# ADR-025: 动态编排与运行中输入

Status: Accepted (2026-09-29, 用户已确认)

## 背景与决定
现有固定轮次只适用于旧运行。新领先版创建 executionMode=dynamic；取消总轮数边界，沿用逐轮独立验证及无进展退出。单个执行节点仍只承担自己的工作，不把节点MaxTurns=1解释为总运行上限。

同一Hub持久化消息并向Bridge派发，浏览器保留同一runID。追加指令进入当前原生会话并传递后续工作器，不能仅修改页面文字。消息状态区分存储、投递和原生接收；不得宣称接收即执行。

新旧前端共用同一实验Hub与SQLite，避免两个调度器争抢数据库和Bridge连接。动态模式属于运行，不能由域名隐式修改历史运行。保留JWTSecret及数据目录。

## 协议与兼容
- orchestrationCreateRequest、OrchestrationStartPayload、OrchestrationRun增加executionMode，空值按bounded。
- prompts端点新增运行中追加及GET列表，终态仍走原有继续流程。
- Bridge新增live-input能力及指令/回执协议；旧Bridge明确报不支持。
- 详细目标、状态和验收见[功能设计](../features/dynamic-orchestration-input.md)。

## 取舍
无固定轮次意味着有进展的任务可持续运行。每轮判停不构成单命令时长限制。保留现有故障处理与手动停止，不添加隐蔽轮次上限。

## 事实来源
[Codex原生输入](https://learn.chatgpt.com/docs/developer-commands)；[Codex App Server](https://learn.chatgpt.com/docs/app-server)；[Claude排队与立即发送](https://code.claude.com/docs/en/interactive-mode#queue-messages-while-claude-works)。Claude TUI与headless内部priority接口不预设等价，需实际探针。
