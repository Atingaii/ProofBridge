import type { OrchestrationRoundStats } from './types';

export type RoundModelUsage = {
  round: number; model: string; inputTokens: number; outputTokens: number;
  cacheTokens: number; totalTokens: number; estimatedCostUsd: number; costKnown: boolean;
};
const amount = (value: number | undefined) => Number.isFinite(value) ? Math.max(0, value!) : 0;

export type ModelUsageTotal = Omit<RoundModelUsage, 'round'>;

// Fold the displayed rows so model totals and round details always reconcile.
export function modelUsageTotals(rows: RoundModelUsage[]): ModelUsageTotal[] {
  const totals = new Map<string, ModelUsageTotal>();
  for (const { round: _round, ...row } of rows) {
    const total = totals.get(row.model);
    if (!total) { totals.set(row.model, { ...row }); continue; }
    total.inputTokens += row.inputTokens;
    total.outputTokens += row.outputTokens;
    total.cacheTokens += row.cacheTokens;
    total.totalTokens += row.totalTokens;
    total.estimatedCostUsd += row.estimatedCostUsd;
    total.costKnown &&= row.costKnown;
  }
  return [...totals.values()];
}

// Cache is already normalized; reasoning belongs to output. Sum original quotes
// instead of repricing merged usage (individual calls may have different tiers).
export function roundModelUsage(rounds: OrchestrationRoundStats[] = []): RoundModelUsage[] {
  return [...(Array.isArray(rounds) ? rounds : [])].sort((a, b) => a.round - b.round).flatMap(round => {
    const models = new Map<string, RoundModelUsage>();
    const get = (model: string) => {
      if (!models.has(model)) models.set(model, { round: round.round, model, inputTokens: 0, outputTokens: 0, cacheTokens: 0, totalTokens: 0, estimatedCostUsd: 0, costKnown: true });
      return models.get(model)!;
    };
    let input = 0, output = 0, cache = 0, total = 0, cost = 0;
    for (const item of round.byWorker || []) {
      const i = amount(item.inputTokens), o = amount(item.outputTokens);
      const c = amount(item.cacheReadTokens) + amount(item.cacheWriteTokens), price = amount(item.estimatedCostUsd);
      if (!i && !o && !c && !price) continue;
      const row = get(item.model?.trim() || '');
      row.inputTokens += i; row.outputTokens += o; row.cacheTokens += c; row.totalTokens += i + o + c;
      row.estimatedCostUsd += price; row.costKnown &&= item.costKnown === true;
      input += i; output += o; cache += c; total += i + o + c; cost += price;
    }
    const remainder = Math.max(0, amount(round.totalTokens) - total);
    const remainingCost = Math.max(0, amount(round.estimatedCostUsd) - cost);
    if (remainder || remainingCost > 1e-9) {
      const row = get('');
      row.inputTokens += Math.max(0, amount(round.inputTokens) - input);
      row.outputTokens += Math.max(0, amount(round.outputTokens) - output);
      row.cacheTokens += Math.max(0, amount(round.cacheTokens) - cache);
      row.totalTokens += remainder; row.estimatedCostUsd += remainingCost; row.costKnown = false;
    }
    return [...models.values()];
  });
}
