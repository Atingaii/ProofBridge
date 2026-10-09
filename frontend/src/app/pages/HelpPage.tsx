import React, { useEffect, useRef, useState } from 'react';
import {
  ArrowRight,
  Check,
  CheckCircle2,
  ChevronRight,
  CircleHelp,
  Cloud,
  Code2,
  Copy,
  ExternalLink,
  FileCheck2,
  GitBranch,
  Globe,
  KeyRound,
  Laptop,
  Menu,
  MessageSquare,
  Moon,
  History,
  Network,
  Scale,
  Settings,
  ShieldCheck,
  Sparkles,
  Sun,
  Terminal,
  UsersRound,
  X,
  Zap,
} from 'lucide-react';
import type { Language } from '../lib/i18n';
import { SiteBuildInfo } from '../components/SiteBuildInfo';
import { useDialogFocus } from '../components/MobileSurface';

type HelpPageProps = {
  language: Language;
  setLanguage: (language: Language) => void;
  isDarkMode: boolean;
  setIsDarkMode: (dark: boolean) => void;
};

type FigureProps = {
  src: string;
  alt: string;
  caption: string;
  number: number;
};

function helpNavItems(text: (zh: string, en: string) => string) {
  return [
    ['start', text("快速开始", "Quick start")],
    ['account', text("注册与登录", "Sign up and sign in")],
    ['endpoint', text("连接 CLI 端点", "Connect a CLI endpoint")],
    ['chat', text("单 CLI 对话", "Single CLI chat")],
    ['orchestration', text("编排总览", "Orchestration overview")],
    ['live-input', text("追加输入与问答", "Follow-ups and questions")],
    ['collaboration', text("协作模式", "Collaboration mode")],
    ['debate', text("辩论模式", "Debate mode")],
    ['formal-proof', text("形式化验证", "Formal verification")],
    ['context', text("节省上下文", "Save context")],
    ['sharing', text("分享与会话", "Sharing and sessions")],
    ['settings', text("设置与维护", "Settings and maintenance")],
    ['troubleshooting', text("排错清单", "Troubleshooting")],
  ] as const;
}

