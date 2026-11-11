import { api } from './api';
import { beginStatsRequest } from './statsRequest';

// One workspace selection owns its reads. Reconnection, polling and visibility
// recovery can ask for the same URL, but must not queue duplicate DB work.
export function createRunReadRequests() {
  let generation = 0;
  const pending = new Map<string, { promise: Promise<unknown>; cancel: () => void }>();
  return {
    get generation() { return generation; },
    cancel() {
      generation += 1;
      for (const request of pending.values()) request.cancel();
      pending.clear();
    },
    read<T>(path: string): Promise<T> {
      const existing = pending.get(path);
      if (existing) return existing.promise as Promise<T>;
      const version = generation;
      const request = beginStatsRequest(signal => api<T>(path, { signal }));
      const promise: Promise<T> = request.promise.then(data => {
        if (version !== generation) throw new DOMException('Selection changed', 'AbortError');
        return data;
      }).finally(() => {
        if (pending.get(path)?.promise === promise) pending.delete(path);
      });
      pending.set(path, { promise, cancel: request.cancel });
      return promise;
    },
  };
}
