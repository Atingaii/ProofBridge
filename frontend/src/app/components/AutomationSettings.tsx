import React, { useEffect, useState } from 'react';
import { AlertCircle, Check, ChevronDown, Copy, Eye, EyeOff, Loader2, RefreshCw, Terminal, Trash2 } from 'lucide-react';
import { api, ApiError } from '../lib/api';
import { copyText } from '../lib/utils';
import type { Agent, Language } from '../lib/types';
import { Button, Input } from './ui';

export type AutomationAccess = {
  token: string | null;
  mcpUrl: string;
  codexConfig: string;
  claudeConfig: string;
  skillInstallCommands: { codex: string; claude: string };
  mailReady: boolean;
  email?: string;
};

export type AutomationPreferenceData = {
  email?: string;
  completionEnabled?: boolean;
  attentionEnabled?: boolean;
  defaultEndpointId?: string;
  defaultPresetId?: string;
  monitoringMode?: 'interactive' | 'background-mail';
  [key: string]: unknown;
};
export type AutomationPreferences = { scope: string; version: number; data: AutomationPreferenceData };

const text = {
  zh: {
    title: '终端与邮件', hint: '一次接入，在 Codex 或 Claude Code 中用自然语言调用 ProofBridge。',
    loading: '正在读取接入配置…', retry: '重新加载', unavailable: '无法读取接入配置。检查登录和网络后重试。',
    client: '终端客户端', address: 'MCP 地址', token: '个人 MCP Token', reveal: '显示 Token', hide: '隐藏 Token',
    copyToken: '复制 Token', copyConfig: '复制 MCP 配置', config: 'MCP 配置', installer: 'Skill 安装命令',
    copyInstaller: '复制 Skill 安装命令', copied: '已复制。', copyFailed: '复制失败，请选中内容后手动复制。',
    secretHint: '复制的配置包含个人凭据，请放入个人客户端配置。',
    installHint: '在终端运行安装命令，然后粘贴 MCP 配置；必要时重启客户端。',
    rotate: '重新生成 Token', revoke: '撤销 Token',
    rotateConfirm: '重新生成后，原来的 MCP 配置会失效。是否继续？',
    revokeConfirm: '撤销后，当前 MCP 客户端将无法继续调用。运行中的证明任务不会因此取消。是否继续？',
    revoked: 'Token 已撤销。需要再次接入时，重新生成并复制配置。',
    emailTitle: '邮件与默认设置', email: '收件邮箱', emailHint: '报告只发送到此账户绑定的邮箱。',
    completion: '任务结束时发送报告', attention: '需要审批或回答时发送提醒',
    smtpUnavailable: '管理员尚未配置可用邮件服务。可以先保存邮箱并使用交互等待；后台邮件模式准备好后再启用。',
    smtpReady: '邮件服务已配置。实际投递结果可在任务中查询。',
    defaultEndpoint: '默认执行端点', chooseEndpoint: '每次任务按要求选择',
    mode: '默认结果接收方式', interactive: '在终端交互等待', background: '提交后邮件通知',
    backgroundNeedsMail: '后台邮件模式需要可用邮件服务、收件邮箱，以及结束和待处理通知。请补齐设置，或选择交互等待。',
    save: '保存设置', saved: '设置已保存。两个终端客户端将共用这些默认值。',
    conflict: '设置已在其他客户端更新。请重新加载后再保存。',
    endpointHint: '项目仍在选中的用户机器上执行。Hub 部署在实验室内网时，外部客户端或执行端需通过 VPN 连接；内网机器可直接连接。',
    presetHint: '可在终端用自然语言保存模型组合或设为项目默认，不需要在这里重复填写。',
    help: '帮助', feedback: '反馈问题', refreshing: '正在处理…', offline: '离线', endpointUnavailable: '之前的默认端点已不可用，请重新选择。',
  },
  en: {
    title: 'Terminal & email', hint: 'Connect once, then use ProofBridge from Codex or Claude Code.',
    loading: 'Loading connection settings…', retry: 'Reload', unavailable: 'Unable to load connection settings. Check your session and connection, then retry.',
    client: 'CLI client', address: 'MCP address', token: 'Personal MCP Token', reveal: 'Show token', hide: 'Hide token',
    copyToken: 'Copy token', copyConfig: 'Copy MCP configuration', config: 'MCP configuration', installer: 'Skill install command',
    copyInstaller: 'Copy Skill install command', copied: 'Copied.', copyFailed: 'Copy failed. Select the text and copy it manually.',
    secretHint: 'The copied configuration contains personal credentials. Save it in your personal client configuration.',
    installHint: 'Run the install command, then paste the MCP configuration. Restart the client if needed.',
    rotate: 'Regenerate token', revoke: 'Revoke token',
    rotateConfirm: 'Regenerating invalidates the previous MCP configuration. Continue?',
    revokeConfirm: 'Revoking prevents connected MCP clients from making further calls. It does not cancel running proof tasks. Continue?',
    revoked: 'Token revoked. Regenerate it and copy the configuration to connect again.',
    emailTitle: 'Email & defaults', email: 'Report email', emailHint: 'Reports go only to the address bound to this account.',
    completion: 'Email a report when work ends', attention: 'Email when approval or an answer is needed',
    smtpUnavailable: 'The administrator has not configured a ready mail service. Save your address and use interactive monitoring until background mail is ready.',
    smtpReady: 'Mail service configured. Check each task for its delivery status.',
    defaultEndpoint: 'Default execution endpoint', chooseEndpoint: 'Select from the task request',
    mode: 'Default monitoring', interactive: 'Stay in the terminal', background: 'Submit and email me',
    backgroundNeedsMail: 'Background mail requires a ready mail service, an email address, and completion and attention notifications. Complete setup or choose interactive monitoring.',
    save: 'Save settings', saved: 'Settings saved. Both CLI clients use these defaults.',
    conflict: 'Another client updated these settings. Reload before saving.',
    endpointHint: 'Projects run on the selected user machine. With a lab-only Hub, external clients or execution endpoints need VPN access; machines inside the lab connect directly.',
    presetHint: 'Use natural language in the terminal to save a model combination or a project default.',
    help: 'Help', feedback: 'Report an issue', refreshing: 'Working…', offline: 'offline', endpointUnavailable: 'The previous default endpoint is unavailable. Choose another endpoint.',
  },
};

