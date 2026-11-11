import { useCallback, useEffect, useState } from 'react';
import { beginStatsRequest, StatsTimeoutError } from './statsRequest';

export function useStatsRequest<T>(key: string, load: (signal: AbortSignal) => Promise<T>, failureText: string, timeoutText: string): { data: T | null; loading: boolean; error: string; reload: () => void } {
  const [revision, setRevision] = useState(0);
  const [state, setState] = useState<{ key: string; data: T | null; loading: boolean; error: string }>({ key, data: null, loading: true, error: '' });
  const reload = useCallback(() => setRevision(value => value + 1), []);

  useEffect(() => {
    let current = true;
    setState({ key, data: null, loading: true, error: '' });
    const request = beginStatsRequest(load);
    void request.promise.then(data => {
      if (current) setState({ key, data, loading: false, error: '' });
    }, error => {
      const timedOut = error instanceof StatsTimeoutError || (error instanceof Error && 'status' in error && error.status === 504);
      if (current) setState({ key, data: null, loading: false, error: timedOut ? timeoutText : error instanceof Error ? error.message : failureText });
    });
    return () => {
      current = false;
      request.cancel();
    };
  }, [key, load, revision, failureText, timeoutText]);

  // A selection renders before effects run. Never label the previous target's
  // data with the new range/run during that render.
  return { ...(state.key === key ? state : { data: null, loading: true, error: '' }), reload };
}
