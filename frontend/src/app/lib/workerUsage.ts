import type { OrchestrationRoundStats, OrchestrationUsageStats } from './types';

export type WorkerMetric = 'totalTokens' | 'inputTokens' | 'outputTokens' | 'cacheReadTokens' | 'cacheWriteTokens' | 'estimatedCostUsd';
export const workerKey = (item: OrchestrationUsageStats) => JSON.stringify([item.cli, item.workerSlot || 'unknown', item.model || '']);
export function workerLabel(item: OrchestrationUsageStats, unknown: string) {
  const verifier = item.workerSlot?.startsWith("verifier:");
  const slot = verifier ? item.workerSlot!.slice("verifier:".length) : item.workerSlot;
  if (slot === 'verifier') return `${item.cli} · verifier`;
  if (!slot || slot === 'unknown') return `${item.cli} · ${unknown}`;
  return slot.replace(/^codex/, 'Codex').replace(/^claude/, 'Claude').replace(/-([ab])$/, (_, letter: string) => ` ${letter.toUpperCase()}`) + (verifier ? " · verifier" : "");
}
export function workerMetric(item: OrchestrationUsageStats | undefined, metric: WorkerMetric): number | null {
  if (!item) return 0;
  if (metric === 'estimatedCostUsd' && !item.costKnown) return null;
  if (metric === 'totalTokens') return item.inputTokens + item.outputTokens + item.cacheReadTokens + item.cacheWriteTokens;
  return item[metric] || 0;
}
export function workerChartRows(rounds: OrchestrationRoundStats[], series: OrchestrationUsageStats[], metric: WorkerMetric, cumulative: boolean) {
  const sums: (number | null)[] = series.map(() => 0);
  return rounds.map((round) => {
    const row: Record<string, number | null> = { round: round.round };
    const workers = new Map((round.byWorker || []).map((worker) => [workerKey(worker), worker]));
    series.forEach((worker, index) => {
      const value = workerMetric(workers.get(workerKey(worker)), metric);
      sums[index] = sums[index] === null || value === null ? null : sums[index]! + value;
      row[`worker${index}`] = cumulative ? sums[index] : value;
    });
    return row;
  });
}
