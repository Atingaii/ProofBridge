import React, { useEffect, useState } from 'react';
import type { Language } from '../lib/i18n';

declare const __PROOFBRIDGE_FRONTEND_BUILD__: { builtAt: string } | undefined;

const frontendBuild = typeof __PROOFBRIDGE_FRONTEND_BUILD__ === 'undefined'
  ? undefined
  : __PROOFBRIDGE_FRONTEND_BUILD__;

type HubHealth = { state: 'loading' } | { state: 'unavailable' } | { state: 'ready'; version: string };

// Public help must work without a login or a reachable Hub. This reports only
// the live Hub; the embedded frontend and each private Bridge have their own builds.
export function SiteBuildInfo({ language }: { language: Language }) {
  const [health, setHealth] = useState<HubHealth>({ state: 'loading' });
  const [attempt, setAttempt] = useState(0);
  const text = (zh: string, en: string) => language === 'zh' ? zh : en;

  useEffect(() => {
    const controller = new AbortController();
    let mounted = true;
    const timeout = window.setTimeout(() => controller.abort(), 5000);
    setHealth({ state: 'loading' });
    void (async () => {
      try {
        const response = await fetch('/health', { signal: controller.signal, cache: 'no-store' });
        if (!response.ok) throw new Error('Hub unavailable');
        const data: unknown = await response.json();
        if (!data || typeof data !== 'object' || !('status' in data) || data.status !== 'ok'
          || !('version' in data) || typeof data.version !== 'string' || !data.version.trim()) {
          throw new Error('Missing Hub version');
        }
        if (mounted) setHealth({ state: 'ready', version: data.version.trim() });
      } catch {
        if (mounted) setHealth({ state: 'unavailable' });
      } finally {
        window.clearTimeout(timeout);
      }
    })();
    return () => {
      mounted = false;
      window.clearTimeout(timeout);
      controller.abort();
    };
  }, [attempt]);

  return (
    <section id="build-info" aria-label={text('页面与服务版本', 'Page and service versions')} className="mt-8 rounded-md border border-border bg-muted/25 p-4">
      <h2 className="text-sm font-semibold">{text('页面与服务版本', 'Page and service versions')}</h2>
      <dl className="mt-3 grid gap-4 text-xs sm:grid-cols-2">
        <div>
          <dt className="text-muted-foreground">{text('前端构建时间（UTC）', 'Frontend build time (UTC)')}</dt>
          <dd className="mt-1 break-words font-mono" data-testid="frontend-build">
            {frontendBuild?.builtAt ? <time dateTime={frontendBuild.builtAt}>{frontendBuild.builtAt}</time> : text('构建信息不可用', 'Build metadata unavailable')}
          </dd>
          <dd className="mt-1 text-muted-foreground">{import.meta.env.DEV && text('开发预览 · ', 'Development preview · ')}{import.meta.env.VITE_LEADING === 'true' ? text('动态执行界面', 'Dynamic execution interface') : text('标准界面', 'Standard interface')}</dd>
        </div>
        <div>
          <dt className="text-muted-foreground">{text('Hub 版本（当前站点 /health）', 'Hub version (this site’s /health)')}</dt>
          <dd className="mt-1 break-words" aria-live="polite" data-testid="hub-version">
            {health.state === 'ready' ? <span className="font-mono">{health.version}</span> : health.state === 'loading' ? text('正在查询…', 'Checking…') : (
              <>{text('暂时无法获取', 'Currently unavailable')} <button type="button" className="ml-2 underline underline-offset-2 hover:text-primary" onClick={() => setAttempt((value) => value + 1)}>{text('重试', 'Retry')}</button></>
            )}
          </dd>
        </div>
      </dl>
      <p className="mt-3 text-xs leading-5 text-muted-foreground">{text('前端构建、Hub 和各端点 Bridge 独立更新。历史发布记录不代表本站部署版本；端点版本以各自连接上报为准，本页不查询端点。', 'The frontend, Hub and each endpoint’s Bridge update independently. Release notes do not identify this site’s deployed build. Each endpoint reports its own version when connected; this page does not query endpoints.')}</p>
    </section>
  );
}