export function HelpPage({ language, setLanguage, isDarkMode, setIsDarkMode }: HelpPageProps) {
  const text = (zh: string, en: string) => language === 'zh' ? zh : en;
  const navItems = helpNavItems(text);
  const currentOrigin = window.location.origin;

  const collaborationDemo = text("目标：在不改变公开 API 的前提下，为当前 Go 项目增加请求幂等性。\n\n验收条件：\n1. 同一 Idempotency-Key 只执行一次写入；\n2. 并发请求返回同一结果；\n3. 新增竞态测试并运行 go test -race ./...；\n4. 不引入新的外部服务。\n\n分工建议：先由 Claude 梳理不变量与失败路径，再由 Codex 实现和测试；最后一轮只核对验收条件与测试证据。", "Goal: Add request idempotency to the current Go project without changing its public API.\n\nAcceptance criteria:\n1. The same Idempotency-Key performs a write only once.\n2. Concurrent requests return the same result.\n3. Add race coverage and run go test -race ./...\n4. Do not introduce external services.\n\nSuggested responsibilities: Claude identifies invariants and failure paths, then Codex implements and tests. The final review checks only the acceptance criteria and test evidence.");

  const debateDemo = text("命题：把当前证明缓存从进程内 map 改为 SQLite 是否会破坏运行隔离？\n\n请围绕以下可证伪主张辩论：\n- 并发写入不会把不同 user_id 的证明状态混合；\n- Hub 重启后恢复语义与现有协议一致；\n- 最终方案不增加 Bridge 对 Hub 的入站连接。\n\n要求：每个反对意见必须给出反例或复现实验；最终结论列出证据、残余风险和最小实现方案。", "Question: Would moving the proof cache from an in-process map to SQLite break run isolation?\n\nDebate these falsifiable claims:\n- Concurrent writes never mix proof state across user_id values.\n- Recovery after a Hub restart remains consistent with the existing protocol.\n- The design introduces no inbound connection from Hub to Bridge.\n\nEvery objection must include a counterexample or reproducible experiment. The conclusion must list evidence, remaining risks and the smallest viable implementation.");

  const formalProofDemo = text("目标系统：Coq/Rocq\n工作目录：选择包含 _CoqProject 的目录\n附件：Task.v、Contract.v（只上传本题相关文件）\n\n证明目标：完成 theorem reverse_acc_correct，不允许 Admitted、Axiom 或弱化命题。\n\n验收命令：coqc Contract.v && coqc Task.v\n额外检查：搜索 admitted/axiom；说明使用的归纳不变量；最终结论引用成功命令和修改文件。", "Proof system: Coq/Rocq\nWorking directory: Select the directory containing _CoqProject.\nAttachments: Task.v and Contract.v (only files needed for this task).\n\nGoal: Complete theorem reverse_acc_correct without Admitted, Axiom or weakening the statement.\n\nAcceptance command: coqc Contract.v && coqc Task.v\nAdditional checks: Search for admitted/axiom, explain the induction invariant, and cite successful commands and changed files in the conclusion.");

  const [menuOpen, setMenuOpen] = useState(false);
  const menuRef = useRef<HTMLElement>(null);
  useDialogFocus(menuRef, menuOpen);
  const [copied, setCopied] = useState('');
  const [activeSection, setActiveSection] = useState('start');

  useEffect(() => {
    document.title = text("ProofBridge 使用帮助", "ProofBridge Help");
    const sections = navItems
      .map(([id]) => document.getElementById(id))
      .filter(Boolean) as HTMLElement[];
    const update = () => {
      const current = sections.slice().reverse().find((section) => section.getBoundingClientRect().top <= 120);
      if (current) setActiveSection(current.id);
    };
    update();
    window.addEventListener('scroll', update, { passive: true });
    return () => window.removeEventListener('scroll', update);
  }, [language]);

  const copyDemo = async (key: string, value: string) => {
    await navigator.clipboard.writeText(value);
    setCopied(key);
    window.setTimeout(() => setCopied(''), 1400);
  };

  return (
    <div lang={language === 'zh' ? 'zh-CN' : 'en'} className="phone-page min-h-screen bg-background text-foreground font-sans">
      <header className="sticky top-0 z-40 border-b border-border bg-background/95 backdrop-blur supports-[backdrop-filter]:bg-background/85">
        <div className="mx-auto flex h-14 max-w-[1440px] items-center gap-3 px-4 md:px-6">
          <button type="button" className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted lg:hidden" onClick={() => setMenuOpen(true)} aria-label={text("打开目录", "Open contents")}>
            <Menu className="h-4 w-4" />
          </button>
          <a href="/" className="flex items-center gap-2 font-medium">
            <span className="flex h-7 w-7 items-center justify-center rounded-md bg-primary text-primary-foreground"><Terminal className="h-4 w-4" /></span>
            <span className="text-sm">ProofBridge</span>
          </a>
          <span className="hidden h-4 w-px bg-border sm:block" />
          <span className="hidden text-sm text-muted-foreground sm:block">{text("使用帮助", "Help")}</span>
          <div className="ml-auto flex items-center gap-1">
            <a href="/updates" className="hidden h-8 items-center gap-1.5 rounded-md px-2.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground sm:inline-flex"><History className="h-3.5 w-3.5" />{text("更新记录", "Release notes")}</a>
            <button type="button" className="inline-flex h-8 items-center gap-1.5 rounded-md px-2.5 text-xs text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => setLanguage(language === 'zh' ? 'en' : 'zh')} aria-label={text("切换语言", "Change language")}>
              <Globe className="h-3.5 w-3.5" />{language === 'zh' ? '中文' : 'EN'}
            </button>
            <button type="button" className="inline-flex h-8 w-8 items-center justify-center rounded-md text-muted-foreground hover:bg-muted hover:text-foreground" onClick={() => setIsDarkMode(!isDarkMode)} aria-label={isDarkMode ? text("切换浅色模式", "Switch to light mode") : text("切换深色模式", "Switch to dark mode")}>
              {isDarkMode ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
            </button>
            <a href="/" className="ml-1 inline-flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-xs font-medium text-primary-foreground hover:bg-primary/90">{text("打开应用", "Open app")}<ArrowRight className="h-3.5 w-3.5" />
            </a>
          </div>
        </div>
      </header>

      {menuOpen && (
        <div className="fixed inset-0 z-50 lg:hidden">
          <button type="button" className="absolute inset-0 bg-black/45" onClick={() => setMenuOpen(false)} aria-label={text("关闭目录", "Close contents")} />
          <aside ref={menuRef} role="dialog" aria-modal="true" aria-label={text("使用目录", "Contents")} onKeyDown={(event) => { if (event.key === 'Escape') { event.stopPropagation(); setMenuOpen(false); } }} className="absolute inset-y-0 left-0 w-[min(286px,100vw)] overflow-y-auto overscroll-contain border-r border-border bg-background p-4 shadow-xl">
            <div className="mb-4 flex items-center justify-between"><span className="text-sm font-medium">{text("使用目录", "Contents")}</span><button type="button" className="flex h-8 w-8 items-center justify-center rounded-md hover:bg-muted" aria-label={text("关闭目录", "Close contents")} onClick={() => setMenuOpen(false)}><X className="h-4 w-4" /></button></div>
            <HelpNav language={language} activeSection={activeSection} close={() => setMenuOpen(false)} />
          </aside>
        </div>
      )}

      <div className="mx-auto grid max-w-[1440px] grid-cols-1 lg:grid-cols-[230px_minmax(0,900px)] xl:grid-cols-[230px_minmax(0,900px)_210px] lg:gap-8 xl:gap-10 px-4 md:px-6">
        <aside className="sticky top-14 hidden h-[calc(100vh-3.5rem)] overflow-y-auto border-r border-border py-8 pr-5 lg:block elegant-scrollbar">
          <p className="mb-3 px-2 text-[11px] font-semibold uppercase text-muted-foreground">{text("使用目录", "Contents")}</p>
          <HelpNav language={language} activeSection={activeSection} />
          <div className="mt-6 border-t border-border px-2 pt-5 text-xs leading-relaxed text-muted-foreground">
            <p>{text("按当前功能说明；控件是否可用取决于页面版本、账户及端点能力。", "This guide describes current features. Availability depends on the page build, account and endpoint capabilities.")}</p>
            <a className="mt-2 inline-flex items-center gap-1 hover:text-foreground" href="/updates">{text("更新记录", "Release notes")}<History className="h-3 w-3" /></a>
            <a className="mt-2 inline-flex items-center gap-1 hover:text-foreground" href="https://github.com/Atingaii/ProofBridge" target="_blank" rel="noreferrer">GitHub <ExternalLink className="h-3 w-3" /></a>
          </div>
        </aside>

        <main className="min-w-0 pb-24 pt-10 md:pt-14">
          <section id="start" className="scroll-mt-24 border-b border-border pb-14">
            <div className="mb-5 flex items-center gap-2 text-xs font-medium text-emerald-600 dark:text-emerald-400"><span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />{text("从浏览器安全连接你的私有 CLI", "Secure browser access to your private CLI")}</div>
            <h1 className="max-w-3xl text-3xl font-semibold leading-tight sm:text-4xl">{text("ProofBridge 详细使用教程", "ProofBridge user guide")}</h1>
            <p className="mt-5 max-w-3xl text-base leading-7 text-muted-foreground">{text("本页从注册开始，带你连接私有机器上的 Codex / Claude Code，完成单 CLI 对话、协作编排、证据驱动辩论与形式化验证。所有示例都以“少占上下文、可验证、可继续”为目标。", "Start with an account, connect Codex or Claude Code on your private machine, then use single CLI chat, collaboration, evidence-driven debate and formal verification. Each example aims to keep context focused, results verifiable and follow-ups continuous.")}</p>
            <p className="mt-3 text-xs leading-6 text-muted-foreground">{text("以下 17 张截图保留为历史界面示意，截图中的文字与布局可能属于旧版本；当前操作以正文和实际可见控件为准。英文模式下截图仍保留原始中文。", "The 17 screenshots below are historical illustrations. Their text and layouts may show older versions; follow the written guide and the controls currently visible in your app. Screenshots retain their original Chinese text in English mode.")}</p>
            <div className="mt-8 grid gap-px overflow-hidden rounded-md border border-border bg-border sm:grid-cols-3">
              <QuickFact icon={ShieldCheck} title={text("Hub 不接入私有机", "Your machine stays private")} text={text("Bridge 主动反向连接，模型凭据与工作区保留在你的机器。", "Bridge connects outward. Model credentials and your workspace stay on your machine.")} />
              <QuickFact icon={UsersRound} title={text("两种编排模式", "Two orchestration modes")} text={text("协作用于分工交付，辩论用于证伪高风险主张。", "Collaborate to deliver work, or debate to test high-risk claims.")} />
              <QuickFact icon={FileCheck2} title={text("形式化验证优先", "Verify with real proof tools")} text={text("Proof profile 固化证明义务、审计证据和最终检查。", "The proof profile keeps obligations, audit evidence and final checks explicit.")} />
            </div>
            <div className="mt-8 rounded-md border border-border bg-muted/35 p-5">
              <h2 className="text-sm font-semibold">{text("5 分钟路径", "The five-minute path")}</h2>
              <ol className="mt-4 grid gap-3 sm:grid-cols-5">
                {[text("注册账户", "Create an account"), text("添加 CLI 端点", "Add a CLI endpoint"), text("运行连接命令", "Run the connection command"), text("新建会话", "Create a session"), text("发送首个任务", "Send your first task")].map((item, index) => <li key={item} className="flex gap-2 text-xs leading-5 text-muted-foreground"><span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-primary text-[10px] text-primary-foreground">{index + 1}</span><span>{item}</span></li>)}
              </ol>
            </div>
            <SiteBuildInfo language={language} />
          </section>

          <GuideSection id="account" eyebrow={text("01 · 账户", "01 · Account")} title={text("注册、Cloudflare 验证与登录", "Registration, Cloudflare verification and sign-in")} icon={KeyRound}>
            <p>{text("打开当前站点 ", "Open this site at ")}<a href="/" className="underline underline-offset-2"><code>{currentOrigin}</code></a>{text("。已有账户直接登录；新用户切换到“注册账户”，用户名使用 3–32 位 ASCII 字符，密码至少 10 位，并按页面提示完成 Cloudflare Turnstile 人机验证。", ". Sign in with an existing account, or select Register. Use a username of 3–32 ASCII characters and a password of at least 10 characters, then complete Cloudflare Turnstile when prompted.")}</p>
            <StepList items={[
              [text("选择登录或注册", "Choose sign-in or registration"), text("注册模式会增加确认密码和安全验证；登录模式不会重复要求 Turnstile。", "Registration adds password confirmation and security verification. Sign-in does not repeat the Turnstile challenge.")],
              [text("检查密码", "Check your password"), text("点击输入框右侧眼睛可独立显示/隐藏密码；切换模式后会恢复隐藏。", "The eye buttons show or hide each password independently. Switching modes hides the passwords again.")],
              [text("完成验证", "Complete verification"), text("看到绿色验证状态后再提交。Token 过期时重新勾选，不要反复刷新页面。", "Submit once the verification indicator is green. If the token expires, complete the challenge again instead of repeatedly refreshing.")],
              [text("进入工作区", "Enter the workspace"), text("注册成功会自动登录；账户只能看到自己拥有的端点、会话和编排运行。", "Registration signs you in automatically. Your account can access only the endpoints, sessions and runs you own.")],
            ]} />
            <div className="mt-7 grid gap-5 md:grid-cols-2">
              <Figure number={1} src="/help/auth-login.webp" alt={text("ProofBridge 登录页面，显示用户名、密码与眼睛按钮", "ProofBridge sign-in screen with username, password and password visibility button")} caption={text("登录：输入已有账户，眼睛按钮只改变可见性，不改变密码值。", "Sign-in: the eye button changes visibility without changing the password value.")} />
              <Figure number={2} src="/help/auth-register.webp" alt={text("ProofBridge 注册页面，显示两次密码与 Cloudflare Turnstile", "ProofBridge registration screen with password confirmation and Cloudflare Turnstile")} caption={text("注册：两次密码一致并通过 Cloudflare 安全验证后创建账户。", "Registration: matching passwords and successful verification allow account creation.")} />
            </div>
            <Callout icon={ShieldCheck} title={text("安全边界", "Security boundary")}>{text("Turnstile 的 Secret 只保存在 Hub 服务端；浏览器只收到公开 Site Key。不要把注册验证码、Cookie 或密码发给任何 CLI。", "The Turnstile secret stays on the Hub; the browser receives only the public site key. Do not send verification tokens, cookies or passwords to a CLI.")}</Callout>
          </GuideSection>

          <GuideSection id="endpoint" eyebrow={text("02 · 首次连接", "02 · First connection")} title={text("把私有机器注册为 CLI 端点", "Register your private machine as a CLI endpoint")} icon={Network}>
            <p>{text("浏览器只是控制面。真正读取代码、调用模型和执行命令的是你私有机器上的 Bridge。每个用户都应连接自己的端点。", "The browser is the control interface. Bridge on your private machine reads code, calls models and runs commands. Each user should connect their own endpoint.")}</p>
            <StepList items={[
              [text("打开“设置”", "Open Settings"), text("点击左下角设置，进入“添加 CLI 端点”。", "Open Settings at the lower left, then choose Add CLI endpoint.")],
              [text("填写端点名称", "Name the endpoint"), text("使用能识别机器和用途的名字，例如 macbook-proof 或 wsl2-work。", "Choose a name that identifies the machine and its purpose, such as macbook-proof or wsl2-work.")],
              [text("选择权限策略", "Choose a permission policy"), text("“需要确认”会把危险命令交给浏览器审批；可信专机才使用“自动执行”。", "Require confirmation routes commands requiring approval to the browser. Use automatic execution only on a machine you trust for the task.")],
              [text("生成并运行命令", "Generate and run the command"), text("复制“安装并连接”命令，在端点机器普通用户的终端运行。Token 24 小时有效且只用于注册。", "Copy Install and connect, and run it as a regular user on the endpoint machine. The enrollment token is valid for 24 hours and is used only for registration.")],
              [text("确认在线", "Confirm the endpoint is online"), text("状态点变绿且能力矩阵出现 Codex / Claude 后即可使用。", "Look for a green status indicator and available Codex or Claude capabilities.")],
            ]} />
            <Figure number={3} src="/help/settings-overview.webp" alt={text("设置窗口，包含账户、主题、语言和 CLI 端点状态", "Settings with account, theme, language and CLI endpoint status")} caption={text("设置总览：查看账户、外观、语言以及当前端点状态。", "Settings overview: inspect your account, appearance, language and endpoint status.")} />
            <Figure number={4} src="/help/endpoint-enroll.webp" alt={text("添加 CLI 端点窗口，显示权限策略与安装连接命令", "Add CLI endpoint dialog showing the permission policy and connection command")} caption={text("端点注册：复制生成的命令到私有机器终端执行，命令中的 token 不要分享。", "Endpoint enrollment: run the generated command on your private machine and keep its token private.")} />
            <Callout icon={Terminal} title={text("端点前置条件", "Endpoint prerequisites")}>{text("目标机器需要已安装并登录 Codex CLI 和/或 Claude Code。Bridge 不代替模型登录，也不会把模型密钥上传到 Hub。", "Install and sign in to Codex CLI and/or Claude Code on the target machine. Bridge does not replace model authentication or upload model keys to the Hub.")}</Callout>
          </GuideSection>

          <GuideSection id="chat" eyebrow={text("03 · 单 CLI", "03 · Single CLI")} title={text("创建会话并完成一次可继续的任务", "Create a session you can continue")} icon={MessageSquare}>
            <p>{text("单 CLI 对话适合明确、线性的工作：理解代码、修复一个缺陷、运行测试或解释日志。系统会复用当前会话 ID 和原生 thread，后续消息不会静默开启新上下文。", "Single CLI chat suits focused work: understanding code, fixing a defect, running tests or explaining logs. Follow-up messages reuse the selected session and its native thread instead of silently starting a new context.")}</p>
            <Figure number={5} src="/help/workspace-overview.webp" alt={text("ProofBridge 工作区，左侧是会话，顶部是端点，中央是聊天区域", "ProofBridge workspace with sessions on the left, endpoint controls above and chat in the center")} caption={text("工作区：左侧管理会话，顶部选择在线端点，底部输入任务和上传图片。", "Workspace: manage sessions on the left, select an online endpoint above, and enter tasks or attach images below.")} />
            <StepList items={[
              [text("选端点", "Select an endpoint"), text("顶部选择器决定任务在哪台私有机器、哪个工作目录运行。绿色状态表示在线。", "The endpoint selection determines the private machine and working directory. Green indicates an online connection.")],
              [text("新建会话", "Create a session"), text("明确的新任务使用“新会话”；补充同一任务直接在当前会话继续。", "Choose New session for a separate task. Send follow-ups for the current task in the existing session.")],
              [text("写可验证任务", "Write a verifiable task"), text("说明目标、边界、验收命令；避免只写“优化一下”或一次塞入多个无关目标。", "State the goal, scope and acceptance commands. Avoid vague requests or combining unrelated goals.")],
              [text("查看工具事件", "Inspect tool events"), text("命令、输出、审批和最终回答按发生顺序显示；重要修改仍需检查 diff 与测试。", "Commands, output, approvals and the final response appear in order. Review the diff and test results for significant changes.")],
              [text("需要时停止", "Stop when needed"), text("生成中可点击停止。停止后可在同一会话说明失败点并继续。", "You can stop generation, explain the issue and continue in the same session.")],
            ]} />
            <DemoBlock language={language} title={text("单 CLI demo：小而完整的修复", "Single CLI demo: a small, complete fix")} value={text("修复上传文件名包含空格时的解析问题。\n\n边界：只改上传解析与相关测试，不重构存储层。\n验收：新增失败复现测试；运行 go test ./internal/hub -run Upload；总结修改文件和残余风险。", "Fix parsing of uploaded filenames containing spaces.\n\nScope: Change only upload parsing and related tests; do not refactor storage.\nAcceptance: Add a regression test, run go test ./internal/hub -run Upload, and summarize changed files and remaining risks.")} copied={copied === 'chat'} onCopy={() => copyDemo('chat', text("修复上传文件名包含空格时的解析问题。\n\n边界：只改上传解析与相关测试，不重构存储层。\n验收：新增失败复现测试；运行 go test ./internal/hub -run Upload；总结修改文件和残余风险。", "Fix parsing of uploaded filenames containing spaces.\n\nScope: Change only upload parsing and related tests; do not refactor storage.\nAcceptance: Add a regression test, run go test ./internal/hub -run Upload, and summarize changed files and remaining risks."))} />
            <div className="mt-7 grid gap-5 md:grid-cols-2">
              <Figure number={6} src="/help/chat-demo.webp" alt={text("包含用户任务、工具命令和助手结果的聊天会话", "Chat containing a user task, tool commands and the assistant result")} caption={text("任务执行：将目标、命令证据和最终结论放在同一连续会话。", "Task execution: keep the goal, command evidence and conclusion in one continuous session.")} />
              <Figure number={7} src="/help/chat-approval.webp" alt={text("浏览器中的命令审批卡片，提供允许和拒绝按钮", "Browser command approval card with Allow and Deny buttons")} caption={text("浏览器审批：先读命令、目录与理由，再决定允许或拒绝。", "Browser approval: read the command, directory and reason before allowing or denying it.")} />
            </div>
          </GuideSection>

          <GuideSection id="orchestration" eyebrow={text("04 · 多 CLI", "04 · Multiple CLIs")} title={text("编排界面的四个关键选择", "Four key orchestration choices")} icon={GitBranch}>
            <p>{text("编排在同一运行中安排 CLI 参与者规划、执行、整合与复核，并在时间线中保留证据。参与者按工作器区分，角色和模型可以不同；可用组合以端点能力为准。", "Orchestration assigns CLI participants to plan, execute, integrate and review within one run, keeping evidence in the timeline. Participants are identified by worker; their roles and models may differ. Available combinations depend on endpoint capabilities.")}</p>
            <Figure number={8} src="/help/orchestration-overview.webp" alt={text("CLI 编排页面，包含时间线、协作模式、工作器、配置、轮次和任务输入", "Historical orchestration screen with a timeline, mode, workers, profile, rounds and task input")} caption={text("编排总览：左侧保留 run，中央是轮次时间线，右侧配置模式、工作器、证明配置和轮次。", "Historical overview: runs on the left, the timeline in the center, and mode, worker, profile and round controls on the right.")} />
            <div className="mt-7 grid gap-3 sm:grid-cols-2">
              <Choice title={text("模式", "Mode")} value={text("协作 / 辩论", "Collaboration / Debate")} text={text("协作追求分工交付；辩论追求反例、证伪和更强替代方案。", "Collaboration divides delivery responsibilities; debate seeks counterexamples and stronger alternatives.")} />
              <Choice title={text("工作器", "Workers")} value="Claude + Codex / Codex + Codex / Claude + Claude" text={text("按端点能力选择。异构组合适合互补审查，同构组合适合双实现或交叉验证。", "Choose according to endpoint capabilities. Mixed CLIs can complement each other; matching CLIs can implement or verify independently.")} />
              <Choice title={text("配置", "Profile")} value={text("默认 / 形式化证明", "Default / Formal proof")} text={text("证明任务选择 formal-proof；普通代码和设计任务保持默认。", "Choose formal-proof for proof tasks; keep the default for ordinary code or design work.")} />
              <Choice title={text("执行方式", "Execution")} value={text("动态执行 / 有界轮次", "Dynamic / Bounded rounds")} text={text("动态运行每轮检查完成或停滞，没有固定轮次上限；有界运行保留所选轮次。两者都可随时停止。", "Dynamic runs check for completion or a stall after each cycle without a fixed round limit. Bounded runs retain their selected round limit. Both can be stopped at any time.")} />
            </div>
            <Callout icon={GitBranch} title={text("连续性原则", "Keep the same context")}>{text("对同一目标的补充直接在当前运行输入框发送，结束后也可继续同一运行。只有明确选择“新运行”才创建新上下文。旧运行保留原有执行方式，不会因切换站点或刷新变成动态模式。", "Send follow-ups in the current run’s composer, including after it finishes. Only an explicit New run action creates a new context. Existing runs retain their execution mode when you switch sites or refresh.")}</Callout>
          </GuideSection>

          <GuideSection id="live-input" eyebrow={text("04.1 · 运行中交互", "04.1 · Interaction during a run")} title={text("追加输入与原生问题卡", "Follow-up input and native question cards")} icon={MessageSquare}>
            <p>{text("运行中的输入框用于补充要求。默认排队，等待当前原生工作阶段结束后交付；选择立即发送会请求当前 CLI 尽快调整方向，实际行为取决于 CLI 能力。已输出的内容不会撤回，也不会新建运行。", "Use the composer to add requirements during a run. Queue is the default: delivery waits for the current native work phase to end. Send now asks the current CLI to adjust promptly, with behavior depending on CLI support. Earlier output is not withdrawn and no new run is created.")}</p>
            <StepList items={[
              [text("检查追加状态", "Check delivery status"), text("追加要求会显示排队、发送中、已送达 CLI 或失败。已送达只表示原生接口接收或完整写出，仍需从后续输出确认模型处理结果。", "Follow-ups show Queued, Sending, Delivered to CLI or Failed. Delivered means the native interface accepted the input or the write completed; check subsequent output to confirm the model acted on it.")],
              [text("能力不足时", "When support is unavailable"), text("运行中追加需要端点上报支持。控件不可用时查看提示或端点能力；也可在任务结束后继续当前运行，不要为补充要求另建运行。", "Live input requires endpoint support. Read the disabled control’s explanation or check endpoint capabilities. You can also continue the same run after it finishes; a follow-up does not need a new run.")],
              [text("在问题卡回答", "Answer in the question card"), text("CLI 发起的原生问题显示在待答入口或问题卡中。按实际选项、单选或多选以及自定义输入规则填写；多题需要整组显式提交。普通追加输入不会代替问题卡答案。", "Native CLI questions appear in the pending-question area or a question card. Follow the provided single-choice, multiple-choice and custom-answer rules, then explicitly submit the whole group. Ordinary follow-up input does not answer a question card.")],
              [text("断线后先核对", "Check state after reconnecting"), text("刷新会同步仍有效的问题和追加记录。答案待确认时先查询状态，不要重复提交；问题关闭、运行结束或原生进程重启后，旧问题可能失效。未提交的问题答案不保证跨刷新保存。", "Refreshing syncs valid questions and follow-up records. Query an unconfirmed answer’s status before submitting again. A question may expire when it closes, the run ends or the native process restarts. Unsubmitted question answers are not guaranteed to survive a refresh.")],
            ]} />
            <Callout icon={ShieldCheck} title={text("问答可用条件", "Question support depends on capabilities")}>{text("原生问答同时取决于账户功能开关、Hub 与页面协议、端点 CLI 版本和权限模式。以设置与运行页面报告的可用性为准。问题卡用于澄清任务，命令审批仍遵循原有权限策略。", "Native questions require the account feature, compatible Hub and page protocols, and a supported endpoint CLI version and permission mode. Check the availability shown in Settings and the run. Question cards clarify tasks; command approvals keep their existing permission policy.")}</Callout>
          </GuideSection>

          <GuideSection id="collaboration" eyebrow={text("05 · 编排模式 A", "05 · Mode A")} title={text("协作模式：分析、实现、验证逐步收敛", "Collaboration: analyze, implement and verify")} icon={UsersRound}>
            <p>{text("协作模式适合能拆分为互补职责的交付任务。下面是职责示例，实际规划、执行与审核由运行安排；动态模式会按检查结果继续或结束，不固定为这四步。", "Collaboration suits work with complementary responsibilities. The roles below are examples; actual planning, execution and review are scheduled by the run. Dynamic execution continues or stops based on checks rather than a fixed four-step sequence.")}</p>
            <RoleFlow roles={[
              [text("第 1 轮", "Round 1"), text("建模", "Model the problem"), text("读取最小相关文件，列出不变量、风险和执行计划。", "Read the smallest relevant file set and identify invariants, risks and an execution plan.")],
              [text("第 2 轮", "Round 2"), text("实现", "Implement"), text("依据上一轮结论改代码并运行聚焦测试。", "Use the previous findings to change code and run focused tests.")],
              [text("第 3 轮", "Round 3"), text("审查", "Review"), text("针对验收条件寻找遗漏、竞态和反例。", "Look for omissions, races and counterexamples against the acceptance criteria.")],
              [text("最终检查", "Final check"), text("验证", "Verify"), text("只复跑关键命令，给出满足/不满足结论。", "Rerun essential commands and state whether the criteria are satisfied.")],
            ]} />
            <DemoBlock language={language} title={text("协作模式 demo", "Collaboration demo")} value={collaborationDemo} copied={copied === 'collaboration'} onCopy={() => copyDemo('collaboration', collaborationDemo)} />
            <Figure number={9} src="/help/collaboration-run.webp" alt={text("协作编排运行时间线，显示建模、实现、审查和最终检查轮次", "Collaboration timeline with analysis, implementation, review and final checks")} caption={text("协作时间线：每轮职责不同，命令与结论折叠在对应轮次中，减少重复上下文。", "Collaboration timeline: distinct responsibilities and grouped evidence reduce repeated context.")} />
            <GoodBad language={language} good={[text("交付目标清晰，可拆成分析/实现/验证", "A clear delivery goal that can be split into analysis, implementation and verification"), text("需要一个 CLI 修改、另一个 CLI 独立审查", "One CLI makes changes while another reviews independently"), text("需要保留命令、审批和验收证据", "Commands, approvals and acceptance evidence need to remain visible")]} bad={[text("只有一个简单问题，单 CLI 一轮即可完成", "A simple question that one CLI can finish in one turn"), text("两个角色没有不同职责，只会重复回答", "Two roles without distinct responsibilities would repeat each other"), text("任务包含多个互不相关的仓库或目标", "The request combines unrelated repositories or goals")]} />
          </GuideSection>

          <GuideSection id="debate" eyebrow={text("06 · 编排模式 B", "06 · Mode B")} title={text("辩论模式：先写可证伪主张，再寻找反例", "Debate: state a falsifiable claim, then seek counterexamples")} icon={Scale}>
            <p>{text("辩论模式不追求“观点数量”，而是测试最强版本的主张。支持方给出可检查的方案与证据；质疑方必须提出反例、隐藏假设或失败实验，并给出更安全的替代方案。", "Debate tests the strongest form of a claim. The proposer provides a checkable design and evidence. The critic identifies counterexamples, hidden assumptions or failing experiments, and proposes a safer alternative.")}</p>
            <RoleFlow roles={[
              [text("主张", "Claim"), text("提案者", "Proposer"), text("明确不变量、边界和可证伪结果，避免泛泛立场。", "Specify invariants, boundaries and falsifiable outcomes instead of general positions.")],
              [text("反证", "Counterexample"), text("质疑者", "Critic"), text("构造具体反例或运行不利实验，不接受抽象反对。", "Construct a concrete counterexample or adverse test instead of an abstract objection.")],
              [text("修正", "Revision"), text("提案者", "Proposer"), text("吸收有效反例，收紧设计或最小化修改。", "Incorporate valid counterexamples, tighten the design or reduce the change.")],
              [text("裁决", "Decision"), text("验证者", "Verifier"), text("按证据判断 satisfied / unsatisfied / blocked。", "Use evidence to judge satisfied, unsatisfied or blocked.")],
            ]} />
            <DemoBlock language={language} title={text("辩论模式 demo", "Debate demo")} value={debateDemo} copied={copied === 'debate'} onCopy={() => copyDemo('debate', debateDemo)} />
            <Figure number={10} src="/help/debate-run.webp" alt={text("辩论编排运行时间线，显示主张、反证、修正和裁决", "Debate timeline with a claim, counterexample, revision and decision")} caption={text("辩论时间线：把争议压缩成可证伪主张、反例和最终证据，而不是重复长篇观点。", "Debate timeline: capture falsifiable claims, counterexamples and final evidence instead of repeating long opinions.")} />
            <GoodBad language={language} good={[text("安全边界、并发语义或数据隔离存在高风险假设", "Security, concurrency or data isolation depends on risky assumptions"), text("需要在实现前比较两种架构并主动找反例", "Two architectures need comparison and counterexample testing before implementation"), text("形式化规格可能被弱化，需要独立质疑", "A formal specification needs independent scrutiny against weakening")]} bad={[text("需求已经确定，只需按步骤实现", "The requirements are settled and only implementation remains"), text("没有可验证命题或数据，只有偏好讨论", "There is no testable claim or data, only a preference discussion"), text("为了“更聪明”无条件增加轮次", "Extra rounds are being added without a concrete purpose")]} />
          </GuideSection>

          <GuideSection id="formal-proof" eyebrow={text("07 · 专项配置", "07 · Specialized profile")} title={text("形式化验证：让证明义务和证据成为主线", "Formal verification: center the work on obligations and evidence")} icon={FileCheck2}>
            <p>{text("选择“形式化证明”配置后，Bridge 会保留实际项目和一份轻量证明记录，集中记录目标、未解决义务、关键命令证据与阻塞点。它适合 Coq/Rocq、Isabelle 等需要真实编译器验证的任务，不把语言模型的文字判断当作证明完成。", "The Formal proof profile keeps the real project and a lightweight proof record with goals, open obligations, key commands and blockers. It suits Coq/Rocq, Isabelle and other tasks requiring real proof-tool checks. A model’s verbal judgment is not a completed proof.")}</p>
            <Figure number={11} src="/help/formal-proof-setup.webp" alt={text("形式化证明编排配置，显示 proof profile、工作目录、文件和轮次", "Formal-proof configuration with profile, working directory, attachments and rounds")} caption={text("证明配置：选择 formal-proof、准确工作目录和最少附件，再给出具体构建命令。", "Proof setup: select formal-proof, the correct working directory and minimal attachments, then supply a concrete build command.")} />
            <StepList items={[
              [text("限定系统与目标", "Identify the system and goal"), text("写明 Coq/Rocq 或 Isabelle、目标 theorem/session，以及不可使用的 shortcut。", "Name Coq/Rocq or Isabelle, the target theorem or session, and prohibited shortcuts.")],
              [text("上传最少材料", "Attach only what is needed"), text("只上传目标源文件、契约、ROOT/_CoqProject 和必要日志；不要上传整个 build 输出。", "Include target sources, contracts, ROOT/_CoqProject and relevant logs. Do not attach complete build output.")],
              [text("声明证明义务", "State proof obligations"), text("列出不得弱化的前置/后置条件、允许修改范围和必须保持的不变量。", "List conditions that must not be weakened, allowed changes and invariants to preserve.")],
              [text("给出真实命令", "Provide real commands"), text("例如 coqc、rocq compile、isabelle build；版本探测不等于证明通过。", "Use commands such as coqc, rocq compile or isabelle build. A version check does not verify a proof.")],
              [text("要求负向审计", "Require an audit for shortcuts"), text("搜索 sorry、admitted、axiom、oops 以及被偷偷改弱的命题。", "Search for sorry, admitted, axiom, oops and silently weakened statements.")],
              [text("阅读最终结论", "Read the conclusion"), text("结论应包含 outcome、成功命令、未满足义务、证据引用和残余风险。", "Look for the outcome, successful commands, unmet obligations, evidence references and remaining risks.")],
            ]} />
            <DemoBlock language={language} title={text("Coq/Rocq 形式化证明 demo", "Coq/Rocq formal proof demo")} value={formalProofDemo} copied={copied === 'proof'} onCopy={() => copyDemo('proof', formalProofDemo)} />
            <Figure number={12} src="/help/formal-proof-result.webp" alt={text("形式化证明编排结果，显示构建命令、证明轮次和 satisfied 结论", "Formal-proof result with build commands, proof rounds and a satisfied conclusion")} caption={text("证明结果：只有真实 proof assistant 命令成功且负向审计通过，才接受 satisfied。", "Proof result: accept satisfied only after real proof-tool commands succeed and the shortcut audit passes.")} />
            <Callout icon={FileCheck2} title={text("不要接受这些替代品", "Do not accept these substitutes")}>{text("“代码看起来正确”、只运行", "Saying “the code looks correct,” running only ")}<code>--version</code>{text("、添加", ", adding ")}<code>Admitted</code>/<code>sorry</code>{text("、更改 theorem 使其更弱、或者只给伪代码，都不等于形式化验证完成。", ", weakening the theorem or providing only pseudocode does not complete formal verification.")}</Callout>
          </GuideSection>

          <GuideSection id="context" eyebrow={text("08 · 性能", "08 · Performance")} title={text("用更少上下文获得更稳定的编排", "Make orchestration steadier with less context")} icon={Zap}>
            <p>{text("上下文成本主要来自重复扫描、无边界附件、过多轮次和新建 run。下面的做法同时减少 token、延迟和无关输出。", "Repeated scans, oversized attachments, unnecessary rounds and new runs increase context cost. The following practices reduce tokens, latency and irrelevant output.")}</p>
            <div className="mt-6 overflow-hidden rounded-md border border-border">
              <table className="w-full text-left text-sm">
                <thead className="bg-muted/50 text-xs"><tr><th className="px-4 py-3 font-medium">{text("做法", "Practice")}</th><th className="px-4 py-3 font-medium">{text("为什么有效", "Why it helps")}</th><th className="hidden px-4 py-3 font-medium sm:table-cell">{text("建议值", "Suggestion")}</th></tr></thead>
                <tbody className="divide-y divide-border text-muted-foreground">
                  {[
                    [text("任务先写边界和验收", "Start with scope and acceptance criteria"), text("避免两个 CLI 各自猜测目标并扫描无关目录", "Keeps both CLIs from guessing the goal or scanning unrelated directories"), text("4–12 行", "4–12 lines")],
                    [text("按执行方式控制成本", "Control cost for the execution mode"), text("有界模式选择够用的轮次；动态模式关注进展与停滞，必要时停止", "Choose enough rounds in bounded mode; monitor progress or stalls in dynamic mode and stop when needed"), text("以验收结果为准", "Follow the acceptance results")],
                    [text("续问留在同一 run", "Keep follow-ups in the same run"), text("按支持方式延续原生 Codex thread / Claude session", "Preserves continuity with the native Codex thread or Claude session where supported"), text("使用“继续”", "Use Continue")],
                    [text("附件只给相关文件", "Attach only relevant files"), text("大日志和构建目录会淹没证明义务", "Large logs and build directories obscure proof obligations"), text("通常 2–8 个文件", "Usually 2–8 files")],
                    [text("打开每轮后压缩", "Use compaction after turns"), text("支持的原生 CLI 可在长运行轮次间压缩历史", "Supported native CLIs can compact history between turns in long runs"), text("长任务再启用", "Enable for long tasks")],
                    [text("最终轮只做验证", "Keep the final round focused on verification"), text("不重新设计，只运行验收命令并输出结论", "Run acceptance commands and report results instead of redesigning"), text("明确验证职责", "A clear verifier role")],
                  ].map(([a, b, c]) => <tr key={a}><td className="px-4 py-3 font-medium text-foreground">{a}</td><td className="px-4 py-3 leading-6">{b}</td><td className="hidden px-4 py-3 font-mono text-xs sm:table-cell">{c}</td></tr>)}
                </tbody>
              </table>
            </div>
            <Figure number={13} src="/help/context-controls.webp" alt={text("旧版编排侧栏中的轮次和原生上下文压缩控制", "Historical orchestration sidebar with round and native compaction controls")} caption={text("历史界面：有界运行保留轮次选择；动态运行不显示固定轮次。原生压缩以端点支持为准。", "Historical interface: bounded runs retain round controls; dynamic runs have no fixed-round selector. Native compaction depends on endpoint support.")} />
            <Callout icon={Sparkles} title={text("一个实用判断", "A practical test")}>{text("如果第二个 CLI 无法得到不同职责，先用单 CLI。只有当独立审查、反证或 proof assistant 证据能明显降低错误风险时，才启动编排。", "Start with one CLI if the second has no distinct responsibility. Use orchestration when independent review, counterexamples or proof-tool evidence can meaningfully reduce mistakes.")}</Callout>
          </GuideSection>

          <GuideSection id="sharing" eyebrow={text("09 · 结果管理", "09 · Results")} title={text("会话、继续运行与只读分享", "Sessions, continued runs and read-only sharing")} icon={Cloud}>
            <p>{text("左侧会话和 run 列表用于恢复上下文。分享按钮生成只读快照链接，适合让同事检查结果；分享不会授权对方访问你的端点或继续执行。", "The session and run lists restore context. Share creates a read-only snapshot link for reviewing results; it does not grant access to your endpoint or permission to execute more work.")}</p>
            <StepList items={[
              [text("重命名", "Rename"), text("使用能表达目标的标题，后续搜索比“新会话”更可靠。", "Use a title that describes the goal so it is easier to find later.")],
              [text("查找与归档", "Find and archive runs"), text("编排历史按端点、状态和关键词查找，可继续加载更早记录。已结束的运行可归档或恢复；归档不删除上下文，也不移除用量。", "Filter orchestration history by endpoint, status and keywords, and load older records. Finished runs can be archived and restored; archiving preserves context and usage totals.")],
              [text("继续", "Continue"), text("同一任务直接发送补充；编排使用当前 run 的“继续”。", "Send follow-ups for the same task in the current session or run.")],
              [text("分享", "Share"), text("点击分享后复制只读链接，先自行打开检查是否包含敏感内容。", "Copy the read-only link, then open it yourself to check for sensitive information.")],
              [text("撤销", "Revoke"), text("不再需要时撤销分享；删除会话会移除 Hub 记录，但不会改动工作区文件。", "Revoke sharing when no longer needed. Deleting a chat session removes Hub records without changing workspace files.")],
            ]} />
            <div className="mt-7 grid gap-5 md:grid-cols-2">
              <Figure number={14} src="/help/session-actions.webp" alt={text("会话列表中的分享、重命名和删除操作", "Session actions for sharing, renaming and deleting")} caption={text("会话管理：分享、重命名和删除都作用于当前用户拥有的记录。", "Session management: share, rename and delete apply only to records owned by the current user.")} />
              <Figure number={15} src="/help/public-share.webp" alt={text("公开只读对话快照页面", "Public read-only conversation snapshot")} caption={text("只读分享：访问者只能查看已保存内容，不能调用你的 CLI。", "Read-only sharing: visitors can view saved content but cannot call your CLI.")} />
            </div>
          </GuideSection>

          <GuideSection id="settings" eyebrow={text("10 · 设置", "10 · Settings")} title={text("主题、语言、端点维修与移动端", "Theme, language, endpoint repair and mobile access")} icon={Settings}>
            <p>{text("设置页可以切换主题和语言、查看端点能力矩阵、生成修复命令或删除旧端点。移动端保留主要工作流，侧栏通过左上角菜单打开。", "Settings provides theme and language controls, endpoint capabilities, repair commands and endpoint removal. On smaller screens, the upper-left menu opens the sidebar.")}</p>
            <div className="mt-7 grid gap-5 md:grid-cols-[1.45fr_0.75fr]">
              <Figure number={16} src="/help/endpoint-capabilities.webp" alt={text("CLI 端点展开后的能力矩阵和修复连接按钮", "Expanded endpoint capabilities and the repair connection button")} caption={text("能力矩阵：确认 chat、orchestration 和 browser approval 均符合所选策略。", "Capabilities: check chat, orchestration and browser approval support for the selected permission policy.")} />
              <Figure number={17} src="/help/mobile-workspace.webp" alt={text("手机尺寸的 ProofBridge 工作区", "ProofBridge workspace at a phone screen size")} caption={text("历史移动端界面：菜单、端点状态、消息和输入区。", "Historical mobile interface: menu, endpoint status, messages and composer.")} />
            </div>
            <Callout icon={Laptop} title={text("端点离线时", "When an endpoint is offline")}>{text("先确认本机 Bridge 进程和网络；若机器 ID 已存在但 token 过期，在设置中展开该端点并生成“修复连接”命令，不要重复创建多个同名端点。", "Check the Bridge process and network first. If the machine already exists but its token has expired, expand that endpoint in Settings and generate Repair connection instead of creating duplicate endpoints.")}</Callout>
          </GuideSection>

          <GuideSection id="troubleshooting" eyebrow={text("11 · 排错", "11 · Troubleshooting")} title={text("按现象快速定位问题", "Find the cause from the symptom")} icon={CircleHelp}>
            <div className="mt-5 divide-y divide-border rounded-md border border-border">
              <Trouble title={text("注册按钮不可用或验证反复出现", "Registration is disabled or verification keeps repeating")} answer={text("确认两次密码一致且至少 10 位；关闭会拦截 challenges.cloudflare.com 的脚本扩展；等待 Turnstile 显示完成后再提交。", "Check that both passwords match and have at least 10 characters. Disable extensions blocking challenges.cloudflare.com and wait for Turnstile to complete before submitting.")} />
              <Trouble title={text("端点一直离线", "The endpoint stays offline")} answer={text("在端点机器检查 Bridge 进程、系统时间和出站 HTTPS/WSS；token 过期时从设置生成修复命令。", "Check Bridge, the system clock and outbound HTTPS/WSS on the endpoint machine. Generate a repair command in Settings if its token expired.")} />
              <Trouble title={text("编排开始按钮禁用", "Start orchestration is disabled")} answer={text("所选端点必须在线，并同时上报所选两个工作器的编排能力；“需要确认”模式还要求 browser approval 可用。", "The selected endpoint must be online and report orchestration support for both chosen workers. Require confirmation also needs browser approval support.")} />
              <Trouble title={text("形式化证明显示完成但没有可信证据", "Formal proof says complete without trustworthy evidence")} answer={text("检查最终结论是否列出真实构建命令和退出结果；没有 coqc/rocq/isabelle build 成功记录时，继续当前 run 要求补验证。", "Look for real build commands and exit results. If successful coqc, rocq or isabelle build evidence is missing, continue the same run and request verification.")} />
              <Trouble title={text("上下文越来越长、响应变慢", "Context keeps growing and responses slow down")} answer={text("停止增加重复轮次；在同一 run 里明确只读相关文件；长任务启用每轮后压缩；将新目标拆成新 run。", "Avoid repeated rounds, restrict reads to relevant files, and use supported native compaction for long tasks. Start a new run for an unrelated goal.")} />
              <Trouble title={text("线上页面仍是旧版本", "The site still looks like an older version")} answer={text("刷新后对照本页或更新页的前端构建时间。Hub 的 /health 只说明后端版本，不能证明网页或 Bridge 已更新。各组件可能独立部署；端点 Bridge 版本以各自连接上报为准。", "Refresh and compare the frontend build time here or on Release notes. The Hub’s /health identifies only the backend, not the webpage or Bridge. Components may be deployed independently; each endpoint reports its own Bridge version when connected.")} />
            </div>
            <div className="mt-8 flex flex-col items-start justify-between gap-4 rounded-md border border-border bg-muted/30 p-5 sm:flex-row sm:items-center">
              <div><h3 className="text-sm font-semibold">{text("准备开始？", "Ready to start?")}</h3><p className="mt-1 text-xs leading-5 text-muted-foreground">{text("先连接一个端点，再从小而可验证的任务开始。", "Connect an endpoint and begin with a small, verifiable task.")}</p></div>
              <a href="/" className="inline-flex h-9 items-center gap-2 rounded-md bg-primary px-4 text-sm font-medium text-primary-foreground hover:bg-primary/90">{text("打开 ProofBridge", "Open ProofBridge")}<ArrowRight className="h-4 w-4" /></a>
            </div>
          </GuideSection>
        </main>

        <aside className="sticky top-14 hidden h-[calc(100vh-3.5rem)] py-8 xl:block">
          <div className="border-l border-border pl-4">
            <p className="text-[11px] font-semibold uppercase text-muted-foreground">{text("本页重点", "On this page")}</p>
            <div className="mt-3 space-y-3 text-xs leading-5 text-muted-foreground">
              <p className="flex gap-2"><Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-500" />{text("17 张历史界面截图", "17 historical interface screenshots")}</p>
              <p className="flex gap-2"><Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-500" />{text("4 个可复制 demo", "4 copyable demos")}</p>
              <p className="flex gap-2"><Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-500" />{text("协作与辩论分开讲解", "Separate collaboration and debate guides")}</p>
              <p className="flex gap-2"><Check className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-500" />{text("形式化验证验收清单", "A formal verification checklist")}</p>
            </div>
          </div>
        </aside>
      </div>
    </div>
  );
}

function HelpNav({ language, activeSection, close }: { language: Language; activeSection: string; close?: () => void }) {
  const text = (zh: string, en: string) => language === 'zh' ? zh : en;
  const navItems = helpNavItems(text);
  return <nav className="space-y-0.5">{navItems.map(([id, label]) => <a key={id} href={`#${id}`} onClick={close} className={`flex items-center justify-between rounded-md px-2 py-1.5 text-xs transition-colors ${activeSection === id ? 'bg-muted font-medium text-foreground' : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'}`}><span>{label}</span>{activeSection === id && <ChevronRight className="h-3 w-3" />}</a>)}</nav>;
}

function GuideSection({ id, eyebrow, title, icon: Icon, children }: { id: string; eyebrow: string; title: string; icon: React.ComponentType<{ className?: string }>; children: React.ReactNode }) {
  return <section id={id} className="scroll-mt-24 border-b border-border py-14 last:border-b-0"><div className="mb-4 flex items-center gap-2 text-xs font-medium text-muted-foreground"><Icon className="h-4 w-4" />{eyebrow}</div><h2 className="text-2xl font-semibold leading-tight">{title}</h2><div className="mt-5 space-y-5 text-sm leading-7 text-muted-foreground [&_code]:rounded [&_code]:bg-muted [&_code]:px-1.5 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-xs [&_code]:text-foreground">{children}</div></section>;
}

function Figure({ src, alt, caption, number }: FigureProps) {
  return <figure className="overflow-hidden rounded-md border border-border bg-card"><div className="aspect-[16/10] overflow-hidden bg-muted/30 p-1"><img src={src} alt={alt} loading="lazy" className="h-full w-full object-contain" /></div><figcaption className="flex gap-3 border-t border-border px-3 py-3 text-xs leading-5 text-muted-foreground"><span className="font-mono text-[10px] text-foreground">{String(number).padStart(2, '0')}</span><span>{caption}</span></figcaption></figure>;
}

function QuickFact({ icon: Icon, title, text }: { icon: React.ComponentType<{ className?: string }>; title: string; text: string }) {
  return <div className="bg-background p-4"><Icon className="h-4 w-4 text-primary" /><h3 className="mt-3 text-sm font-medium">{title}</h3><p className="mt-1 text-xs leading-5 text-muted-foreground">{text}</p></div>;
}

function StepList({ items }: { items: Array<[string, string]> }) {
  return <ol className="mt-6 space-y-4">{items.map(([title, text], index) => <li key={title} className="grid grid-cols-[28px_1fr] gap-3"><span className="flex h-7 w-7 items-center justify-center rounded-md border border-border bg-muted/40 font-mono text-[11px] text-foreground">{String(index + 1).padStart(2, '0')}</span><div><h3 className="text-sm font-medium text-foreground">{title}</h3><p className="mt-0.5 text-sm leading-6 text-muted-foreground">{text}</p></div></li>)}</ol>;
}

function Callout({ icon: Icon, title, children }: { icon: React.ComponentType<{ className?: string }>; title: string; children: React.ReactNode }) {
  return <div className="mt-6 flex gap-3 rounded-md border border-border bg-muted/35 p-4"><Icon className="mt-0.5 h-4 w-4 shrink-0 text-primary" /><div><h3 className="text-xs font-semibold text-foreground">{title}</h3><div className="mt-1 text-xs leading-6 text-muted-foreground">{children}</div></div></div>;
}

function DemoBlock({ language, title, value, copied, onCopy }: { language: Language; title: string; value: string; copied: boolean; onCopy: () => void }) {
  const text = (zh: string, en: string) => language === 'zh' ? zh : en;
  return <div className="mt-7 overflow-hidden rounded-md border border-border bg-[#111827] text-slate-100"><div className="flex items-center justify-between border-b border-white/10 px-4 py-2.5"><span className="flex items-center gap-2 text-xs font-medium"><Code2 className="h-3.5 w-3.5" />{title}</span><button type="button" onClick={onCopy} className="inline-flex h-7 items-center gap-1.5 rounded px-2 text-[11px] text-slate-300 hover:bg-white/10 hover:text-white">{copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}{copied ? text("已复制", "Copied") : text("复制", "Copy")}</button></div><pre className="overflow-x-auto whitespace-pre-wrap p-4 font-mono text-xs leading-6 text-slate-300">{value}</pre></div>;
}

function Choice({ title, value, text }: { title: string; value: string; text: string }) {
  return <div className="rounded-md border border-border p-4"><p className="text-[11px] font-semibold uppercase text-muted-foreground">{title}</p><h3 className="mt-1 text-sm font-medium text-foreground">{value}</h3><p className="mt-2 text-xs leading-5 text-muted-foreground">{text}</p></div>;
}

function RoleFlow({ roles }: { roles: Array<[string, string, string]> }) {
  return <div className="mt-6 grid gap-px overflow-hidden rounded-md border border-border bg-border sm:grid-cols-2">{roles.map(([turn, role, text]) => <div key={turn} className="bg-background p-4"><p className="font-mono text-[10px] text-muted-foreground">{turn}</p><h3 className="mt-1 text-sm font-medium text-foreground">{role}</h3><p className="mt-2 text-xs leading-5 text-muted-foreground">{text}</p></div>)}</div>;
}

function GoodBad({ language, good, bad }: { language: Language; good: string[]; bad: string[] }) {
  const text = (zh: string, en: string) => language === 'zh' ? zh : en;
  return <div className="mt-6 grid gap-4 sm:grid-cols-2"><div className="rounded-md border border-emerald-500/25 bg-emerald-500/5 p-4"><h3 className="text-xs font-semibold text-emerald-700 dark:text-emerald-400">{text("适合使用", "Good fit")}</h3><ul className="mt-3 space-y-2">{good.map((item) => <li key={item} className="flex gap-2 text-xs leading-5"><CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0 text-emerald-500" />{item}</li>)}</ul></div><div className="rounded-md border border-border bg-muted/25 p-4"><h3 className="text-xs font-semibold text-foreground">{text("先不要使用", "Start without it")}</h3><ul className="mt-3 space-y-2">{bad.map((item) => <li key={item} className="flex gap-2 text-xs leading-5"><X className="mt-0.5 h-3.5 w-3.5 shrink-0 text-muted-foreground" />{item}</li>)}</ul></div></div>;
}

function Trouble({ title, answer }: { title: string; answer: string }) {
  return <details className="group p-4"><summary className="flex cursor-pointer list-none items-center justify-between gap-4 text-sm font-medium text-foreground"><span>{title}</span><ChevronRight className="h-4 w-4 shrink-0 text-muted-foreground transition-transform group-open:rotate-90" /></summary><p className="mt-3 pr-8 text-xs leading-6 text-muted-foreground">{answer}</p></details>;
}
