import React, { useState } from 'react';
import { BarChart3, ChartLine } from 'lucide-react';
import { Bar, BarChart, CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import type { OrchestrationRunStatsBase } from '../lib/types';
import type { UIText } from '../lib/i18n';
import { modelUsageTotals, roundModelUsage } from '../lib/roundModelUsage';

const colors = ['#38bdf8', '#a78bfa', '#34d399', '#f59e0b', '#f472b6', '#fb7185'];
const compact = (n: number) => Intl.NumberFormat(undefined, { notation: 'compact', maximumFractionDigits: 1 }).format(n);

export function RoundModelUsageCharts({ stats, t }: { stats: OrchestrationRunStatsBase; t: UIText }) {
  const [chartType, setChartType] = useState<'bar' | 'line'>('bar');
  const rows = roundModelUsage(stats.rounds);
  const totals = modelUsageTotals(rows);
  const maxTokens = Math.max(1, ...totals.map(row => row.totalTokens));
  const maxCost = Math.max(0, ...totals.map(row => row.estimatedCostUsd));
  const models = [...new Set(rows.map(row => row.model))];
  const data = [...(Array.isArray(stats.rounds) ? stats.rounds : [])].sort((a, b) => a.round - b.round).map(round => {
    const values: Record<string, number> = { round: round.round };
    for (const row of rows) if (row.round === round.round) values[`model${models.indexOf(row.model)}`] = row.totalTokens;
    return values;
  });
  const modelLabel = (model: string) => model || t.unknownUsageModel;
  const tooltip = ({ active, payload, label }: any) => active && payload?.length ? <div className="max-w-[min(28rem,85vw)] rounded-md border border-border bg-popover p-3 text-xs shadow-sm"><div className="mb-2 text-muted-foreground">{t.roundNumberColumn} #{label}</div>{payload.filter((entry: any) => entry.value > 0).map((entry: any) => <div key={entry.dataKey} className="flex items-start justify-between gap-4 py-1"><span className="break-all" style={{ color: entry.color }}>{entry.name}</span><span className="shrink-0 tabular-nums">{Number(entry.value).toLocaleString()}</span></div>)}</div> : null;
  return <><section className="space-y-5 rounded-lg border border-border bg-card p-4 md:p-5" data-testid="round-model-usage">
    <div><h2 className="text-sm font-semibold">{t.roundModelSummary}</h2><p className="mt-1 text-xs leading-relaxed text-muted-foreground">{t.roundModelNotice}</p></div>
    <p className="rounded-md bg-muted/50 p-3 text-xs leading-relaxed text-muted-foreground">{t.roundModelCycleNotice}</p>
    <div className="inline-flex max-w-full gap-1.5 rounded-xl border border-primary/30 bg-primary/10 p-1.5" role="group" aria-label={t.roundModelSummary}>
      {([{ value: 'bar', label: t.roundModelBarButton, icon: BarChart3 }, { value: 'line', label: t.roundModelLineButton, icon: ChartLine }] as const).map(({ value, label, icon: Icon }) => <button key={value} type="button" aria-pressed={chartType === value} onClick={() => setChartType(value)} className={`inline-flex min-h-11 items-center justify-center gap-2 rounded-lg px-5 text-sm font-semibold transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 focus-visible:ring-offset-background ${chartType === value ? 'bg-primary text-primary-foreground shadow-md' : 'text-foreground hover:bg-primary/15'}`}><Icon className="h-4 w-4" aria-hidden="true" />{label}</button>)}
    </div>
    <ul className="flex flex-wrap gap-x-5 gap-y-2 text-xs" aria-label={t.roundModelColumn}>{models.map((model, index) => <li key={model} className="flex min-w-0 items-start gap-2"><span className="mt-1 h-2 w-2 shrink-0 rounded-full" style={{ backgroundColor: colors[index % colors.length] }} /><span className="break-all">{modelLabel(model)}</span></li>)}</ul>
    <div className="min-w-0">
      {chartType === 'bar' ? <div className="min-w-0" data-testid="round-model-bars"><h3 className="mb-3 text-xs font-medium">{t.roundModelBars} · {t.totalTokens}</h3><div className="overflow-x-auto"><div className="h-64" style={{ minWidth: Math.max(280, data.length * 48) }}>
      {rows.length ? <ResponsiveContainer width="100%" height="100%"><BarChart data={data} margin={{ top: 8, right: 12, left: 0, bottom: 4 }} accessibilityLayer><CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 3" /><XAxis dataKey="round" tickFormatter={(value) => `#${value}`} tick={{ fontSize: 11 }} /><YAxis width={54} tick={{ fontSize: 11 }} tickFormatter={compact} /><Tooltip content={tooltip} />{models.map((model, index) => <Bar key={model} dataKey={`model${index}`} name={modelLabel(model)} stackId="tokens" fill={colors[index % colors.length]} maxBarSize={64} isAnimationActive={false} />)}</BarChart></ResponsiveContainer> : <div className="flex h-full items-center justify-center text-xs text-muted-foreground">{t.noUsageYet}</div>}
      </div></div></div> :
      <div className="min-w-0" data-testid="round-model-lines"><h3 className="mb-3 text-xs font-medium">{t.roundModelLines} · {t.totalTokens}</h3><div className="overflow-x-auto"><div className="h-64" style={{ minWidth: Math.max(280, data.length * 48) }}>
      {rows.length ? <ResponsiveContainer width="100%" height="100%"><LineChart data={data} margin={{ top: 8, right: 12, left: 0, bottom: 4 }} accessibilityLayer><CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 3" /><XAxis dataKey="round" tickFormatter={(value) => `#${value}`} tick={{ fontSize: 11 }} /><YAxis width={54} tick={{ fontSize: 11 }} tickFormatter={compact} /><Tooltip content={tooltip} />{models.map((model, index) => <Line key={model} type="linear" dataKey={`model${index}`} name={modelLabel(model)} stroke={colors[index % colors.length]} strokeWidth={2} dot={{ r: 3, fill: colors[index % colors.length] }} activeDot={{ r: 5 }} connectNulls={false} isAnimationActive={false} />)}</LineChart></ResponsiveContainer> : <div className="flex h-full items-center justify-center text-xs text-muted-foreground">{t.noUsageYet}</div>}
      </div></div></div>}
    </div>
    <div className="max-w-full overflow-x-auto" tabIndex={0} role="region" aria-label={t.roundModelSummary}>
      <table className="w-full min-w-[760px] text-left text-xs"><caption className="sr-only">{t.roundModelSummary}</caption><thead><tr>{[t.roundNumberColumn, t.roundModelColumn, t.inputTokens, t.outputTokens, t.cacheTokens, t.totalTokens, t.usageCost].map((label, i) => <th key={label} className={`whitespace-nowrap p-2 font-medium text-muted-foreground ${i > 1 ? 'text-right' : ''}`}>{label}</th>)}</tr></thead><tbody>{rows.map(row => <tr key={JSON.stringify([row.round, row.model])} className="border-t border-border"><td className="p-2">#{row.round}</td><td className="break-all p-2">{modelLabel(row.model)}</td>{[row.inputTokens, row.outputTokens, row.cacheTokens, row.totalTokens].map((value, i) => <td key={i} className="p-2 text-right tabular-nums">{value.toLocaleString()}</td>)}<td className="whitespace-nowrap p-2 text-right tabular-nums">{!row.costKnown && !row.estimatedCostUsd ? '—' : `$${row.estimatedCostUsd.toFixed(6)}${row.costKnown ? '' : '*'}`}</td></tr>)}</tbody></table>
    </div>
  </section>
  <section className="space-y-4 rounded-lg border border-border bg-card p-4 md:p-5" data-testid="model-usage-summary">
    <div><h2 className="text-sm font-semibold">{t.modelTotalSummary}</h2><p className="mt-1 text-xs leading-relaxed text-muted-foreground">{t.modelTotalNotice}</p></div>
    {totals.length ? <div className="grid gap-4 lg:grid-cols-2">{totals.map((row, index) => {
      const color = colors[index % colors.length];
      const cost = !row.costKnown && !row.estimatedCostUsd ? '—' : `$${row.estimatedCostUsd.toFixed(6)}${row.costKnown ? '' : '*'}`;
      return <article key={row.model} className="min-w-0 space-y-4 rounded-lg border border-border p-4" data-testid="model-total">
        <h3 className="flex items-start gap-2 text-sm font-semibold"><span className="mt-1 h-2.5 w-2.5 shrink-0 rounded-full" style={{ backgroundColor: color }} /><span className="break-all">{modelLabel(row.model)}</span></h3>
        <div className="space-y-3">
          {[{ label: t.totalTokens, value: row.totalTokens.toLocaleString(), width: row.totalTokens / maxTokens * 100 }, { label: t.usageCost, value: cost, width: maxCost ? row.estimatedCostUsd / maxCost * 100 : 0 }].map(metric => <div key={metric.label}>
            <div className="mb-1.5 flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1"><span className="text-xs text-muted-foreground">{metric.label}</span><strong className="tabular-nums text-lg">{metric.value}</strong></div>
            {metric.value !== '—' && <div className="h-2 overflow-hidden rounded-full bg-muted" aria-hidden="true"><div className="h-full rounded-full" style={{ width: `${metric.width}%`, backgroundColor: color }} /></div>}
          </div>)}
        </div>
        <dl className="grid grid-cols-3 gap-2 border-t border-border pt-3 text-xs">{[{ label: t.inputTokens, value: row.inputTokens }, { label: t.outputTokens, value: row.outputTokens }, { label: t.cacheTokens, value: row.cacheTokens }].map(metric => <div key={metric.label} className="min-w-0"><dt className="text-muted-foreground">{metric.label}</dt><dd className="mt-1 break-all font-medium tabular-nums">{metric.value.toLocaleString()}</dd></div>)}</dl>
      </article>;
    })}</div> : <p className="text-xs text-muted-foreground">{t.noUsageYet}</p>}
  </section></>;
}
