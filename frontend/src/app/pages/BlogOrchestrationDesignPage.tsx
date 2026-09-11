import React, { useEffect, useMemo, useState } from 'react';
import {
  ArrowDown,
  ArrowUpRight,
  Check,
  ChevronRight,
  Copy,
  Github,
  RotateCcw,
  Share2,
} from 'lucide-react';
import './BlogOrchestrationDesignPage.css';

type PhaseKey = 'execute' | 'supplement' | 'integrate' | 'review';

type Phase = {
  key: PhaseKey;
  actor: 'A' | 'B';
  label: string;
  eyebrow: string;
  action: string;
  receives: string;
  delivers: string;
  why: string;
};

const phases: Phase[] = [
  {
    key: 'execute',
    actor: 'A',
    label: '执行',
    eyebrow: 'Phase 01',
    action: '先推进一条可行路径，并在真实工作区落下候选实现。',
    receives: '原始任务、已审查计划、当前工作区。',
    delivers: '文件修改、运行过的命令、已通过检查、未覆盖缺口。',
    why: 'A 的完成只意味着“有了候选成果”，还没有形成可审查的完成结论。',
  },
  {
    key: 'supplement',
    actor: 'B',
    label: '补充执行',
    eyebrow: 'Phase 02',
    action: '从已有文件与证据继续工作，补缺、挑战遗漏，必要时走另一条实现路径。',
    receives: 'A 的实际修改、命令结果、未解决问题。',
    delivers: '补充修改、新证据、反例或仍然存在的阻塞。',
    why: '第二个视角不是为了重复 A，而是为了扩大缺陷发现面，并让遗漏尽早暴露。',
  },
  {
    key: 'integrate',
    actor: 'A',
    label: '整合',
    eyebrow: 'Phase 03',
    action: '把双方修改收敛到同一最终工作区，统一接口、文件状态与测试。',
    receives: 'A/B 两侧已经落地的工作与证据。',
    delivers: '可供最终审查的统一候选状态。',
    why: '没有显式整合，两个 Agent 可能分别“做对一部分”，却没有人对组合后的行为负责。',
  },
  {
    key: 'review',
    actor: 'B',
    label: '审查',
    eyebrow: 'Phase 04',
    action: '回到原始验收条件，重新检查实际工作区并运行相关验证。',
    receives: '整合后的工作区、原始要求、关键检查线索。',
    delivers: '通过、需要修订、受阻或不确定，以及支持该判断的证据。',
    why: '审查发生在整合之后，才能检查最终组合状态，而不是只认可上一位参与者的口头结论。',
  },
];

