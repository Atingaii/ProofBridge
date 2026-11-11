import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { ArrowLeft, BarChart3, Clock3, Coins, Database, RefreshCw, Ticket, Layers3, Globe2, Monitor, Route, Activity } from 'lucide-react';
import { Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import { RoundModelUsageCharts } from '../components/RoundModelUsageCharts';
import { api } from '../lib/api';
import { beginStatsRequest, StatsTimeoutError } from '../lib/statsRequest';
import { useStatsRequest } from '../lib/useStatsRequest';
import { useUsageOverview } from '../lib/useUsageOverview';
import type { OrchestrationRun, OrchestrationRunStats, OrchestrationUsageOverview } from '../lib/types';
import type { UIText } from '../lib/i18n';
import { Button } from '../components/ui';
import { formatDuration, formatTime } from '../lib/utils';

type OverviewMetric = 'totalTokens' | 'inputTokens' | 'outputTokens' | 'cacheTokens' | 'estimatedCostUsd';
type OverviewRange = 7 | 30 | 90 | 0;

function statsRequestText(t: UIText) {
  return t.task === '任务'
    ? { timeout: '统计请求超时，请重试。', retry: '重试', loading: '正在加载统计…' }
    : { timeout: 'Statistics request timed out. Please retry.', retry: 'Retry', loading: 'Loading statistics…' };
}

function StatsError({ error, retry, label }: { error: string; retry: () => void; label: string }) {
  return error ? <div role="alert" className="flex items-center justify-between gap-3 rounded-md border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive"><span>{error}</span><Button variant="secondary" size="sm" onClick={retry}>{label}</Button></div> : null;
}


function finiteNumber(value: unknown) {
  const parsed = typeof value === 'number' ? value : Number(value);
  return Number.isFinite(parsed) ? parsed : 0;
}

function values<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : [];
}

function formatCompact(value: unknown, digits = 1) {
  value = finiteNumber(value);
  const absolute = Math.abs(value);
  const units = [{ value: 1e12, suffix: 'T' }, { value: 1e9, suffix: 'B' }, { value: 1e6, suffix: 'M' }, { value: 1e3, suffix: 'K' }];
  const unit = units.find((item) => absolute >= item.value);
  if (!unit) return value.toLocaleString();
  return `${(value / unit.value).toFixed(absolute >= unit.value * 100 ? 0 : digits).replace(/\.0$/, '')}${unit.suffix}`;
}

function exactMetric(value: unknown, metric: OverviewMetric) {
  value = finiteNumber(value);
  return metric === 'estimatedCostUsd' ? `$${value.toFixed(6)}` : value.toLocaleString();
}

function AxisTooltip({ active, payload, label, metric, t }: any) {
  if (!active || !payload?.length) return null;
  return <div className="rounded-md border border-border bg-popover px-3 py-2 text-xs shadow-sm"><div className="mb-1 text-muted-foreground">{label}</div>{payload.map((item: any) => <div key={item.dataKey} className="flex min-w-44 items-center justify-between gap-5 py-0.5"><span style={{ color: item.color }}>{item.name}</span><span className="font-medium tabular-nums">{exactMetric(item.value, metric)}</span></div>)}{metric === 'estimatedCostUsd' && payload[0]?.payload?.costKnown === false ? <div className="mt-1 border-t border-border pt-1 text-muted-foreground">{t.knownCostOnly}</div> : null}</div>;
}

