import type { OrchestrationUsageOverview, OrchestrationUsageOverviewPage } from './types';

export type UsageOverviewAccumulator = {
  overview: OrchestrationUsageOverview;
  scannedRuns: number;
  cursors: Set<string>;
  runIds: Set<string>;
};
const sumFields = ['inputTokens', 'outputTokens', 'cacheTokens', 'totalTokens', 'estimatedCostUsd', 'callCount'] as const;
const number = (value: number) => Number.isFinite(value) ? value : 0;

// The caller deliberately requests one source run per page. This keeps a
// run's aggregate trend indivisible, including when a duplicate is retried.
export function mergeUsageOverviewPage(current: UsageOverviewAccumulator | null, response: OrchestrationUsageOverviewPage, cursor: string): UsageOverviewAccumulator {
  if (current?.cursors.has(cursor)) return current;
  const incoming = response.overview;
  const items = incoming.items || [];
  if (items.length > 1) throw new Error('Expected a single-run statistics page');
  const cursors = new Set(current?.cursors); cursors.add(cursor);
  const runIds = new Set(current?.runIds);
  const duplicate = items.length > 0 && runIds.has(items[0].runId);
  const overview: OrchestrationUsageOverview = current ? { ...current.overview, items: [...current.overview.items], trend: current.overview.trend.map(point => ({ ...point })) } : {
    inputTokens: 0, outputTokens: 0, cacheTokens: 0, totalTokens: 0, estimatedCostUsd: 0, callCount: 0,
    runs: 0, machines: 0, days: incoming.days, timezoneOffset: incoming.timezoneOffset, costKnown: false, trend: [], items: [],
  };
  if (!duplicate && items.length) {
    for (const field of sumFields) overview[field] += number(incoming[field]);
    overview.costKnown = overview.items.length ? overview.costKnown && incoming.costKnown : incoming.costKnown;
    overview.items.push(...items);
    runIds.add(items[0].runId);
    const dates = new Map(overview.trend.map(point => [point.date, point]));
    for (const point of incoming.trend || []) {
      const previous = dates.get(point.date);
      if (!previous) dates.set(point.date, { ...point });
      else {
        for (const field of sumFields) previous[field] += number(point[field]);
        previous.costKnown = previous.costKnown && point.costKnown;
      }
    }
    overview.trend = [...dates.values()].sort((a, b) => a.date.localeCompare(b.date));
  }
  overview.runs = overview.items.length;
  overview.machines = new Set(overview.items.map(item => item.machineId)).size;
  return { overview, cursors, runIds, scannedRuns: (current?.scannedRuns || 0) + (duplicate ? 0 : number(response.page.scannedRuns)) };
}
