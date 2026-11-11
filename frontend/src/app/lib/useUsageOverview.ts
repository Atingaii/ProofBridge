import { useCallback, useEffect, useRef, useState } from 'react';
import { api } from './api';
import { beginStatsRequest, StatsTimeoutError } from './statsRequest';
import type { OrchestrationUsageOverviewPage } from './types';
import { mergeUsageOverviewPage, type UsageOverviewAccumulator } from './usageOverviewPages';

type State = { key: string; value: UsageOverviewAccumulator | null; loading: boolean; complete: boolean; error: string };
export function useUsageOverview(days: number, failureText: string, timeoutText: string) {
  const timezoneOffset = new Date().getTimezoneOffset();
  const key = `${days}:${timezoneOffset}`;
  const [revision, setRevision] = useState(0);
  const [state, setState] = useState<State>({ key, value: null, loading: true, complete: false, error: '' });
  const retryRef = useRef<() => void>(() => undefined);
  const retry = useCallback(() => retryRef.current(), []);
  const reload = useCallback(() => setRevision(value => value + 1), []);
  useEffect(() => {
    let disposed = false, running = false;
    let cursor = '';
    let value: UsageOverviewAccumulator | null = null;
    let pending: ReturnType<typeof beginStatsRequest<OrchestrationUsageOverviewPage>> | null = null;
    const read = async () => {
      if (disposed || running) return;
      running = true;
      setState({ key, value, loading: true, complete: false, error: '' });
      try {
        while (!disposed) {
          const params = new URLSearchParams({ days: String(days), timezoneOffset: String(timezoneOffset), limit: '1' });
          if (cursor) params.set('cursor', cursor);
          pending = beginStatsRequest(signal => api<OrchestrationUsageOverviewPage>(`/api/usage/overview?${params}`, { signal }));
          const response = await pending.promise;
          if (disposed) return;
          if (!response.page || typeof response.page.hasMore !== 'boolean') throw new Error('Invalid statistics page');
          const next = response.page.nextCursor || '';
          if (response.page.hasMore && (!next || next === cursor || value?.cursors.has(next))) throw new Error('Statistics cursor did not advance');
          value = mergeUsageOverviewPage(value, response, cursor);
          const complete = !response.page.hasMore;
          setState({ key, value, loading: !complete, complete, error: '' });
          if (complete) return;
          cursor = next;
        }
      } catch (error) {
        if (disposed) return;
        const timedOut = error instanceof StatsTimeoutError || (error instanceof Error && 'status' in error && error.status === 504);
        setState({ key, value, loading: false, complete: false, error: timedOut ? timeoutText : error instanceof Error ? error.message : failureText });
      } finally { running = false; pending = null; }
    };
    retryRef.current = () => { void read(); };
    void read();
    return () => { disposed = true; pending?.cancel(); retryRef.current = () => undefined; };
  }, [key, days, timezoneOffset, revision, failureText, timeoutText]);
  const visible = state.key === key ? state : { key, value: null, loading: true, complete: false, error: '' };
  return { overview: visible.value?.overview || null, scannedRuns: visible.value?.scannedRuns || 0, loading: visible.loading, complete: visible.complete, error: visible.error, retry, reload };
}
