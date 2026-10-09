import React, { useCallback, useEffect, useRef, useState } from 'react';
import { AlertCircle, Check, ChevronDown, Clock3, MessageSquare, RefreshCw, Send, Zap } from 'lucide-react';
import { api } from '../lib/api';
import { cn } from '../lib/utils';
import type { OrchestrationPromptMessage } from '../lib/types';
import { Button } from './ui';

// The server is authoritative: accepting HTTP input does not mean the CLI received it.
export function OrchestrationLiveInput({ runId, running, refreshRevision, supported, online, language, prompt, setPrompt, onPromptAccepted, continuing, onContinue, hasFiles, continueDisabled }: {
  runId: string; running: boolean; supported: boolean; online: boolean; language: string;
  refreshRevision: number;
  prompt: string; setPrompt: (value: string) => void; continuing: boolean; onContinue: () => Promise<void>;
  onPromptAccepted: () => boolean;
  hasFiles: boolean; continueDisabled: boolean;
}) {
  const zh = language === 'zh';
  const [messages, setMessages] = useState<OrchestrationPromptMessage[]>([]);
  const [sending, setSending] = useState(false);
  const [historyExpanded, setHistoryExpanded] = useState(false);
  const [error, setError] = useState('');
  const [queryError, setQueryError] = useState('');
  const mounted = useRef(true);
  const submitting = useRef(false);
  const querySequence = useRef(0);
  const appliedQuerySequence = useRef(0);
  const queryControllers = useRef(new Set<AbortController>());
  const pending = useRef<{ messageId: string; deliveryMode: 'queue' | 'immediate'; prompt: string } | null>(null);
  const endpoint = `/api/orchestrations/${encodeURIComponent(runId)}/prompts`;
  const refresh = useCallback(async () => {
    const sequence = ++querySequence.current;
    const controller = new AbortController();
    queryControllers.current.add(controller);
    const timeout = window.setTimeout(() => controller.abort(), 15000);
    try {
      const data = await api<{ messages: OrchestrationPromptMessage[] }>(endpoint, { cache: 'no-store', signal: controller.signal });
      if (!Array.isArray(data.messages)) throw new Error(zh ? '后台尚不支持追加指令状态，请更新 Hub。' : 'Update the Hub to read message delivery status.');
      // A newer request being in flight must not invalidate a useful response.
      if (mounted.current && sequence > appliedQuerySequence.current) {
        appliedQuerySequence.current = sequence;
        setMessages(data.messages); setQueryError('');
      }
      return data.messages;
    } catch (err) {
      if (mounted.current && sequence > appliedQuerySequence.current) setQueryError(controller.signal.aborted
        ? (zh ? '查询超时，正在自动重试。' : 'Query timed out. Retrying automatically.')
        : err instanceof Error ? err.message : String(err));
      return null;
    } finally {
      window.clearTimeout(timeout);
      queryControllers.current.delete(controller);
    }
  }, [endpoint, zh]);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      appliedQuerySequence.current = ++querySequence.current;
      queryControllers.current.forEach((controller) => controller.abort());
      queryControllers.current.clear();
    };
  }, []);
  useEffect(() => {
    let stopped = false;
    let timer: number | undefined;
    const poll = async () => {
      await refresh();
      if (!stopped) timer = window.setTimeout(poll, 3000);
    };
    void poll();
    const onVisibility = () => { if (document.visibilityState === 'visible') void refresh(); };
    document.addEventListener('visibilitychange', onVisibility);
    return () => { stopped = true; window.clearTimeout(timer); document.removeEventListener('visibilitychange', onVisibility); };
  }, [refresh, refreshRevision, running]);
  const send = async (deliveryMode: 'queue' | 'immediate') => {
    const text = prompt.trim();
    if (!text || submitting.current || !online || !supported || hasFiles) return;
    submitting.current = true;
    setSending(true); setError('');
    // Reuse the ID when retrying an ambiguous HTTP outcome. Never silently resend with a new ID.
    const previous = pending.current;
    const body = previous?.prompt === text && previous.deliveryMode === deliveryMode ? previous
      : { messageId: crypto.randomUUID(), deliveryMode, prompt: text };
    pending.current = body;
    try {
      await api(endpoint, { method: 'POST', body: JSON.stringify(body) });
      onPromptAccepted();
      pending.current = null;
      await refresh();
    } catch (err) {
      const recovered = await refresh();
      if (recovered?.some((message) => message.id === body.messageId)) {
        onPromptAccepted();
        pending.current = null;
      } else if (mounted.current) setError(err instanceof Error ? err.message : String(err));
    } finally { submitting.current = false; if (mounted.current) setSending(false); }
  };
  const unavailable = !online ? (zh ? '运行机器已离线，重新连接后可追加指令。' : 'The machine is offline. Reconnect to send instructions.')
    : !supported ? (zh ? '此机器的 Bridge 尚不支持运行中追加指令，请更新本机 Bridge。' : 'Update this machine’s Bridge to send instructions during a run.')
    : hasFiles ? (zh ? '运行中仅支持文字追加。请移除待发送附件，或在运行结束后连同附件继续。' : 'Live instructions support text only. Remove pending files, or continue with attachments after the run.') : '';
  const statusText = (status: OrchestrationPromptMessage['status']) => ({ queued: zh ? '排队中' : 'Queued', sending: zh ? '等待 CLI 接收' : 'Awaiting CLI receipt', delivered: zh ? '已送达 CLI' : 'Delivered to CLI', failed: zh ? '发送失败' : 'Failed' })[status];
  const waitingCount = messages.filter((message) => message.status === 'queued' || message.status === 'sending').length;
  const failedCount = messages.filter((message) => message.status === 'failed').length;
  const displayedMessages = historyExpanded ? messages : messages.slice(-1);
  return <section className="shrink-0 border-t border-border/70 bg-background px-3 pb-3 pt-2.5 md:px-6 md:pb-4" aria-label={zh ? '运行对话' : 'Run conversation'}>
    <div className="mb-2 flex min-h-8 items-center justify-between gap-2">
      <div className="flex min-w-0 items-center gap-2">
        <MessageSquare aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        <h2 className="text-xs font-medium">{zh ? '运行对话' : 'Run conversation'}</h2>
        {waitingCount > 0 && <span className="rounded-full bg-amber-100 px-2 py-0.5 text-[11px] text-amber-900 dark:bg-amber-950 dark:text-amber-200">{waitingCount} {zh ? '条待送达' : 'pending'}</span>}
        {failedCount > 0 && <span className="text-[11px] text-destructive">{failedCount} {zh ? '条失败' : 'failed'}</span>}
      </div>
      <div className="flex shrink-0 items-center gap-1">
        {messages.length > 1 && <button type="button" onClick={() => setHistoryExpanded(!historyExpanded)} aria-expanded={historyExpanded} aria-controls={`instruction-history-${runId}`}
          className="inline-flex min-h-9 items-center gap-1 rounded-md px-2 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
          {zh ? `记录 ${messages.length}` : `History ${messages.length}`}<ChevronDown aria-hidden="true" className={cn('h-3.5 w-3.5 transition-transform', historyExpanded && 'rotate-180')} />
        </button>}
        <Button type="button" variant="ghost" size="icon" className="h-9 w-9 text-muted-foreground" aria-label={zh ? '刷新送达状态' : 'Refresh delivery'} title={zh ? '刷新送达状态' : 'Refresh delivery'} onClick={() => void refresh()}><RefreshCw aria-hidden="true" className="h-3.5 w-3.5" /></Button>
      </div>
    </div>
    {messages.length > 0 && <ol id={`instruction-history-${runId}`} aria-label={zh ? '追加指令记录' : 'Instruction history'} className="mb-2 max-h-32 space-y-1 overflow-y-auto rounded-lg bg-muted/60 p-1 elegant-scrollbar">
      {displayedMessages.map((message) => {
        const StatusIcon = message.status === 'delivered' ? Check : message.status === 'failed' ? AlertCircle : message.status === 'sending' ? RefreshCw : Clock3;
        return <li key={message.id} className="px-2 py-1.5 text-xs" data-message-id={message.id}>
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span className={cn('inline-flex items-center gap-1 font-medium', message.status === 'delivered' ? 'text-emerald-700 dark:text-emerald-400' : message.status === 'failed' ? 'text-destructive' : 'text-amber-700 dark:text-amber-300')}>
              <StatusIcon aria-hidden="true" className={cn('h-3 w-3', message.status === 'sending' && 'animate-spin motion-reduce:animate-none')} />{statusText(message.status)}
            </span>
            <span className="text-[11px] text-muted-foreground">{message.deliveryMode === 'immediate' ? (zh ? '立即发送' : 'Immediate') : (zh ? '排队发送' : 'Queued')}</span>
          </div>
          <p className="mt-1 whitespace-pre-wrap break-words leading-relaxed text-foreground/80">{message.prompt}</p>
          {message.error && <p className="mt-1 break-words text-destructive">{message.error}</p>}
        </li>;
      })}
    </ol>}
    {queryError && <p role="status" className="mb-2 text-xs text-amber-700 dark:text-amber-300">{zh ? '暂时无法确认送达状态：' : 'Unable to confirm delivery: '}{queryError}</p>}
    <div className="overflow-hidden rounded-xl border border-border bg-card shadow-sm transition-colors focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/10">
      <label className="block"><span className="sr-only">{zh ? '追加指令' : 'Additional instruction'}</span>
        <textarea id="orchestration-live-input" value={prompt} onChange={(event) => setPrompt(event.target.value)}
          placeholder={running ? (zh ? '追加要求或纠正方向…' : 'Add requirements or redirect the work…') : (zh ? '继续这个任务…' : 'Continue this task…')}
          className="block min-h-20 w-full resize-y border-0 bg-transparent px-3.5 pb-2 pt-3 text-sm leading-relaxed placeholder:text-muted-foreground/75 focus:outline-none disabled:opacity-60 max-h-48"
          onKeyDown={(event) => { if ((event.ctrlKey || event.metaKey) && event.key === 'Enter' && !event.nativeEvent.isComposing) { event.preventDefault(); if (running) void send('queue'); else if (!continueDisabled) void onContinue(); } }} />
      </label>
      <div className="flex flex-wrap items-center justify-end gap-2 px-2.5 pb-2.5">
        <span className="mr-auto hidden text-[11px] text-muted-foreground sm:inline">{zh ? '⌘ / Ctrl + Enter 发送' : '⌘ / Ctrl + Enter to send'}</span>
        {running ? <>
          <Button type="button" variant="ghost" size="sm" className="min-h-10 gap-1.5 rounded-lg px-3 text-muted-foreground hover:text-foreground sm:min-h-9" title={zh ? 'Codex 会在当前响应中接收新指令；Claude 会中断当前步骤，再在原会话继续。' : 'Codex receives guidance during its current response. Claude interrupts the current step and continues in the same session.'} onClick={() => void send('immediate')} disabled={!prompt.trim() || sending || Boolean(unavailable)}><Zap aria-hidden="true" className="h-3.5 w-3.5" />{zh ? '立即发送' : 'Send immediately'}</Button>
          <Button type="button" size="sm" className="min-h-10 gap-1.5 rounded-lg px-3.5 sm:min-h-9" onClick={() => void send('queue')} disabled={!prompt.trim() || sending || Boolean(unavailable)}>{sending ? <RefreshCw aria-hidden="true" className="h-3.5 w-3.5 animate-spin motion-reduce:animate-none" /> : <Send aria-hidden="true" className="h-3.5 w-3.5" />}{zh ? '排队发送' : 'Queue message'}</Button>
        </> : <Button type="button" size="sm" className="min-h-10 gap-1.5 rounded-lg px-3.5 sm:min-h-9" onClick={() => void onContinue()} disabled={!prompt.trim() || continuing || continueDisabled}><Send aria-hidden="true" className="h-3.5 w-3.5" />{zh ? '继续运行' : 'Continue run'}</Button>}
      </div>
    </div>
    {running && unavailable && <p role="status" className="mt-2 text-xs text-amber-700 dark:text-amber-300">{unavailable}</p>}
    {error && <p role="alert" className="mt-2 break-words text-xs text-destructive">{error}</p>}
    <p className="mt-2 px-0.5 text-[11px] leading-relaxed text-muted-foreground">{running ? (zh ? '排队将在当前步骤结束后处理 · 状态自动更新 · 送达不代表已执行' : 'Queued after the current step · Status updates automatically · Delivered does not mean executed') : (zh ? '继续使用当前运行与会话上下文' : 'Continues this run with its existing context')}</p>
  </section>;
}
