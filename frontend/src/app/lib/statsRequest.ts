// Browser deadline is longer than the Hub's 20-second statistics budget.
export const STATS_REQUEST_TIMEOUT_MS = 25_000;

export class StatsTimeoutError extends Error {
  constructor() {
    super('Statistics request timed out');
    this.name = 'StatsTimeoutError';
  }
}

// Settle even when a transport ignores cancellation. Consumers still guard
// request identity because a completed earlier request may already be queued.
export function beginStatsRequest<T>(load: (signal: AbortSignal) => Promise<T>, timeoutMs = STATS_REQUEST_TIMEOUT_MS) {
  const controller = new AbortController();
  let onAbort: () => void;
  const timer = setTimeout(() => controller.abort(new StatsTimeoutError()), timeoutMs);
  const promise = new Promise<T>((resolve, reject) => {
    onAbort = () => reject(controller.signal.reason);
    controller.signal.addEventListener('abort', onAbort, { once: true });
    Promise.resolve().then(() => {
      controller.signal.throwIfAborted();
      return load(controller.signal);
    }).then(resolve, reject);
  }).finally(() => {
    clearTimeout(timer);
    controller.signal.removeEventListener('abort', onAbort);
    // Also stop unfinished peers when one branch of Promise.all fails.
    controller.abort();
  });
  return { promise, cancel: () => controller.abort() };
}