function UsageOverviewPage({ t, navigate }: { t: UIText; navigate: (path: string) => void }) {
  const [range, setRange] = useState<OverviewRange>(30);
  const [metric, setMetric] = useState<OverviewMetric>('totalTokens');
  const messages = statsRequestText(t);
  const { overview, scannedRuns, loading, complete, error, retry, reload: load } = useUsageOverview(range, t.failedLoadOrchestration, messages.timeout);
  const zh = t.task === '任务';
  const metricOptions: { value: OverviewMetric; label: string; color: string }[] = [
    { value: 'totalTokens', label: t.totalTokens, color: '#a78bfa' },
    { value: 'inputTokens', label: t.inputTokens, color: '#38bdf8' },
    { value: 'outputTokens', label: t.outputTokens, color: '#34d399' },
    { value: 'cacheTokens', label: t.cacheTokens, color: '#f59e0b' },
    { value: 'estimatedCostUsd', label: t.costMetric, color: '#f472b6' },
  ];
  const selectedMetric = metricOptions.find((item) => item.value === metric) || metricOptions[0];
  const machineGroups = useMemo(() => {
    const groups = new Map<string, { id: string; name: string; hostname?: string; items: OrchestrationUsageOverview['items'] }>();
    for (const item of values(overview?.items)) {
      const group = groups.get(item.machineId) || { id: item.machineId, name: item.machineName, hostname: item.hostname, items: [] };
      group.items.push(item); groups.set(item.machineId, group);
    }
    return Array.from(groups.values());
  }, [overview]);
  const trend = values(overview?.trend);
  const ranges: { value: OverviewRange; label: string }[] = [{ value: 7, label: t.last7Days }, { value: 30, label: t.last30Days }, { value: 90, label: t.last90Days }, { value: 0, label: t.allTime }];
  return <div className="phone-page min-h-screen bg-background text-foreground">
    <header className="flex min-h-14 items-center justify-between gap-3 border-b border-border px-4 py-2 md:px-8">
      <div className="flex min-w-0 items-center gap-3"><Globe2 className="h-5 w-5 shrink-0 text-primary" /><div className="min-w-0"><h1 className="truncate text-sm font-semibold">{t.usageOverview}</h1><p className="truncate text-xs text-muted-foreground">{t.byMachineAndTask}</p></div></div>
      <div className="flex shrink-0 items-center gap-2"><Button variant="ghost" size="icon" onClick={() => void load()} disabled={loading} aria-label={t.refresh} title={t.refresh}><RefreshCw className={loading ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} /></Button><Button variant="secondary" size="sm" onClick={() => navigate('/orchestrate')}><ArrowLeft className="mr-1.5 h-3.5 w-3.5" />{t.backToRun}</Button></div>
    </header>
    <main className="mx-auto max-w-6xl space-y-5 p-4 md:p-8">
      <StatsError error={error} retry={retry} label={messages.retry} />
      <div role="status" className="rounded-md border border-border bg-muted/20 px-3 py-2 text-sm text-muted-foreground">
        {complete
          ? (zh ? `统计完成 · 已统计 ${scannedRuns} 个对话` : `Complete · ${scannedRuns} conversations scanned`)
          : (zh ? `${loading ? '统计中' : '统计未完成'} · 已统计 ${scannedRuns} 个对话；当前显示部分结果，并非最终总额。` : `${loading ? 'Calculating' : 'Incomplete'} · ${scannedRuns} conversations scanned; these are partial results, not final totals.`)}
      </div>
      <section className="flex flex-col gap-4 border-b border-border pb-5 lg:flex-row lg:items-end lg:justify-between">
        <div><p className="text-xs font-medium uppercase text-muted-foreground">{t.usageOverview}</p><h2 className="mt-1 text-xl font-semibold">{selectedMetric.label}</h2><p className="mt-1 text-sm text-muted-foreground">{t.byMachineAndTask}</p></div>
        <div className="flex flex-col gap-3 sm:flex-row">
          <div><div className="mb-1.5 text-[11px] text-muted-foreground">{t.timeRange}</div><div className="inline-flex max-w-full overflow-x-auto rounded-md border border-border bg-muted/30 p-0.5">{ranges.map((item) => <button key={item.value} type="button" onClick={() => setRange(item.value)} className={`h-7 whitespace-nowrap rounded px-2.5 text-xs transition-colors ${range === item.value ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>{item.label}</button>)}</div></div>
          <div><div className="mb-1.5 text-[11px] text-muted-foreground">{t.metric}</div><div className="flex max-w-full overflow-x-auto rounded-md border border-border bg-muted/30 p-0.5">{metricOptions.map((item) => <button key={item.value} type="button" onClick={() => setMetric(item.value)} className={`h-7 whitespace-nowrap rounded px-2.5 text-xs transition-colors ${metric === item.value ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>{item.label}</button>)}</div></div>
        </div>
      </section>
      {loading && !overview ? <div role="status" aria-label={messages.loading} className="flex justify-center p-16"><RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" /></div> : overview ? <>
        <section className="grid gap-px overflow-hidden rounded-lg border border-border bg-border sm:grid-cols-2 lg:grid-cols-4">
          {[
            { label: t.totalTokens, value: overview.totalTokens, icon: Layers3, display: formatCompact(overview.totalTokens) },
            { label: t.usageCost, value: overview.estimatedCostUsd, icon: Coins, display: overview.costKnown ? `$${finiteNumber(overview.estimatedCostUsd).toFixed(2)}` : `$${finiteNumber(overview.estimatedCostUsd).toFixed(2)}*` },
            { label: t.callsMetric, value: overview.callCount, icon: Activity, display: formatCompact(overview.callCount) },
            { label: `${t.machinesMetric} / ${t.tasksMetric}`, value: overview.runs, icon: Monitor, display: `${finiteNumber(overview.machines)} / ${finiteNumber(overview.runs)}` },
          ].map(({ label, value, icon: Icon, display }) => <div key={label} className="bg-card p-4" title={Number(value).toLocaleString()}><div className="flex items-center justify-between"><p className="text-xs text-muted-foreground">{label}</p><Icon className="h-3.5 w-3.5 text-muted-foreground" /></div><p className="mt-2 text-xl font-semibold tabular-nums">{display}</p></div>)}
        </section>
        {overview.runs > 0 && !overview.costKnown && <p className="text-xs text-muted-foreground">* {t.knownCostOnly} · {t.unpricedModels}</p>}
        <section className="rounded-lg border border-border bg-card p-4 md:p-5">
          <div className="mb-4 flex items-center justify-between"><div><h3 className="text-sm font-semibold">{t.usageTrend}</h3><p className="mt-1 text-xs text-muted-foreground">{selectedMetric.label}{metric === 'estimatedCostUsd' && !overview.costKnown ? ` · ${t.knownCostOnly}` : ''}</p></div><BarChart3 className="h-4 w-4 text-muted-foreground" /></div>
          <div className="h-72 w-full">{trend.length ? <ResponsiveContainer width="100%" height="100%"><LineChart data={trend} margin={{ top: 8, right: 10, left: 0, bottom: 4 }}><XAxis dataKey="date" tick={{ fontSize: 11 }} minTickGap={24} tickFormatter={(value) => String(value).slice(5)} /><YAxis width={54} tick={{ fontSize: 11 }} tickFormatter={(value) => metric === 'estimatedCostUsd' ? `$${formatCompact(value)}` : formatCompact(value)} /><Tooltip content={<AxisTooltip metric={metric} t={t} />} /><Line type="monotone" dataKey={metric} name={selectedMetric.label} stroke={selectedMetric.color} strokeWidth={2.5} dot={trend.length <= 31 ? { r: 2.5 } : false} activeDot={{ r: 4 }} /></LineChart></ResponsiveContainer> : <div className="flex h-full items-center justify-center text-sm text-muted-foreground">{complete ? t.noOverviewUsage : (zh ? '统计尚未完成…' : 'Statistics are not complete yet…')}</div>}</div>
        </section>
      <section className="space-y-3"><div className="flex items-center justify-between"><div><h3 className="text-sm font-semibold">{t.byMachineAndTask}</h3><p className="mt-1 text-xs text-muted-foreground">{finiteNumber(overview.machines)} {t.machinesMetric} · {finiteNumber(overview.runs)} {t.tasksMetric}</p></div><Monitor className="h-4 w-4 text-muted-foreground" /></div>{machineGroups.map((machine) => <div key={machine.id} className="overflow-hidden rounded-lg border border-border bg-card"><div className="flex items-center gap-3 border-b border-border bg-muted/20 px-4 py-3"><div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-md border border-border bg-background"><Monitor className="h-4 w-4" /></div><div className="min-w-0"><div className="truncate text-sm font-medium">{machine.name}</div><div className="truncate text-xs text-muted-foreground">{machine.hostname || machine.id} · {machine.items.length} {t.tasksMetric}</div></div></div><div className="divide-y divide-border">{machine.items.map((item) => <button key={item.runId} type="button" onClick={() => navigate(`/orchestrate/stats?run=${encodeURIComponent(item.runId)}`)} className="grid w-full gap-3 px-4 py-3 text-left transition-colors hover:bg-muted/20 sm:grid-cols-[minmax(0,1fr)_auto_auto] sm:items-center"><div className="min-w-0"><div className="truncate text-sm font-medium">{item.title || item.runId}</div><div className="mt-1 flex items-center gap-2 text-xs text-muted-foreground"><Route className="h-3 w-3" /><span>{formatTime(finiteNumber(item.createdAt))} · {item.status}</span></div></div><div className="grid grid-cols-2 gap-x-5 text-xs sm:text-right"><div><span className="text-muted-foreground">{selectedMetric.label}</span><div className="mt-0.5 font-medium tabular-nums" title={exactMetric(item[metric], metric)}>{metric === 'estimatedCostUsd' ? (`$${finiteNumber(item.estimatedCostUsd).toFixed(2)}${item.costKnown ? '' : '*'}`) : formatCompact(item[metric])}</div></div><div><span className="text-muted-foreground">{t.modelCalls}</span><div className="mt-0.5 font-medium tabular-nums">{finiteNumber(item.callCount).toLocaleString()}</div></div></div><ArrowLeft className="hidden h-4 w-4 rotate-180 text-muted-foreground sm:block" /></button>)}</div></div>)}{machineGroups.length === 0 ? <div className="rounded-lg border border-dashed border-border p-12 text-center text-sm text-muted-foreground">{complete ? t.noOverviewUsage : (zh ? '统计尚未完成…' : 'Statistics are not complete yet…')}</div> : null}</section>
      </> : null}
    </main>
  </div>;
}

function RunStatsPage({ t, navigate, runId }: { t: UIText; navigate: (path: string) => void; runId: string }) {
  const messages = statsRequestText(t);
  const fetchRun = useCallback(async (signal: AbortSignal) => {
    const [runData, statsData] = await Promise.all([
      api<{ run: OrchestrationRun }>(`/api/orchestrations/${encodeURIComponent(runId)}`, { signal }),
      api<{ stats: OrchestrationRunStats }>(`/api/orchestrations/${encodeURIComponent(runId)}/stats`, { signal }),
    ]);
    return { run: runData.run, stats: statsData.stats };
  }, [runId]);
  const { data, loading, error: loadError, reload: load } = useStatsRequest(`run:${runId}`, fetchRun, t.failedLoadOrchestration, messages.timeout);
  const run = data?.run;
  const stats = data?.stats;
  const [syncing, setSyncing] = useState(false);
  const [syncError, setSyncError] = useState('');
  const error = loadError || syncError;
  const [selectedTaskNumber, setSelectedTaskNumber] = useState<number | 'all'>('all');
  const syncRequest = useRef<ReturnType<typeof beginStatsRequest> | null>(null);
  const syncRefresh = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    const request = syncRequest.current;
    syncRequest.current = null;
    request?.cancel();
    if (syncRefresh.current !== null) clearTimeout(syncRefresh.current);
  }, []);
  const syncUsage = async () => {
    syncRequest.current?.cancel();
    if (syncRefresh.current !== null) clearTimeout(syncRefresh.current);
    setSyncing(true); setSyncError('');
    const request = beginStatsRequest(signal => api(`/api/orchestrations/${encodeURIComponent(runId)}/usage-sync`, { method: 'POST', signal }));
    syncRequest.current = request;
    try {
      await request.promise;
      if (syncRequest.current === request) syncRefresh.current = setTimeout(load, 1200);
    } catch (err) {
      if (syncRequest.current === request) setSyncError(err instanceof StatsTimeoutError ? messages.timeout : err instanceof Error ? err.message : t.failedLoadOrchestration);
    } finally {
      if (syncRequest.current === request) { syncRequest.current = null; setSyncing(false); }
    }
  };
  useEffect(() => {
    if (selectedTaskNumber === 'all') return;
    if (!stats?.tasks?.some((task) => task.taskNumber === selectedTaskNumber)) setSelectedTaskNumber('all');
  }, [selectedTaskNumber, stats?.tasks]);
  const displayStats = selectedTaskNumber === 'all' ? stats : stats?.tasks?.find((task) => task.taskNumber === selectedTaskNumber) || stats;
  const runtime = useMemo(() => displayStats ? formatDuration(finiteNumber(displayStats.runtimeSeconds) * 1000) || '0s' : '', [displayStats]);
  const rounds = values(displayStats?.rounds);
  const costDescription = displayStats?.costSource === 'models.dev' ? t.liveCatalogPrice : displayStats?.costSource === 'provider' ? t.providerCost : ['official-catalog', 'catalog'].includes(displayStats?.costSource || '') ? t.officialCatalog : t.usageCost;
  const cards = displayStats ? [
    { label: t.runtime, value: runtime, icon: Clock3 },
    { label: t.turns, value: rounds.length.toLocaleString(), icon: Layers3 },
    { label: t.inputTokens, value: finiteNumber(displayStats.inputTokens).toLocaleString(), icon: Layers3 },
    { label: t.outputTokens, value: finiteNumber(displayStats.outputTokens).toLocaleString(), icon: Layers3 },
    { label: t.cacheTokens, value: finiteNumber(displayStats.cacheTokens).toLocaleString(), icon: Layers3 },
    { label: t.totalTokens, value: finiteNumber(displayStats.totalTokens).toLocaleString(), icon: Globe2 },
    { label: t.usageCost, value: `$${finiteNumber(displayStats.estimatedCostUsd).toFixed(6)}${displayStats.costKnown ? '' : '*'}`, icon: Coins },
  ] : [];
  const isZh = t.task === '任务';
  return <div className="phone-page min-h-screen bg-background text-foreground"><header className="flex min-h-14 items-center justify-between gap-3 border-b border-border px-4 py-2 md:px-8"><div className="flex min-w-0 items-center gap-3"><BarChart3 className="h-5 w-5 shrink-0 text-primary" /><div className="min-w-0"><h1 className="truncate text-sm font-semibold">{t.runStatistics}</h1><p className="truncate text-xs text-muted-foreground">{run?.title || runId}</p></div></div><div className="flex shrink-0 items-center gap-2"><Button variant="ghost" size="icon" onClick={() => void load()} disabled={loading} aria-label={t.refresh} title={t.refresh}><RefreshCw className={loading ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} /></Button><Button variant="secondary" size="sm" onClick={() => navigate('/orchestrate/usage')}><Globe2 className="mr-1.5 h-3.5 w-3.5" />{t.overview}</Button><Button variant="secondary" size="sm" onClick={() => navigate(`/orchestrate/runs/${encodeURIComponent(runId)}`)}><ArrowLeft className="mr-1.5 h-3.5 w-3.5" />{t.backToRun}</Button></div></header><main className="mx-auto max-w-5xl space-y-6 p-4 md:p-8">
    <StatsError error={error} retry={syncError && !loadError ? () => void syncUsage() : load} label={messages.retry} />
    {loading && !stats ? <div role="status" aria-label={messages.loading} className="flex justify-center p-16"><RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" /></div> : stats ? <>
      <section className="rounded-lg border border-border bg-card p-5"><div className="flex flex-wrap items-center justify-between gap-3"><div><p className="text-xs uppercase text-muted-foreground">{t.status}</p><p className="mt-1 text-lg font-semibold">{run?.status || t.idle}</p><p className="mt-1 text-xs text-muted-foreground">{displayStats.accountingSource === 'local-cli-ledger' ? `${t.localLedgerUsage} · ${displayStats.accountingStatus === 'complete' ? t.usageComplete : t.usagePartial}` : t.usageLegacyIncomplete}</p></div><div className="text-right text-xs text-muted-foreground"><div>{displayStats.startedAt ? formatTime(displayStats.startedAt) : ''}{displayStats.finishedAt ? ` -> ${formatTime(displayStats.finishedAt)}` : ''}</div><div className="mt-1">{displayStats.native ? t.nativeUsage : t.estimatedUsageNotice}{displayStats.costKnown ? ` · ${costDescription}` : ` · * ${t.knownCostOnly} · ${t.unpricedModels}`}</div>{stats.catalogUpdatedAt ? <div className="mt-1">{t.liveCatalogPrice} · {formatTime(stats.catalogUpdatedAt)}</div> : null}{displayStats.pricingModels?.length ? <div className="mt-1">{t.pricingAnchor}: {displayStats.pricingModels.join(', ')}</div> : null}<Button className="mt-3" variant="secondary" size="sm" onClick={() => void syncUsage()} disabled={syncing}>{syncing ? t.syncingUsage : t.syncUsage}</Button></div></div></section>
      {(stats.tasks?.length || 0) > 0 && <section><div className="mb-1.5 text-[11px] font-medium text-muted-foreground">{isZh ? '统计范围' : 'Usage scope'}</div><div className="flex max-w-full gap-1 overflow-x-auto rounded-md border border-border bg-muted/30 p-1 elegant-scrollbar"><button type="button" onClick={() => setSelectedTaskNumber('all')} className={`h-8 shrink-0 rounded px-3 text-xs transition-colors ${selectedTaskNumber === 'all' ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>{isZh ? '全部任务' : 'All tasks'}</button>{stats.tasks?.map((task) => <button key={task.taskNumber} type="button" onClick={() => setSelectedTaskNumber(task.taskNumber)} className={`h-8 shrink-0 rounded px-3 text-xs transition-colors ${selectedTaskNumber === task.taskNumber ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'}`}>{isZh ? `任务 ${task.taskNumber}` : `Task ${task.taskNumber}`}</button>)}</div></section>}
      <section className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">{cards.map(({ label, value, icon: Icon }) => <div key={label} className="rounded-lg border border-border bg-card p-4"><Icon className="h-4 w-4 text-muted-foreground" /><p className="mt-3 text-xs text-muted-foreground">{label}</p><p className="mt-1 break-words text-xl font-semibold tabular-nums">{value}</p></div>)}</section>
      {stats.modelRecoveryStatus && <p className="text-xs leading-relaxed text-muted-foreground" role="status">{{ 'upgrade-required': t.modelRecoveryUpgrade, offline: t.modelRecoveryOffline, pending: t.modelRecoveryPending, unresolved: t.modelRecoveryUnresolved }[stats.modelRecoveryStatus]}</p>}
      <RoundModelUsageCharts stats={displayStats} t={t} />
    </> : null}
  </main></div>;
}

export function OrchestrationStatsPage({ t, navigate, runId }: { t: UIText; navigate: (path: string) => void; runId: string }) {
  return runId ? <RunStatsPage key={runId} t={t} navigate={navigate} runId={runId} /> : <UsageOverviewPage t={t} navigate={navigate} />;
}
