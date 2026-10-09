import React, { lazy, Suspense, useCallback, useEffect, useRef, useState } from 'react';
import { RefreshCw } from 'lucide-react';
import { api, ApiError } from './lib/api';
import { beginStatsRequest, StatsTimeoutError } from './lib/statsRequest';
import { clearUserInputStorage } from './lib/userInput';
import { clearAccountOrchestrationDrafts } from './lib/orchestrationDrafts';
import type { UserAccount } from './lib/types';
import { initialLanguage, uiText, type Language } from './lib/i18n';

// Page modules are independent interaction surfaces. Loading them on demand
// keeps initial login and workspace navigation responsive on slower networks.
const ConversationSnapshotPage = lazy(() => import('./pages/ConversationSnapshotPage').then(({ ConversationSnapshotPage }) => ({ default: ConversationSnapshotPage })));
const HelpPage = lazy(() => import('./pages/HelpPage').then(({ HelpPage }) => ({ default: HelpPage })));
const OrchestrationDesignPage = lazy(() => import('./pages/OrchestrationDesignPage').then(({ OrchestrationDesignPage }) => ({ default: OrchestrationDesignPage })));
const LoginScreen = lazy(() => import('./pages/LoginScreen').then(({ LoginScreen }) => ({ default: LoginScreen })));
const OrchestrationWorkspace = lazy(() => import('./pages/OrchestrationWorkspace').then(({ OrchestrationWorkspace }) => ({ default: OrchestrationWorkspace })));
const OrchestrationStatsPage = lazy(() => import('./pages/OrchestrationStatsPage').then(({ OrchestrationStatsPage }) => ({ default: OrchestrationStatsPage })));
const PublicSharePage = lazy(() => import('./pages/PublicSharePage').then(({ PublicSharePage }) => ({ default: PublicSharePage })));
const UpdatesPage = lazy(() => import('./pages/UpdatesPage').then(({ UpdatesPage }) => ({ default: UpdatesPage })));
const Workspace = lazy(() => import('./pages/Workspace').then(({ Workspace }) => ({ default: Workspace })));
const AdminUsagePage = lazy(() => import('./pages/AdminUsagePage').then(({ AdminUsagePage }) => ({ default: AdminUsagePage })));
const AdminUserUsagePage = lazy(() => import('./pages/AdminUserUsagePage').then(({ AdminUserUsagePage }) => ({ default: AdminUserUsagePage })));

function PageLoading() {
  return (
    <div className="min-h-screen w-full flex items-center justify-center bg-background text-foreground">
      <RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" />
    </div>
  );
}