const sources = [
  {
    short: 'Anthropic',
    title: 'Building Effective AI Agents',
    tag: 'Evaluator–Optimizer',
    href: 'https://www.anthropic.com/engineering/building-effective-agents',
    body: '把“生成”与“评价/反馈”连接成迭代闭环，并明确指出：当评价标准清晰、迭代改进能带来可测价值时，这类工作流尤其适合。',
    relation: '直接支持：候选成果之后应有评价与反馈，反馈再决定是否进入下一次改进。',
  },
  {
    short: 'Anthropic',
    title: 'Harness design for long-running application development',
    tag: 'Generator / Evaluator',
    href: 'https://www.anthropic.com/engineering/harness-design-long-running-apps',
    body: '在长时软件开发 harness 中分离 generator 与 evaluator，并使用结构化 artifact 在阶段之间交接上下文；文中同时强调 evaluator 有成本，应只在超出单 Agent 稳定能力边界时使用。',
    relation: '直接支持：执行者与评价者分离、artifact handoff、以及“额外阶段必须证明增量价值”。',
  },
  {
    short: 'Google',
    title: 'Agent Development Kit — Sequential Agents',
    tag: 'Sequential Pipeline',
    href: 'https://google.github.io/adk-docs/agents/workflow-agents/sequential-agents/',
    body: '官方代码开发示例以 Code Writer → Code Reviewer → Code Refactorer 串联阶段；后一步消费前一步写入共享状态的结果。',
    relation: '支持：强依赖任务应按职责顺序交接，而不是把参与者当作彼此独立的聊天回合。',
  },
  {
    short: 'Microsoft',
    title: 'AutoGen — Reflection',
    tag: 'Coder / Reviewer',
    href: 'https://microsoft.github.io/autogen/dev/user-guide/core-user-guide/design-patterns/reflection.html',
    body: 'Coder 生成结果，Reviewer 给出 critique 与 approved；未批准则把反馈送回 Coder，直到批准或达到停止条件。',
    relation: '直接支持：执行 → 审查 → 决定继续/结束是一个天然反馈闭环。',
  },
  {
    short: 'NeurIPS 2023',
    title: 'Self-Refine: Iterative Refinement with Self-Feedback',
    tag: 'Feedback / Refine',
    href: 'https://proceedings.neurips.cc/paper_files/paper/2023/hash/91edff07232fb1b55a505a9e9f6c0ff3-Abstract-Conference.html',
    body: '把初始生成后的 feedback → refinement 作为反复迭代的核心机制；同一个模型也可以分别承担 generator、feedback provider 与 refiner。',
    relation: '支持：迭代边界可以围绕“反馈如何推动成果变化”定义，而不必绑定到发言者身份。',
  },
  {
    short: 'ICLR 2024',
    title: 'MetaGPT: Meta Programming for A Multi-Agent Collaborative Framework',
    tag: 'SOP / Artifacts',
    href: 'https://proceedings.iclr.cc/paper_files/paper/2024/hash/6507b115562bb0a305f1958ccc87355a-Abstract-Conference.html',
    body: '通过 SOP、角色分工与中间产物验证组织多 Agent 协作，强调让具备不同职责的 Agent 检查中间结果，减少朴素串联带来的级联错误。',
    relation: '支持：角色、交付物和检查责任应编码进工作流，而不是依赖临时对话默契。',
  },
];

const toc = [
  ['thesis', '核心判断'],
  ['workflow', 'ProofBridge 的一轮'],
  ['benefits', '为什么这样更好'],
  ['comparison', '和 A/B 轮流有什么不同'],
  ['evidence', '权威依据'],
  ['boundaries', '边界与代价'],
  ['evaluation', '如何验证它真的值得'],
] as const;

const comparisonRows = [
  ['轮次语义', '一次推进 + 检查 + 决策的完整周期', '一次 Agent 接手 / 发言'],
  ['完成边界', '审查后才形成稳定状态', 'Agent 输出后即结束当前轮，是否已验收需另行安排'],
  ['职责完整性', '实现、补缺、整合、审查默认存在', '取决于后续提示词与人工是否记得安排'],
  ['交接对象', '工作区、文件、命令结果、缺口与证据', '容易退化成对话文本继续传递'],
  ['恢复定位', '可定位到具体 Phase 与未确认的检查', '通常只能知道“上一位做到哪里”'],
  ['预算含义', 'max rounds ≈ 完整质量闭环预算', 'max rounds ≈ Agent turn 数量'],
];

function EvidenceStrip() {
  return (
    <div className="pb-evidence-strip" aria-label="支持这一设计的研究与框架">
      {['Anthropic', 'Google ADK', 'AutoGen', 'Self-Refine', 'MetaGPT'].map((item) => (
        <span key={item}>{item}</span>
      ))}
    </div>
  );
}