export function AutomationSettings({ userId, language, agents }: { userId: string; language: Language; agents: Agent[] }) {
  const t = text[language];
  const [open, setOpen] = useState(false);
  const [client, setClient] = useState<'codex' | 'claude'>('codex');
  const [access, setAccess] = useState<AutomationAccess | null>(null);
  const [preferences, setPreferences] = useState<AutomationPreferences | null>(null);
  const [data, setData] = useState<AutomationPreferenceData>({});
  const [loading, setLoading] = useState(false);
  const [reload, setReload] = useState(0);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const [copied, setCopied] = useState('');
  const [revealed, setRevealed] = useState(false);

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    setLoading(true);
    setError('');
    Promise.all([
      api<AutomationAccess>('/api/automation/access', { signal: controller.signal }),
      api<AutomationPreferences>('/api/automation/preferences?scope=account', { signal: controller.signal }),
    ]).then(([nextAccess, nextPreferences]) => {
      if (controller.signal.aborted) return;
      setAccess(nextAccess);
      setPreferences(nextPreferences);
      setData(nextPreferences.data || {});
      setMessage('');
      setCopied('');
    }).catch((err: unknown) => {
      if (!controller.signal.aborted) setError(err instanceof Error ? err.message : 'AUTOMATION_LOAD_FAILED');
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false);
    });
    return () => controller.abort();
  }, [open, userId, reload]);

  const copy = async (value: string, name: string) => {
    setError('');
    setMessage('');
    try {
      if (!await copyText(value)) throw new Error(t.copyFailed);
      setCopied(name);
      setMessage(t.copied);
    } catch {
      setError(t.copyFailed);
    }
  };
  const changeToken = async (action: 'rotate' | 'revoke') => {
    if (!window.confirm(action === 'rotate' ? t.rotateConfirm : t.revokeConfirm)) return;
    setBusy(action);
    setError('');
    setMessage('');
    try {
      const next = await api<AutomationAccess>('/api/automation/access/' + action, { method: 'POST' });
      setAccess(next);
      setCopied('');
      setRevealed(false);
      setMessage(action === 'revoke' ? t.revoked : '');
    } catch (err) {
      setError(err instanceof Error ? err.message : t.unavailable);
    } finally {
      setBusy('');
    }
  };
  const save = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (!preferences) return;
    if (data.monitoringMode === 'background-mail' &&
      (!access?.mailReady || !data.email?.trim() || !data.completionEnabled || !data.attentionEnabled)) {
      setError(t.backgroundNeedsMail);
      return;
    }
    setBusy('save');
    setError('');
    setMessage('');
    try {
      const next = await api<AutomationPreferences>('/api/automation/preferences', {
        method: 'PUT',
        body: JSON.stringify({
          scope: 'account', expectedVersion: preferences.version,
          data: { ...data, email: data.email?.trim() || '', monitoringMode: data.monitoringMode || 'interactive' },
        }),
      });
      setPreferences(next);
      setData(next.data || {});
      setMessage(t.saved);
    } catch (err) {
      setError(err instanceof ApiError && err.status === 409 ? t.conflict : err instanceof Error ? err.message : t.unavailable);
    } finally {
      setBusy('');
    }
  };
  const config = access ? client === 'codex' ? access.codexConfig : access.claudeConfig : '';
  const preview = !revealed && access?.token ? config.split(access.token).join('••••••••') : config;
  const installCommand = access?.skillInstallCommands?.[client] || '';
  const disabled = loading || Boolean(busy);

  return <section className="min-w-0 space-y-3 max-sm:[&_button]:min-h-[48px] max-sm:[&_button]:min-w-[48px] max-sm:[&_input:not([type=checkbox])]:min-h-[48px] max-sm:[&_select]:min-h-[48px]" aria-label={t.title} data-testid="automation-settings">
    <button type="button" aria-expanded={open} onClick={() => setOpen(!open)}
      className="flex w-full items-center gap-2 rounded-md py-2 text-left text-xs font-semibold uppercase tracking-wider text-muted-foreground">
      <Terminal className="h-4 w-4" /><span>{t.title}</span><ChevronDown className={'ml-auto h-4 w-4 transition-transform ' + (open ? 'rotate-180' : '')} />
    </button>
    {open && <div className="min-w-0 space-y-4 rounded-lg border border-border bg-muted/20 p-3">
      <p className="text-xs leading-relaxed text-muted-foreground">{t.hint}</p>
      {loading && <p role="status" className="flex items-center gap-2 text-xs"><Loader2 className="h-3.5 w-3.5 animate-spin" />{t.loading}</p>}
      {error && <div role="alert" className="space-y-2 rounded-md border border-destructive/30 bg-destructive/5 p-2 text-xs text-destructive">
        <p className="flex gap-1.5"><AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" /><span>{error === 'AUTOMATION_LOAD_FAILED' ? t.unavailable : error}</span></p>
        <Button type="button" size="sm" variant="secondary" disabled={disabled} onClick={() => setReload((value) => value + 1)}>{t.retry}</Button>
      </div>}
      {message && <p role="status" className="text-xs text-emerald-700 dark:text-emerald-400">{message}</p>}
      {access && <>
        <label className="block space-y-1 text-xs">
          <span>{t.client}</span>
          <select aria-label={t.client} value={client} onChange={(event) => setClient(event.target.value === 'claude' ? 'claude' : 'codex')}
            className="h-9 w-full rounded-md border border-input bg-background px-2 text-sm">
            <option value="codex">Codex</option><option value="claude">Claude Code</option>
          </select>
        </label>
        <label className="block space-y-1 text-xs"><span>{t.address}</span><Input readOnly value={access.mcpUrl || ''} className="min-w-0 text-xs" /></label>
        <div className="space-y-1">
          <label htmlFor="automation-mcp-token" className="text-xs">{t.token}</label>
          <div className="flex min-w-0 gap-1">
            <Input id="automation-mcp-token" autoComplete="off" readOnly type={revealed ? 'text' : 'password'}
              value={access.token || ''} className="min-w-0 flex-1 font-mono text-xs" />
            <Button type="button" size="icon" variant="ghost" aria-label={revealed ? t.hide : t.reveal}
              aria-pressed={revealed} disabled={!access.token} onClick={() => setRevealed(!revealed)}>
              {revealed ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
            </Button>
            <Button type="button" size="icon" variant="ghost" aria-label={t.copyToken} disabled={!access.token}
              onClick={() => copy(access.token || '', 'token')}>
              {copied === 'token' ? <Check className="h-4 w-4" /> : <Copy className="h-4 w-4" />}
            </Button>
          </div>
        </div>
        {!access.token && <p className="text-xs text-muted-foreground">{t.revoked}</p>}
        {access.token && <div className="min-w-0 space-y-2">
          <div className="flex items-center justify-between gap-2">
            <span className="text-xs font-medium">{t.config}</span>
            <Button type="button" variant="secondary" size="sm" disabled={!config} onClick={() => copy(config, 'config')}>
              {copied === 'config' ? <Check className="mr-1 h-3.5 w-3.5" /> : <Copy className="mr-1 h-3.5 w-3.5" />}{t.copyConfig}
            </Button>
          </div>
          <pre data-testid="automation-config-preview" className="max-h-36 overflow-auto whitespace-pre-wrap break-all rounded-md border border-border bg-background p-2 font-mono text-[11px]">{preview}</pre>
          <p className="text-[11px] text-muted-foreground">{t.secretHint}</p>
        </div>}
        <div className="flex flex-wrap gap-2">
          <Button type="button" size="sm" variant="secondary" disabled={disabled} onClick={() => changeToken('rotate')}>
            <RefreshCw className={'mr-1 h-3.5 w-3.5 ' + (busy === 'rotate' ? 'animate-spin' : '')} />{t.rotate}
          </Button>
          <Button type="button" size="sm" variant="ghost" disabled={disabled || !access.token} onClick={() => changeToken('revoke')}>
            <Trash2 className="mr-1 h-3.5 w-3.5" />{t.revoke}
          </Button>
        </div>
        <div className="min-w-0 space-y-2">
          <div className="flex items-center justify-between gap-2">
            <span className="text-xs font-medium">{t.installer}</span>
            <Button type="button" size="sm" variant="ghost" aria-label={t.copyInstaller} disabled={!installCommand} onClick={() => copy(installCommand, 'installer')}>
              {copied === 'installer' ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            </Button>
          </div>
          <pre className="max-h-24 overflow-auto whitespace-pre-wrap break-all rounded-md border border-border bg-background p-2 font-mono text-[11px]">{installCommand}</pre>
          <p className="text-[11px] text-muted-foreground">{t.installHint}</p>
        </div>
        <p className="text-xs leading-relaxed text-muted-foreground">{t.endpointHint}</p>
        <p className="text-xs leading-relaxed text-muted-foreground">{t.presetHint}</p>
        {preferences && <form onSubmit={save} className="space-y-3 border-t border-border pt-3">
          <h4 className="text-xs font-semibold">{t.emailTitle}</h4>
          <p className={'rounded-md p-2 text-xs leading-relaxed ' + (access.mailReady ? 'bg-emerald-500/10 text-emerald-800 dark:text-emerald-300' : 'bg-amber-500/10 text-amber-900 dark:text-amber-300')}>
            {access.mailReady ? t.smtpReady : t.smtpUnavailable}
          </p>
          <label className="block space-y-1 text-xs"><span>{t.email}</span>
            <Input type="email" autoComplete="email" value={data.email || ''} onChange={(event) => setData({ ...data, email: event.target.value })} />
          </label>
          <p className="text-[11px] text-muted-foreground">{t.emailHint}</p>
          <label className="flex items-start gap-2 text-xs max-sm:min-h-[48px] max-sm:items-center"><input type="checkbox" checked={Boolean(data.completionEnabled)} onChange={(event) => setData({ ...data, completionEnabled: event.target.checked })} />{t.completion}</label>
          <label className="flex items-start gap-2 text-xs max-sm:min-h-[48px] max-sm:items-center"><input type="checkbox" checked={Boolean(data.attentionEnabled)} onChange={(event) => setData({ ...data, attentionEnabled: event.target.checked })} />{t.attention}</label>
          <label className="block space-y-1 text-xs"><span>{t.defaultEndpoint}</span>
            <select aria-label={t.defaultEndpoint} value={data.defaultEndpointId || ''} onChange={(event) => setData({ ...data, defaultEndpointId: event.target.value })}
              className="h-9 w-full rounded-md border border-input bg-background px-2 text-sm">
              <option value="">{t.chooseEndpoint}</option>
              {Boolean(data.defaultEndpointId) && !agents.some((agent) => agent.id === data.defaultEndpointId) && <option value={data.defaultEndpointId}>{t.endpointUnavailable}</option>}
              {agents.map((agent) => <option key={agent.id} value={agent.id}>{agent.name + (agent.online ? '' : ' (' + t.offline + ')')}</option>)}
            </select>
          </label>
          <label className="block space-y-1 text-xs"><span>{t.mode}</span>
            <select aria-label={t.mode} value={data.monitoringMode || 'interactive'} onChange={(event) => setData({ ...data, monitoringMode: event.target.value === 'background-mail' ? 'background-mail' : 'interactive' })}
              className="h-9 w-full rounded-md border border-input bg-background px-2 text-sm">
              <option value="interactive">{t.interactive}</option><option value="background-mail">{t.background}</option>
            </select>
          </label>
          <Button type="submit" size="sm" disabled={disabled}>{busy === 'save' ? t.refreshing : t.save}</Button>
        </form>}
      </>}
      <div className="flex gap-3 text-xs">
        <a href="/help" className="text-primary underline underline-offset-2">{t.help}</a>
        <a href="https://github.com/Atingaii/ProofBridge/issues" target="_blank" rel="noreferrer" className="text-primary underline underline-offset-2">{t.feedback}</a>
      </div>
    </div>}
  </section>;
}