export default function App() {
  const [user, setUser] = useState<UserAccount | null>(null);
  const [nativeUserInputProtocolVersion, setNativeUserInputProtocolVersion] = useState(0);
  const [booting, setBooting] = useState(true);
  const [authError, setAuthError] = useState<'unavailable' | 'timeout' | ''>('');
  const [authChecking, setAuthChecking] = useState(false);
  const retryAuthRef = useRef<() => void>(() => undefined);
  const authGeneration = useRef(0);
  const [isDarkMode, setIsDarkMode] = useState(() => {
    try { return localStorage.getItem('codexBridge.theme') !== 'light'; } catch { return true; }
  });
  const [language, setLanguage] = useState<Language>(initialLanguage);
  const [path, setPath] = useState(() => window.location.pathname);
  const t = uiText[language];
  const isSnapshotRoute = path.startsWith('/conversation-snapshot');
  const isShareRoute = path.startsWith('/share/');
  const isHelpRoute = path === '/help' || path === '/help/' || path === '/hlep' || path === '/hlep/';
  const isUpdatesRoute = path === '/updates' || path === '/updates/';
  const isDesignArticleRoute = path === '/blog/orchestration-design' || path === '/blog/orchestration-design/';

  useEffect(() => {
    document.documentElement.classList.toggle('dark', isDarkMode);
    try { localStorage.setItem('codexBridge.theme', isDarkMode ? 'dark' : 'light'); } catch { /* Preferences are optional. */ }
  }, [isDarkMode]);

  useEffect(() => {
    document.documentElement.lang = isDesignArticleRoute || language === 'zh' ? 'zh-CN' : 'en';
    try { localStorage.setItem('codexBridge.language', language); } catch { /* Preferences are optional. */ }
  }, [language, isDesignArticleRoute]);

  useEffect(() => {
    const generation = ++authGeneration.current;
    if (isShareRoute || isHelpRoute || isUpdatesRoute || isDesignArticleRoute) {
      setBooting(false);
      return;
    }
    let disposed = false;
    let retryable = false;
    let failures = 0;
    let lastStart = 0;
    let retryTimer: number | null = null;
    let pending: ReturnType<typeof beginStatsRequest<{ user: UserAccount; nativeUserInputProtocolVersion?: number }>> | null = null;
    const isCurrent = () => !disposed && generation === authGeneration.current;
    const canRecover = () => isCurrent() && retryable && document.visibilityState === 'visible' && navigator.onLine;
    const clearRetry = () => { if (retryTimer !== null) window.clearTimeout(retryTimer); retryTimer = null; };
    const schedule = (delay: number) => {
      clearRetry();
      if (!canRecover() || pending) return;
      retryTimer = window.setTimeout(() => { retryTimer = null; if (canRecover()) check(); }, delay);
    };
    const check = () => {
      if (!isCurrent() || pending) return;
      clearRetry();
      lastStart = Date.now();
      setAuthChecking(true);
      const request = beginStatsRequest(signal => api<{ user: UserAccount; nativeUserInputProtocolVersion?: number }>('/api/me', { signal }), 12000);
      pending = request;
      void request.promise.then(data => {
        if (!isCurrent()) return;
        if (!data.user?.id) throw new Error('Invalid session response');
        retryable = false;
        setAuthError('');
        setUser(data.user);
        setNativeUserInputProtocolVersion(data.nativeUserInputProtocolVersion === 3 ? 3 : 0);
      }).catch(err => {
        if (!isCurrent()) return;
        if (err instanceof ApiError && err.status === 401) {
          retryable = false;
          setAuthError('');
          setUser(null);
          setNativeUserInputProtocolVersion(0);
        } else {
          retryable = true;
          setAuthError(err instanceof StatsTimeoutError ? 'timeout' : 'unavailable');
        }
      }).finally(() => {
        if (!isCurrent()) return;
        pending = null;
        setAuthChecking(false);
        setBooting(false);
        if (retryable) schedule([2000, 5000, 10000, 30000][Math.min(failures++, 3)]);
      });
    };
    const recover = () => {
      if (!canRecover()) { clearRetry(); return; }
      // Repeated browser events cannot create parallel or rapid-fire probes.
      schedule(Math.max(0, 2000 - (Date.now() - lastStart)));
    };
    retryAuthRef.current = check;
    setBooting(true);
    setAuthError('');
    check();
    window.addEventListener('online', recover);
    window.addEventListener('offline', recover);
    document.addEventListener('visibilitychange', recover);
    return () => {
      disposed = true; clearRetry(); pending?.cancel(); retryAuthRef.current = () => undefined;
      window.removeEventListener('online', recover);
      window.removeEventListener('offline', recover);
      document.removeEventListener('visibilitychange', recover);
    };
  }, [isHelpRoute, isShareRoute, isUpdatesRoute, isDesignArticleRoute]);

  useEffect(() => {
    const handlePop = () => setPath(window.location.pathname);
    window.addEventListener('popstate', handlePop);
    return () => window.removeEventListener('popstate', handlePop);
  }, []);

  useEffect(() => {
    if (!booting && user && path.startsWith('/admin') && !user.isAdmin) {
      window.history.replaceState({}, '', '/');
      setPath('/');
    }
  }, [booting, path, user]);

  const navigate = useCallback((nextPath: string, options: { replace?: boolean } = {}) => {
    if (window.location.pathname !== nextPath) {
      if (options.replace) {
        window.history.replaceState({}, '', nextPath);
      } else {
        window.history.pushState({}, '', nextPath);
      }
      setPath(nextPath);
    }
  }, []);

  const onLogin = useCallback((account: UserAccount) => {
    const generation = ++authGeneration.current;
    if (user && user.id !== account.id) {
      try { clearUserInputStorage(window.sessionStorage, user.id); } catch { /* Login must remain available. */ }
      clearAccountOrchestrationDrafts(user.id);
    }
    setAuthError('');
    setUser(account);
    setNativeUserInputProtocolVersion(0);
    void api<{ nativeUserInputProtocolVersion?: number }>('/api/me')
      .then((data) => { if (generation === authGeneration.current) setNativeUserInputProtocolVersion(data.nativeUserInputProtocolVersion === 3 ? 3 : 0); })
      .catch(() => { if (generation === authGeneration.current) setNativeUserInputProtocolVersion(0); });
  }, [user]);

  const onLogout = useCallback(() => {
    authGeneration.current += 1;
    if (user) {
      try { clearUserInputStorage(window.sessionStorage, user.id); } catch { /* Logout must remain available. */ }
      clearAccountOrchestrationDrafts(user.id);
    }
    setNativeUserInputProtocolVersion(0);
    setUser(null);
  }, [user]);

  if (booting) {
    return (
      <div className="min-h-screen w-full flex items-center justify-center bg-background text-foreground">
        <RefreshCw className="h-5 w-5 animate-spin text-muted-foreground" />
      </div>
    );
  }

  let page: React.ReactNode;
  if (isShareRoute) {
    page = <PublicSharePage shareID={decodeURIComponent(path.replace(/^\/share\/?/, '').split('/')[0] || '')} t={t} />;
  } else if (isHelpRoute) {
    page = <HelpPage language={language} setLanguage={setLanguage} isDarkMode={isDarkMode} setIsDarkMode={setIsDarkMode} />;
  } else if (isUpdatesRoute) {
    page = <UpdatesPage language={language} setLanguage={setLanguage} isDarkMode={isDarkMode} setIsDarkMode={setIsDarkMode} />;
  } else if (isDesignArticleRoute) {
    page = <OrchestrationDesignPage />;
  } else if (authError) {
    page = <div className="min-h-screen flex items-center justify-center bg-background p-6 text-foreground">
      <div className="max-w-md space-y-4 rounded-xl border border-border bg-card p-6">
        <h1 className="text-lg font-semibold">{t.authUnavailable}</h1>
        <p role="alert" className="text-sm text-muted-foreground">{authError === 'timeout' ? t.authTimedOut : t.authRetryHint}</p>
        <button type="button" className="inline-flex min-h-10 items-center gap-2 rounded-md bg-primary px-4 text-sm text-primary-foreground" disabled={authChecking} onClick={() => retryAuthRef.current()}>
          <RefreshCw className={authChecking ? "h-4 w-4 animate-spin" : "h-4 w-4"} />{t.retry}
        </button>
        <p role="status" className="text-xs text-muted-foreground">{authChecking ? (language === 'zh' ? '正在重新验证登录状态…' : 'Checking your session again…') : (language === 'zh' ? '服务恢复后将自动重试，也可手动重试。' : 'We will retry automatically when available. You can also retry manually.')}</p>
      </div>
    </div>;
  } else if (!user) {
    page = <LoginScreen onLogin={onLogin} language={language} setLanguage={setLanguage} t={t} />;
  } else if (isSnapshotRoute) {
    page = <ConversationSnapshotPage t={t} />;
  } else if (path.startsWith('/admin')) {
    if (!user.isAdmin) return null;
    const adminUserMatch = path.match(/^\/admin\/usage\/users\/([^/]+)$/);
    page = adminUserMatch
      ? <AdminUserUsagePage userID={decodeURIComponent(adminUserMatch[1])} t={t} navigate={navigate} />
      : <AdminUsagePage t={t} navigate={navigate} />;
  } else if (path.startsWith('/orchestrate')) {
    if (path.startsWith('/orchestrate/stats') || path.startsWith('/orchestrate/usage')) {
      let runId = '';
      if (path.startsWith('/orchestrate/stats')) {
        runId = new URLSearchParams(window.location.search).get('run') || '';
        if (!runId) {
          try { runId = localStorage.getItem('codexBridge.activeOrchestrationRunId') || ''; } catch { /* The remembered run is optional. */ }
        }
      }
      page = <OrchestrationStatsPage t={t} navigate={navigate} runId={runId} />;
    } else {
      page = <OrchestrationWorkspace key={user.id} user={user} nativeUserInputProtocolVersion={nativeUserInputProtocolVersion} onLogout={onLogout} isDarkMode={isDarkMode} setIsDarkMode={setIsDarkMode} language={language} setLanguage={setLanguage} t={t} canOpenMain path={path} navigate={navigate} />;
    }
  } else {
    page = <Workspace user={user} onLogout={onLogout} isDarkMode={isDarkMode} setIsDarkMode={setIsDarkMode} language={language} setLanguage={setLanguage} t={t} navigate={navigate} />;
  }
  return <Suspense fallback={<PageLoading />}>{page}</Suspense>;
}