export function BlogOrchestrationDesignPage() {
  const [activePhase, setActivePhase] = useState<PhaseKey>('execute');
  const [activeSection, setActiveSection] = useState('thesis');
  const [comparisonMode, setComparisonMode] = useState<'cycle' | 'turn'>('cycle');
  const [copied, setCopied] = useState(false);
  const [progress, setProgress] = useState(0);

  const selectedPhase = useMemo(
    () => phases.find((phase) => phase.key === activePhase) ?? phases[0],
    [activePhase],
  );

  useEffect(() => {
    const previousTitle = document.title;
    document.title = '一轮，不只是一次发言：ProofBridge 为什么把执行与审查编进同一个闭环';
    const description = document.querySelector<HTMLMetaElement>('meta[name="description"]');
    const previousDescription = description?.content;
    if (description) {
      description.content = 'ProofBridge 编排设计：为什么把执行、补缺、整合与审查放在同一协作轮次，并用权威 Agent workflow 研究解释这一选择。';
    }

    const onScroll = () => {
      const scrollTop = window.scrollY;
      const scrollHeight = document.documentElement.scrollHeight - window.innerHeight;
      setProgress(scrollHeight > 0 ? Math.min(1, scrollTop / scrollHeight) : 0);
    };
    onScroll();
    window.addEventListener('scroll', onScroll, { passive: true });

    return () => {
      document.title = previousTitle;
      if (description && previousDescription !== undefined) description.content = previousDescription;
      window.removeEventListener('scroll', onScroll);
    };
  }, []);

  useEffect(() => {
    const elements = toc
      .map(([id]) => document.getElementById(id))
      .filter((item): item is HTMLElement => Boolean(item));

    const observer = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((entry) => entry.isIntersecting)
          .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top);
        if (visible[0]?.target?.id) setActiveSection(visible[0].target.id);
      },
      { rootMargin: '-18% 0px -64% 0px', threshold: [0, 0.1, 0.4] },
    );

    elements.forEach((element) => observer.observe(element));
    return () => observer.disconnect();
  }, []);

  const share = async () => {
    const data = {
      title: document.title,
      text: 'ProofBridge 编排设计：为什么把执行与审查组织成一个可验证的协作闭环。',
      url: window.location.href,
    };
    try {
      if (navigator.share) {
        await navigator.share(data);
      } else {
        await navigator.clipboard.writeText(window.location.href);
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1800);
      }
    } catch {
      // Sharing is user-cancellable; no error surface is needed.
    }
  };

  return (
    <main className="pb-essay">
      <div className="pb-reading-progress" style={{ transform: `scaleX(${progress})` }} />

      <header className="pb-topbar">
        <a className="pb-brand" href="/" aria-label="返回 ProofBridge">
          ProofBridge
        </a>
        <div className="pb-topbar-actions">
          <a href="https://github.com/Atingaii/ProofBridge" target="_blank" rel="noreferrer">
            <Github size={17} />
            <span>GitHub</span>
          </a>
          <button type="button" onClick={share}>
            {copied ? <Check size={17} /> : <Share2 size={17} />}
            <span>{copied ? '已复制' : '分享'}</span>
          </button>
        </div>
      </header>

      <article>
        <section className="pb-hero">
          <div className="pb-hero-meta">
            <span>2026 年 9 月 12 日</span>
            <span>工程设计</span>
            <span>约 12 分钟</span>
          </div>
          <h1>一轮，<br />不只是一次发言</h1>
          <p className="pb-deck">
            ProofBridge 为什么把 <strong>执行 → 补缺 → 整合 → 审查</strong> 放进同一个 Round：
            不是为了少算几个轮次，而是为了让每一轮都从“候选成果”走到“可检查状态”。
          </p>
          <EvidenceStrip />
          <figure className="pb-hero-figure">
            <img
              src="/blog/orchestration-design/01-closed-cycle.svg"
              alt="小黑角色把候选成果依次交给执行、补缺、整合与审查阶段，最后形成闭环"
            />
            <figcaption>Round 的边界不是“谁刚说完”，而是“系统是否已经得到足以决定下一步的证据”。</figcaption>
          </figure>
        </section>

        <div className="pb-article-grid">
          <aside className="pb-toc" aria-label="文章目录">
            <p>本文内容</p>
            <nav>
              {toc.map(([id, label]) => (
                <a key={id} className={activeSection === id ? 'is-active' : ''} href={`#${id}`}>
                  <span>{label}</span>
                  <ChevronRight size={14} />
                </a>
              ))}
            </nav>
          </aside>

          <div className="pb-content">
            <section id="thesis" className="pb-section">
              <p className="pb-kicker">核心判断</p>
              <h2>把 A→B 放在同一轮，真正优化的是“完成语义”</h2>
              <p className="pb-lead">
                如果底层调用仍然是 A → B → A → B，那么把 A、B 各叫一轮，和把 A→B 叫作一轮，
                <em>不会凭空改变模型能力、调用次数或费用</em>。真正的差异在于：系统把什么定义为一个可以
                checkpoint、retry、terminate、统计和恢复的稳定工作单元。
              </p>

              <div className="pb-thesis-card">
                <span className="pb-thesis-label">ProofBridge 的定义</span>
                <div className="pb-formula" aria-label="Round t 等于 E t V t D t">
                  R<sub>t</sub> = ⟨ E<sub>t</sub>, V<sub>t</sub>, D<sub>t</sub> ⟩
                </div>
                <div className="pb-formula-grid">
                  <div><strong>E · Execute</strong><span>推进成果</span></div>
                  <div><strong>V · Verify</strong><span>检查成果</span></div>
                  <div><strong>D · Decide</strong><span>决定结束、修订或受阻</span></div>
                </div>
                <p>
                  这是 ProofBridge 的工程抽象，不是某篇论文规定的公式。它把“一轮”定义为：
                  从一个稳定状态出发，经过推进与检查，最终得到下一个稳定状态。
                </p>
              </div>

              <p>
                因此，A 完成代码并不等于这一轮已经完成。那只是候选成果。只有经过后续检查，
                调度器才知道应该 <code>PASS</code>、<code>REVISE</code>、<code>BLOCKED</code>，
                还是保留 <code>INCONCLUSIVE</code>。
              </p>
            </section>

            <section id="workflow" className="pb-section pb-section-wide">
              <p className="pb-kicker">当前实现</p>
              <h2>一轮内部，A 与 B 的身份不变，但职责会变化</h2>
              <p>
                新任务在开启规划时先经过 <strong>A 规划 → B 计划审查</strong>。
                随后的每个协作执行周期采用 <strong>A 执行 → B 补充执行 → A 整合 → B 审查</strong>。
                A/B 是工作槽位；实际可以绑定 Codex 或 Claude Code，也可以配置相同或不同模型。
              </p>

              <div className="pb-phase-explorer">
                <div className="pb-phase-tabs" role="tablist" aria-label="ProofBridge 四个阶段">
                  {phases.map((phase) => (
                    <button
                      type="button"
                      role="tab"
                      aria-selected={activePhase === phase.key}
                      className={activePhase === phase.key ? 'is-active' : ''}
                      key={phase.key}
                      onClick={() => setActivePhase(phase.key)}
                    >
                      <span>{phase.actor}</span>
                      <small>{phase.eyebrow}</small>
                      <strong>{phase.label}</strong>
                    </button>
                  ))}
                </div>

                <div className="pb-phase-detail" role="tabpanel">
                  <div className="pb-phase-detail-heading">
                    <span className={`pb-agent-dot is-${selectedPhase.actor.toLowerCase()}`}>{selectedPhase.actor}</span>
                    <div>
                      <small>{selectedPhase.eyebrow}</small>
                      <h3>{selectedPhase.label}</h3>
                    </div>
                  </div>
                  <p className="pb-phase-action">{selectedPhase.action}</p>
                  <dl>
                    <div><dt>接收</dt><dd>{selectedPhase.receives}</dd></div>
                    <div><dt>交付</dt><dd>{selectedPhase.delivers}</dd></div>
                    <div><dt>为什么还需要下一棒</dt><dd>{selectedPhase.why}</dd></div>
                  </dl>
                </div>
              </div>

              <figure className="pb-inline-figure">
                <img
                  src="/blog/orchestration-design/02-artifact-handoff.svg"
                  alt="小黑角色通过文件、命令结果和缺口清单把工作交给下一阶段"
                />
                <figcaption>优先交接 artifact 与 evidence，而不是让下一位 Agent 只继承上一位的自我描述。</figcaption>
              </figure>
            </section>

            <section id="benefits" className="pb-section">
              <p className="pb-kicker">为什么这样更好</p>
              <h2>优势不是“多一个 Reviewer”，而是把质量责任写进状态机</h2>

              <div className="pb-benefit-stack">
                <div className="pb-benefit">
                  <span>01</span>
                  <div>
                    <h3>每轮有明确的收口条件</h3>
                    <p>
                      A 的输出是 candidate，不是 accepted result。把审查放在 Round 内，
                      轮次结束时才形成一个可解释的状态：通过、修订、受阻或不确定。
                      这让 termination 不再依赖“某个 Agent 说自己完成了”。
                    </p>
                  </div>
                </div>
                <div className="pb-benefit">
                  <span>02</span>
                  <div>
                    <h3>容易遗漏的收尾责任被默认编排</h3>
                    <p>
                      实现、补缺、整合、审查都有固定位置。对复杂修改而言，
                      最大风险往往不是没人写代码，而是“每个人都完成了自己的部分，却没人检查组合后的系统”。
                    </p>
                  </div>
                </div>
                <div className="pb-benefit">
                  <span>03</span>
                  <div>
                    <h3>交接围绕证据，而不是围绕聊天历史</h3>
                    <p>
                      后一阶段读取真实文件、运行结果和未解决问题。上下文可以压缩，
                      但可回查的 workspace 与命令线索仍然存在。这样能降低“长对话越传越像”的上下文漂移。
                    </p>
                  </div>
                </div>
                <div className="pb-benefit">
                  <span>04</span>
                  <div>
                    <h3>checkpoint、retry 与恢复都有稳定边界</h3>
                    <p>
                      如果 B 在最终审查时断开，系统知道“执行与整合已完成，但审查尚未确认”。
                      这比只有“上次轮到 B”更容易做精确恢复，也更容易避免盲目重放有副作用的命令。
                    </p>
                  </div>
                </div>
                <div className="pb-benefit">
                  <span>05</span>
                  <div>
                    <h3>轮数预算更接近用户真正关心的进度</h3>
                    <p>
                      <code>maxRounds</code> 表示“允许多少次推进—检查闭环”，而不是“允许多少次 Agent 发言”。
                      对用户而言，前者更接近任务离验收还有多少次完整尝试。
                    </p>
                  </div>
                </div>
                <div className="pb-benefit">
                  <span>06</span>
                  <div>
                    <h3>扩展 Agent 数量时，不必推翻顶层语义</h3>
                    <p>
                      将来可以在 Verify 中加入 Tester、Security Reviewer 或独立 Verifier，
                      也可以把可隔离的检查并行化。Round 仍然是“推进 → 检查 → 决策”，只是内部 Phase 更丰富。
                    </p>
                  </div>
                </div>
              </div>

              <figure className="pb-inline-figure">
                <img
                  src="/blog/orchestration-design/03-stable-boundary.svg"
                  alt="小黑角色站在有证据的决策门前，根据审查结果选择结束或进入下一轮"
                />
                <figcaption>稳定边界：有证据才结束；有缺口就把缺口变成下一轮的明确输入。</figcaption>
              </figure>
            </section>

            <section id="comparison" className="pb-section pb-section-wide">
              <p className="pb-kicker">比较真实机制，而不是比较编号</p>
              <h2>A/B 同轮与 A/B 轮流，差别到底在哪里</h2>
              <p>
                如果“单次 A/B 交替”也被补上完全相同的职责、依赖、交付证据和验收条件，
                两种写法可以实现几乎同一条执行链。因此 ProofBridge 的优势不能表述成
                “同轮天然比分轮质量更高”；更准确的说法是：
                <strong>它把完整闭环作为默认语义，减少每次任务都重新拼装质量流程的机会。</strong>
              </p>

              <div className="pb-mode-switch" aria-label="比较两种轮次语义">
                <button
                  type="button"
                  className={comparisonMode === 'cycle' ? 'is-active' : ''}
                  onClick={() => setComparisonMode('cycle')}
                >
                  完整协作周期
                </button>
                <button
                  type="button"
                  className={comparisonMode === 'turn' ? 'is-active' : ''}
                  onClick={() => setComparisonMode('turn')}
                >
                  单次 A/B 交替
                </button>
              </div>

              <div className={`pb-sequence-demo is-${comparisonMode}`}>
                {comparisonMode === 'cycle' ? (
                  <>
                    <div className="pb-round-label">Round 1</div>
                    <div className="pb-sequence-steps">
                      <span>A · 执行</span><ArrowDown size={16} />
                      <span>B · 补缺</span><ArrowDown size={16} />
                      <span>A · 整合</span><ArrowDown size={16} />
                      <span>B · 审查</span><ArrowDown size={16} />
                      <strong>Decide → PASS / REVISE</strong>
                    </div>
                    <p>轮次结束时，系统拥有一个可以解释和恢复的完整状态。</p>
                  </>
                ) : (
                  <>
                    <div className="pb-round-label">Round 1 → 4</div>
                    <div className="pb-sequence-steps">
                      <span>R1 · A</span><ArrowDown size={16} />
                      <span>R2 · B</span><ArrowDown size={16} />
                      <span>R3 · A</span><ArrowDown size={16} />
                      <span>R4 · B</span>
                    </div>
                    <p>仍然可以严谨，但“轮次”本身只说明轮到谁，完成语义需要额外约定。</p>
                  </>
                )}
              </div>

              <div className="pb-comparison-table" role="table" aria-label="两种编排语义的比较">
                <div className="pb-comparison-row pb-comparison-head" role="row">
                  <span role="columnheader">维度</span>
                  <span role="columnheader">完整协作周期</span>
                  <span role="columnheader">单次 A/B 交替</span>
                </div>
                {comparisonRows.map(([dimension, cycle, turn]) => (
                  <div className="pb-comparison-row" role="row" key={dimension}>
                    <strong role="cell">{dimension}</strong>
                    <span role="cell">{cycle}</span>
                    <span role="cell">{turn}</span>
                  </div>
                ))}
              </div>
            </section>

            <section id="evidence" className="pb-section">
              <p className="pb-kicker">研究与一手工程依据</p>
              <h2>主流 Agent 设计已经反复采用“生成—评价—修订”这一结构</h2>
              <p className="pb-lead">
                下列来源并没有规定“两个 Agent 必须叫同一个 Round”。它们真正支持的是更重要的机制：
                <strong>生成/执行之后接评价，后一步消费前一步的成果，反馈进入下一次决策，并让角色与交付物显式化。</strong>
                ProofBridge 只是把这组机制映射为自己的 Round / Phase 状态机。
              </p>

              <div className="pb-source-list">
                {sources.map((source, index) => (
                  <a className="pb-source-card" href={source.href} target="_blank" rel="noreferrer" key={source.title}>
                    <span className="pb-source-index">{String(index + 1).padStart(2, '0')}</span>
                    <div className="pb-source-main">
                      <div className="pb-source-meta">
                        <span>{source.short}</span>
                        <span>{source.tag}</span>
                      </div>
                      <h3>{source.title}</h3>
                      <p>{source.body}</p>
                      <small>{source.relation}</small>
                    </div>
                    <ArrowUpRight size={18} />
                  </a>
                ))}
              </div>

              <figure className="pb-inline-figure">
                <img
                  src="/blog/orchestration-design/04-evidence-stack.svg"
                  alt="小黑角色把反馈、顺序、交付物、审查和停止条件叠成支撑编排设计的证据台阶"
                />
                <figcaption>研究依据支撑的是机制，不是“Round”这个名字。命名本身不产生质量提升。</figcaption>
              </figure>

              <div className="pb-evidence-note">
                <strong>一个需要特别避免的过度表述</strong>
                <p>
                  不能写成“已有论文证明 A 与 B 放在同一轮一定更优”。目前这些工作支持反馈闭环、
                  顺序依赖、角色分工与结构化交接；ProofBridge 尚未完成针对“完整周期 vs. 简单交替”的
                  控制变量对照实验，因此本文不把任何外部论文的性能数字转移成 ProofBridge 自身收益。
                </p>
              </div>
            </section>

            <section id="boundaries" className="pb-section">
              <p className="pb-kicker">边界与代价</p>
              <h2>完整闭环不是免费的，最好的编排也应该允许自己“少做一步”</h2>
              <p>
                每个额外 Phase 都会增加 token、wall-clock latency、模型调用和上下文交接。
                Reviewer 也不是天然独立：它可能被 A 的方案锚定，也可能与 A 共享相同错误假设。
                因此 ProofBridge 的目标不是“阶段越多越好”，而是让每一次额外调用都有明确职责。
              </p>

              <div className="pb-boundary-grid">
                <div>
                  <span>什么时候值得</span>
                  <h3>耦合强、失败代价高、验收可执行</h3>
                  <p>跨文件修改、复杂依赖、需要测试/证明/检查器确认，或者任务靠近当前模型稳定能力边界。</p>
                </div>
                <div>
                  <span>什么时候可以省</span>
                  <h3>边界清楚、修改很小、单步即可验证</h3>
                  <p>例如一个局部文案修复或已有确定性测试覆盖的极小改动，完整四阶段可能只是额外开销。</p>
                </div>
                <div>
                  <span>如何控制成本</span>
                  <h3>early exit + evidence gate</h3>
                  <p>一旦独立核验已经确认原始任务完成，就结束剩余阶段；不要为了“走满流程”继续调用模型。</p>
                </div>
              </div>
            </section>

            <section id="evaluation" className="pb-section">
              <p className="pb-kicker">接下来真正应该做的实验</p>
              <h2>要证明“值得”，必须比较到同一个验收终点的总成本</h2>
              <p>
                “四轮对四轮”不是公平比较。更严谨的评估应该固定任务集合、仓库起点、工具权限、
                模型配置、时间/费用预算和验收标准，然后比较从相同起点走到相同 acceptance criteria 时，
                两种编排分别付出了什么。
              </p>

              <div className="pb-metric-grid">
                {[
                  ['最终验收', '任务是否真正通过可执行的验收条件'],
                  ['残余缺陷', '通过后仍可复现的问题与遗漏'],
                  ['总费用', '所有模型与核验调用的真实计费汇总'],
                  ['总耗时', '从提交任务到得到稳定 verdict 的 wall-clock time'],
                  ['人工介入', '需要用户纠偏、重启或补充说明的次数'],
                  ['恢复质量', '中断后是否能从正确 Phase 与证据继续'],
                ].map(([title, body]) => (
                  <div key={title}>
                    <strong>{title}</strong>
                    <span>{body}</span>
                  </div>
                ))}
              </div>

              <div className="pb-ablation">
                <div>
                  <RotateCcw size={22} />
                  <span>建议的消融顺序</span>
                </div>
                <p>
                  完整周期 → 去掉 B 补充执行 → 去掉 A 整合 → 去掉最终审查。
                  一次只移除一个职责，才能看出哪个 Phase 真正提供增量价值，而不是把“多调用”本身当成质量证据。
                </p>
              </div>
            </section>

            <section className="pb-closing">
              <p>好的编排，不是让 Agent 说得更多。</p>
              <h2>
                它应该让每一次额外调用都有明确职责，<br />
                让每一个“完成”都有可回查的依据。
              </h2>
              <div className="pb-closing-actions">
                <a href="/orchestrate">
                  打开 ProofBridge <ArrowUpRight size={17} />
                </a>
                <button type="button" onClick={share}>
                  {copied ? <Copy size={17} /> : <Share2 size={17} />}
                  {copied ? '链接已复制' : '分享这篇文章'}
                </button>
              </div>
            </section>

            <footer className="pb-footer">
              <div>
                <strong>参考与说明</strong>
                <p>
                  本文依据 2026 年 9 月 12 日的 ProofBridge 实现与工程设计整理。外部来源用于解释
                  workflow 机制，不把其跨任务性能数字作为 ProofBridge 的效果证明。
                </p>
              </div>
              <div>
                <strong>插图</strong>
                <p>
                  正文插图遵循 Ian Xiaohei Illustrations 的小黑视觉语言重新创作，用于解释概念，
                  不代表真实运行截图或实验结果。
                  {' '}
                  <a href="https://github.com/helloianneo/ian-xiaohei-illustrations" target="_blank" rel="noreferrer">
                    查看原项目 <ArrowUpRight size={13} />
                  </a>
                </p>
              </div>
            </footer>
          </div>
        </div>
      </article>
    </main>
  );
}
