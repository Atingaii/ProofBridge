import React, { useEffect, useState } from 'react';
import { ArrowLeft, ArrowUpRight, Printer } from 'lucide-react';
import './orchestration-design.css';

const sections = [
  ['premise', '为什么一轮要有多个阶段'], ['workflow', '从任务到证据'], ['comparison', '两种编排的取舍'],
  ['workspace', '交接与恢复'], ['accounting', '成本如何比较'], ['origins', '设计依据与来源'],
  ['decision', '如何选择与验证'], ['references', '参考与说明'],
];
const stages = [
  { who: 'A', name: '执行', detail: '交付第一版实现与检查记录', input: '共同目标：导出的数据必须符合当前筛选条件。', work: 'A 完成筛选参数到导出接口的传递，并检查常规筛选。', handoff: '交给 B：具体改动、已通过的检查，以及尚未覆盖的空结果与组合筛选。', status: '已有候选实现，验收证据仍不完整。' },
  { who: 'B', name: '补充执行', detail: '沿着证据补缺，挑战遗漏', input: '接手 A 的实际文件与检查记录。', work: 'B 发现组合筛选遗漏一个条件，补上实现与回归用例。', handoff: '交给 A：新发现的缺口、补丁、回归结果，以及需要检查的一致性问题。', status: '已补充边界情况，仍需整合检查。' },
  { who: 'A', name: '整合', detail: '统一最终文件、接口与测试', input: '检查双方修改和证据之间是否一致。', work: 'A 统一筛选参数的处理方式，确认列表与导出逻辑一致，并运行集成检查。', handoff: '交给 B：最终工作区状态、检查命令与结果、剩余风险。', status: '形成整合后的结果，进入最终审查。' },
  { who: 'B', name: '审查', detail: '用原始要求检查整合后的结果', input: '依据原始需求审查最终工作区，而非只看 A 的总结。', work: 'B 复跑常规、组合筛选与空结果用例，核对导出内容是否满足要求。', handoff: '交付结论：已解决、受阻，或带着具体缺口进入下一轮；适用时再接受独立核验。', status: '依据证据判断是否完成；走完阶段本身不等于验收通过。' },
];
const comparisons = [
  ['职责与反馈', '把实现、整合、审查显式写入同一个周期，交接责任更清楚。', '每次只安排一位参与者，链条短；是否包含审查取决于提示词和后续安排。'],
  ['结果质量', '增加发现遗漏和反例的机会，但也可能重复劳动、相互附和或过度修改。', '适合边界清楚的工作；同样可以设置严谨的审查，只是需要显式安排。'],
  ['成本与延迟', '一个完整周期包含更多阶段，规划、上下文交接和复查都有开销。', '单次调度通常更轻，较早得到一个阶段结果；返工可能抵消节省。'],
  ['上下文管理', '按角色交付目标、变更与证据，便于定位责任；摘要可能遗漏细节。', '交替对话直观；若不断重传全文，上下文和缓存读取仍可能增长。'],
  ['共享目录', '当前实现让写入按依赖顺序发生，避免 A、B 同时改同一份文件。', '顺序交替也能避免写入重叠；这不是完整周期独有的能力。'],
  ['状态与恢复', 'ProofBridge 用持久化任务、依赖和执行尝试记录进度。', '交替模式也能采用相同机制；可靠性取决于状态设计，不取决于轮次名称。'],
  ['控制粒度', '以完整协作周期限制轮数，适合观察一次实现到审查的推进。', '以单次参与者任务限制轮数，便于细粒度人工介入，但轮数不能直接横向比较。'],
];
function Illustration({ file, alt, caption, eager = false }: { file: string; alt: string; caption: string; eager?: boolean }) {
  return <figure className="od-figure"><img src={`/article-assets/orchestration-design/${file}.png`} alt={alt} width="1536" height="864" loading={eager ? 'eager' : 'lazy'} decoding="async" /><figcaption>{caption}</figcaption></figure>;
}
function SectionTitle({ index, children }: { index: string; children: React.ReactNode }) {
  return <div className="od-section-title"><span>{index}</span><h2>{children}</h2></div>;
}
function HandoffDemo() {
  const [step, setStep] = useState(0);
  const stage = stages[step];
  return <div className="od-demo" data-testid="handoff-demo">
    <div className="od-demo-head"><div><span className="od-eyebrow">交互示意 · 同一个协作轮次</span><h3>一个筛选导出任务，如何交到下一阶段</h3></div></div>
    <p className="od-demo-caption">点击阶段，查看这一棒接收什么、交付什么，以及为什么还需要下一棒。</p>
    <ol className="od-stages">{stages.map((item, i) => <li key={item.name}><button type="button" aria-pressed={step === i} aria-controls="handoff-detail" onClick={() => setStep(i)}><span className="od-stage-top"><span className={`od-person od-person-${item.who.toLowerCase()}`}>{item.who}</span><span>阶段 {i + 1}</span></span><strong>{item.name}</strong><span className="od-stage-description">{item.detail}</span></button></li>)}</ol>
    <div id="handoff-detail" className="od-handoff-detail" aria-live="polite" aria-atomic="true"><h4>{stage.who} · {stage.name}</h4><p>{stage.input}</p><p>{stage.work}</p><p><strong>本阶段交接：</strong>{stage.handoff}</p><p className="od-handoff-status">{stage.status}</p></div>
    <div className="od-demo-result"><span>一个目标 → 四段职责 → 一份有依据的结论</span><button type="button" onClick={() => setStep((step + 1) % stages.length)}>{step === stages.length - 1 ? '重新查看' : '下一阶段'} →</button></div>
    <p className="od-small">这是解释职责的假设示例，不是真实运行记录。规划准备见下文；提前完成条件满足时，实际执行可以结束剩余阶段。</p>
  </div>;
}
function useActiveSection() {
  const [activeSection, setActiveSection] = useState(sections[0][0]);
  useEffect(() => {
    const targets = sections.map(([id]) => document.getElementById(id)).filter((element): element is HTMLElement => Boolean(element));
    let frame = 0;
    const update = () => {
      frame = 0;
      const readingLine = Math.min(160, window.innerHeight * 0.2);
      let current = targets[0];
      for (const target of targets) {
        // The section's top padding is whitespace; activate when its heading arrives.
        const heading = target.querySelector('h2') || target;
        if (heading.getBoundingClientRect().top <= readingLine) current = target;
      }
      if (window.scrollY > 0 && window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 2) current = targets[targets.length - 1];
      if (current) setActiveSection(current.id);
    };
    const schedule = () => { if (!frame) frame = window.requestAnimationFrame(update); };
    window.addEventListener('scroll', schedule, { passive: true });
    window.addEventListener('resize', schedule);
    window.addEventListener('hashchange', schedule);
    window.addEventListener('pageshow', schedule);
    const observer = new ResizeObserver(schedule);
    const article = document.querySelector('.od-main');
    if (article) observer.observe(article);
    // A lazy route can mount after the browser's initial fragment navigation.
    const initialTarget = targets.find(target => `#${target.id}` === window.location.hash);
    if (initialTarget) {
      const margin = parseFloat(getComputedStyle(initialTarget).scrollMarginTop) || 0;
      window.scrollTo({ top: window.scrollY + initialTarget.getBoundingClientRect().top - margin, behavior: 'instant' });
    }
    update();
    return () => {
      window.cancelAnimationFrame(frame);
      observer.disconnect();
      window.removeEventListener('scroll', schedule);
      window.removeEventListener('resize', schedule);
      window.removeEventListener('hashchange', schedule);
      window.removeEventListener('pageshow', schedule);
    };
  }, []);
  return activeSection;
}
export function OrchestrationDesignPage() {
  const activeSection = useActiveSection();
  useEffect(() => {
    const oldTitle = document.title;
    document.title = '一轮协作，如何走到可验证的结果 · ProofBridge';
    return () => { document.title = oldTitle; };
  }, []);
  return <div className="od-page">
    <a className="od-skip" href="#article">跳到正文</a>
    <header className="od-header"><a href="/" className="od-brand"><img src="/icon.svg" width="30" height="30" alt="ProofBridge" /><span>ProofBridge <em>工程手记</em></span></a><nav aria-label="文章导航"><a href="/help">使用指南 <ArrowUpRight size={14} /></a><button type="button" onClick={() => window.print()}><Printer size={15} />打印</button><a className="od-back" href="/"><ArrowLeft size={14} />返回应用</a></nav></header>
    <main id="article" className="od-main">
      <div className="od-hero">
        <div className="od-publication"><time dateTime="2026-09-11">2026 年 9 月 11 日</time><span>工程设计</span></div>
        <h1>一轮协作，如何走到<br /><span>可验证的结果</span></h1>
        <p className="od-deck">为什么让 A 与 B 在同一轮里分阶段工作？让执行、补缺、整合与审查围绕同一个目标，交付一份有依据的结论。</p>
        <div className="od-meta"><span>ProofBridge 工程手记</span><span>约 15 分钟</span></div>
        <Illustration file="01-collaboration" alt="两只小黑共同处理一本折叠任务册，一只缝合交接处，另一只用放大镜复核接缝。" caption="协作的价值，来自前后相接的工作与可以复查的证据。" eager />
      </div>
      <div className="od-layout">
        <aside className="od-toc"><details open><summary>本文目录</summary><nav aria-label="本文目录">{sections.map(([id, title], i) => <a key={id} href={`#${id}`} aria-current={activeSection === id ? 'location' : undefined}><span>{String(i + 1).padStart(2, '0')}</span>{title}</a>)}</nav></details><p>先比较实际工作，<br />再比较轮次与费用。</p></aside>
        <article className="od-body">
          <div className="od-lead"><p>为什么要把 A 和 B 的多次工作放在同一轮？直接让 A 做第 1 轮、B 做第 2 轮，再这样交替下去，看起来更简单。这个问题的关键是：系统究竟只安排了“下一位谁来做”，还是同时安排了“这一轮怎样走到可检查的结果”。</p><p>ProofBridge 选择后者：围绕同一项任务，预先安排执行、补充执行、整合与审查，并要求各阶段交付证据。它让用户交出的一个目标，在默认流程里就有人推进、有人补缺、有人整合、有人复查，减少用户逐棒追问和安排下一步的负担。</p></div>
          <section id="premise"><SectionTitle index="01">为什么把 A、B 的多个阶段放进同一轮</SectionTitle>
            <p>简单交替回答“下一次由 A 还是 B 执行”。完整协作周期还回答“当前缺少哪一种工作，做完后应交给谁检查”。A 与 B 仍按顺序工作，但每次接手的职责会改变：A 先实现，B 补缺，A 整合，B 再审查整合后的成果。</p>
            <h3>把检查纳入默认流程，减少漏掉收尾工作</h3><p>A 完成一次实现，只说明有了候选成果；B 接着修改，也只说明成果又向前推进了一步。把它们分别称为两轮，并不会自动安排最终整合和复查。分阶段设计预先给这些工作留出位置，让“代码写好了”之后还有明确的检查责任。</p>
            <h3>让接力有交付物，让分歧有整合人</h3><p>B 接手的是 A 的文件、命令结果和未解决问题，可以从现有成果继续补缺；A 随后承担整合职责，把双方的修改统一到最终工作区。这样可以减少重复摸索，也降低双方各改一部分、最后没人检查整体行为的风险。</p>
            <h3>用共同验收条件组织进度与下一步</h3><p>同一轮把“已经实现”“仍需补缺”“等待审查”串成一条可追踪的进度。审查依据原始要求给出已解决、受阻或需要下一轮的结论；继续时承接具体缺口，用户也更容易判断结果离目标还有多远。配置的协作轮数因此代表推进与检查的周期预算，不能直接当作单次发言次数。</p>
            <p>这种组织方式有明确的设计依据。Anthropic 的 <a href="https://www.anthropic.com/engineering/building-effective-agents" target="_blank" rel="noopener noreferrer">Evaluator–Optimizer</a> 把生成与评价接成反馈循环；Microsoft 的 <a href="https://microsoft.github.io/autogen/dev/user-guide/core-user-guide/design-patterns/reflection.html" target="_blank" rel="noopener noreferrer">AutoGen Reflection</a> 让 Coder 根据 Reviewer 的意见继续修改，直到批准或满足停止条件。这些模式为把“推进成果、检查成果、决定后续”视为一个协作周期提供了参照；具体的阶段划分则由项目约束决定。</p>
            <HandoffDemo />
            <p>当然，也可以把这四个阶段叫作第 1、2、3、4 轮。如果同时保留相同的职责、依赖、交接证据和验收条件，两种写法可以实现同一套流程。<strong>当前设计的实际优点，是把这些责任与检查编进默认工作流，让用户无需每次重新组织一遍。</strong>轮次分组让这个工作单元更容易理解，单独换编号则不会改变结果或费用。</p>
            <div className="od-note"><strong>参与者名称 ≠ 实际模型</strong><p>A、B 是工作槽位；Codex、Claude Code 是运行 CLI；实际模型由该次调用的配置和返回记录决定。两个 Codex 参与者可以使用不同模型，也可以使用同一个模型。角色分工同样不要求模型必须不同。</p></div>
          </section>
          <section id="workflow"><SectionTitle index="02">从任务到证据：每个阶段交付什么</SectionTitle>
            <p>当前实现以固定任务依赖组织工作。开启规划时，一个新用户任务的首轮包含规划和计划审查，随后进入候选执行、整合与审查。后续内部轮次复用已审查的计划，继续四个执行阶段，避免每轮重新做完整规划。</p>
            <div className="od-flow" aria-label="当前编排顺序"><div><span>新任务的前置准备</span><strong>A 规划 <b>→</b> B 计划审查</strong></div><div><span>每个协作执行周期</span><strong>A 执行 <b>→</b> B 执行 <b>→</b> A 整合 <b>→</b> B 审查</strong></div></div>
            <p>一轮是组织工作与检查的单元，走完所有阶段不代表任务已经通过验收。需要继续时，下一轮承接审查指出的缺口；若独立提前完成核验已确认原始任务完成，系统也可以结束剩余阶段。</p>
            <p className="od-small">版本边界：早期运行可能每轮都保存了六个阶段。历史统计应如实呈现当时的实际调用，不能用今天的调度规则改写旧账单。条件满足时还可能有额外的独立核验，其用量也应计入。</p>
            <div className="od-formula"><span>本文的工程抽象 · 并非论文原式或协议字段</span><strong>R<sub>t</sub> = ⟨E<sub>t</sub>, V<sub>t</sub>, D<sub>t</sub>⟩</strong><p><strong>E · 推进成果：</strong>A 执行、B 补充执行、A 整合。<br /><strong>V · 检查成果：</strong>B 审查，以及条件适用时的独立核验。<br /><strong>D · 决定后续：</strong>调度器结合证据、运行状态和预算，决定结束、承接缺口继续，或保留受阻状态。</p><p>这个抽象突出一轮需要回答的三个问题。实际核验也可能在中途触发提前完成；阶段耗尽或预算用完，都不能等同于验收通过。</p></div>
            <h3>规划让目标变得可检查</h3><p>规划阶段需要明确验收条件、依赖关系和可执行的工作顺序。计划审查检查遗漏与不成立的前提。两者的价值在于降低方向性返工；对于一个已经十分明确的小修改，这部分价值可能小于它的开销。</p>
            <h3>执行与整合推动真实修改</h3><p>A 先推进一条可行路径，B 从实际工作区与已有证据出发补足缺口，必要时挑战原方案。整合阶段负责让最终文件、接口和测试保持一致。交接需要指出改了什么、运行了什么、失败在哪里，以及下一步能直接做什么。</p>
            <h3>审查回到原始验收条件</h3><p>审查者应重新检查需求并运行相关验证，不能只复述上一位参与者的结论。角色上的分离也不等于统计意义上的独立：B 看过 A 的方案和证据，仍可能受到锚定影响；两个不同模型也可能共享相同的错误假设。</p>
          </section>
          <section id="comparison"><SectionTitle index="03">完整周期与单次交替，各自付出什么</SectionTitle>
            <p>分阶段协作把复杂任务中容易遗漏的责任预先安排好，以额外阶段换取更清楚的交付过程。下面比较这一默认流程与只规定 A/B 轮流接手的简单流程；为简单交替补上同样的职责约束后，两者的差异会缩小。</p>
            <div className="od-table-scroll" tabIndex={0} role="region" aria-label="两种编排方式的优缺点"><table><caption>比较实际机制，而不是比较谁的轮次更多</caption><thead><tr><th>维度</th><th>完整协作周期</th><th>单次 A/B 交替</th></tr></thead><tbody>{comparisons.map(row => <tr key={row[0]}>{row.map((cell, i) => i ? <td key={i}>{cell}</td> : <th key={i} scope="row">{cell}</th>)}</tr>)}</tbody></table></div>
            <Illustration file="03-review-tradeoff" alt="一只小黑扶住手工椅子，另一只认真测量歪斜的椅腿，旁边的小发条钟表示检查也需要时间。" caption="多一次检查，可能发现偏差；也一定需要投入注意力与执行时间。" />
            <p>完整周期适合职责较多、修改耦合较强、验收成本较高的工作。简单交替适合范围较小、反馈频繁或需要人逐步控制方向的工作。这是工程上的选型判断，不是经过本项目对照实验验证的性能排名。</p>
          </section>
          <section id="workspace"><SectionTitle index="04">交接与恢复，比“同时说话”更重要</SectionTitle>
            <p>ProofBridge 的参与者在用户选择的同一项目目录中工作。当前依赖顺序要求 B 等待 A，整合等待候选工作结束，审查再等待整合结束。这样，后一个阶段看到的是已经落地的文件与命令结果。</p>
            <Illustration file="02-shared-workspace" alt="两只小黑围绕同一张工作台交接一把巨大的钥匙，表达同一时刻只有一个阶段获得写入机会。" caption="共享一份可写工作区时，顺序交接让文件状态更容易解释。" />
            <p>这种设计放弃了候选阶段并行执行可能带来的时间收益，换取直接操作同一工作区的可理解性。隔离目录并行执行是另一种可行设计，但还需要处理修改合并、环境一致性与额外资源占用，不能只打开并发开关。</p>
            <h3>交接传递证据，状态记录负责恢复</h3><p>交接内容应保留目标、关键修改、检查结果、阻塞原因和下一个动作。压缩上下文可以减少重复输入，但压缩过度会丢失细节，因此需要保留可回查的文件与命令线索。</p>
            <p>Hub 把任务依赖、调度状态和执行尝试写入 SQLite，Bridge 负责私有机器上的实际执行。如果断线后无法确认远端是否执行过某项工作，系统需要保留这种不确定性并进行核对。持久化调度记录有助于避免盲目重放，却不能保证任意外部命令的副作用只发生一次。</p>
            <p>阶段边界也给恢复提供了明确的定位：若 B 在审查时中断，系统能够区分已经完成的执行与整合，以及尚未确认的审查尝试。这些信息帮助判断接下来应核对什么。可靠恢复依赖持久化状态和证据，简单交替也可以引入同样的机制。</p>
          </section>
          <section id="accounting"><SectionTitle index="05">费用比较，要沿着真实调用计算</SectionTitle>
            <p>“四轮对四轮”不是公平的成本比较。如果一种方式每轮包含多个阶段，另一种方式每轮只有一个阶段，它们完成的工作量并不相同。更有意义的比较是：从相同任务起点出发，达到相同验收条件，分别花费多少费用和时间。</p>
            <Illustration file="04-accounting" alt="一只小黑把长条收据折成一本，另一只展开同一条收据逐项核对，表示改变分组不会改变实际调用。" caption="收据折成一册或逐项展开，计费的仍然是原来的工作。" />
            <div className="od-formula"><span>聚合口径</span><strong>某模型总费用 = Σ 该模型各次调用的记录费用</strong><p>先按调用确认实际模型、用量与费用，再按轮次或模型汇总。存在阶梯价格、缓存价格或服务商直接报价时，不应把合并后的 Token 简单乘以一个统一单价。</p></div>
            <p>统计中的输入、输出和缓存需要采用一致口径。例如，某些原生计量把缓存读取包含在输入中，需要先归一化；推理 Token 若已计入输出，也不能再额外相加。缓存读取和写入应分别正确计价，再汇总展示。</p>
            <p>同一模型在一轮中由 A、B 或核验阶段使用，可以合并到同一模型行；跨轮次继续求和，就得到模型累计用量。模型未能归属或费用不完整时，应保留对应的未知状态，而不是显示为免费。历史费用的完整性和模型识别是证据问题，不能通过换一种图表解决。</p>
          </section>
          <section id="origins"><SectionTitle index="06">设计依据：反馈闭环与明确的阶段职责</SectionTitle>
            <p>下面五个一手来源分别解释：为什么要把反馈接回执行，为什么后一步应消费前一步的成果，以及为什么交接需要明确的输出。它们共同支持本文的闭环设计思路；没有哪个来源要求所有系统都把 A、B 命名为同一 Round。</p>
            <h3>Anthropic：生成之后，还需要评价与反馈</h3>
            <p><a href="https://www.anthropic.com/engineering/building-effective-agents" target="_blank" rel="noopener noreferrer">Building Effective Agents 的 Evaluator–Optimizer 小节</a>描述一个调用生成结果，另一个调用评价并提供反馈，再循环改进。它特别指出适用前提：评价标准清楚，迭代改进有实际价值。对应到 ProofBridge，执行阶段产出候选成果，审查反馈为是否继续、下一步改什么提供依据。这支持把工作和检查一起组织；额外调用是否划算，仍取决于任务。</p>
            <h3>Google ADK：后一个阶段消费前一个阶段的结果</h3>
            <p><a href="https://adk.dev/agents/workflow-agents/sequential-agents/" target="_blank" rel="noopener noreferrer">Sequential agents 的 Code Development Pipeline 示例</a>依次安排 Code Writer、Code Reviewer、Code Refactorer。子 Agent 共享同一个 InvocationContext，前一阶段通过会话状态提供结果，后一阶段据此工作。它支持的是依赖顺序与信息交接；单独的顺序流水线并不自动构成反复迭代，也不保证验收通过。</p>
            <p>ProofBridge 同样要求后续阶段承接已有成果，但采用的是 A 执行、B 补充执行、A 整合、B 最终审查。保留最后一次审查，是为了检查整合后的实际工作区。这个四阶段顺序来自本项目的职责安排，并非照搬 ADK 示例。</p>
            <h3>Microsoft AutoGen：把审查结果接入下一步决策</h3>
            <p><a href="https://microsoft.github.io/autogen/dev/user-guide/core-user-guide/design-patterns/reflection.html" target="_blank" rel="noopener noreferrer">Reflection 设计模式</a>给出 Coder 与 Reviewer 的消息交互，审查结果包含 review 和 approved。未批准时，Coder 利用反馈继续修改；批准或达到停止条件时结束。这直接支持“执行—审查—决定后续”的闭环。ProofBridge 进一步要求审查回到文件与检查记录：模型的批准意见不能替代可执行的验收证据。</p>
            <h3>Self-Refine：角色可以变化，模型不必变化</h3>
            <p>Madaan 等人的 <a href="https://proceedings.neurips.cc/paper_files/paper/2023/file/91edff07232fb1b55a505a9e9f6c0ff3-Paper-Conference.pdf#page=3" target="_blank" rel="noopener noreferrer">Self-Refine（NeurIPS 2023，第 2 节与算法 1）</a>先生成初始结果，再反复获取反馈并改进；同一个模型使用不同提示承担这些职责。其循环具体是“反馈—改进”，并非规定“A 生成、B 审查”的轮次命名。我们从中采用的工程理解是：迭代边界可以围绕反馈如何推动成果变化来定义，无需绑定到发言者身份。</p>
            <h3>MetaGPT：让角色分工落到标准化交付物</h3>
            <p>Hong 等人的 <a href="https://arxiv.org/html/2308.00352v7#S3" target="_blank" rel="noopener noreferrer">MetaGPT（ICLR 2024；作者稿第 3.1–3.3 节）</a>用 SOP 组织角色，以需求文档、设计与接口等结构化成果交接，并用执行反馈推动代码改进。这为明确责任、检查中间产物提供了设计参照。对应到 ProofBridge，阶段交接要说明文件改动、命令结果与剩余缺口；整合者还要检查整体一致性。MetaGPT 的团队角色与流程不同，不能把它的实验结果直接当成本项目的收益。</p>
            <div className="od-note"><strong>从来源到本项目的结论</strong><p>这些工作为反馈循环、顺序依赖、明确交付物提供了依据。把它们落实为一轮内的职责与检查，能让用户围绕一个目标追踪进度、证据和下一步。本文把这个工作单元称为 Round；它的价值来自所包含的工作约束，命名本身不产生质量提升。</p></div>
            <h3>设计依据与项目沿革</h3>
            <p>项目早期策略文档记录的参考是 AutoGen、LangChain / LangGraph、CrewAI 和 OpenAI Swarm。上面补充的论文与官方文档，是用于解释和审视当前设计的依据，不能倒推成项目最初采用它们的历史证据。固定任务顺序、共享目录、持久化恢复与 Hub/Bridge 分工，仍是围绕私有机器上的 CLI 协作所做的具体选择。</p>
          </section>
          <section id="decision"><SectionTitle index="07">如何选择，也如何检验选择</SectionTitle>
            <p>选择编排方式时，可以先问：当前任务最昂贵的失败是什么？如果是方向错误，需要加强目标与计划审查；如果是实现遗漏，需要明确独立检查；如果主要瓶颈是延迟和预算，就应检查每个额外阶段是否带来了足够价值。</p>
            <div className="od-decision"><h3>让每个阶段回答一个具体问题</h3><ul><li><strong>规划：</strong>是否减少了需求误解，还是重复已有的清晰说明？</li><li><strong>B 的执行：</strong>是否补充了证据或新路径，还是重复扫描同一目录？</li><li><strong>整合：</strong>是否解决了真实的耦合与冲突，还是只有文字总结？</li><li><strong>审查：</strong>是否重跑验证并发现问题，还是接受了上一位的结论？</li></ul></div>
            <p>若要验证完整周期是否值得，应固定任务集合、仓库起点、可用工具、模型配置和验收标准，在一致的预算或时间限制下比较。记录最终验收通过情况、未解决缺陷、总费用、总耗时以及人工介入次数；对有随机性的任务进行多次运行。</p>
            <p>移除某个阶段后进行对比，可以检验它是否提供增量价值。这是后续评估方法的建议，本文没有报告此类实验结果，也没有把检查次数当成质量提升的证据。</p>
            <blockquote>好的编排，应当让每一次额外调用都有明确职责，让每一个完成结论都有可回查的依据。</blockquote>
          </section>
          <section id="references"><SectionTitle index="08">参考与说明</SectionTitle>
            <p>本文依据 2026 年 9 月 11 日的项目实现与设计记录撰写。流程说明描述实现机制；优缺点与选型建议属于工程分析。未使用私人对话内容，也未把单次运行的消耗当作模型性能评测。</p>
            <p>以下链接分别指向官方文档、会议论文或作者稿；核验日期为 2026 年 9 月 11 日。本文引用其流程机制，不引用跨任务的性能数字作为 ProofBridge 的效果证明。</p>
            <ol className="od-sources">
              <li><a href="https://www.anthropic.com/engineering/building-effective-agents" target="_blank" rel="noopener noreferrer">Anthropic · Building Effective Agents</a>（2024），Evaluator–Optimizer 小节：生成、评价与反馈循环。</li>
              <li><a href="https://adk.dev/agents/workflow-agents/sequential-agents/" target="_blank" rel="noopener noreferrer">Google ADK · Sequential agents</a>，Code Development Pipeline 示例：顺序执行与共享上下文。</li>
              <li><a href="https://microsoft.github.io/autogen/dev/user-guide/core-user-guide/design-patterns/reflection.html" target="_blank" rel="noopener noreferrer">Microsoft AutoGen · Reflection</a>，CodeReviewResult 与 CoderAgent 示例：审查反馈和终止条件。</li>
              <li><a href="https://proceedings.neurips.cc/paper_files/paper/2023/hash/91edff07232fb1b55a505a9e9f6c0ff3-Abstract-Conference.html" target="_blank" rel="noopener noreferrer">Madaan et al. · Self-Refine: Iterative Refinement with Self-Feedback</a>，NeurIPS 2023，第 2 节与算法 1：初始生成后的反馈—改进循环。</li>
              <li><a href="https://proceedings.iclr.cc/paper_files/paper/2024/hash/6507b115562bb0a305f1958ccc87355a-Abstract-Conference.html" target="_blank" rel="noopener noreferrer">Hong et al. · MetaGPT: Meta Programming for a Multi-Agent Collaborative Framework</a>，ICLR 2024；正文机制对照<a href="https://arxiv.org/html/2308.00352v7#S3" target="_blank" rel="noopener noreferrer">作者稿第 3 节</a>：SOP、结构化交接与执行反馈。</li>
            </ol>
            <details className="od-implementation"><summary>项目实现依据</summary><p>任务图：<code>internal/hub/task_graph.go:orchestrationTaskSpecs</code>；持久化调度：<code>internal/store/task_graph.go:ClaimReadyTask</code>；统计聚合：<code>frontend/src/app/lib/roundModelUsage.ts</code>。</p><p>设计沿革参见项目文档：ADR 008、Durable Bounded Orchestration Task Graph、Orchestration Plan And Progress Workspace，以及已标记为历史文档的 Orchestration Strategy Optimization。历史设计与当前代码不一致时，本文的当前行为说明以实现为准。</p></details>
            <p className="od-credit">正文插画由 AI 生成，使用 Ian 创作的 <a href="https://github.com/helloianneo/ian-xiaohei-illustrations" target="_blank" rel="noopener noreferrer">Ian Xiaohei Illustrations</a> skill。“小黑”视觉语言归属原作者，配图用于解释概念，不代表真实运行截图或实验结果。</p>
          </section>
        </article>
      </div>
      <footer className="od-footer"><a href="/" className="od-brand"><img src="/icon.svg" width="26" height="26" alt="" /><span>ProofBridge</span></a><span>让协作可理解，让结果可复查。</span><a href="#article">回到开头 ↑</a></footer>
    </main>
  </div>;
}
