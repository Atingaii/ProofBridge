import type { Language, OrchestrationRun } from '../lib/types';

export type OrchestrationExecutionConfiguration = {
  schemaVersion?: number;
  workerPair?: string;
  mode?: string;
  firstCli?: string;
  profile?: string;
  verificationMode?: string;
  cwd?: string;
  maxTurns?: number;
  workers?: Record<string, {
    cli?: string;
    nativeDefault?: boolean;
    presetId?: string;
    name?: string;
    officialLogin?: boolean;
    model?: string;
    reasoningEffort?: string;
  }>;
};

export function ExecutionConfigurationCard({ run, configuration, language }: {
  run: OrchestrationRun;
  configuration?: OrchestrationExecutionConfiguration | null;
  language: Language;
}) {
  const zh = language === 'zh';
  const pair = configuration?.workerPair || run.workerPair || 'claude-codex';
  const slots = pair === 'codex-codex' ? ['codex-a', 'codex-b']
    : pair === 'claude-claude' ? ['claude-a', 'claude-b'] : ['claude', 'codex'];
  const nativeDefault = zh ? '原生 CLI 默认（未固定）' : 'Native CLI default (not pinned)';
  const unknown = zh ? '未记录' : 'Not recorded';
  const title = zh ? '本次运行配置' : 'Accepted run configuration';
  return <section aria-label={title} className="space-y-2 rounded-lg border border-border bg-muted/30 p-3 text-xs">
    <h3 className="font-semibold text-foreground">{title}</h3>
    <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1 text-muted-foreground">
      <dt>{zh ? '工作器' : 'Workers'}</dt><dd className="break-words text-foreground">{pair}</dd>
      <dt>{zh ? '配置' : 'Profile'}</dt><dd>{configuration?.profile || run.profile || 'default'}</dd>
      <dt>{zh ? '首轮' : 'First CLI'}</dt><dd>{configuration?.firstCli || run.firstCli || unknown}</dd>
      <dt>{zh ? '工作目录' : 'Workspace'}</dt><dd className="break-all">{configuration?.cwd || run.cwd || unknown}</dd>
    </dl>
    {slots.map(slot => {
      const worker = configuration?.workers?.[slot];
      const defaultEffort = worker?.nativeDefault || worker?.officialLogin ? nativeDefault
        : zh ? '预设默认（未单独指定）' : 'Preset default (not separately specified)';
      return <div key={slot} className="space-y-1 rounded-md border border-border/70 bg-background/50 p-2" data-worker-slot={slot}>
        <div className="font-medium">{slot}</div>
        <div className="break-words">{zh ? '模型：' : 'Model: '}{worker?.model || (worker?.nativeDefault || worker?.officialLogin ? nativeDefault : unknown)}</div>
        <div className="break-words">{zh ? '思考强度：' : 'Reasoning effort: '}{worker?.reasoningEffort || (worker ? defaultEffort : unknown)}</div>
        {worker?.name && <div className="break-words text-muted-foreground">{zh ? '预设：' : 'Preset: '}{worker.name}</div>}
      </div>;
    })}
    {!configuration && <p className="text-muted-foreground">{zh ? '该历史运行没有可展示的提交快照，无法据此确定实际模型与思考强度。' : 'This historical run has no displayable submission snapshot; its model and effort are unknown.'}</p>}
    <p className="text-muted-foreground">{zh ? '以上是本次已接受的配置。下方表单用于后续提交，不代表当前任务的模型快照。' : 'These are the accepted parameters. The form below applies to later submissions, not the current model snapshot.'}</p>
  </section>;
}
