import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Activity,
  AlertCircle,
  Archive,
  ArchiveRestore,
  ArrowDownToLine,
  ArrowLeft,
  BookOpen,
  Check,
  ChevronDown,
  Command,
  FileUp,
  FolderInput,
  GitBranch,
  HelpCircle,
  History,
  Maximize2,
  Menu,
  MessageSquare,
  MoreHorizontal,
  Plus,
  Pencil,
  RefreshCw,
  Send,
  Server,
  Settings,
  Share2,
  ShieldCheck,
  ShieldQuestion,
  Square,
  BarChart3,
  PieChart,
  Swords,
  Trash2,
  UsersRound,
  Workflow,
} from 'lucide-react';
import { api } from '../lib/api';
import { beginStatsRequest, StatsTimeoutError } from '../lib/statsRequest';
import { createRunReadRequests } from '../lib/runReadRequests';
import { useOrchestrationDraft } from '../lib/orchestrationDrafts';
import type {
  Agent,
  ApprovalItemState,
  ApprovalRequest,
  CLIConfigPreset,
  Envelope,
  NativeContextCompaction,
  OfficialCLIModel,
  OrchestrationEvent,
  OrchestrationTaskGraph,
  OrchestrationProgress,
  OrchestrationRun,
  ShareInfo,
  UploadAttachment,
  UserAccount,
  UserInputAnswer,
  UserInputPendingSubmission,
  VerificationMode,
  WorkerPair,
} from '../lib/types';
import type { Language, UIText } from '../lib/i18n';
import { MobileSurface, MobileViewport } from '../components/MobileSurface';
import { AgentSelector } from '../components/AgentSelector';
import { OrchestrationFileRow } from '../components/OrchestrationFiles';
import { OrchestrationProgressMap } from '../components/OrchestrationProgressMap';
import { OrchestrationProofProgress } from '../components/OrchestrationProofProgress';
import {
  CapabilityMatrix,
  RunConclusionCard,
  OrchestrationTimelineGroupItem,
  defaultCollapsedTimelineGroups,
  reconcileCollapsedTimelineGroups,
} from '../components/OrchestrationComponents';
import { OrchestrationLiveInput } from '../components/OrchestrationLiveInput';
import { SettingsModal } from '../components/Settings';
import { UserInputCard } from '../components/UserInputCard';
import { clearUserInputStorage, loadUserInputStorage, saveUserInputStorage, confirmPendingUserInputs, decodeUserInputRequest, decodeUserInputSnapshot, decodeUserInputStatus, decodeUserInputSyncError, emptyUserInputCollection, receiveUserInputRequest, receiveUserInputSnapshot, receiveUserInputStatus, submittingUserInput } from '../lib/userInput';
import type { UserInputCollection } from '../lib/userInput';
import { Button, Input } from '../components/ui';
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '../components/ui/dialog';
import {
  activeOrchestrationRunStorageKey,
  activeOrchestrationStatus,
  terminalOrchestrationStatus,
  applyOrchestrationEventToRun,
  approvalStatusFromDecision,
  canCancelOrchestrationStatus,
  compareOrchestrationEvents,
  cn,
  copyText,
  isNearBottom,
  isOrchestrationRun,
  mergeOrchestrationEvents,
  mergeOrchestrationFiles,
  normalizeOrchestrationWorkerPair,
  orchestrationApprovalMode,
  orchestrationParticipantLabel,
  orchestrationCapabilityProblems,
  orchestrationRunFilesFromEvents,
  orchestrationTimelineGroups,
  orchestrationTimelineItems,
  orchestrationTurnInfoFromEvents,
  orchestrationTurnLabel,
  orchestrationWorkerLabel,
  preferredAgentID,
  readUploadAttachment,
  forgetActiveOrchestrationRunForAgent,
  rememberActiveOrchestrationRunForAgent,
  sessionDateLabel,
  formatDuration,
  startWSHeartbeat,
  titleFromPrompt,
  updateApprovalItemStatus,
  upsertApprovalItem,
  upsertOrchestrationRun,
  visibleOrchestrationEvents,
} from '../lib/utils';

const leadingEdition = import.meta.env.VITE_LEADING === 'true';
const officialCodexModelsEdition = import.meta.env.VITE_OFFICIAL_CODEX_MODELS === 'true';
const defaultOrchestrationMaxTurns = leadingEdition ? 2 : 6;
const orchestrationInitialEventPageSize = 100;
const orchestrationEventPageSize = 300;
const OFFICIAL_LOGIN_PRESET_ID = '__official__';

type RunHistoryPage = { key: string; ids: string[]; nextCursor: string; loading: boolean; error: boolean };

function readWorkspaceSelection(key: string) {
  try { return localStorage.getItem(key) || ''; } catch { return ''; }
}

function saveWorkspaceSelection(key: string, value: string) {
  try {
    if (value) localStorage.setItem(key, value);
    else localStorage.removeItem(key);
  } catch { /* Selection and sending must work without browser storage. */ }
}

function runHistoryParams(agentId: string, filters: { q: string; status: string; archived: string }) {
  const params = new URLSearchParams({ agentId, archived: filters.archived, limit: '50' });
  if (filters.q) params.set('q', filters.q);
  if (filters.status) params.set('status', filters.status);
  return params;
}

type OrchestrationEventsPage = {
  events: OrchestrationEvent[];
  hasMoreBefore?: boolean;
  hasMoreAfter?: boolean;
};

function selectedWorkerSlotValues(slots: Array<{ slot: string }>, values: Record<string, string>) {
  return Object.fromEntries(slots.flatMap(({ slot }) => {
    const value = values[slot]?.trim();
    return value ? [[slot, value]] : [];
  }));
}

function nativeInputCapabilityStatus(agent: Agent | null, pair: WorkerPair, featureEnabled: boolean, protocolVersion: number | undefined, language: Language) {
  const zh = language === 'zh';
  if (!featureEnabled) return { available: false, text: zh ? '原生结构化提问未启用。' : 'Native structured questions are not enabled.' };
  if (protocolVersion !== 3) return { available: false, text: zh ? '当前 Hub 不支持原生问答 v3，请更新 Hub 并刷新页面。' : 'This Hub does not support native input v3. Update the Hub and reload.' };
  if (!agent) return { available: false, text: zh ? '尚未确认运行机器的结构化提问能力。' : 'The run machine’s structured input capability is not confirmed.' };
  if (!agent.online) return { available: false, text: zh ? '运行机器已离线，当前无法使用原生结构化提问。' : 'The run machine is offline; native structured questions are unavailable.' };
  const clis = pair === 'codex-codex' ? ['codex'] : pair === 'claude-claude' ? ['claude'] : ['claude', 'codex'];
  const problems = clis.flatMap((cli) => {
    const capability = agent.capabilities?.nativeUserInput?.[cli];
    if (capability?.available && capability.version >= 3) return [];
    const name = cli === 'claude' ? 'Claude Code' : 'Codex';
    return [`${name}: ${capability?.reason || (zh ? '当前 Bridge 尚未确认原生问答能力' : 'Native input is not verified for this Bridge')}`];
  });
  return problems.length
    ? { available: false, text: problems.join(' · ') }
    : { available: true, text: zh ? 'Bridge 支持结构化提问；CLI 实际发起问题后会显示问答面板。' : 'The Bridge reports structured input ready. The question panel opens when the CLI asks.' };
}

export function OrchestrationWorkspace({
  user,
  nativeUserInputProtocolVersion,
  onLogout,
  isDarkMode,
  setIsDarkMode,
  language,
  setLanguage,
  t,
  canOpenMain,
  path,
  navigate,
}: {
  user: UserAccount;
  nativeUserInputProtocolVersion?: number;
  onLogout: () => void;
  isDarkMode: boolean;
  setIsDarkMode: (value: boolean) => void;
  language: Language;
  setLanguage: (value: Language) => void;
  t: UIText;
  canOpenMain: boolean;
  path: string;
  navigate: (path: string, options?: { replace?: boolean }) => void;
}) {
  const [mobileHistoryOpen, setMobileHistoryOpen] = useState(false);
  const [mobileConfigOpen, setMobileConfigOpen] = useState(false);
  const [mobileActionsOpen, setMobileActionsOpen] = useState(false);
  const [mobileConversationOpen, setMobileConversationOpen] = useState(false);
  const openMobilePanel = (panel: 'history' | 'configuration' | 'actions' | 'conversation') => {
    setMobileHistoryOpen(panel === 'history');
    setMobileConfigOpen(panel === 'configuration');
    setMobileActionsOpen(panel === 'actions');
    setMobileConversationOpen(panel === 'conversation');
  };
  const [agents, setAgents] = useState<Agent[]>([]);
  const [agentsLoaded, setAgentsLoaded] = useState(false);
  const [selectedAgentId, setSelectedAgentId] = useState(() => readWorkspaceSelection('codexBridge.selectedAgentId'));
  const [runs, setRuns] = useState<OrchestrationRun[]>([]);
  const [historySearch, setHistorySearch] = useState('');
  const [historyQuery, setHistoryQuery] = useState('');
  const [historyStatus, setHistoryStatus] = useState('');
  const [historyArchived, setHistoryArchived] = useState('false');
  const [historyPage, setHistoryPage] = useState<RunHistoryPage>({ key: '', ids: [], nextCursor: '', loading: false, error: false });
  const historyPageRef = useRef(historyPage);
  const historyFiltersRef = useRef({ q: historyQuery, status: historyStatus, archived: historyArchived });
  historyPageRef.current = historyPage;
  historyFiltersRef.current = { q: historyQuery, status: historyStatus, archived: historyArchived };
  const historyRequestRef = useRef(0);
  const historyControllerRef = useRef<AbortController | null>(null);
  const [editingRun, setEditingRun] = useState<OrchestrationRun | null>(null);
  const [editingTitle, setEditingTitle] = useState('');
  const [metadataError, setMetadataError] = useState('');
  const [updatingRunId, setUpdatingRunId] = useState('');
  const [activeRunId, setActiveRunId] = useState('');
  useEffect(() => { if (activeRunId) setMobileConfigOpen(false); setMobileConversationOpen(false); }, [activeRunId]);
  const [runReads] = useState(createRunReadRequests);
  const [runLoad, setRunLoad] = useState({ runId: '', loading: false, ready: false, error: '' });
  const selectionEpochRef = useRef(0);
  const runReadyRef = useRef(false);
  const [events, setEvents] = useState<OrchestrationEvent[]>([]);
  const [approvals, setApprovals] = useState<ApprovalItemState[]>([]);
  const [userInputs, setUserInputs] = useState<UserInputCollection>(emptyUserInputCollection);
  const deliveredInputRequestsRef = useRef(new Set<string>());
  const [inputPanelOpen, setInputPanelOpen] = useState(false);
  const [selectedInputRequestId, setSelectedInputRequestId] = useState('');
  const [mode, setMode] = useState<'collaboration' | 'debate'>('collaboration');
  const [workerPair, setWorkerPair] = useState<WorkerPair>('claude-codex');
  const [presets, setPresets] = useState<CLIConfigPreset[]>([]);
  const [selectedWorkerProfiles, setSelectedWorkerProfiles] = useState<Record<string, string>>({});
  const [officialLoginConfirmed, setOfficialLoginConfirmed] = useState<Record<string, boolean>>({});
  const [selectedWorkerEfforts, setSelectedWorkerEfforts] = useState<Record<string, string>>({});
  const [workerProfilesTouched, setWorkerProfilesTouched] = useState(false);
  const [selectedOfficialModels, setSelectedOfficialModels] = useState<Record<string, string>>({});
  const [officialCatalog, setOfficialCatalog] = useState<{ key: string; models: OfficialCLIModel[]; loading: boolean; error: string }>({ key: '', models: [], loading: false, error: '' });
  const [firstCli, setFirstCli] = useState<'claude' | 'codex'>('claude');
  const [profile, setProfile] = useState<'default' | 'formal-proof'>('default');
  const [verificationMode, setVerificationMode] = useState<VerificationMode>('agent');
  const [nativeContextCompaction, setNativeContextCompaction] = useState<NativeContextCompaction>('off');
  const [cwd, setCwd] = useState('');
  const [maxTurns, setMaxTurns] = useState(defaultOrchestrationMaxTurns);
  const [files, setFiles] = useState<UploadAttachment[]>([]);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [settingsFocus, setSettingsFocus] = useState<'cli' | ''>('');
  const [error, setError] = useState('');
  const [sharingRunId, setSharingRunId] = useState('');
  const [shareCopiedRunId, setShareCopiedRunId] = useState('');
  const [creating, setCreating] = useState(false);
  const [deletingRunId, setDeletingRunId] = useState('');
  const [planningWorkspaceOpen, setPlanningWorkspaceOpen] = useState(false);
  const [orchestrationProgress, setOrchestrationProgress] = useState<OrchestrationProgress | null>(null);
  const [selectedProgressTaskNumber, setSelectedProgressTaskNumber] = useState<number | null>(null);
  const [selectedProgressGraphId, setSelectedProgressGraphId] = useState('');
  const [taskMapExpanded, setTaskMapExpanded] = useState(false);
  const [agentGraphExpanded, setAgentGraphExpanded] = useState(false);
  const [selectedPromptKey, setSelectedPromptKey] = useState('initial');
  const [connectionStatus, setConnectionStatus] = useState(t.disconnected);
  const [showScrollBottom, setShowScrollBottom] = useState(false);
  const [collapsedTimelineGroups, setCollapsedTimelineGroups] = useState<Record<string, boolean>>({});
  const [refreshingOrchestration, setRefreshingOrchestration] = useState(false);
  const [loadingOlderEvents, setLoadingOlderEvents] = useState(false);
  const [hasOlderEventsByRun, setHasOlderEventsByRun] = useState<Record<string, boolean>>({});
  const [elapsedTick, setElapsedTick] = useState(0);
  const wsRef = useRef<WebSocket | null>(null);
  const receiptOnlySocketRef = useRef(false);
  const terminalInputRecoveryRef = useRef({ runId: '', attempts: 0, complete: false });
  const terminalInputSyncTimerRef = useRef<number | null>(null);
  const socketRunIdRef = useRef('');
  const userInputsRef = useRef(userInputs);
  const seenInputRequestsRef = useRef<{ runId: string; ids: Set<string> }>({ runId: '', ids: new Set() });
  const userInputSyncRef = useRef('');
  const [inputRecoveryNotice, setInputRecoveryNotice] = useState('');
  const [inputStorageNotice, setInputStorageNotice] = useState('');
  const userInputQueryRef = useRef<(manual?: boolean) => void>(() => undefined);
  const reconnectTimerRef = useRef<number | null>(null);
  const reconnectAttemptsRef = useRef(0);
  const activeRunIdRef = useRef('');
  const desiredRunIdRef = useRef('');
  const activeRunAgentIdRef = useRef('');
  const selectedAgentIdRef = useRef(selectedAgentId);
  const stickToBottomRef = useRef(true);
  const timelineOrderRef = useRef(0);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const taskInputRef = useRef<HTMLTextAreaElement | null>(null);
  const [liveInputRevision, setLiveInputRevision] = useState(0);
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const endRef = useRef<HTMLDivElement | null>(null);
  const collapsedTimelineRunIdRef = useRef('');
  const refreshOrchestrationInFlightRef = useRef<Promise<void> | null>(null);
  const olderEventsLoadInFlightRef = useRef(false);
  const pendingLiveEventsRef = useRef<OrchestrationEvent[]>([]);
  const liveEventFrameRef = useRef<number | null>(null);
  // HTTP continuity is independent of live WS events, which can arrive ahead of a missing page.
  const eventSyncedSeqByRunRef = useRef<Record<string, number>>({});
  const eventCatchupRef = useRef<{ runId: string; generation: number; promise: Promise<OrchestrationEvent[]> } | null>(null);
  const eventMaxSeqByRunRef = useRef<Record<string, number>>({});
  const eventMinSeqByRunRef = useRef<Record<string, number>>({});
  const orchestrationBootedRef = useRef(false);
  const agentSelectionEpochRef = useRef(0);
  const draftingRunRef = useRef(false);
  const pathRunId = useMemo(() => orchestrationRunIdFromPath(path), [path]);
  const selectionBlocked = Boolean(activeRunId || pathRunId) && (!runLoad.ready || runLoad.runId !== activeRunId || Boolean(pathRunId && pathRunId !== activeRunId));
  const draftTarget = activeRunId || pathRunId ? `run:${encodeURIComponent(activeRunId || pathRunId)}` : `new:${encodeURIComponent(selectedAgentId)}`;
  const { text: prompt, setText: setPrompt, clearSubmitted: clearSubmittedPrompt, persistenceFailed: draftPersistenceFailed } = useOrchestrationDraft(user.id, draftTarget);
  const draftTargetRef = useRef(draftTarget);
  draftTargetRef.current = draftTarget;

  const selectedAgent = agents.find((agent) => agent.id === selectedAgentId) || null;
  const activeAgentIds = useMemo(
    () => new Set(runs.filter((run) => activeOrchestrationStatus(run.status)).map((run) => run.agentId)),
    [runs],
  );
  const onlineAgent = selectedAgent?.online ? selectedAgent : agents.find((agent) => agent.online);
  const historyKey = runHistoryParams(selectedAgentId, historyFiltersRef.current).toString();
  // Sidebar order and pagination belong to the server; the cache also retains
  // a directly selected run that may not match the current history filters.
  const agentRuns = useMemo(() => {
    if (!selectedAgentId || historyPage.key !== historyKey) return [];
    const byId = new Map(runs.map((run) => [run.id, run]));
    return historyPage.ids.flatMap((id) => {
      const run = byId.get(id);
      return run && run.agentId === selectedAgentId ? [run] : [];
    });
  }, [historyKey, historyPage, runs, selectedAgentId]);
  const activeRun = runs.find((run) => run.id === activeRunId) || null;
  const nativeInputEnabled = Boolean(user.features?.includes('native-user-input')) && nativeUserInputProtocolVersion === 3;
  const nativeInputReadEnabled = nativeInputEnabled || Boolean(activeRun && !activeOrchestrationStatus(activeRun.status));
  const visibleUserInputs = userInputs.items.filter((item) => item.request.runId === activeRunId);
  const actionableUserInputs = visibleUserInputs.filter((item) => item.status !== 'delivered');
  const deliveredQuestionCount = visibleUserInputs.filter((item) => item.status === 'delivered').reduce((count, item) => count + item.request.questions.length, 0);
  const pendingQuestionCount = actionableUserInputs.reduce((count, item) => count + item.request.questions.length, 0);
  const selectedInputItem = visibleUserInputs.find((item) => item.request.requestId === selectedInputRequestId)
    || actionableUserInputs.find((item) => item.status === 'pending') || actionableUserInputs[0] || visibleUserInputs[0];
  const blockingUserInputCount = actionableUserInputs.filter((item) => item.request.blocking && item.status === 'pending').length;
  const confirmingUserInputCount = actionableUserInputs.filter((item) => item.status === 'confirming' || item.status === 'submitting').length;
  const terminalUserInputs = Object.values(userInputs.terminal).filter((status) => status.runId === activeRunId)
    .sort((a, b) => b.revision - a.revision).slice(0, 5);
  const inputFeatureEnabled = Boolean(user.features?.includes('native-user-input'));
  const runInputAgent = activeRun ? agents.find((agent) => agent.id === activeRun.agentId) || null : selectedAgent;
  const runInputPair = activeRun ? normalizeOrchestrationWorkerPair(activeRun.workerPair) : workerPair;
  const runInputCapability = nativeInputCapabilityStatus(runInputAgent, runInputPair, inputFeatureEnabled, nativeUserInputProtocolVersion, language);
  const draftInputCapability = nativeInputCapabilityStatus(selectedAgent, workerPair, inputFeatureEnabled, nativeUserInputProtocolVersion, language);
  const showDraftInputCapability = Boolean(activeRun && (activeRun.agentId !== selectedAgentId || runInputPair !== workerPair));

  const planWorkspaceEnabled = Boolean(user.features?.includes('orchestration-plan-workspace'));
  const activeRunFiles = useMemo(() => {
    return activeRun ? mergeOrchestrationFiles(activeRun.files, orchestrationRunFilesFromEvents(events, activeRun.id)) : [];
  }, [activeRun, events]);
  const visibleEvents = useMemo(() => activeRun ? visibleOrchestrationEvents(events, activeRunId, activeRun, t).filter((event) => event.kind !== 'run.conclusion') : [], [activeRun, events, activeRunId, t]);
  const finalConclusion = useMemo(() => events.slice().reverse().find((event) => event.runId === activeRunId && Boolean(event.runConclusion) && (event.kind === 'run.conclusion' || event.kind === 'run.end' || event.kind === 'run.error' || event.kind === 'run.cancelled'))?.runConclusion || null, [events, activeRunId]);
  const isRunning = activeOrchestrationStatus(activeRun?.status);
  const dynamicExecution = activeRun ? activeRun.executionMode === 'dynamic' : leadingEdition;
  const currentTurnInfo = useMemo(() => activeRun ? orchestrationTurnInfoFromEvents(events, activeRun.id, dynamicExecution ? undefined : activeRun.maxTurns, isRunning && !dynamicExecution) : {}, [activeRun, dynamicExecution, events, isRunning]);
  const currentTurnLabel = useMemo(() => orchestrationTurnLabel(currentTurnInfo, t), [currentTurnInfo, t]);
  const visibleApprovals = useMemo(() => approvals.filter((item) => item.approval.runId === activeRunId), [approvals, activeRunId]);
  const timelineItems = useMemo(() => orchestrationTimelineItems(visibleEvents, visibleApprovals), [visibleEvents, visibleApprovals]);
  const timelineGroups = useMemo(() => orchestrationTimelineGroups(timelineItems, activeRun, events), [timelineItems, activeRun, events, elapsedTick]);
  const progressTasks = orchestrationProgress?.tasks || [];
  const selectedProgressTask = useMemo(() => {
    if (!progressTasks.length) return null;
    return progressTasks.find((item) => item.taskNumber === selectedProgressTaskNumber) || progressTasks[progressTasks.length - 1];
  }, [progressTasks, selectedProgressTaskNumber]);
  const selectedProgressGraphIDs = useMemo(() => new Set((selectedProgressTask?.graphs || []).map((graph) => graph.id)), [selectedProgressTask]);
  const promptViews = useMemo(() => {
    if (!activeRun) return [];
    const taskNumber = selectedProgressTask?.taskNumber;
    const taskPrompt = selectedProgressTask?.prompt || activeRun.prompt;
    const views = [{ key: `task-${taskNumber || 1}`, label: taskNumber ? `${language === 'zh' ? '任务' : 'Task'} ${taskNumber}` : t.initialPrompt, content: taskPrompt }];
    events.forEach((event) => {
      const content = event.turnStartData?.promptText?.trim();
      if (!content) return;
      if (selectedProgressGraphIDs.size && event.task?.graphId && !selectedProgressGraphIDs.has(event.task.graphId)) return;
      if (selectedProgressGraphIDs.size && !event.task?.graphId && selectedProgressTask && event.createdAt) {
        const taskStart = selectedProgressTask.createdAt || 0;
        const taskEnd = selectedProgressTask.finishedAt || 0;
        if ((taskStart && event.createdAt < taskStart) || (taskEnd && event.createdAt > taskEnd)) return;
      }
      const key = `turn-${event.id || event.seq || views.length}`;
      const round = event.task?.round || event.turnStartData?.round;
      const node = event.task?.name || event.role || event.cli;
      const label = [round ? `${t.turnPrefix} ${round}` : t.roundPrompt, node].filter(Boolean).join(' · ');
      views.push({ key, label, content });
    });
    return views;
  }, [activeRun, events, language, selectedProgressGraphIDs, selectedProgressTask, t.initialPrompt, t.roundPrompt, t.turnPrefix]);
  const selectedPromptView = promptViews.find((item) => item.key === selectedPromptKey) || promptViews[0];
  const projectedPlan = selectedProgressTask?.plan || orchestrationProgress?.plan;
  const projectedPlanItems = projectedPlan?.items?.length ? projectedPlan.items : (selectedProgressTask?.planItems || orchestrationProgress?.planItems || []);
  const selectedAgentGraph = useMemo(() => {
    const graphs = selectedProgressTask?.graphs || [];
    return graphs.find((graph) => graph.id === selectedProgressGraphId) || selectedProgressTask?.graph || graphs[graphs.length - 1] || orchestrationProgress?.graph;
  }, [orchestrationProgress?.graph, selectedProgressGraphId, selectedProgressTask]);
  const currentPlanItem = projectedPlanItems.find((item) => item.id === projectedPlan?.currentFocus);
  const progressRefreshKey = useMemo(() => {
    const relevant = events.filter((event) => event.task && ['run.start', 'turn.end', 'run.end', 'run.error', 'run.cancelled'].includes(event.kind));
    const latest = relevant[relevant.length - 1];
    return latest ? `${latest.seq || latest.id || relevant.length}:${latest.kind}` : '';
  }, [events]);
  const orchestrationStreamStatus = activeRun && isRunning ? connectionStatus : t.idle;
  const continuingRun = Boolean(activeRun && !isRunning);
  const canCancelRun = canCancelOrchestrationStatus(activeRun?.status);
  const hasOlderEvents = Boolean(activeRunId && hasOlderEventsByRun[activeRunId]);
  const capabilityProblems = useMemo(() => orchestrationCapabilityProblems(selectedAgent, t, workerPair), [selectedAgent, t, workerPair]);
	const tbciCapability = selectedAgent?.capabilities?.tbci;
	const tbciAvailable = Boolean(selectedAgent?.online && tbciCapability?.available && tbciCapability.state === 'ready');
	const tbciUnavailableReason = tbciCapability?.reason || t.tbciUpdateBridgeHint;
	const selectedTBCIUnavailable = profile === 'formal-proof' && verificationMode === 'tbci' && !tbciAvailable;
  const workingDirs = useMemo(() => {
    return Array.from(new Set((selectedAgent?.workingDirs || []).map((dir) => dir.trim()).filter(Boolean)));
  }, [selectedAgent]);
  const workerProfileSlots = useMemo(() => workerPair === 'codex-codex'
    ? [{ slot: 'codex-a', cli: 'codex' as const, label: 'Codex A' }, { slot: 'codex-b', cli: 'codex' as const, label: 'Codex B' }]
    : workerPair === 'claude-claude'
      ? [{ slot: 'claude-a', cli: 'claude' as const, label: 'Claude A' }, { slot: 'claude-b', cli: 'claude' as const, label: 'Claude B' }]
      : [{ slot: 'claude', cli: 'claude' as const, label: 'Claude' }, { slot: 'codex', cli: 'codex' as const, label: 'Codex' }], [workerPair]);
  const officialModelsEnabled = officialCodexModelsEdition && Boolean(user.features?.includes('official-codex-worker-models'));
  const officialModelsVersion = selectedAgent?.capabilities?.configSwitcher?.version || 0;
  const officialModelsSupported = Boolean(selectedAgent?.online && officialModelsVersion >= 3);
  const officialCatalogKey = `${user.id}:${selectedAgentId}:${workerPair}:${officialModelsVersion}:${officialModelsEnabled}:${Boolean(selectedAgent?.online)}`;
  const officialModels = officialCatalog.key === officialCatalogKey ? officialCatalog.models : [];
  const officialCatalogLoading = officialModelsSupported && (officialCatalog.key !== officialCatalogKey || officialCatalog.loading);
  const officialCatalogError = officialCatalog.key === officialCatalogKey ? officialCatalog.error : '';
  const workerProfilesComplete = useMemo(
    () => workerProfileSlots.every(({ slot }) => {
      const value = selectedWorkerProfiles[slot];
      return Boolean(value) && (value !== OFFICIAL_LOGIN_PRESET_ID || officialLoginConfirmed[slot]);
    }),
    [officialLoginConfirmed, selectedWorkerProfiles, workerProfileSlots],
  );

  const loadAgents = useCallback(async () => {
    const data = await api<{ agents: Agent[] }>('/api/agents');
    const nextAgents = data.agents || [];
    setAgents(nextAgents);
    setAgentsLoaded(true);
    setSelectedAgentId((current) => {
      const next = activeRunIdRef.current && activeRunAgentIdRef.current === current
        ? current
        : preferredAgentID(nextAgents, current);
      selectedAgentIdRef.current = next;
      saveWorkspaceSelection('codexBridge.selectedAgentId', next);
      return next;
    });
    return nextAgents;
  }, []);

  const refreshAgentsQuietly = useCallback(async () => {
    const data = await api<{ agents: Agent[] }>('/api/agents');
    const nextAgents = data.agents || [];
    setAgents(nextAgents);
    setAgentsLoaded(true);
    return nextAgents;
  }, []);

  const loadRuns = useCallback(async (append = false) => {
    const agentId = selectedAgentIdRef.current;
    if (!agentId) return [];
    const params = runHistoryParams(agentId, historyFiltersRef.current);
    const key = params.toString();
    const previous = historyPageRef.current;
    if (append && (previous.key !== key || previous.loading || !previous.nextCursor)) return [];
    if (append) params.set('cursor', previous.nextCursor);
    const request = ++historyRequestRef.current;
    historyControllerRef.current?.abort();
    const controller = new AbortController();
    historyControllerRef.current = controller;
    const timer = window.setTimeout(() => controller.abort(), 15000);
    const pending = { key, ids: append ? previous.ids : [], nextCursor: append ? previous.nextCursor : '', loading: true, error: false };
    historyPageRef.current = pending;
    setHistoryPage(pending);
    const isCurrent = () => request === historyRequestRef.current && key === runHistoryParams(selectedAgentIdRef.current, historyFiltersRef.current).toString();
    try {
      const data = await api<{ runs: OrchestrationRun[]; nextCursor?: string }>(`/api/orchestrations?${params.toString()}`, { signal: controller.signal });
      if (!isCurrent()) return [];
      const incoming = Array.isArray(data.runs) ? data.runs.filter(isOrchestrationRun) : [];
      setRuns((current) => {
        const merged = new Map(current.map((run) => [run.id, run]));
        incoming.forEach((run) => merged.set(run.id, run));
        return [...merged.values()];
      });
      const next = { key, ids: [...new Set([...(append ? previous.ids : []), ...incoming.map((run) => run.id)])], nextCursor: data.nextCursor || '', loading: false, error: false };
      historyPageRef.current = next;
      setHistoryPage(next);
      return incoming;
    } catch {
      if (isCurrent()) {
        const next = { ...pending, loading: false, error: true };
        historyPageRef.current = next;
        setHistoryPage(next);
      }
      return [];
    } finally { window.clearTimeout(timer); }
  }, []);

  useEffect(() => {
    const timer = window.setTimeout(() => setHistoryQuery(historySearch.trim()), 250);
    return () => window.clearTimeout(timer);
  }, [historySearch]);

  useEffect(() => {
    if (agentsLoaded && selectedAgentId) void loadRuns();
    return () => { historyRequestRef.current += 1; historyControllerRef.current?.abort(); };
  }, [agentsLoaded, historyKey, loadRuns, selectedAgentId]);

  const loadRun = useCallback(async (runId: string) => {
    const data = await runReads.read<{ run: OrchestrationRun }>(`/api/orchestrations/${encodeURIComponent(runId)}`);
    if (!isOrchestrationRun(data.run)) throw new Error(t.failedLoadOrchestration);
    setRuns((current) => upsertOrchestrationRun(current, { ...data.run, archivedAt: data.run.archivedAt || 0 }));
    return data.run;
  }, [runReads, t.failedLoadOrchestration]);

  const loadRunEvents = useCallback(async (runId: string, replace = false, mode: 'latest' | 'after' | 'before' = 'latest') => {
    const generation = runReads.generation;
    const isCurrent = () => activeRunIdRef.current === runId && runReads.generation === generation;
    const pending = eventCatchupRef.current;
    if (mode === 'after' && pending?.runId === runId && pending.generation === generation) return pending.promise;
    const readPages = async () => {
      let cursor = eventSyncedSeqByRunRef.current[runId] || 0;
      const received: OrchestrationEvent[] = [];
      do {
        const params = new URLSearchParams();
        if (mode === 'after') {
          params.set('afterSeq', String(cursor));
          params.set('limit', '1000');
        } else if (mode === 'before') {
          const beforeSeq = eventMinSeqByRunRef.current[runId] || 0;
          if (beforeSeq <= 1) return [];
          params.set('beforeSeq', String(beforeSeq));
          params.set('limit', String(orchestrationEventPageSize));
        } else {
          params.set('limit', String(orchestrationInitialEventPageSize));
        }
        const data = await runReads.read<OrchestrationEventsPage>(`/api/orchestrations/${encodeURIComponent(runId)}/events?${params}`);
        if (!isCurrent()) return [];
        const incoming = data.events || [];
        const pageMaxSeq = maxOrchestrationEventSeq(incoming, 0);
        if (mode === 'after' && data.hasMoreAfter && pageMaxSeq <= cursor) throw new Error('History cursor did not advance');
        if (replace) timelineOrderRef.current = 0;
        eventMaxSeqByRunRef.current[runId] = maxOrchestrationEventSeq(incoming, replace ? 0 : eventMaxSeqByRunRef.current[runId] || 0);
        const currentMinSeq = replace ? 0 : eventMinSeqByRunRef.current[runId] || 0;
        const nextMinSeq = minOrchestrationEventSeq(incoming, currentMinSeq);
        eventMinSeqByRunRef.current[runId] = currentMinSeq > 0 && nextMinSeq > 0 ? Math.min(currentMinSeq, nextMinSeq) : nextMinSeq;
        if (replace || mode === 'before') setHasOlderEventsByRun((current) => ({ ...current, [runId]: Boolean(data.hasMoreBefore) }));
        if (replace || mode === 'after') eventSyncedSeqByRunRef.current[runId] = replace ? pageMaxSeq : Math.max(cursor, pageMaxSeq);
        setEvents((current) => !isCurrent() ? current : replace ? incoming.slice().sort(compareOrchestrationEvents) : mergeOrchestrationEvents(current, incoming));
        received.push(...incoming);
        // Never read the WS max here: it may already point beyond a missing page.
        cursor = Math.max(cursor, pageMaxSeq);
        if (mode !== 'after' || !data.hasMoreAfter) break;
      } while (isCurrent());
      return received;
    };
    const promise = readPages();
    if (mode === 'after') {
      eventCatchupRef.current = { runId, generation, promise };
      try { return await promise; }
      finally { if (eventCatchupRef.current?.promise === promise) eventCatchupRef.current = null; }
    }
    return promise;
  }, [runReads]);

  const loadOlderEvents = useCallback(async () => {
    const runId = activeRunIdRef.current;
    if (!runId || loadingOlderEvents || olderEventsLoadInFlightRef.current || !hasOlderEventsByRun[runId]) return;
    const epoch = selectionEpochRef.current;
    olderEventsLoadInFlightRef.current = true;
    const container = scrollRef.current;
    const previousScrollHeight = container?.scrollHeight || 0;
    const previousScrollTop = container?.scrollTop || 0;
    setLoadingOlderEvents(true);
    stickToBottomRef.current = false;
    try {
      await loadRunEvents(runId, false, 'before');
      // Keep the user's viewport anchored while the older page is inserted.
      window.requestAnimationFrame(() => {
        const nextContainer = scrollRef.current;
        if (!nextContainer || activeRunIdRef.current !== runId || epoch !== selectionEpochRef.current) return;
        const delta = nextContainer.scrollHeight - previousScrollHeight;
        nextContainer.scrollTop = previousScrollTop + Math.max(0, delta);
      });
    } catch (err) {
      if (epoch === selectionEpochRef.current) setError(err instanceof Error ? err.message : t.failedLoadOrchestration);
    } finally {
      if (epoch === selectionEpochRef.current) {
        olderEventsLoadInFlightRef.current = false;
        setLoadingOlderEvents(false);
      }
    }
  }, [hasOlderEventsByRun, loadRunEvents, loadingOlderEvents, t.failedLoadOrchestration]);

  const scrollTimelineToBottom = useCallback((behavior: ScrollBehavior = 'smooth') => {
    const container = scrollRef.current;
    if (!container) return;
    container.scrollTo({ top: container.scrollHeight, behavior });
    stickToBottomRef.current = true;
    setShowScrollBottom(false);
  }, []);

  const updateTimelineScrollState = useCallback(() => {
    const container = scrollRef.current;
    if (!container) {
      setShowScrollBottom(false);
      return;
    }
    const nearBottom = isNearBottom(container);
    stickToBottomRef.current = nearBottom;
    setShowScrollBottom(timelineItems.length > 0 && !nearBottom);
  }, [timelineItems.length]);

  const clearReconnect = useCallback(() => {
    if (reconnectTimerRef.current !== null) {
      window.clearTimeout(reconnectTimerRef.current);
      reconnectTimerRef.current = null;
    }
  }, []);

  const restoreUserInputs = useCallback((runId: string) => {
    const next = emptyUserInputCollection();
    setInputRecoveryNotice('');
    try {
      next.pendingSubmissions = loadUserInputStorage(sessionStorage, user.id).filter((entry) => entry.runId === runId);
      setInputStorageNotice('');
    } catch {
      setInputStorageNotice(language === 'zh' ? '无法读取未确认提交记录；请恢复浏览器会话存储后重新查询。' : 'Cannot read pending submissions. Restore session storage and query again.');
    }
    userInputsRef.current = next;
    setUserInputs(next);
  }, [language, user.id]);

  const updateUserInputs = useCallback((update: (state: UserInputCollection) => UserInputCollection) => {
    const previous = userInputsRef.current;
    const next = update(previous);
    let stored = true;
    if (next.pendingSubmissions !== previous.pendingSubmissions) {
      try {
        const others = loadUserInputStorage(sessionStorage, user.id).filter((entry) => entry.runId !== activeRunIdRef.current);
        saveUserInputStorage(sessionStorage, user.id, [...others, ...next.pendingSubmissions]);
      } catch {
        stored = false;
        setInputStorageNotice(language === 'zh' ? '未确认提交记录无法更新；请恢复会话存储后重新查询。' : 'Pending submissions could not be saved. Restore session storage and query again.');
      }
    }
    userInputsRef.current = next;
    setUserInputs(next);
    return stored;
  }, [language, user.id]);

  useEffect(() => {
    const seen = seenInputRequestsRef.current;
    if (seen.runId !== activeRunId) {
      deliveredInputRequestsRef.current.clear();
      seenInputRequestsRef.current = { runId: activeRunId, ids: new Set() };
      setInputPanelOpen(false);
      setSelectedInputRequestId('');
    }
    if (!activeRunId) return;
    const current = seenInputRequestsRef.current;
    const incoming = visibleUserInputs.filter((item) => item.status !== 'delivered' && !current.ids.has(item.request.requestId));
    const newlyDelivered = new Set(visibleUserInputs.filter((item) => item.status === 'delivered'
      && !deliveredInputRequestsRef.current.has(item.request.requestId)).map((item) => item.request.requestId));
    for (const id of newlyDelivered) deliveredInputRequestsRef.current.add(id);
    for (const item of visibleUserInputs) current.ids.add(item.request.requestId);
    if (newlyDelivered.size && visibleUserInputs.every((item) => item.status === 'delivered')) {
      const focusWasInPanel = document.getElementById('native-user-input-panel')?.contains(document.activeElement);
      setInputPanelOpen(false);
      if (focusWasInPanel) window.requestAnimationFrame(() => document.getElementById('native-user-input-entry')?.focus({ preventScroll: true }));
    }
    if (newlyDelivered.size) {
      setSelectedInputRequestId((previous) => {
        if (!newlyDelivered.has(previous)) return previous;
        const index = visibleUserInputs.findIndex((item) => item.request.requestId === previous);
        const remaining = [...visibleUserInputs.slice(index + 1), ...visibleUserInputs.slice(0, index)];
        return remaining.find((item) => item.status === 'pending')?.request.requestId || previous;
      });
    }
    if (!incoming.length) return;
    // Open for a genuinely new request, including one recovered by the first
    // snapshot. Keep the selected card and its draft when another arrives.
    setSelectedInputRequestId((previous) => visibleUserInputs.some((item) => item.request.requestId === previous && item.status !== 'delivered')
      ? previous : incoming[0].request.requestId);
    setInputPanelOpen(true);
  }, [activeRunId, userInputs.items]);

  useEffect(() => {
    if (!inputPanelOpen || !selectedInputRequestId) return;
    const frame = window.requestAnimationFrame(() => {
      const card = document.getElementById(`native-user-input-${selectedInputRequestId}`);
      card?.focus({ preventScroll: true });
      const panel = document.getElementById('native-user-input-panel');
      if (panel) panel.scrollTop = 0;
    });
    return () => window.cancelAnimationFrame(frame);
  }, [selectedInputRequestId, inputPanelOpen]);

  const openInputPanel = () => {
    setInputPanelOpen(true);
    const requestId = selectedInputItem?.request.requestId;
    if (requestId) window.requestAnimationFrame(() => document.getElementById(`native-user-input-${requestId}`)?.focus());
  };

  const closeWS = useCallback(() => {
    clearReconnect();
    if (terminalInputSyncTimerRef.current !== null) {
      window.clearTimeout(terminalInputSyncTimerRef.current);
      terminalInputSyncTimerRef.current = null;
    }
    if (wsRef.current) {
      const ws = wsRef.current;
      wsRef.current = null;
      socketRunIdRef.current = '';
      ws.close();
    }
  }, [clearReconnect]);

  const clearActiveOrchestration = useCallback(() => {
    selectionEpochRef.current += 1;
    runReads.cancel();
    runReadyRef.current = false;
    setRunLoad({ runId: '', loading: false, ready: false, error: '' });
    closeWS();
    activeRunIdRef.current = '';
    activeRunAgentIdRef.current = '';
    desiredRunIdRef.current = '';
    setActiveRunId('');
    saveWorkspaceSelection(activeOrchestrationRunStorageKey, '');
    setEvents([]);
    setApprovals([]);
    restoreUserInputs('');
    timelineOrderRef.current = 0;
    setLoadingOlderEvents(false);
    setConnectionStatus(t.idle);
    setShowScrollBottom(false);
    if (window.location.pathname.startsWith('/orchestrate/runs/')) {
      navigate('/orchestrate', { replace: true });
    }
  }, [closeWS, navigate, restoreUserInputs, runReads, t.idle]);

  const applyEvent = useCallback((event: OrchestrationEvent) => {
    const nextEvent = { ...event, timelineOrder: typeof event.timelineOrder === 'number' ? event.timelineOrder : ++timelineOrderRef.current };
    if (typeof nextEvent.seq === 'number' && Number.isFinite(nextEvent.seq)) {
      eventMaxSeqByRunRef.current[nextEvent.runId] = Math.max(eventMaxSeqByRunRef.current[nextEvent.runId] || 0, nextEvent.seq);
    }
    pendingLiveEventsRef.current.push(nextEvent);
    if (liveEventFrameRef.current !== null) return;
    liveEventFrameRef.current = window.requestAnimationFrame(() => {
      liveEventFrameRef.current = null;
      const pending = pendingLiveEventsRef.current.splice(0);
      if (!pending.length) return;
      setEvents((current) => mergeOrchestrationEvents(
        current,
        pending.filter((item) => item.runId === activeRunIdRef.current),
      ));
      setRuns((current) => pending
        .reduce((nextRuns, item) => nextRuns.map((run) => run.id === item.runId ? applyOrchestrationEventToRun(run, item) : run), current)
        .sort((a, b) => (b.updatedAt || b.createdAt || 0) - (a.updatedAt || a.createdAt || 0)));
    });
  }, []);

  const connectRun = useCallback((runId: string, readTerminalInput = false) => {
    closeWS();
    receiptOnlySocketRef.current = readTerminalInput;
    if (!readTerminalInput || terminalInputRecoveryRef.current.runId !== runId) {
      terminalInputRecoveryRef.current = { runId, attempts: 0, complete: false };
    }
    if (readTerminalInput) {
      const recovery = terminalInputRecoveryRef.current;
      if (recovery.complete || recovery.attempts >= 3) {
        setConnectionStatus(recovery.complete ? t.idle : t.disconnected);
        return;
      }
      recovery.attempts += 1;
    }
    const readNativeInput = () => nativeInputEnabled || receiptOnlySocketRef.current;
    const scheme = location.protocol === 'https:' ? 'wss' : 'ws';
    const ws = new WebSocket(`${scheme}://${location.host}/ws/orchestrations?runId=${encodeURIComponent(runId)}`);
    wsRef.current = ws;
    socketRunIdRef.current = runId;
    setConnectionStatus(t.connecting);
    let queryInFlight = false;
    let queryAttempts = 0;
    let queriedPending = false;
    let hasQuerySnapshot = false;
    let stopHeartbeat: (() => void) | null = null;
    const clearQueryTimer = () => {
      if (terminalInputSyncTimerRef.current !== null) window.clearTimeout(terminalInputSyncTimerRef.current);
      terminalInputSyncTimerRef.current = null;
    };
    const finishQuery = (notice = '') => {
      clearQueryTimer();
      queryInFlight = false;
      setInputRecoveryNotice(notice);
      if (!notice) userInputSyncRef.current = '';
      if (receiptOnlySocketRef.current) {
        terminalInputRecoveryRef.current = { runId, attempts: 0, complete: true };
        closeWS();
        setConnectionStatus(t.idle);
      }
    };
    const unavailableNotice = () => userInputsRef.current.pendingSubmissions.some((entry) => entry.runId === runId)
      ? (language === 'zh' ? '暂时无法确认答案是否送达；可重新查询，不会自动重发答案。'
        : 'Delivery cannot be confirmed yet. Query again; answers are never replayed automatically.')
      : (language === 'zh' ? '暂时无法同步结构化问题；请重新查询。' : 'Structured questions could not be synchronized. Query again.');
    const syncFailureReason = (reason: string) => {
      if (language !== 'zh') return reason;
      const reasons: Record<string, string> = {
        'CLI endpoint is offline': 'CLI 端点离线，请恢复 Bridge 连接后重新查询。',
        'Bridge needs protocol v3 for structured input': '当前 Bridge 不支持所需问答协议，请更新 Bridge 后重新查询。',
        'Browser needs to reload for structured input': '请刷新页面以使用当前问答协议。',
        'Structured input is unavailable': '结构化问答暂不可用。',
        'Run is no longer active': '任务状态已变化，请刷新任务后重新查询。',
        'Invalid structured input query': '结构化问题查询无效，请刷新页面后重试。',
      };
      return reasons[reason] || reason;
    };
    const scheduleQuery = () => {
      clearQueryTimer();
      if (queryAttempts >= 3) { finishQuery(unavailableNotice()); return; }
      terminalInputSyncTimerRef.current = window.setTimeout(() => {
        terminalInputSyncTimerRef.current = null;
        sendQuery();
      }, 1000 * Math.max(1, queryAttempts));
    };
    const sendQuery = (manual = false) => {
      if (wsRef.current !== ws || ws.readyState !== WebSocket.OPEN || queryInFlight || !readNativeInput()) return;
      if (manual) { queryAttempts = 0; setInputRecoveryNotice(''); }
      if (queryAttempts >= 3) { finishQuery(unavailableNotice()); return; }
      clearQueryTimer();
      const current = userInputsRef.current;
      const pending = current.pendingSubmissions.filter((entry) => entry.runId === runId && entry.instanceId === current.instanceId);
      queriedPending = pending.length > 0;
      const syncId = crypto.randomUUID();
      userInputSyncRef.current = syncId;
      queryAttempts += 1;
      queryInFlight = true;
      try {
        ws.send(JSON.stringify({ type: 'user_input_sync', payload: { runId, syncId,
          instanceId: current.instanceId, clientVersion: 3,
          pendingSubmissions: pending.map(({ requestId, submissionId, executionId }) => ({ requestId, submissionId, executionId })),
        } }));
      } catch {
        queryInFlight = false;
        finishQuery(unavailableNotice());
        return;
      }
      terminalInputSyncTimerRef.current = window.setTimeout(() => {
        terminalInputSyncTimerRef.current = null;
        if (wsRef.current !== ws) return;
        queryInFlight = false;
        scheduleQuery();
      }, 10000);
    };
    userInputQueryRef.current = sendQuery;
    if (readTerminalInput) {
      terminalInputSyncTimerRef.current = window.setTimeout(() => {
        terminalInputSyncTimerRef.current = null;
        if (wsRef.current === ws) ws.close();
      }, 10000);
    }
    ws.onopen = () => {
      if (wsRef.current !== ws) return;
      clearQueryTimer();
      reconnectAttemptsRef.current = 0;
      setConnectionStatus(t.connected);
      setLiveInputRevision((revision) => revision + 1);
      stopHeartbeat = startWSHeartbeat(ws);
      sendQuery();
      void loadRunEvents(runId, false, 'after').catch(() => undefined);
    };
    ws.onmessage = (message) => {
      if (wsRef.current !== ws) return;
      try {
        const env = JSON.parse(message.data) as Envelope;
        if (env.type === 'orchestration_event') {
          const event = env.payload as OrchestrationEvent;
          if (event.runId === runId) applyEvent(event);
        } else if (env.type === 'orchestration_input_status') {
          if (env.payload?.runId === runId) setLiveInputRevision((revision) => revision + 1);
        } else if (env.type === 'approval_request') {
          const approval = env.payload as ApprovalRequest;
          if (approval.requestId && approval.runId === runId) {
            setApprovals((current) => upsertApprovalItem(current, approval, ++timelineOrderRef.current));
          }
        } else if (nativeInputEnabled && !receiptOnlySocketRef.current && env.type === 'user_input_request') {
          const request = decodeUserInputRequest(env.payload);
          if (request?.runId === runId) updateUserInputs((current) => receiveUserInputRequest(current, request));
        } else if (readNativeInput() && env.type === 'user_input_status') {
          const syncError = decodeUserInputSyncError(env.payload);
          if (syncError?.runId === runId && syncError.syncId === userInputSyncRef.current) {
            finishQuery(`${unavailableNotice()} ${syncFailureReason(syncError.reason)}`);
            return;
          }
          const status = decodeUserInputStatus(env.payload);
          if (status?.runId === runId) {
            const previous = userInputsRef.current;
            updateUserInputs((current) => receiveUserInputStatus(current, status));
            if (status.source !== 'hub' && userInputsRef.current !== previous &&
                previous.pendingSubmissions.length > 0 && userInputsRef.current.pendingSubmissions.length === 0 &&
                status.receipt?.outcome !== 'unknown') {
              // A live receipt cannot replace initial discovery of other open questions.
              if (hasQuerySnapshot) finishQuery();
              else setInputRecoveryNotice('');
            }
            if (status.status === 'rejected' && status.source === 'hub') sendQuery();
          }
        } else if (readNativeInput() && env.type === 'user_input_snapshot') {
          const snapshot = decodeUserInputSnapshot(env.payload);
          if (snapshot?.runId === runId && snapshot.syncId === userInputSyncRef.current &&
              (Boolean(snapshot.instanceId && userInputsRef.current.instanceId && snapshot.instanceId !== userInputsRef.current.instanceId) ||
                snapshot.revision >= userInputsRef.current.snapshotRevision)) {
            hasQuerySnapshot = true;
            clearQueryTimer();
            queryInFlight = false;
            updateUserInputs((current) => receiveUserInputSnapshot(current, snapshot));
            if (snapshot.reconcileError) {
              finishQuery(`${unavailableNotice()} ${snapshot.reconcileError}`);
            } else if (userInputsRef.current.pendingSubmissions.length > 0) {
              if (!queriedPending) sendQuery();
              else scheduleQuery();
            } else {
              finishQuery();
            }
          }
        } else if (env.type === 'status') {
          setConnectionStatus(env.payload?.status || t.connected);
        }
      } catch {
        // Ignore malformed frames.
      }
    };
    ws.onerror = () => {
      if (wsRef.current === ws) setConnectionStatus(t.connectionError);
    };
    ws.onclose = () => {
      stopHeartbeat?.();
      if (wsRef.current !== ws) return;
      if (terminalInputSyncTimerRef.current !== null) {
        window.clearTimeout(terminalInputSyncTimerRef.current);
        terminalInputSyncTimerRef.current = null;
      }
      updateUserInputs(confirmPendingUserInputs);
      setConnectionStatus(t.disconnected);
      if (activeRunIdRef.current !== runId) return;
      if (receiptOnlySocketRef.current && terminalInputRecoveryRef.current.attempts >= 3) {
        setInputRecoveryNotice(unavailableNotice());
        return;
      }
      const delay = Math.min(10000, 1000 * Math.max(1, reconnectAttemptsRef.current + 1));
      reconnectAttemptsRef.current += 1;
      clearReconnect();
      reconnectTimerRef.current = window.setTimeout(() => {
        reconnectTimerRef.current = null;
        if (activeRunIdRef.current !== runId || wsRef.current !== ws) return;
        void Promise.all([loadRun(runId), loadRunEvents(runId, false, 'after')])
          .then(([run]) => {
            if (activeRunIdRef.current !== runId || wsRef.current !== ws) return;
            connectRun(runId, !activeOrchestrationStatus(run.status));
          })
          .catch(() => {
            if (activeRunIdRef.current === runId && wsRef.current === ws) connectRun(runId, receiptOnlySocketRef.current);
          });
      }, delay);
    };
  }, [applyEvent, clearReconnect, closeWS, loadRun, loadRunEvents, startWSHeartbeat, nativeInputEnabled, language, updateUserInputs, t.connected, t.connecting, t.connectionError, t.disconnected, t.idle]);

  const activateRun = useCallback(async (run: OrchestrationRun, options: { syncURL?: boolean; replaceURL?: boolean } = {}) => {
    if (desiredRunIdRef.current && desiredRunIdRef.current !== run.id) return;
    const urlRunID = orchestrationRunIdFromPath(window.location.pathname);
    if (urlRunID && urlRunID !== run.id) return;
    const epoch = selectionEpochRef.current;
    setRunLoad({ runId: run.id, loading: true, ready: false, error: '' });
    draftingRunRef.current = false;
    const runAgentId = run.agentId || selectedAgentIdRef.current;
    timelineOrderRef.current = 0;
    activeRunIdRef.current = run.id;
    activeRunAgentIdRef.current = runAgentId;
    setActiveRunId(run.id);
    setLoadingOlderEvents(false);
    setRuns((current) => upsertOrchestrationRun(current, run));
    saveWorkspaceSelection(activeOrchestrationRunStorageKey, run.id);
    if (runAgentId) {
      selectedAgentIdRef.current = runAgentId;
      setSelectedAgentId(runAgentId);
      saveWorkspaceSelection('codexBridge.selectedAgentId', runAgentId);
      try { rememberActiveOrchestrationRunForAgent(runAgentId, run.id); } catch { /* Selection still works. */ }
    }
    setEvents((current) => current.filter((event) => event.runId === run.id));
    setApprovals((current) => current.filter((item) => item.approval.runId === run.id));
    restoreUserInputs(run.id);
    setMode(run.mode === 'debate' ? 'debate' : 'collaboration');
    const nextWorkerPair = normalizeOrchestrationWorkerPair(run.workerPair);
    setWorkerPair(nextWorkerPair);
    setFirstCli(nextWorkerPair === 'codex-codex' || run.firstCli === 'codex' ? 'codex' : 'claude');
    setProfile(run.profile === 'formal-proof' ? 'formal-proof' : 'default');
    setVerificationMode(run.profile === 'formal-proof' && run.verificationMode === 'tbci' ? 'tbci' : 'agent');
    setNativeContextCompaction(run.nativeContextCompaction === 'after-turn' ? 'after-turn' : 'off');
    setCwd(run.cwd || '');
    setMaxTurns(run.maxTurns || 4);
    stickToBottomRef.current = true;
    setShowScrollBottom(false);
    if (options.syncURL !== false) {
      navigate(orchestrationRunPath(run.id), { replace: options.replaceURL });
    }
    if (epoch !== selectionEpochRef.current || activeRunIdRef.current !== run.id || desiredRunIdRef.current !== run.id) return;
    runReadyRef.current = true;
    setRunLoad({ runId: run.id, loading: false, ready: true, error: '' });
    connectRun(run.id, !activeOrchestrationStatus(run.status));
  }, [connectRun, navigate, restoreUserInputs]);

  const selectRun = useCallback(async (runId: string, options: { syncURL?: boolean; replaceURL?: boolean } = {}) => {
    const epoch = ++selectionEpochRef.current;
    olderEventsLoadInFlightRef.current = false;
    setLoadingOlderEvents(false);
    runReads.cancel();
    runReadyRef.current = false;
    setRunLoad({ runId, loading: true, ready: false, error: '' });
    setError('');
    closeWS();
    terminalInputRecoveryRef.current = { runId, attempts: 0, complete: false };
    draftingRunRef.current = false;
    desiredRunIdRef.current = runId;
    if (options.syncURL !== false && orchestrationRunIdFromPath(window.location.pathname) !== runId) {
      navigate(orchestrationRunPath(runId), { replace: options.replaceURL });
    }
    timelineOrderRef.current = 0;
    activeRunIdRef.current = runId;
    setActiveRunId(runId);
    setEvents((current) => current.filter((event) => event.runId === runId));
    setApprovals((current) => current.filter((item) => item.approval.runId === runId));
    restoreUserInputs(runId);
    try {
      const [run] = await Promise.all([loadRun(runId), loadRunEvents(runId, true, 'latest')]);
      if (epoch !== selectionEpochRef.current || activeRunIdRef.current !== runId || desiredRunIdRef.current !== runId) return;
      await activateRun(run, options);
    } catch (err) {
      if (epoch !== selectionEpochRef.current) return;
      runReads.cancel();
      setRunLoad({ runId, loading: false, ready: false, error: err instanceof StatsTimeoutError
        ? (language === 'zh' ? '对话加载超时，请重试。' : 'Conversation request timed out. Please retry.')
        : err instanceof Error ? err.message : t.failedLoadOrchestration });
    }
  }, [activateRun, closeWS, language, loadRun, loadRunEvents, navigate, restoreUserInputs, runReads, t.failedLoadOrchestration]);

  const respondOrchestrationApproval = useCallback((requestId: string, decision: 'accept' | 'decline' | 'cancel') => {
    if (!wsRef.current || wsRef.current.readyState !== WebSocket.OPEN || receiptOnlySocketRef.current || !activeRunIdRef.current) return;
    wsRef.current.send(JSON.stringify({
      type: 'approval_response',
      payload: { requestId, decision },
    }));
    setApprovals((current) => updateApprovalItemStatus(current, requestId, approvalStatusFromDecision(decision)));
  }, []);

  const respondUserInput = useCallback((requestId: string, answers: Record<string, UserInputAnswer>) => {
    const ws = wsRef.current;
    const item = userInputsRef.current.items.find((entry) => entry.request.requestId === requestId);
    if (!ws || ws.readyState !== WebSocket.OPEN || receiptOnlySocketRef.current || !item || item.status !== 'pending' || item.request.runId !== activeRunIdRef.current) return;
    if (!item.request.instanceId) return;
    const pending: UserInputPendingSubmission = { userId: user.id, runId: item.request.runId, requestId,
      executionId: item.request.executionId, instanceId: item.request.instanceId,
      submissionId: crypto.randomUUID(), createdAt: Date.now() };
    try {
      const stored = loadUserInputStorage(sessionStorage, user.id);
      // Never send an answer without a durable identity for this tab.
      if (stored.some((entry) => entry.runId === pending.runId && entry.requestId === requestId)) {
        setInputRecoveryNotice(language === 'zh' ? '此问题还有未确认的提交，请先重新查询。' : 'This question has an unconfirmed submission. Query again first.');
        return;
      }
      saveUserInputStorage(sessionStorage, user.id, [...stored, pending]);
    } catch {
      setInputStorageNotice(language === 'zh' ? '无法保存未确认提交记录（存储不可用或已满）；答案尚未发送。恢复存储后可重试。' : 'Pending submission storage is unavailable or full. The answer was not sent. Restore storage and retry.');
      return;
    }
    updateUserInputs((current) => submittingUserInput({ ...current, pendingSubmissions: [...current.pendingSubmissions, pending] }, requestId, pending.submissionId));
    try {
      ws.send(JSON.stringify({ type: 'user_input_response', payload: { requestId, runId: item.request.runId,
        submissionId: pending.submissionId, answers } }));
    } catch {
      updateUserInputs(confirmPendingUserInputs);
    }
    userInputQueryRef.current(true);
  }, [language, updateUserInputs, user.id]);

  const retryUserInputQuery = useCallback(() => {
    try {
      const pending = loadUserInputStorage(sessionStorage, user.id).filter((entry) => entry.runId === activeRunIdRef.current);
      const stored = updateUserInputs((current) => ({ ...current, pendingSubmissions: pending, items: current.items.map((item) => {
        const identity = pending.find((entry) => entry.requestId === item.request.requestId);
        return identity && item.status === 'pending' ? { ...item, status: 'confirming', submissionId: identity.submissionId } : item;
      }) }));
      if (!stored) return;
    } catch {
      setInputStorageNotice(language === 'zh' ? '浏览器会话存储仍不可用，无法核对未确认提交。' : 'Session storage remains unavailable. Pending submissions cannot be queried.');
      return;
    }
    setInputStorageNotice('');
    setInputRecoveryNotice('');
    if (wsRef.current?.readyState === WebSocket.OPEN) userInputQueryRef.current(true);
    else if (activeRunIdRef.current) {
      terminalInputRecoveryRef.current = { runId: activeRunIdRef.current, attempts: 0, complete: false };
      connectRun(activeRunIdRef.current, !activeOrchestrationStatus(activeRun?.status));
    }
  }, [activeRun?.status, connectRun, language, updateUserInputs, user.id]);

  const nativeInputAgentOnlineRef = useRef(selectedAgent?.online);
  useEffect(() => {
    const wasOnline = nativeInputAgentOnlineRef.current;
    nativeInputAgentOnlineRef.current = selectedAgent?.online;
    if (wasOnline === false && selectedAgent?.online && userInputsRef.current.pendingSubmissions.length > 0) retryUserInputQuery();
  }, [retryUserInputQuery, selectedAgent?.online]);

  const refreshOrchestration = useCallback(async () => {
    if (refreshOrchestrationInFlightRef.current) return refreshOrchestrationInFlightRef.current;
    const task = (async () => {
      const refreshEpoch = agentSelectionEpochRef.current;
      const selectionEpoch = selectionEpochRef.current;
      const isCurrentRefresh = () => refreshEpoch === agentSelectionEpochRef.current && selectionEpoch === selectionEpochRef.current;
      setRefreshingOrchestration(true);
      try {
        const requestedRunId = orchestrationRunIdFromPath(window.location.pathname);
        const loadedAgents = await loadAgents();
        if (!isCurrentRefresh()) return;
        if (requestedRunId) {
          if (orchestrationRunIdFromPath(window.location.pathname) !== requestedRunId) return;
          // selectRun owns detail + first history; the path effect may already have started it.
          if (activeRunIdRef.current !== requestedRunId) await selectRun(requestedRunId, { syncURL: false });
          return;
        }
        const savedAgentId = readWorkspaceSelection('codexBridge.selectedAgentId') || selectedAgentIdRef.current;
        const agentId = preferredAgentID(loadedAgents, savedAgentId);
        selectedAgentIdRef.current = agentId;
        setSelectedAgentId(agentId);
        saveWorkspaceSelection('codexBridge.selectedAgentId', agentId);
        await loadRuns();
        if (!isCurrentRefresh() || selectedAgentIdRef.current !== agentId) return;
        if (draftingRunRef.current) return;
        if (activeRunIdRef.current && activeRunAgentIdRef.current === agentId) {
          return;
        }
        clearActiveOrchestration();
      } catch (err) {
        if (isCurrentRefresh()) throw err;
      } finally {
        refreshOrchestrationInFlightRef.current = null;
        setRefreshingOrchestration(false);
        orchestrationBootedRef.current = true;
      }
    })();
    refreshOrchestrationInFlightRef.current = task;
    return task;
  }, [clearActiveOrchestration, loadAgents, loadRuns, selectRun]);

  const refreshVisibleOrchestrationData = useCallback(async (refreshHistory = true) => {
    const runId = orchestrationRunIdFromPath(window.location.pathname) || activeRunIdRef.current;
    if (runId && !runReadyRef.current) return;
    setRefreshingOrchestration(true);
    try {
      const [nextAgents, , refreshedRun] = await Promise.all([
        api<{ agents: Agent[] }>('/api/agents'),
        refreshHistory ? loadRuns() : Promise.resolve([]),
        runId ? runReads.read<{ run: OrchestrationRun }>(`/api/orchestrations/${encodeURIComponent(runId)}`) : Promise.resolve(null),
        runId ? loadRunEvents(runId, false, 'after') : Promise.resolve([]),
      ]);
      setAgents(nextAgents.agents || []);
      setAgentsLoaded(true);
      const currentRun = refreshedRun && isOrchestrationRun(refreshedRun.run) ? refreshedRun.run : null;
      if (currentRun) setRuns((current) => upsertOrchestrationRun(current, { ...currentRun, archivedAt: currentRun.archivedAt || 0 }));
    } finally {
      setRefreshingOrchestration(false);
    }
  }, [loadRunEvents, loadRuns, runReads]);

  useEffect(() => {
    let stopped = false;
    let retryTimer: number | null = null;
    const retry = () => {
      if (stopped || document.visibilityState !== 'visible' || !navigator.onLine) return;
      if (retryTimer !== null) {
        window.clearTimeout(retryTimer);
        retryTimer = null;
      }
      void refreshOrchestration()
        .then(() => {
          if (!stopped) setError('');
        })
        .catch((err) => {
          if (stopped) return;
          setError(err instanceof Error ? err.message : t.failedLoadOrchestration);
          retryTimer = window.setTimeout(retry, 3000);
        });
    };
    const recover = () => {
      if (document.visibilityState !== 'visible' || !navigator.onLine) return;
      if (!orchestrationBootedRef.current) {
        retry();
        return;
      }
      // A background recovery must not reset the pages the user is browsing.
      void refreshVisibleOrchestrationData(false).catch(() => undefined);
    };
    retry();
    window.addEventListener('online', recover);
    document.addEventListener('visibilitychange', recover);
    return () => {
      stopped = true;
      if (retryTimer !== null) window.clearTimeout(retryTimer);
      window.removeEventListener('online', recover);
      document.removeEventListener('visibilitychange', recover);
      selectionEpochRef.current += 1;
      runReads.cancel();
      closeWS();
    };
  }, []);

  useEffect(() => {
    if (!orchestrationBootedRef.current || !pathRunId || pathRunId === activeRunIdRef.current) return;
    selectRun(pathRunId, { syncURL: false }).catch((err) => setError(err instanceof Error ? err.message : t.failedLoadOrchestration));
  }, [pathRunId, selectRun, t.failedLoadOrchestration]);

  useEffect(() => {
    let stopped = false;
    const syncAgents = () => {
      if (stopped || document.visibilityState !== 'visible') return;
      refreshAgentsQuietly().catch(() => undefined);
    };
    const interval = window.setInterval(syncAgents, 5000);
    document.addEventListener('visibilitychange', syncAgents);
    return () => {
      stopped = true;
      window.clearInterval(interval);
      document.removeEventListener('visibilitychange', syncAgents);
    };
  }, [refreshAgentsQuietly]);

  useEffect(() => {
    selectedAgentIdRef.current = selectedAgentId;
  }, [selectedAgentId]);

	const refreshWorkerPresets = useCallback(async () => {
		const agentId = selectedAgentIdRef.current;
		if (!agentId) {
			setPresets([]);
			return;
		}
		const data = await api<{ presets: CLIConfigPreset[] }>(`/api/agents/${encodeURIComponent(agentId)}/cli-config/presets`);
		if (selectedAgentIdRef.current === agentId) {
			setPresets(Array.isArray(data.presets) ? data.presets : []);
		}
	}, []);

  useEffect(() => {
    let cancelled = false;
    setPresets([]);
    setSelectedWorkerProfiles(Object.fromEntries(workerProfileSlots.map(({ slot }) => [slot, OFFICIAL_LOGIN_PRESET_ID])));
    setOfficialLoginConfirmed({});
    setSelectedWorkerEfforts({});
    setSelectedOfficialModels({});
    setWorkerProfilesTouched(false);
    if (!selectedAgentId) return () => { cancelled = true; };
		void refreshWorkerPresets()
      .catch(() => undefined);
    return () => { cancelled = true; };
  }, [refreshWorkerPresets, selectedAgentId, user.id, workerProfileSlots]);

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();
    if (!officialModelsEnabled || !officialModelsSupported || !workerProfileSlots.some(({ cli }) => cli === 'codex')) {
      setOfficialCatalog({ key: officialCatalogKey, models: [], loading: false, error: '' });
      return () => { cancelled = true; controller.abort(); };
    }
    setOfficialCatalog({ key: officialCatalogKey, models: [], loading: true, error: '' });
    void api<{ models: OfficialCLIModel[] }>(`/api/agents/${encodeURIComponent(selectedAgentId)}/cli-config/official-models?cli=codex`, { signal: controller.signal })
      .then((data) => {
        if (!cancelled) setOfficialCatalog({ key: officialCatalogKey, models: Array.isArray(data.models) ? data.models : [], loading: false, error: '' });
      })
      .catch((err) => {
        if (!cancelled) setOfficialCatalog({ key: officialCatalogKey, models: [], loading: false, error: err instanceof Error ? err.message : String(err) });
      });
    return () => { cancelled = true; controller.abort(); };
  }, [officialCatalogKey, officialModelsEnabled, officialModelsSupported, selectedAgentId, workerProfileSlots]);

  useEffect(() => {
    setSelectedPromptKey('initial');
    setOrchestrationProgress(null);
    setSelectedProgressTaskNumber(null);
    setSelectedProgressGraphId('');
    setAgentGraphExpanded(false);
    setPlanningWorkspaceOpen(false);
  }, [activeRunId]);

  useEffect(() => {
    if (!progressTasks.length) return;
    setSelectedProgressTaskNumber((current) => progressTasks.some((item) => item.taskNumber === current) ? current : progressTasks[progressTasks.length - 1].taskNumber);
  }, [progressTasks]);

  useEffect(() => {
    setSelectedPromptKey(`task-${selectedProgressTask?.taskNumber || 1}`);
    setSelectedProgressGraphId(selectedProgressTask?.graph?.id || selectedProgressTask?.graphs?.[selectedProgressTask.graphs.length - 1]?.id || '');
  }, [selectedProgressTask?.taskNumber]);

  useEffect(() => {
    if (!activeRunId || !planWorkspaceEnabled) return;
    // Initial history supplies the first refresh key; don't race it with two
    // expensive progress reads (one empty key, then the history's key).
    if (runLoad.runId !== activeRunId || !runLoad.ready) return;
    let current = true;
    const request = beginStatsRequest(signal => api<OrchestrationProgress>(`/api/orchestrations/${encodeURIComponent(activeRunId)}/progress`, { signal }));
    void request.promise.then(data => { if (current) setOrchestrationProgress(data); }, () => undefined);
    return () => { current = false; request.cancel(); };
  }, [activeRunId, planWorkspaceEnabled, progressRefreshKey, runLoad.ready, runLoad.runId]);

  useEffect(() => {
    setCollapsedTimelineGroups((current) => {
      if (collapsedTimelineRunIdRef.current !== activeRunId) {
        if (!timelineGroups.length) {
          collapsedTimelineRunIdRef.current = '';
          return {};
        }
        collapsedTimelineRunIdRef.current = activeRunId;
        return defaultCollapsedTimelineGroups(timelineGroups);
      }
      return reconcileCollapsedTimelineGroups(current, timelineGroups);
    });
  }, [activeRunId, timelineGroups]);

  useEffect(() => {
    // Before the first /api/agents response lands, selectedAgent is always
    // empty; clearing then would wipe the remembered run and strip a deep-link
    // URL on every page load.
    if (!agentsLoaded) return;
    // A deep-linked run is an explicit selection. Agent synchronization can
    // update endpoint metadata, but must never replace the URL-selected run
    // with the browser's remembered run for another endpoint.
    if (pathRunId) {
      if (activeRunIdRef.current !== pathRunId) {
        selectRun(pathRunId, { syncURL: false }).catch((err) => setError(err instanceof Error ? err.message : t.failedLoadOrchestration));
      }
      return;
    }
    if (draftingRunRef.current) return;
    if (!selectedAgent?.id) {
      if (activeRunIdRef.current && activeRunAgentIdRef.current === selectedAgentIdRef.current) return;
      clearActiveOrchestration();
      return;
    }
    const currentRun = runs.find((run) => run.id === activeRunIdRef.current);
    if (currentRun?.agentId === selectedAgent.id) return;
    clearActiveOrchestration();
  }, [agentsLoaded, clearActiveOrchestration, pathRunId, runs, selectRun, selectedAgent?.id, t.failedLoadOrchestration]);

  useEffect(() => {
    if (!activeRunId || !activeOrchestrationStatus(activeRun?.status) || runLoad.runId !== activeRunId || !runLoad.ready) return;
    let stopped = false;
    let inFlight = false;
    const syncActiveRun = async () => {
      if (stopped || inFlight || document.visibilityState !== 'visible') return;
      inFlight = true;
      try {
        await Promise.all([loadRun(activeRunId), loadRunEvents(activeRunId, false, 'after')]);
      } catch {
        // The websocket remains the primary live path; polling is a quiet fallback.
      } finally {
        inFlight = false;
      }
    };
    const interval = window.setInterval(() => {
      if (!stopped) void syncActiveRun();
    }, 3000);
    const handleVisibility = () => {
      if (document.visibilityState === 'visible' && !stopped) void syncActiveRun();
    };
    document.addEventListener('visibilitychange', handleVisibility);
    return () => {
      stopped = true;
      window.clearInterval(interval);
      document.removeEventListener('visibilitychange', handleVisibility);
    };
  }, [activeRunId, activeRun?.status, loadRun, loadRunEvents, runLoad.ready, runLoad.runId]);

  useEffect(() => {
    if (!activeRunId || !activeRun?.status || runLoad.runId !== activeRunId || !runLoad.ready) return;
    if (activeOrchestrationStatus(activeRun.status)) {
      if (receiptOnlySocketRef.current && terminalInputRecoveryRef.current.runId === activeRunId) {
        connectRun(activeRunId);
      }
      return;
    }
    if (!nativeInputReadEnabled) {
      closeWS();
      restoreUserInputs(activeRunId);
      setConnectionStatus(t.idle);
      return;
    }
    if (terminalInputRecoveryRef.current.runId === activeRunId && terminalInputRecoveryRef.current.complete) return;
    const ws = wsRef.current;
    receiptOnlySocketRef.current = true;
    if (ws && socketRunIdRef.current !== activeRunId) {
      connectRun(activeRunId, true);
    } else if (ws?.readyState === WebSocket.OPEN) {
      userInputQueryRef.current(true);
    } else if (ws?.readyState === WebSocket.CLOSED) {
      // Initial selection is connected by activateRun after its state reset.
      // A closed live socket, however, must recover even after the run ended.
      connectRun(activeRunId, true);
    }
  }, [activeRunId, activeRun?.status, closeWS, connectRun, nativeInputReadEnabled, restoreUserInputs, runLoad.ready, runLoad.runId, t.idle]);

  useEffect(() => {
    if (!isRunning) return;
    const interval = window.setInterval(() => setElapsedTick((current) => current + 1), 1000);
    return () => window.clearInterval(interval);
  }, [isRunning]);

  useEffect(() => {
    const id = window.requestAnimationFrame(() => {
      const container = scrollRef.current;
      if (!container) return;
      if (stickToBottomRef.current) {
        scrollTimelineToBottom('auto');
        return;
      }
      setShowScrollBottom(timelineItems.length > 0 && !isNearBottom(container));
    });
    return () => window.cancelAnimationFrame(id);
  }, [activeRunId, scrollTimelineToBottom, timelineItems]);

  useEffect(() => {
    if (!workingDirs.length) {
      if (cwd) setCwd('');
      return;
    }
    if (!cwd || !workingDirs.includes(cwd)) {
      setCwd(workingDirs[0]);
    }
  }, [cwd, workingDirs]);

  const addFiles = async (inputFiles: FileList | null) => {
    if (!inputFiles?.length) return;
    const next = await Promise.all(Array.from(inputFiles).map(readUploadAttachment));
    setFiles((current) => [...current, ...next].slice(0, 12));
    if (fileInputRef.current) fileInputRef.current.value = '';
  };

  const removeFile = (id: string) => {
    setFiles((current) => current.filter((file) => file.id !== id));
  };

  const startRun = async () => {
    const task = prompt.trim();
    if (!task || creating || isRunning || selectionBlocked) return;
    if (!activeRun && !workerProfilesComplete) {
      setError(t.workerPresetRequired);
      return;
    }
    if (capabilityProblems.length > 0) {
      setError(capabilityProblems.join(' '));
      return;
    }
    if ((!activeRun || workerProfilesTouched) && officialModelsEnabled && !officialModelsSupported && workerProfileSlots.some(({ slot, cli }) => cli === 'codex' && selectedWorkerProfiles[slot] === OFFICIAL_LOGIN_PRESET_ID && selectedOfficialModels[slot])) {
      setError(t.officialModelsUpdateBridge);
      return;
    }
	if (selectedTBCIUnavailable) {
		setError(`${t.tbciCapabilityUnavailable} ${tbciUnavailableReason}`);
		return;
	}
    setCreating(true);
    setError('');
    const submittedTarget = draftTarget;
    const submittedFileIds = new Set(files.map((file) => file.id));
    try {
      const endpoint = activeRun ? `/api/orchestrations/${encodeURIComponent(activeRun.id)}/prompts` : '/api/orchestrations';
      const selectedProfiles = selectedWorkerSlotValues(workerProfileSlots, selectedWorkerProfiles);
      const body: Record<string, unknown> = {
        mode,
        workerPair,
        firstCli: workerPair === 'codex-codex' ? 'codex' : workerPair === 'claude-claude' ? 'claude' : firstCli,
        profile,
        verificationMode: profile === 'formal-proof' ? verificationMode : 'agent',
        nativeContextCompaction,
        nativeUserInputClientVersion: 3,
        prompt: task,
        title: titleFromPrompt(task, t),
        cwd: cwd.trim(),
        ...(dynamicExecution ? { executionMode: 'dynamic' } : { maxTurns }),
        agentId: selectedAgent?.id || '',
        files: files.map(({ name, mimeType, size, data }) => ({ name, mimeType, size, data })),
      };
      if (!activeRun || workerProfilesTouched) {
        body.workerProfilePresetIds = selectedProfiles;
        body.workerProfileEfforts = selectedWorkerSlotValues(workerProfileSlots, selectedWorkerEfforts);
        if (officialModelsEnabled) {
          const models = selectedWorkerSlotValues(workerProfileSlots.filter(({ slot, cli }) => cli === 'codex' && selectedProfiles[slot] === OFFICIAL_LOGIN_PRESET_ID), selectedOfficialModels);
          if (Object.keys(models).length) body.workerProfileModels = models;
        }
      }
      const data = await api<{ run: OrchestrationRun }>(endpoint, {
        method: 'POST',
        body: JSON.stringify(body),
      });
      if (!isOrchestrationRun(data.run)) throw new Error(t.failedLoadOrchestration);
      setRuns((current) => upsertOrchestrationRun(current, { ...data.run, archivedAt: data.run.archivedAt || 0 }));
      const cleared = clearSubmittedPrompt();
      void loadRuns();
      if (draftTargetRef.current === submittedTarget) {
        setFiles((current) => current.filter((file) => !submittedFileIds.has(file.id)));
        // Keep newer text in the new-run composer. A delayed response must not
        // change a selection made after this request was submitted.
        if (activeRun || cleared) await selectRun(data.run.id);
      }
    } catch (err) {
      if (draftTargetRef.current === submittedTarget) setError(err instanceof Error ? err.message : t.failedStartOrchestration);
    } finally {
      setCreating(false);
    }
  };

  const confirmOfficialLogin = async (slot: string, cli: 'codex' | 'claude') => {
    if (!selectedAgent?.id || creating || isRunning) return;
    setCreating(true);
    setError('');
    try {
      await api(`/api/agents/${encodeURIComponent(selectedAgent.id)}/cli-config/official-reset`, {
        method: 'POST',
        body: JSON.stringify({ cli }),
      });
      setOfficialLoginConfirmed((current) => ({ ...current, [slot]: true }));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setCreating(false);
    }
  };

  const cancelRun = async () => {
    if (!activeRun || !canCancelOrchestrationStatus(activeRun.status)) return;
    setRuns((current) => current.map((run) => run.id === activeRun.id ? { ...run, status: 'canceling' } : run));
    const data = await api<{ status?: string }>(`/api/orchestrations/${encodeURIComponent(activeRun.id)}/cancel`, { method: 'POST', body: '{}' });
    if (data.status && data.status !== 'canceling') {
      setRuns((current) => current.map((run) => run.id === activeRun.id ? { ...run, status: data.status || run.status } : run));
    }
  };

  const deleteRun = async (run: OrchestrationRun) => {
    const confirmMessage = activeOrchestrationStatus(run.status) ? t.deleteActiveRunConfirm : t.deleteRunConfirm;
    if (deletingRunId || !window.confirm(confirmMessage)) return;
    setDeletingRunId(run.id);
    setError('');
    try {
      await api(`/api/orchestrations/${encodeURIComponent(run.id)}`, { method: 'DELETE' });
      try {
        saveUserInputStorage(sessionStorage, user.id, loadUserInputStorage(sessionStorage, user.id).filter((entry) => entry.runId !== run.id));
      } catch { /* A deleted run cannot accept further answers even if storage is unavailable. */ }
      setRuns((current) => current.filter((item) => item.id !== run.id));
      try { forgetActiveOrchestrationRunForAgent(run.agentId, run.id); } catch { /* Deletion succeeded. */ }
      void loadRuns();
      if (activeRunIdRef.current === run.id) clearActiveOrchestration();
    } catch (err) {
      setError(err instanceof Error ? `${t.failedDeleteRun}: ${err.message}` : t.failedDeleteRun);
    } finally {
      setDeletingRunId('');
    }
  };

  const updateRunMetadata = async (run: OrchestrationRun, changes: { title?: string; archived?: boolean }) => {
    if (updatingRunId) return;
    setUpdatingRunId(run.id);
    setMetadataError('');
    try {
      const data = await api<{ run: OrchestrationRun }>(`/api/orchestrations/${encodeURIComponent(run.id)}`, {
        method: 'PATCH', body: JSON.stringify(changes),
      });
      if (!isOrchestrationRun(data.run)) throw new Error(t.failedLoadOrchestration);
      setRuns((current) => upsertOrchestrationRun(current, { ...data.run, archivedAt: data.run.archivedAt || 0 }));
      if (changes.title !== undefined) setEditingRun(null);
      await loadRuns();
    } catch (err) {
      setMetadataError(err instanceof Error ? err.message : t.failedLoadOrchestration);
    } finally { setUpdatingRunId(''); }
  };

  const shareRun = async (run: OrchestrationRun | null) => {
    if (!run || sharingRunId) return;
    setSharingRunId(run.id);
    setError('');
    try {
      const data = await api<{ share: ShareInfo }>(`/api/orchestrations/${encodeURIComponent(run.id)}/share`, { method: 'POST', body: '{}' });
      const url = data.share.url || `${window.location.origin}/share/${data.share.id}`;
      await copyText(url);
      setShareCopiedRunId(run.id);
      window.setTimeout(() => setShareCopiedRunId(''), 1400);
    } catch (err) {
      setError(err instanceof Error ? `${t.failedCreateShare}: ${err.message}` : t.failedCreateShare);
    } finally {
      setSharingRunId('');
    }
  };

  const logout = async () => {
    closeWS();
    try { clearUserInputStorage(sessionStorage, user.id); } catch { /* Logout still revokes the session. */ }
    await api('/api/logout', { method: 'POST', body: '{}' });
    onLogout();
  };

  const selectAgent = (agentId: string) => {
    agentSelectionEpochRef.current += 1;
    selectedAgentIdRef.current = agentId;
    setSelectedAgentId(agentId);
    saveWorkspaceSelection('codexBridge.selectedAgentId', agentId);
    if (draftingRunRef.current) return;
    clearActiveOrchestration();
  };

  const openSettings = (focus: 'cli' | '' = '') => {
    setSettingsFocus(focus);
    setSettingsOpen(true);
  };

  const selectWorkerPair = (nextWorkerPair: WorkerPair) => {
    setWorkerPair(nextWorkerPair);
    setSelectedWorkerProfiles({});
    setSelectedWorkerEfforts({});
    setSelectedOfficialModels({});
    setWorkerProfilesTouched(false);
    if (nextWorkerPair === 'codex-codex') {
      setFirstCli('codex');
	} else if (nextWorkerPair === 'claude-claude') {
	  setFirstCli('claude');
    }
  };

  const renderWorkerProfileSelectors = () => {
    const disabled = creating || isRunning;
    return (
      <div className="space-y-2">
        <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.workerModelProfiles}</label>
        <div className="grid gap-2 sm:grid-cols-2">
          {workerProfileSlots.map(({ slot, cli, label }) => {
            const options = presets.filter((preset) => preset.cli === cli);
            const selectedPreset = options.find((preset) => preset.id === selectedWorkerProfiles[slot]);
            const isOfficialCodex = officialModelsEnabled && cli === 'codex' && selectedWorkerProfiles[slot] === OFFICIAL_LOGIN_PRESET_ID;
            const officialModel = isOfficialCodex ? officialModels.find((model) => model.model === selectedOfficialModels[slot]) : undefined;
            const reasoningLevels = isOfficialCodex ? (officialModel?.supportedReasoningEfforts || []) : (selectedPreset?.reasoningLevels || []);
            return (
              <div key={slot} className="block min-w-0 space-y-1.5">
                <span className="text-xs text-muted-foreground">{label}</span>
                <select
                  value={selectedWorkerProfiles[slot] || ''}
                  onChange={(event) => {
                    const value = event.target.value;
                    const nextPreset = options.find((preset) => preset.id === value);
                    const nextLevels = nextPreset?.reasoningLevels || [];
                    const nextDefault = nextPreset?.reasoningEffort || nextPreset?.reasoningDefault || '';
                    setSelectedWorkerProfiles((current) => ({ ...current, [slot]: value }));
                    setSelectedOfficialModels((current) => ({ ...current, [slot]: '' }));
                    setOfficialLoginConfirmed((current) => ({ ...current, [slot]: false }));
                    setSelectedWorkerEfforts((current) => ({ ...current, [slot]: nextLevels.includes(nextDefault) ? nextDefault : '' }));
                    setWorkerProfilesTouched(true);
                  }}
                  disabled={disabled}
                  className="flex h-9 w-full min-w-0 rounded-md border border-input bg-transparent px-2 py-1 text-xs shadow-sm disabled:cursor-not-allowed disabled:opacity-50"
                  aria-label={`${label} ${t.workerModelProfiles}`}
                >
                  <option value="" disabled>{options.length ? t.selectPreset : t.noProviderPresets}</option>
                  {options.map((preset) => <option key={preset.id} value={preset.id}>{preset.name} · {preset.model}</option>)}
                  <option value={OFFICIAL_LOGIN_PRESET_ID}>{t.officialLogin}</option>
                </select>
                {selectedWorkerProfiles[slot] === OFFICIAL_LOGIN_PRESET_ID && (
                  <div className="flex items-center justify-between gap-2 rounded-md border border-amber-500/30 bg-amber-500/5 px-2 py-1.5 text-[11px] text-muted-foreground">
                    <span>{officialLoginConfirmed[slot] ? t.officialLoginConfirmed : t.officialLoginHint}</span>
                    {!officialLoginConfirmed[slot] && <Button size="sm" variant="secondary" className="h-7 shrink-0 gap-1" onClick={() => confirmOfficialLogin(slot, cli)} disabled={creating || disabled}><Check className="h-3.5 w-3.5" />{t.confirm}</Button>}
                  </div>
                )}
                {isOfficialCodex && <>
                  <select
                    value={selectedOfficialModels[slot] || ''}
                    onChange={(event) => {
                      const value = event.target.value;
                      const levels = officialModels.find((model) => model.model === value)?.supportedReasoningEfforts || [];
                      setSelectedOfficialModels((current) => ({ ...current, [slot]: value }));
                      setSelectedWorkerEfforts((current) => ({ ...current, [slot]: levels.includes(current[slot]) ? current[slot] : '' }));
                      setWorkerProfilesTouched(true);
                    }}
                    disabled={disabled || !officialModelsSupported || officialCatalogLoading || !officialModels.length}
                    className="flex h-8 w-full min-w-0 rounded-md border border-input bg-transparent px-2 py-1 text-[11px] shadow-sm disabled:cursor-not-allowed disabled:opacity-50"
                    aria-label={`${label} ${t.modelName}`}
                  >
                    <option value="">{officialCatalogLoading ? t.officialModelLoading : t.officialModelDefault}</option>
                    {officialModels.map((model) => <option key={model.model} value={model.model}>{model.displayName || model.model}</option>)}
                  </select>
                  {!officialModelsSupported && <p className="text-[11px] text-muted-foreground">{t.officialModelsUpdateBridge}</p>}
                  {officialModelsSupported && !officialCatalogLoading && (!officialModels.length || officialCatalogError) && <p className="text-[11px] text-destructive" role="status">{t.officialModelUnavailable}{officialCatalogError && ` ${officialCatalogError}`}</p>}
                </>}
                <select
                  value={selectedWorkerEfforts[slot] || ''}
                  onChange={(event) => {
                    setSelectedWorkerEfforts((current) => ({ ...current, [slot]: event.target.value }));
                    setWorkerProfilesTouched(true);
                  }}
                  disabled={disabled || !(isOfficialCodex ? officialModelsSupported && officialModel : selectedPreset) || !reasoningLevels.length}
                  className="flex h-8 w-full min-w-0 rounded-md border border-input bg-transparent px-2 py-1 text-[11px] shadow-sm disabled:cursor-not-allowed disabled:opacity-50"
                  aria-label={`${label} ${t.reasoningEffort}`}
                >
                  <option value="">{isOfficialCodex ? (officialModel ? (reasoningLevels.length ? t.defaultReasoningEffort : t.officialModelNoEfforts) : t.officialModelSelectFirst) : selectedPreset ? (reasoningLevels.length ? t.defaultReasoningEffort : t.modelCatalogUnavailable) : t.selectPreset}</option>
                  {reasoningLevels.map((level) => <option key={level} value={level}>{level}{level === (isOfficialCodex ? officialModel?.defaultReasoningEffort : selectedPreset?.reasoningDefault) ? ` (${t.defaultReasoningEffort})` : ''}</option>)}
                </select>
              </div>
            );
          })}
        </div>
      </div>
    );
  };

  const renderWorkerPairSelector = (placement: 'toolbar' | 'panel') => {
    const isToolbar = placement === 'toolbar';
    const disabled = creating || isRunning;
    return (
      <div className={isToolbar ? "flex shrink-0 items-center gap-2" : "space-y-2"}>
        {isToolbar ? (
          <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.workerPair}</span>
        ) : (
          <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.workerPair}</label>
        )}
        <div className={cn("grid grid-cols-3 gap-1 rounded-lg border border-border bg-muted p-1", isToolbar ? "w-[22rem]" : "w-full")}>
          <button
            className={cn("h-8 rounded-md px-2 text-xs font-medium", workerPair === 'claude-codex' ? "bg-background shadow-sm" : "text-muted-foreground")}
            onClick={() => selectWorkerPair('claude-codex')}
            disabled={disabled}
            aria-pressed={workerPair === 'claude-codex'}
          >
            {t.workerPairClaudeCodex}
          </button>
          <button
            className={cn("h-8 rounded-md px-2 text-xs font-medium", workerPair === 'codex-codex' ? "bg-background shadow-sm" : "text-muted-foreground")}
            onClick={() => selectWorkerPair('codex-codex')}
            disabled={disabled}
            aria-pressed={workerPair === 'codex-codex'}
          >
            {t.workerPairCodexCodex}
          </button>
		  <button
			className={cn("h-8 rounded-md px-2 text-xs font-medium", workerPair === 'claude-claude' ? "bg-background shadow-sm" : "text-muted-foreground")}
			onClick={() => selectWorkerPair('claude-claude')}
			disabled={disabled}
			aria-pressed={workerPair === 'claude-claude'}
		  >
			{t.workerPairClaudeClaude}
		  </button>
        </div>
      </div>
    );
  };

  const startDraftRun = () => {
    agentSelectionEpochRef.current += 1;
    draftingRunRef.current = true;
    clearActiveOrchestration();
    if (!leadingEdition) setMaxTurns(defaultOrchestrationMaxTurns);
    setFiles([]);
    setError('');
    window.setTimeout(() => taskInputRef.current?.focus(), 0);
  };

  const toggleTimelineGroup = (key: string) => {
    setCollapsedTimelineGroups((current) => ({ ...current, [key]: !current[key] }));
  };

  const jumpToTimelineItem = (groupKey: string, targetID: string) => {
    setCollapsedTimelineGroups((current) => ({ ...current, [groupKey]: false }));
    stickToBottomRef.current = false;
    window.requestAnimationFrame(() => {
      window.requestAnimationFrame(() => {
        const container = scrollRef.current;
        const target = document.getElementById(targetID);
        if (!container || !target) return;
        const containerTop = container.getBoundingClientRect().top;
        const targetTop = target.getBoundingClientRect().top;
        container.scrollTo({ top: container.scrollTop + targetTop - containerTop - 12, behavior: 'smooth' });
      });
    });
  };

  return (
    <div className="phone-workspace h-screen w-full flex bg-background text-foreground overflow-hidden font-sans">
      <MobileViewport />
      <MobileSurface id="orchestration-history" open={mobileHistoryOpen} onClose={() => setMobileHistoryOpen(false)} title={t.runHistory} closeLabel={t.cancel} className="hidden md:flex w-[280px] flex-col border-r border-sidebar-border bg-sidebar" bodyClassName="flex min-h-0 flex-1 flex-col">
        <div className="h-14 flex items-center px-4 border-b border-sidebar-border shrink-0">
          <div className="flex items-center gap-2 font-medium">
            <div className="h-6 w-6 rounded-md bg-primary text-primary-foreground flex items-center justify-center">
              <GitBranch className="h-3.5 w-3.5" />
            </div>
            <span className="text-sm">{t.orchestration}</span>
          </div>
        </div>
        <div className="p-3 space-y-2">
          {canOpenMain && (
            <Button variant="ghost" className="w-full justify-start gap-2 h-9 rounded-lg" onClick={() => navigate('/')}>
              <ArrowLeft className="h-4 w-4" />
              {t.codexBridge}
            </Button>
          )}
          <Button variant="secondary" className="w-full justify-start gap-2 h-9 rounded-lg border border-sidebar-border shadow-sm" onClick={() => { startDraftRun(); openMobilePanel('configuration'); }}>
            <Plus className="h-4 w-4" />
            {t.newRun}
          </Button>
          <Input type="search" value={historySearch} onChange={(event) => setHistorySearch(event.target.value)} placeholder={t.searchRuns} aria-label={t.searchRuns} className="h-8 text-xs" />
          <div className="grid grid-cols-2 gap-2">
            <select value={historyStatus} onChange={(event) => setHistoryStatus(event.target.value)} aria-label={t.historyStatus} className="h-8 min-w-0 rounded-md border border-input bg-background px-2 text-xs">
              <option value="">{t.allRunStatuses}</option>
              <option value="queued">{t.historyQueued}</option><option value="running">{t.historyRunning}</option>
              <option value="canceling">{t.historyCanceling}</option><option value="completed">{t.historyCompleted}</option>
              <option value="failed">{t.historyFailed}</option><option value="canceled">{t.historyCanceled}</option>
            </select>
            <select value={historyArchived} onChange={(event) => setHistoryArchived(event.target.value)} aria-label={t.historyArchive} className="h-8 min-w-0 rounded-md border border-input bg-background px-2 text-xs">
              <option value="false">{t.unarchivedRuns}</option><option value="true">{t.archivedRuns}</option><option value="all">{t.allRuns}</option>
            </select>
          </div>
        </div>
        <div aria-label={t.runHistory} aria-busy={historyPage.loading} className="flex-1 overflow-y-auto px-3 py-2 space-y-1 elegant-scrollbar">
          {agentRuns.length === 0 && !historyPage.loading && !historyPage.error ? (
            <div className="px-2 py-1.5 text-xs text-muted-foreground">{historyQuery || historyStatus || historyArchived !== 'false' ? t.historyNoMatches : t.noOrchestrationRuns}</div>
          ) : agentRuns.map((run) => (
            <div
              key={run.id}
              data-run-id={run.id}
              className={cn(
                "w-full rounded-md px-2 py-2 text-sm transition-colors",
                activeRunId === run.id ? "bg-sidebar-accent text-sidebar-accent-foreground" : "text-sidebar-foreground hover:bg-sidebar-accent/50",
                activeOrchestrationStatus(run.status) && "ring-1 ring-inset ring-emerald-500/40",
              )}
            >
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  className="flex min-w-0 flex-1 items-center gap-2 text-left"
                  onClick={() => { setMobileHistoryOpen(false); selectRun(run.id).catch((err) => setError(err.message)); }}
                >
                  {activeOrchestrationStatus(run.status) ? <span className="h-2 w-2 shrink-0 rounded-full bg-emerald-500 shadow-[0_0_0_3px_rgba(16,185,129,0.12)]" /> : run.mode === 'debate' ? <Swords className="h-3.5 w-3.5 shrink-0 opacity-70" /> : <UsersRound className="h-3.5 w-3.5 shrink-0 opacity-70" />}
                  <span className="truncate font-medium">{run.title}</span>
                </button>
              </div>
              <div className="phone-history-actions mt-2 flex items-center gap-1.5">
                <button type="button" className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded border border-sidebar-border bg-sidebar text-muted-foreground hover:bg-sidebar-border" aria-label={t.renameRun} title={t.renameRun}
                  onClick={() => { setEditingRun(run); setEditingTitle(run.title); setMetadataError(''); }}><Pencil className="h-3 w-3" /></button>
                <button type="button" className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded border border-sidebar-border bg-sidebar text-muted-foreground hover:bg-sidebar-border disabled:opacity-40" aria-label={run.archivedAt ? t.restoreRun : t.archiveRun} title={run.archivedAt ? t.restoreRun : t.archiveRun}
                  disabled={Boolean(updatingRunId) || (!run.archivedAt && !terminalOrchestrationStatus(run.status))} onClick={() => void updateRunMetadata(run, { archived: !run.archivedAt })}>
                  {updatingRunId === run.id ? <RefreshCw className="h-3 w-3 animate-spin" /> : run.archivedAt ? <ArchiveRestore className="h-3 w-3" /> : <Archive className="h-3 w-3" />}
                </button>
                <button
                  type="button"
                  className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded border border-sidebar-border bg-sidebar text-muted-foreground hover:bg-destructive/10 hover:text-destructive disabled:opacity-60"
                  aria-label={t.deleteRun}
                  onClick={() => void deleteRun(run)}
                  title={deletingRunId === run.id ? t.deletingRun : t.deleteRun}
                  disabled={Boolean(deletingRunId)}
                >
                  {deletingRunId === run.id ? <RefreshCw className="h-3 w-3 animate-spin" /> : <Trash2 className="h-3 w-3" />}
                </button>
                <button
                  type="button"
                  className={cn(
                    "inline-flex h-6 shrink-0 items-center gap-1 rounded border border-sidebar-border bg-sidebar px-1.5 text-[10px] font-medium text-muted-foreground hover:bg-sidebar-border",
                    shareCopiedRunId === run.id && "text-emerald-600 dark:text-emerald-400",
                  )}
                  onClick={() => shareRun(run).catch((err) => setError(err.message))}
                  title={shareCopiedRunId === run.id ? t.copied : t.shareRun}
                >
                  {sharingRunId === run.id ? <RefreshCw className="h-3 w-3 animate-spin" /> : shareCopiedRunId === run.id ? <Check className="h-3 w-3" /> : <Share2 className="h-3 w-3" />}
                  <span>{shareCopiedRunId === run.id ? t.copied : t.shareRun}</span>
                </button>
              </div>
              <button
                type="button"
                className="mt-1 flex w-full items-center justify-between text-[10px] text-muted-foreground"
                onClick={() => { setMobileHistoryOpen(false); selectRun(run.id).catch((err) => setError(err.message)); }}
              >
                <span className="truncate">{agents.find((agent) => agent.id === run.agentId)?.name || sessionDateLabel(run.updatedAt || run.createdAt, t)}</span>
                <span>{run.archivedAt ? `${t.archivedRun} · ` : ''}{run.status}</span>
              </button>
            </div>
          ))}
          {historyPage.key === historyKey && historyPage.loading && <p role="status" className="px-2 py-2 text-xs text-muted-foreground">{t.loadingRuns}</p>}
          {historyPage.key === historyKey && historyPage.error && <div role="alert" className="space-y-2 p-2 text-xs text-destructive">
            <p>{t.historyLoadFailed}</p><Button size="sm" variant="secondary" onClick={() => void loadRuns(Boolean(historyPage.nextCursor))}>{t.retry}</Button>
          </div>}
          {metadataError && !editingRun && <p role="alert" className="p-2 text-xs text-destructive">{metadataError}</p>}
          {historyPage.key === historyKey && historyPage.nextCursor && !historyPage.error && <Button variant="ghost" size="sm" className="w-full" disabled={historyPage.loading} onClick={() => void loadRuns(true)}>{t.loadMoreRuns}</Button>}
        </div>
        <div className="p-3 border-t border-sidebar-border shrink-0">
          <a href="/help" className="mb-1 flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-foreground">
            <BookOpen className="h-3.5 w-3.5" />
            <span>{t.help}</span>
          </a>
          <a href="/updates" className="mb-1 flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-foreground">
            <History className="h-3.5 w-3.5" />
            <span>{t.updates}</span>
          </a>
          <button onClick={() => openSettings()} className="w-full flex items-center gap-2 px-2 py-1.5 rounded-md text-sm hover:bg-sidebar-accent transition-colors">
            <Settings className="h-3.5 w-3.5" />
            <span className="flex-1 text-left">{t.settings}</span>
            <div className={cn("h-1.5 w-1.5 rounded-full", onlineAgent ? "bg-emerald-500" : "bg-muted-foreground")} />
          </button>
        </div>
      </MobileSurface>

      <main className="relative flex-1 flex flex-col min-w-0 h-full">
        <header className="h-14 shrink-0 border-b border-border flex items-center justify-between px-3 md:px-4 bg-background z-10">
          <div className="flex items-center gap-2 min-w-0">
            <Button variant="ghost" size="icon" className="phone-only text-muted-foreground" aria-label={t.runHistory} aria-controls="orchestration-history" onClick={() => openMobilePanel('history')}><Menu className="h-5 w-5" /></Button>
            {canOpenMain && (
              <Button variant="ghost" size="icon" className="hidden text-muted-foreground" onClick={() => navigate('/')}>
                <ArrowLeft className="h-5 w-5" />
              </Button>
            )}
            <GitBranch className="h-4 w-4 text-muted-foreground" />
            <span className="text-sm font-medium truncate">{activeRun?.title || t.cliOrchestration}</span>
          </div>
          <Button variant="ghost" size="icon" className="phone-only shrink-0" aria-label={language === 'zh' ? '更多操作' : 'More actions'} onClick={() => openMobilePanel('actions')}><MoreHorizontal className="h-5 w-5" /></Button>
          <MobileSurface open={mobileActionsOpen} onClose={() => setMobileActionsOpen(false)} title={language === 'zh' ? '更多操作' : 'More actions'} closeLabel={t.cancel} className="flex items-center gap-2 phone-action-surface" bodyClassName="flex flex-col gap-3 p-4">
            <AgentSelector
              agents={agents}
              selectedAgentId={selectedAgentId}
              onSelect={selectAgent}
              t={t}
              className="hidden sm:inline-flex"
              activeAgentIds={activeAgentIds}
            />
            <Button variant="secondary" size="sm" className="hidden sm:inline-flex h-8 gap-1.5 rounded-lg" onClick={() => openSettings('cli')}>
              <Plus className="h-3.5 w-3.5" />
              {t.addCliEndpoint}
            </Button>
            <Button
              variant="secondary"
              size="sm"
              className={cn(
                "h-8 gap-1.5 rounded-lg",
                shareCopiedRunId === activeRun?.id && "text-emerald-600 dark:text-emerald-400"
              )}
              onClick={() => shareRun(activeRun).catch((err) => setError(err.message))}
              disabled={!activeRun || sharingRunId === activeRun.id}
              aria-label={t.shareRun}
              title={shareCopiedRunId === activeRun?.id ? t.copied : t.shareRun}
            >
              {sharingRunId === activeRun?.id ? <RefreshCw className="h-3.5 w-3.5 animate-spin" /> : shareCopiedRunId === activeRun?.id ? <Check className="h-3.5 w-3.5" /> : <Share2 className="h-3.5 w-3.5" />}
              <span>{shareCopiedRunId === activeRun?.id ? t.copied : t.shareRun}</span>
            </Button>
            <Button variant="secondary" size="icon" className="h-8 w-8 rounded-lg" onClick={() => navigate(activeRun ? `/orchestrate/stats?run=${encodeURIComponent(activeRun.id)}` : '/orchestrate/stats')} aria-label={t.runStatistics} title={t.runStatistics}>
              <BarChart3 className="h-3.5 w-3.5" />
            </Button>
            <Button variant="secondary" size="icon" className="h-8 w-8 rounded-lg" onClick={() => navigate('/orchestrate/usage')} aria-label={t.usageOverview} title={t.usageOverview}>
              <PieChart className="h-3.5 w-3.5" />
            </Button>
            {user.isAdmin && <Button variant="secondary" size="icon" className="h-8 w-8 rounded-lg" onClick={() => navigate('/admin/usage')} aria-label={t.adminDashboard} title={t.adminDashboard}><ShieldCheck className="h-3.5 w-3.5" /></Button>}
            <Button
              variant="ghost"
              size="icon"
              className="text-muted-foreground rounded-full h-8 w-8"
              onClick={() => refreshVisibleOrchestrationData().catch((err) => setError(err instanceof Error ? err.message : t.failedLoadOrchestration))}
              disabled={refreshingOrchestration}
              aria-label={t.refresh}
              title={t.refresh}
            >
              <RefreshCw className={cn("h-4 w-4", refreshingOrchestration && "animate-spin")} />
            </Button>
          </MobileSurface>
        </header>

        <div className="phone-status bg-muted/30 border-b border-border px-4 py-2 flex items-center gap-4 text-xs text-muted-foreground overflow-x-auto whitespace-nowrap elegant-scrollbar">
          {!selectionBlocked && <div className="phone-desktop-only">{renderWorkerPairSelector('toolbar')}</div>}
          <AgentSelector
            agents={agents}
            selectedAgentId={selectedAgentId}
            onSelect={selectAgent}
            t={t}
            className="sm:hidden min-w-[220px] shrink-0"
            activeAgentIds={activeAgentIds}
          />
          <div className="flex items-center gap-1.5">
            <Server className="h-3.5 w-3.5" />
            <span>{t.workers}: {orchestrationWorkerLabel(selectedAgent, t)} · {selectedAgent?.online ? t.online : t.offline}</span>
          </div>
          <div className="flex items-center gap-1.5">
            <ShieldQuestion className="h-3.5 w-3.5" />
            <span>{t.browserApproval}: {orchestrationApprovalMode(selectedAgent) === 'auto-execute' ? t.autoExecute : orchestrationApprovalMode(selectedAgent) === 'strict-workspace' ? t.strictWorkspace : capabilityProblems.length ? t.notAvailable : t.available}</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Activity className="h-3.5 w-3.5" />
            <span>{t.status}: {activeRun?.status || t.idle}</span>
          </div>
          {currentTurnLabel && (
            <div className="flex items-center gap-1.5">
              <GitBranch className="h-3.5 w-3.5" />
              <span>{isRunning ? t.currentTurn : t.lastTurn}: {currentTurnLabel}</span>
            </div>
          )}
          <div className="flex items-center gap-1.5">
            <Command className="h-3.5 w-3.5" />
            <span>{t.stream}: {orchestrationStreamStatus}</span>
          </div>
        </div>

        {draftPersistenceFailed && <p role="status" className="shrink-0 border-b border-border bg-amber-50 px-4 py-2 text-xs text-amber-800 dark:bg-amber-950/30 dark:text-amber-200">{t.draftPersistenceWarning}</p>}

        {planWorkspaceEnabled && activeRun && (
          <section className="shrink-0 border-b border-border bg-background">
            <button
              type="button"
              className="flex min-h-11 w-full items-center gap-3 px-4 py-2 text-left transition-colors hover:bg-muted/30 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-inset focus-visible:ring-ring"
              onClick={() => setPlanningWorkspaceOpen((current) => !current)}
              aria-expanded={planningWorkspaceOpen}
              aria-label={planningWorkspaceOpen ? t.closePlanningWorkspace : t.openPlanningWorkspace}
            >
              <Workflow className="h-4 w-4 shrink-0 text-primary" />
              <span className="shrink-0 text-xs font-semibold">{t.planningWorkspace}</span>
              <div className="h-1.5 min-w-[5rem] max-w-40 flex-1 overflow-hidden rounded-full bg-muted">
                <div className="h-full bg-emerald-500 transition-[width] duration-300" style={{ width: `${Math.max(0, Math.min(100, projectedPlan?.percent || 0))}%` }} />
              </div>
              <span className="shrink-0 font-mono text-[11px] tabular-nums text-muted-foreground">
                {projectedPlanItems.length ? `${projectedPlan?.completed || 0}/${projectedPlan?.total || projectedPlanItems.length} · ${projectedPlan?.percent || 0}%` : t.planWaiting}
              </span>
              {currentPlanItem && <span className="hidden min-w-0 max-w-[28rem] truncate text-xs text-muted-foreground lg:block">{language === 'zh' ? '当前' : 'Current'}: {currentPlanItem.title}</span>}
              <Maximize2 className="h-4 w-4 shrink-0 text-muted-foreground" />
            </button>
            <Dialog open={planningWorkspaceOpen} onOpenChange={setPlanningWorkspaceOpen}>
              <DialogContent className="flex h-[calc(100vh-2rem)] w-[calc(100vw-2rem)] max-w-none flex-col gap-3 overflow-hidden p-4 sm:max-w-none md:p-5">
                <DialogHeader className="shrink-0 pr-10">
                  <DialogTitle className="flex items-center gap-2 text-base"><Workflow className="h-4 w-4 text-primary" />{t.planningWorkspace}</DialogTitle>
                  <DialogDescription>{language === 'zh' ? '在不改变实时对话布局的独立工作区中查看任务提示词、计划清单、分支地图和 Agent 执行链。' : 'Inspect task prompts, plan checklist, branch map, and Agent execution chain without changing the live transcript layout.'}</DialogDescription>
                </DialogHeader>
                <div className="min-h-0 flex-1 overflow-y-auto rounded-md border border-border bg-muted/10 p-3 elegant-scrollbar md:p-4">
                {progressTasks.length > 0 && (
                  <div className="mb-3 flex min-w-0 items-center gap-2 overflow-x-auto rounded-md border border-border bg-background p-1.5 elegant-scrollbar">
                    <span className="shrink-0 px-1.5 text-[10px] font-medium uppercase text-muted-foreground">{language === 'zh' ? '任务' : 'Tasks'}</span>
                    {progressTasks.map((task) => {
                      const active = task.taskNumber === selectedProgressTask?.taskNumber;
                      const end = task.finishedAt || (activeOrchestrationStatus(task.status) ? Math.floor(Date.now() / 1000) : task.updatedAt);
                      const duration = task.createdAt && end && end >= task.createdAt ? formatDuration((end - task.createdAt) * 1000) : '';
                      return (
                        <button
                          key={task.taskNumber}
                          type="button"
                          onClick={() => setSelectedProgressTaskNumber(task.taskNumber)}
                          className={cn('flex h-8 shrink-0 items-center gap-2 rounded px-2.5 text-xs transition-colors', active ? 'bg-primary text-primary-foreground shadow-sm' : 'text-muted-foreground hover:bg-muted hover:text-foreground')}
                          aria-pressed={active}
                        >
                          <span>{language === 'zh' ? `任务 ${task.taskNumber}` : `Task ${task.taskNumber}`}</span>
                          <span className={cn('font-mono text-[10px] tabular-nums', active ? 'text-primary-foreground/75' : 'text-muted-foreground')}>{task.graphs.length} {language === 'zh' ? '轮' : task.graphs.length === 1 ? 'round' : 'rounds'}{duration ? ` · ${duration}` : ''}</span>
                        </button>
                      );
                    })}
                  </div>
                )}
                <div className="grid gap-3 xl:grid-cols-[minmax(15rem,0.42fr)_minmax(38rem,1.58fr)]">
                  <aside className="min-w-0 overflow-hidden rounded-md border border-border bg-background">
                    <div className="flex items-center gap-2 border-b border-border px-3 py-2.5 text-xs font-semibold text-muted-foreground"><BookOpen className="h-3.5 w-3.5" />{t.promptNavigator}</div>
                    <div className="flex max-h-28 gap-1 overflow-auto border-b border-border p-2 elegant-scrollbar xl:flex-col">
                      {promptViews.map((view) => (
                        <button key={view.key} type="button" onClick={() => setSelectedPromptKey(view.key)} className={cn('shrink-0 truncate rounded-md px-2.5 py-2 text-left text-xs transition-colors xl:w-full', selectedPromptView?.key === view.key ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-muted hover:text-foreground')} title={view.label}>{view.label}</button>
                      ))}
                    </div>
                    <div className="max-h-56 overflow-y-auto p-3 elegant-scrollbar"><pre className="whitespace-pre-wrap break-words font-sans text-xs leading-5 text-foreground">{selectedPromptView?.content || t.noRoundPrompts}</pre></div>
                  </aside>

                  <div className="min-w-0 space-y-3">
                    <section className="min-w-0 rounded-md border border-border bg-background p-3">
                      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
                        <div><div className="flex items-center gap-2 text-sm font-semibold"><Workflow className="h-4 w-4 text-primary" />{language === 'zh' ? '任务分支地图' : 'Task branch map'}</div><p className="mt-1 text-[11px] text-muted-foreground">{language === 'zh' ? '整体路线、局部进度、依赖与推荐顺序。' : 'Overall route, local progress, dependencies, and recommended order.'}</p></div>
                        <div className="flex min-w-0 items-center gap-2">
                          {currentPlanItem && <span className="max-w-[20rem] truncate rounded border border-sky-500/20 bg-sky-500/[0.06] px-2 py-1 text-[10px] text-sky-700 dark:text-sky-300">{language === 'zh' ? '当前' : 'Focus'} · {currentPlanItem.title}</span>}
                          <Button variant="ghost" size="icon" className="h-7 w-7 shrink-0 rounded" onClick={() => setTaskMapExpanded(true)} disabled={!projectedPlanItems.length} aria-label={language === 'zh' ? '展开任务分支地图' : 'Expand task branch map'} title={language === 'zh' ? '展开任务分支地图' : 'Expand task branch map'}><Maximize2 className="h-3.5 w-3.5" /></Button>
                        </div>
                      </div>
                      <OrchestrationProgressMap
                        tasks={projectedPlanItems.map((item, index) => ({ id: item.id, name: item.title, role: item.branch || item.kind, status: item.status, position: item.priority || index + 1, dependencies: item.dependsOn || [], detail: item.evidence || item.rationale }))}
                        activeTaskId={projectedPlan?.currentFocus}
                        height={Math.max(320, Math.min(520, Math.ceil(Math.max(projectedPlanItems.length, 3) / 3) * 128))}
                        ariaLabel={language === 'zh' ? '任务分支地图' : 'Task branch map'}
                        emptyLabel={isRunning ? (language === 'zh' ? '正在生成整个任务的中文计划…' : 'Generating the structured whole-task plan...') : (language === 'zh' ? '本次任务没有可用的结构化计划。' : 'No structured task plan is available for this run.')}
                        statusLabels={orchestrationStatusLabels(language, t)}
                        inferSequentialDependencies={false}
                        interactive
                      />
                    </section>

                    <div className="grid min-w-0 gap-3 2xl:grid-cols-[minmax(24rem,1.25fr)_minmax(26rem,1fr)]">
                      <OrchestrationProofProgress plan={projectedPlan} planItems={projectedPlanItems} labels={language === 'zh' ? { empty: isRunning ? '正在生成整个任务的中文计划…' : '本次任务没有可用的结构化计划。' } : { title: 'Whole-task plan', completed: 'Completed', active: 'Active', pending: 'Pending', blocked: 'Blocked', ready: 'Ready', empty: isRunning ? 'Generating the structured whole-task plan...' : 'No structured task plan is available for this run.', evidence: 'Evidence', rationale: 'Rationale', dependency: 'Blocked by', showCompleted: 'Show completed', hideCompleted: 'Hide completed' }} />
                      <section className="min-w-0 rounded-md border border-border bg-background p-3">
                        <div className="mb-2 flex items-start justify-between gap-3">
                          <div><div className="flex items-center gap-2 text-xs font-semibold"><Activity className="h-3.5 w-3.5 text-muted-foreground" />{language === 'zh' ? 'Agent 执行链（次级）' : 'Agent execution chain (secondary)'}</div><p className="mt-1 text-[10px] text-muted-foreground">{language === 'zh' ? '仅用于诊断运行阶段，不作为任务计划。' : 'Runtime diagnostics only; not the task plan.'}</p></div>
                          <div className="flex shrink-0 items-center gap-1">
                            {selectedAgentGraph && <span className="px-1 text-[10px] text-muted-foreground">{orchestrationGraphLabel(selectedAgentGraph, selectedProgressTask?.graphs || [], language)}</span>}
                            <Button variant="ghost" size="icon" className="h-7 w-7 rounded" onClick={() => setAgentGraphExpanded(true)} disabled={!selectedAgentGraph} aria-label={language === 'zh' ? '放大执行链' : 'Expand execution chain'} title={language === 'zh' ? '放大执行链' : 'Expand execution chain'}><Maximize2 className="h-3.5 w-3.5" /></Button>
                          </div>
                        </div>
                        {(selectedProgressTask?.graphs?.length || 0) > 1 && (
                          <div className="mb-2 flex max-w-full gap-1 overflow-x-auto rounded border border-border bg-muted/20 p-1 elegant-scrollbar">
                            {selectedProgressTask?.graphs.map((graph) => <button key={graph.id} type="button" onClick={() => setSelectedProgressGraphId(graph.id)} className={cn('h-6 shrink-0 rounded px-2 text-[10px] transition-colors', selectedAgentGraph?.id === graph.id ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground')}>{orchestrationGraphLabel(graph, selectedProgressTask?.graphs || [], language)}</button>)}
                          </div>
                        )}
                        <OrchestrationProgressMap
                          tasks={(selectedAgentGraph?.tasks || []).map((task) => ({ ...task, name: orchestrationTaskDisplayName(task.name, language), dependencies: task.dependencies || [] }))}
                          height={Math.max(320, Math.min(520, Math.ceil(Math.max(selectedAgentGraph?.tasks?.length || 0, 3) / 3) * 128))}
                          ariaLabel={language === 'zh' ? 'Agent 执行链' : 'Agent execution chain'}
                          emptyLabel={language === 'zh' ? '暂无执行节点。' : 'No execution nodes yet.'}
                          statusLabels={orchestrationStatusLabels(language, t)}
						  interactive
                        />
                      </section>
                    </div>
                  </div>
                </div>
                </div>
              </DialogContent>
            </Dialog>
          </section>
        )}

        <div className="phone-orchestration-layout grid flex-1 min-h-0 auto-rows-max grid-cols-1 overflow-y-auto lg:auto-rows-auto lg:grid-cols-[minmax(0,1fr)_360px] lg:overflow-hidden">
          <div className="phone-transcript-column relative flex min-h-[22rem] flex-col lg:min-h-0">
            {nativeInputReadEnabled && (inputStorageNotice || inputRecoveryNotice || userInputs.pendingSubmissions.length > 0 || terminalUserInputs.some((status) => !userInputs.receipts[status.requestId])) &&
              <div className="shrink-0 border-b border-border bg-muted/30 px-4 py-2 text-xs md:px-6" role="status">
                <span>{inputStorageNotice || inputRecoveryNotice || (language === 'zh' ? '有答案等待确认送达；重新查询不会重发答案。' : 'Answer delivery is unconfirmed. Querying never resends answers.')}</span>
                <Button type="button" variant="ghost" size="sm" className="ml-2 h-7" onClick={retryUserInputQuery}>
                  {language === 'zh' ? '重新查询' : 'Query again'}
                </Button>
              </div>}
            {nativeInputEnabled && visibleUserInputs.length > 0 && <div className="shrink-0 border-b border-border/70 bg-background px-3 py-2 md:px-6" aria-live="polite">
              <button id="native-user-input-entry" type="button" className="flex min-h-11 w-full items-center gap-2.5 rounded-lg px-2 text-left transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                aria-expanded={inputPanelOpen} aria-controls="native-user-input-panel" onClick={() => inputPanelOpen ? setInputPanelOpen(false) : openInputPanel()}>
                <span className={cn('flex h-7 w-7 shrink-0 items-center justify-center rounded-full', pendingQuestionCount ? 'bg-primary/5 text-foreground' : 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/50 dark:text-emerald-400')}>
                  {pendingQuestionCount ? <HelpCircle aria-hidden="true" className="h-4 w-4" /> : <Check aria-hidden="true" className="h-4 w-4" />}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="flex flex-wrap items-center gap-2 text-xs font-medium">
                    {pendingQuestionCount ? t.nativeQuestions : (language === 'zh' ? '问答已完成' : 'Questions answered')}
                    {pendingQuestionCount > 0 && <span className="rounded-md bg-secondary px-1.5 py-0.5 tabular-nums">{pendingQuestionCount}</span>}
                    {!pendingQuestionCount && <span className="font-normal text-muted-foreground">{language === 'zh' ? `${deliveredQuestionCount} 题已送达` : `${deliveredQuestionCount} delivered`}</span>}
                  </span>
                  {pendingQuestionCount > 0 && <span className="mt-0.5 block text-[11px] text-muted-foreground">{[
                    blockingUserInputCount ? `${t.nativeQuestionsWaiting} ${blockingUserInputCount}` : '',
                    confirmingUserInputCount ? `${t.nativeQuestionsConfirming} ${confirmingUserInputCount}` : '',
                    deliveredQuestionCount ? (language === 'zh' ? `${deliveredQuestionCount} 题已送达` : `${deliveredQuestionCount} delivered`) : '',
                  ].filter(Boolean).join(' · ')}</span>}
                </span>
                <span className="shrink-0 text-[11px] text-muted-foreground">{inputPanelOpen ? (language === 'zh' ? '收起' : 'Collapse') : pendingQuestionCount ? (language === 'zh' ? '去回答' : 'Answer') : (language === 'zh' ? '查看记录' : 'History')}</span>
                <ChevronDown aria-hidden="true" className={cn('h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform', inputPanelOpen && 'rotate-180')} />
              </button>
            </div>}
            {nativeInputEnabled && visibleUserInputs.length > 0 &&
              <section id="native-user-input-panel" hidden={!inputPanelOpen} className="max-h-[46vh] shrink-0 overflow-y-auto border-b border-border/70 bg-muted/30 px-3 py-3 elegant-scrollbar md:px-6 lg:max-h-[44vh]" aria-label={language === 'zh' ? '待回答的原生问题' : 'Native questions to answer'}>
                <div className="mb-2 flex items-center justify-between gap-2">
                  <span className="text-[11px] text-muted-foreground">{language === 'zh' ? (pendingQuestionCount ? '选择一个问题继续回答' : '所有答案已送达，可继续查看运行输出') : (pendingQuestionCount ? 'Choose a question to answer' : 'All answers delivered. Follow the run for results.')}</span>
                  <Button type="button" variant="ghost" size="sm" className="h-8 shrink-0 gap-1 text-xs text-muted-foreground" onClick={() => {
                    setInputPanelOpen(false);
                    window.requestAnimationFrame(() => document.getElementById('native-user-input-entry')?.focus({ preventScroll: true }));
                  }}>
                    {language === 'zh' ? '收起' : 'Collapse'}
                  </Button>
                </div>
                {visibleUserInputs.length > 1 && <div className="mb-3 flex gap-1.5 overflow-x-auto pb-1 elegant-scrollbar" role="group" aria-label={language === 'zh' ? '切换待答请求' : 'Switch question requests'}>
                  {visibleUserInputs.map((item, index) => <button key={item.request.requestId} type="button"
                    aria-pressed={selectedInputItem?.request.requestId === item.request.requestId}
                    aria-label={`${orchestrationParticipantLabel(item.request.cli, item.request.workerId)} · ${index + 1} · ${item.request.questions.length} ${language === 'zh' ? '题' : 'questions'} · ${item.status === 'delivered' ? (language === 'zh' ? '已送达' : 'delivered') : item.status === 'submitting' ? (language === 'zh' ? '发送中' : 'sending') : item.status === 'confirming' ? (language === 'zh' ? '待确认' : 'confirming') : (language === 'zh' ? '待回答' : 'pending')}`}
                    onClick={() => setSelectedInputRequestId(item.request.requestId)}
                    className={cn('flex min-h-10 shrink-0 items-center gap-2 rounded-lg border px-3 py-2 text-xs transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring',
                      selectedInputItem?.request.requestId === item.request.requestId ? 'border-foreground/25 bg-card text-foreground shadow-sm' : 'border-transparent text-muted-foreground hover:bg-muted hover:text-foreground')}>
                    <span className={cn('inline-flex h-5 min-w-5 items-center justify-center rounded-full text-[10px] tabular-nums', item.status === 'delivered' ? 'bg-emerald-100 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-300' : 'bg-secondary text-muted-foreground')}>
                      {item.status === 'delivered' ? <Check aria-hidden="true" className="h-3 w-3" /> : index + 1}
                    </span>
                    <span>{orchestrationParticipantLabel(item.request.cli, item.request.workerId)} <span className="ml-1 text-muted-foreground">{index + 1}</span></span>
                    <span className="text-[10px] text-muted-foreground">{item.status === 'delivered' ? (language === 'zh' ? '已送达' : 'delivered') : item.status === 'submitting' ? (language === 'zh' ? '发送中' : 'sending') : item.status === 'confirming' ? (language === 'zh' ? '待确认' : 'confirming') : `${item.request.questions.length} ${language === 'zh' ? '题' : 'questions'}`}</span>
                  </button>)}
                </div>}
                {visibleUserInputs.map((item) => <div key={item.request.requestId} hidden={selectedInputItem?.request.requestId !== item.request.requestId}>
                  <UserInputCard item={item} language={language} t={t} onSubmit={respondUserInput}
                    hasNextRequest={visibleUserInputs.some((other) => other.request.requestId !== item.request.requestId && other.status === 'pending')} />
                </div>)}
              </section>}
            <div
              ref={scrollRef}
              onScroll={updateTimelineScrollState}
              className="phone-transcript min-h-32 max-h-64 flex-1 overflow-y-auto p-4 md:p-6 space-y-3 elegant-scrollbar lg:min-h-0 lg:max-h-none"
            >
              {nativeInputReadEnabled && terminalUserInputs.map((status) => {
                const outcome = userInputs.receipts[status.requestId]?.outcome || status.receipt?.outcome;
                return <div key={status.requestId} className="rounded-md border border-border bg-muted/30 px-3 py-2 text-xs text-muted-foreground" role="status">
                  {outcome === 'delivered' ? t.nativeAnswerDeliveredClosed : t.nativeClosedUnconfirmed}
                </div>;
              })}
              {runLoad.loading || (pathRunId && !activeRunId) ? (
                <div role="status" aria-label={language === 'zh' ? '正在加载对话…' : 'Loading conversation…'} className="flex h-full items-center justify-center gap-2 text-sm text-muted-foreground"><RefreshCw className="h-4 w-4 animate-spin" />{language === 'zh' ? '正在加载对话…' : 'Loading conversation…'}</div>
              ) : runLoad.error ? (
                <div role="alert" className="flex items-center justify-center gap-3 p-6 text-sm text-destructive"><span>{runLoad.error}</span><Button variant="secondary" size="sm" onClick={() => void selectRun(runLoad.runId)}>{t.retry}</Button></div>
              ) : !timelineItems.length && !visibleUserInputs.length && !terminalUserInputs.length && !hasOlderEvents ? (
                <div className="h-full flex flex-col items-center justify-center text-center max-w-md mx-auto space-y-4">
                  <div className="h-12 w-12 rounded-2xl bg-primary/5 border border-border flex items-center justify-center">
                    <GitBranch className="h-6 w-6 text-primary" />
                  </div>
                  <h2 className="text-lg font-medium">{t.coordinateClaudeCodex}</h2>
                  <div className="inline-flex max-w-full items-center gap-1.5 rounded-md border border-border bg-muted/40 px-2 py-1 text-xs text-muted-foreground">
                    <Server className="h-3.5 w-3.5 shrink-0" />
                    <span className="truncate">{selectedAgent?.name || t.noBridgeConnected}</span>
                    {!agentRuns.length && <span className="shrink-0">· {t.noOrchestrationRuns}</span>}
                  </div>
                  <p className="text-sm text-muted-foreground"><span className="phone-desktop-only">{t.startCollaborationHint}</span><span className="phone-only">{language === 'zh' ? '点击“初始化任务”设置并启动协作或辩论运行。' : 'Tap “Initialize task” to configure and start a collaboration or debate run.'}</span></p>
                </div>
              ) : (
                <>
                  {hasOlderEvents && (
                    <div className="flex flex-col items-center gap-2 pb-1">
                      <p className="text-xs text-muted-foreground">{language === 'zh' ? '当前显示最近历史；较早内容和运行中的轮次开头可按需加载。' : 'Showing recent history. Load earlier content and the start of ongoing turns on demand.'}</p>
                      <Button
                        variant="secondary"
                        size="sm"
                        type="button"
                        className="gap-1.5 rounded-lg border border-border bg-background/80 text-muted-foreground shadow-sm hover:text-foreground"
                        onClick={() => void loadOlderEvents()}
                        disabled={loadingOlderEvents}
                      >
                        <RefreshCw className={cn("h-3.5 w-3.5", loadingOlderEvents && "animate-spin")} />
                        {loadingOlderEvents ? t.loadingEarlierEvents : t.loadEarlierEvents}
                      </Button>
                    </div>
                  )}
                  {timelineGroups.map((group) => (
                    <OrchestrationTimelineGroupItem
                      key={group.key}
                      group={group}
                      collapsed={Boolean(collapsedTimelineGroups[group.key])}
                      onToggle={() => toggleTimelineGroup(group.key)}
                      onJumpToFirstMessage={(targetID) => jumpToTimelineItem(group.key, targetID)}
                      onApprovalDecision={respondOrchestrationApproval}
                      t={t}
                    />
                  ))}
                  {finalConclusion && !isRunning && <RunConclusionCard conclusion={finalConclusion} status={activeRun?.status} t={t} />}
                </>
              )}
              <div ref={endRef} className="h-4" />
            </div>

            {leadingEdition && activeRun && !selectionBlocked && <MobileSurface id="orchestration-conversation" open={mobileConversationOpen} onClose={() => setMobileConversationOpen(false)} title={language === 'zh' ? '运行对话' : 'Run conversation'} closeLabel={t.cancel} className="contents" bodyClassName="flex flex-col">
              <OrchestrationLiveInput key={activeRun.id} runId={activeRun.id} running={isRunning}
              refreshRevision={liveInputRevision}
              supported={(runInputAgent?.capabilities?.orchestrationLiveInput || 0) >= 1} online={Boolean(runInputAgent?.online)}
              language={language} prompt={prompt} setPrompt={setPrompt} onPromptAccepted={clearSubmittedPrompt} continuing={creating} onContinue={startRun}
              hasFiles={files.length > 0} continueDisabled={!selectedAgent || capabilityProblems.length > 0 || selectedTBCIUnavailable} />
            </MobileSurface>}
            {showScrollBottom && !(nativeInputEnabled && inputPanelOpen && visibleUserInputs.length > 0) && (
              <Button
                variant="secondary"
                size="icon"
                type="button"
                className={cn("phone-jump-latest absolute left-1/2 z-20 h-9 w-9 -translate-x-1/2 rounded-full border border-border bg-card/95 text-muted-foreground shadow-lg backdrop-blur hover:text-foreground", leadingEdition && activeRun ? "top-4" : "bottom-4")}
                onClick={() => scrollTimelineToBottom()}
                aria-label={t.jumpToLatestMessage}
                title={t.jumpToBottom}
              >
                <ArrowDownToLine className="h-4 w-4" />
              </Button>
            )}
          </div>

          <MobileSurface id="orchestration-configuration" open={mobileConfigOpen} onClose={() => setMobileConfigOpen(false)} title={language === 'zh' ? (activeRun ? '运行配置' : '初始化任务') : (activeRun ? 'Run configuration' : 'Initialize task')} closeLabel={t.cancel} className="phone-config-surface min-h-0 border-t border-border bg-background/95 p-4 overflow-y-auto elegant-scrollbar lg:border-l lg:border-t-0" bodyClassName="min-h-0 flex-1 overflow-y-auto p-4 elegant-scrollbar">
            {selectionBlocked ? <p className="p-3 text-sm text-muted-foreground">{language === 'zh'
              ? (runLoad.error ? '请先重试加载对话，再继续操作。' : '对话加载完成后可继续操作。')
              : (runLoad.error ? 'Retry loading this conversation before continuing.' : 'Conversation controls will be available after loading.')}</p> : <div className="flex min-h-full flex-col gap-3">
              <div className={cn('rounded-lg border p-3 text-xs text-muted-foreground', runInputCapability.available ? 'border-emerald-500/30 bg-emerald-500/5' : 'border-amber-500/30 bg-amber-500/5')} role="status">
                <div className="font-medium text-foreground">{language === 'zh' ? '结构化提问能力' : 'Structured questions'}</div>
                <p className="mt-1 break-words">{activeRun ? (language === 'zh' ? '当前运行' : 'Current run') : (language === 'zh' ? '新运行配置' : 'New run setup')}: {runInputCapability.text}</p>
                {showDraftInputCapability && <p className="mt-1 break-words">{language === 'zh' ? '新运行配置' : 'New run setup'}: {draftInputCapability.text}</p>}
              </div>
              <div className="space-y-3">
                <div className="space-y-2">
                  <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.mode}</label>
                  <div className="grid grid-cols-2 gap-1 rounded-lg border border-border bg-muted p-1">
                    <button className={cn("h-8 rounded-md text-xs font-medium", mode === 'collaboration' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setMode('collaboration')} disabled={creating || isRunning}>
                      {t.collaborate}
                    </button>
                    <button className={cn("h-8 rounded-md text-xs font-medium", mode === 'debate' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setMode('debate')} disabled={creating || isRunning}>
                      {t.debate}
                    </button>
                  </div>
                </div>

                {renderWorkerPairSelector('panel')}
                {renderWorkerProfileSelectors()}

                <div className="space-y-2">
                  <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.firstCli}</label>
                  <div className="grid grid-cols-2 gap-1 rounded-lg border border-border bg-muted p-1">
                    <button className={cn("h-8 rounded-md text-xs font-medium", workerPair !== 'codex-codex' && firstCli === 'claude' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setFirstCli('claude')} disabled={creating || isRunning || workerPair === 'codex-codex'}>
                      Claude
                    </button>
                    <button className={cn("h-8 rounded-md text-xs font-medium", (workerPair === 'codex-codex' || firstCli === 'codex') ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setFirstCli('codex')} disabled={creating || isRunning || workerPair === 'claude-claude'}>
                      Codex
                    </button>
                  </div>
                </div>

                <div className="space-y-2">
                  <label className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.profile}</label>
                  <div className="grid grid-cols-2 gap-1 rounded-lg border border-border bg-muted p-1">
                    <button className={cn("h-8 rounded-md text-xs font-medium", profile === 'default' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => { setProfile('default'); setVerificationMode('agent'); }} disabled={creating || isRunning}>
                      {t.defaultProfile}
                    </button>
                    <button className={cn("h-8 rounded-md text-xs font-medium", profile === 'formal-proof' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setProfile('formal-proof')} disabled={creating || isRunning}>
                      {t.formalProofProfile}
                    </button>
                  </div>
                  {profile === 'formal-proof' && (
                    <div className="space-y-1.5">
                      <span className="text-[11px] font-medium text-muted-foreground">{t.verificationMode}</span>
                      <div className="grid grid-cols-2 gap-1 rounded-lg border border-border bg-muted p-1">
                        <button
                          className={cn("h-8 rounded-md text-xs font-medium", verificationMode === 'agent' ? "bg-background shadow-sm" : "text-muted-foreground")}
                          onClick={() => setVerificationMode('agent')}
						  disabled={creating || isRunning}
                          aria-pressed={verificationMode === 'agent'}
                        >
                          {t.agentVerifierMode}
                        </button>
                        <button
                          className={cn("h-8 rounded-md text-xs font-medium", verificationMode === 'tbci' ? "bg-background shadow-sm" : "text-muted-foreground")}
                          onClick={() => setVerificationMode('tbci')}
						  disabled={creating || isRunning || !tbciAvailable}
						  title={!tbciAvailable ? `${t.tbciCapabilityUnavailable} ${tbciUnavailableReason}` : undefined}
                          aria-pressed={verificationMode === 'tbci'}
                        >
                          TBCI
                        </button>
                      </div>
					  <p className={cn("text-[11px] leading-4", !tbciAvailable ? "text-amber-700 dark:text-amber-300" : "text-muted-foreground")}>
						{!tbciAvailable ? `${t.tbciCapabilityUnavailable} ${tbciUnavailableReason}` : verificationMode === 'tbci' ? t.tbciVerificationMode : t.agentVerificationMode}
					  </p>
                    </div>
                  )}
                </div>

                {leadingEdition && activeRun ? <div className="rounded-lg border border-border bg-muted/30 p-3">
                  <div className="text-xs font-semibold">{language === 'zh' ? (isRunning ? '追加任务指令' : '继续当前任务') : (isRunning ? 'Add instructions' : 'Continue this task')}</div>
                  <p className="mt-1 text-xs leading-relaxed text-muted-foreground"><span className="phone-desktop-only">{language === 'zh'
                    ? (isRunning ? '请在对话区下方的“运行对话”中追加要求或调整方向。' : '请在“运行对话”中输入后点击“继续运行”，沿用当前任务与会话上下文。')
                    : (isRunning ? 'Use Run conversation below the transcript to add requirements or redirect the work.' : 'Enter your next instruction in Run conversation and select Continue run to keep this task and its context.')}</span><span className="phone-only">{language === 'zh' ? '点击“前往输入”或底部“运行对话”，追加指令或继续当前任务。' : 'Tap “Go to input” or “Run conversation” below to add instructions or continue this task.'}</span></p>
                  <Button type="button" variant="outline" size="sm" className="mt-2 min-h-9 w-full" aria-controls="orchestration-live-input" onClick={() => {
                    openMobilePanel('conversation');
                    requestAnimationFrame(() => {
                      const input = document.getElementById('orchestration-live-input');
                      input?.scrollIntoView({ block: 'nearest' });
                      input?.focus({ preventScroll: true });
                    });
                  }}>{language === 'zh' ? '前往输入' : 'Go to input'}</Button>
                </div> : <label className="flex flex-col gap-2">
                  <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.task}</span>
                  <textarea
                    ref={taskInputRef}
                    className="h-24 w-full resize-none overflow-y-auto rounded-md border border-input bg-transparent px-3 py-2 text-sm shadow-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring elegant-scrollbar"
                    placeholder={t.taskPlaceholder}
                    value={prompt}
                    onChange={(event) => setPrompt(event.target.value)}
                  />
                </label>}

                <label className="block shrink-0 space-y-2 sm:hidden">
                  <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.cliEndpoint}</span>
                  <AgentSelector
                    agents={agents}
                    selectedAgentId={selectedAgentId}
                    onSelect={selectAgent}
                    t={t}
                    className="w-full sm:hidden"
                    activeAgentIds={activeAgentIds}
                  />
                </label>

                <CapabilityMatrix agent={selectedAgent} t={t} />
                {capabilityProblems.length > 0 && (
                  <div className="flex items-start gap-2 rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                    <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                    <span>{capabilityProblems.join(' ')}</span>
                  </div>
                )}

                <label className="block space-y-2">
                  <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.workingDirectory}</span>
                  <div className="relative">
                    <FolderInput className="absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" />
                    <select
                      value={cwd}
                      onChange={(event) => setCwd(event.target.value)}
                      className={cn(
                        "flex h-9 w-full rounded-md border border-input bg-transparent py-1 pl-9 pr-8 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
                        !cwd && "text-muted-foreground"
                      )}
                      disabled={creating || isRunning || !workingDirs.length}
                      aria-label={t.workingDirectory}
                    >
                      {workingDirs.length ? workingDirs.map((dir) => (
                        <option key={dir} value={dir}>{dir}</option>
                      )) : (
                        <option value="">{t.noWorkingDirs}</option>
                      )}
                    </select>
                  </div>
                </label>

                <div className={cn("grid gap-3", !dynamicExecution && "sm:grid-cols-[minmax(7rem,0.45fr)_minmax(10rem,0.55fr)]")}>
                  {!dynamicExecution && <label className="block space-y-2">
                    <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.turns}</span>
                    <select value={maxTurns} onChange={(event) => setMaxTurns(Number(event.target.value))} disabled={creating || isRunning} className="flex h-9 w-full rounded-md border border-input bg-transparent px-3 py-1 text-sm shadow-sm">
                      {[1, 2, 3, 4, 6, 8, 12].map((turns) => <option key={turns} value={turns}>{turns} {turns === 1 ? 'round' : 'rounds'}</option>)}
                    </select>
                  </label>}
                  {dynamicExecution && <p className="text-xs text-muted-foreground">{language === 'zh' ? '每轮检查完成情况，完成或停滞时结束；可随时停止。' : 'Checks progress after each cycle; stops on completion or a stall. You can stop at any time.'}</p>}
                  <div className="space-y-2">
                    <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.nativeContextCompaction}</span>
                    <div className="grid grid-cols-2 gap-1 rounded-lg border border-border bg-muted p-1">
                      <button className={cn("h-8 rounded-md text-xs font-medium", nativeContextCompaction === 'off' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setNativeContextCompaction('off')} disabled={creating || isRunning}>
                        {t.nativeContextCompactionOff}
                      </button>
                      <button className={cn("h-8 rounded-md text-xs font-medium", nativeContextCompaction === 'after-turn' ? "bg-background shadow-sm" : "text-muted-foreground")} onClick={() => setNativeContextCompaction('after-turn')} disabled={creating || isRunning}>
                        {t.nativeContextCompactionAfterTurn}
                      </button>
                    </div>
                  </div>
                </div>
              </div>

              <div className="flex shrink-0 flex-col gap-2">
                <div className="flex shrink-0 items-center justify-between">
                  <span className="text-xs font-semibold text-muted-foreground uppercase tracking-wider">{t.files}</span>
                  <Button variant="ghost" size="sm" className="h-7 gap-1.5" onClick={() => fileInputRef.current?.click()} disabled={creating || isRunning}>
                    <FileUp className="h-3.5 w-3.5" />
                    {t.add}
                  </Button>
                </div>
                <input ref={fileInputRef} type="file" multiple className="hidden" onChange={(event) => addFiles(event.target.files).catch((err) => setError(err.message))} />
                <div className="space-y-2">
                  <section className="rounded-md border border-border/70 bg-background/40">
                    <div className="flex h-7 items-center justify-between border-b border-border/70 px-2">
                      <span className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{t.currentRunFiles}</span>
                      {activeRunFiles.length > 0 && <span className="text-[10px] text-muted-foreground">{activeRunFiles.length}</span>}
                    </div>
                    <div className="max-h-32 min-h-[3rem] space-y-1.5 overflow-y-auto p-1.5 elegant-scrollbar">
                      {activeRunFiles.length > 0 ? (
                        activeRunFiles.map((file, index) => (
                          <OrchestrationFileRow key={`${file.name}-${file.size}-${index}`} file={file} status={t.uploadedFileStatus} />
                        ))
                      ) : (
                        <div className="rounded-md border border-dashed border-border p-3 text-xs text-muted-foreground">{t.uploadProofFiles}</div>
                      )}
                    </div>
                  </section>
                  {files.length > 0 && (
                    <section className="rounded-md border border-border/70 bg-background/40">
                      <div className="flex h-7 items-center justify-between border-b border-border/70 px-2">
                        <span className="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">{t.pendingFiles}</span>
                        <span className="text-[10px] text-muted-foreground">{files.length}</span>
                      </div>
                      <div className="max-h-36 space-y-1.5 overflow-y-auto p-1.5 elegant-scrollbar">
                        {files.map((file) => (
                          <OrchestrationFileRow
                            key={file.id}
                            file={file}
                            status={t.pendingFileStatus}
                            onRemove={() => removeFile(file.id)}
                            removeLabel={`${t.removeFile} ${file.name}`}
                          />
                        ))}
                      </div>
                    </section>
                  )}
                </div>
              </div>

              {error && (
                <div className="flex shrink-0 items-start gap-2 rounded-md border border-destructive/20 bg-destructive/10 px-3 py-2 text-sm text-destructive">
                  <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />
                  <span>{error}</span>
                </div>
              )}

              {!(leadingEdition && activeRun && !isRunning) && <div className="mt-auto flex shrink-0 items-center gap-2 pt-1">
                {isRunning ? (
                  <Button variant="secondary" className="w-full gap-2" onClick={() => cancelRun().catch((err) => setError(err.message))} disabled={!canCancelRun}>
                    {canCancelRun ? <Square className="h-3.5 w-3.5 fill-current" /> : <RefreshCw className="h-4 w-4 animate-spin" />}
                    {canCancelRun ? t.stopRun : t.stopping}
                  </Button>
                ) : (
				  <Button className="w-full gap-2" onClick={() => startRun()} disabled={selectionBlocked || !prompt.trim() || creating || !selectedAgent || capabilityProblems.length > 0 || selectedTBCIUnavailable || (!activeRun && !workerProfilesComplete)}>
                    {creating ? <RefreshCw className="h-4 w-4 animate-spin" /> : <Send className="h-4 w-4" />}
                    {continuingRun ? t.continueRun : t.start}
                  </Button>
                )}
              </div>}
            </div>}
          </MobileSurface>
        </div>
        <nav className="phone-only phone-run-navigation border-t border-border bg-background" aria-label={language === 'zh' ? '运行操作' : 'Run actions'}>
          <Button variant="ghost" className="flex-1 gap-2" onClick={() => openMobilePanel('history')}><History className="h-4 w-4" />{t.runHistory}</Button>
          {leadingEdition && activeRun && <Button variant="secondary" className="flex-1 gap-2" aria-controls="orchestration-conversation" onClick={() => openMobilePanel('conversation')}><MessageSquare className="h-4 w-4" />{language === 'zh' ? '运行对话' : 'Run conversation'}</Button>}
          <Button variant={activeRun ? 'ghost' : 'secondary'} className="flex-1 gap-2" aria-controls="orchestration-configuration" onClick={() => openMobilePanel('configuration')}><Settings className="h-4 w-4" />{language === 'zh' ? (activeRun ? '配置 / 附件' : '初始化任务') : (activeRun ? 'Setup / files' : 'Initialize task')}</Button>
        </nav>
      </main>

      <Dialog open={Boolean(editingRun)} onOpenChange={(open) => { if (!open && !updatingRunId) setEditingRun(null); }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader><DialogTitle>{t.renameRun}</DialogTitle><DialogDescription>{t.runTitleLength}</DialogDescription></DialogHeader>
          <form className="space-y-4" onSubmit={(event) => {
            event.preventDefault();
            const title = editingTitle.trim();
            if (!title || Array.from(title).length > 200) { setMetadataError(t.runTitleLength); return; }
            if (editingRun) void updateRunMetadata(editingRun, { title });
          }}>
            <label className="block space-y-2 text-sm"><span>{t.runTitle}</span><Input autoFocus value={editingTitle} onChange={(event) => setEditingTitle(event.target.value)} disabled={Boolean(updatingRunId)} /></label>
            {metadataError && <p role="alert" className="text-sm text-destructive">{metadataError}</p>}
            <div className="flex justify-end gap-2"><Button type="button" variant="ghost" disabled={Boolean(updatingRunId)} onClick={() => setEditingRun(null)}>{t.cancel}</Button><Button type="submit" disabled={Boolean(updatingRunId)}>{t.save}</Button></div>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={agentGraphExpanded} onOpenChange={setAgentGraphExpanded}>
        <DialogContent className="flex h-[calc(100vh-2rem)] w-[calc(100vw-2rem)] max-w-none flex-col gap-3 overflow-hidden p-4 sm:max-w-none md:p-5">
          <DialogHeader className="shrink-0 pr-10">
            <DialogTitle className="text-base">{language === 'zh' ? `任务 ${selectedProgressTask?.taskNumber || 1} · Agent 执行链` : `Task ${selectedProgressTask?.taskNumber || 1} · Agent execution chain`}</DialogTitle>
            <DialogDescription>{`${orchestrationGraphLabel(selectedAgentGraph, selectedProgressTask?.graphs || [], language)} · ${language === 'zh' ? '拖动画布，滚轮或双指缩放。' : 'Drag to pan and scroll or pinch to zoom.'}`}</DialogDescription>
          </DialogHeader>
          {(selectedProgressTask?.graphs?.length || 0) > 1 && <div className="flex shrink-0 gap-1 overflow-x-auto rounded-md border border-border bg-muted/20 p-1 elegant-scrollbar">{selectedProgressTask?.graphs.map((graph) => <button key={graph.id} type="button" onClick={() => setSelectedProgressGraphId(graph.id)} className={cn('h-7 shrink-0 rounded px-2.5 text-xs transition-colors', selectedAgentGraph?.id === graph.id ? 'bg-background font-medium text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground')}>{orchestrationGraphLabel(graph, selectedProgressTask?.graphs || [], language)}</button>)}</div>}
          <div className="min-h-0 flex-1 overflow-hidden rounded-md border border-border">
            <OrchestrationProgressMap tasks={(selectedAgentGraph?.tasks || []).map((task) => ({ ...task, name: orchestrationTaskDisplayName(task.name, language), dependencies: task.dependencies || [] }))} height="100%" interactive ariaLabel={language === 'zh' ? '可缩放的 Agent 执行链' : 'Interactive Agent execution chain'} emptyLabel={language === 'zh' ? '暂无执行节点。' : 'No execution nodes yet.'} statusLabels={orchestrationStatusLabels(language, t)} />
          </div>
        </DialogContent>
      </Dialog>

      <Dialog open={taskMapExpanded} onOpenChange={setTaskMapExpanded}>
        <DialogContent className="flex h-[min(44rem,calc(100vh-5rem))] w-[min(68rem,calc(100vw-2rem))] max-w-none flex-col gap-3 overflow-hidden p-4 md:p-5">
          <DialogHeader className="shrink-0 pr-10">
            <DialogTitle className="flex items-center gap-2 text-base"><Workflow className="h-4 w-4 text-primary" />{language === 'zh' ? `任务 ${selectedProgressTask?.taskNumber || 1} · 任务分支地图` : `Task ${selectedProgressTask?.taskNumber || 1} · Task branch map`}</DialogTitle>
            <DialogDescription>{language === 'zh' ? '拖动画布，滚轮或双指缩放；使用右下角控件调整视图。' : 'Drag to pan, scroll or pinch to zoom, and use the lower-right controls to adjust the view.'}</DialogDescription>
          </DialogHeader>
          <div className="min-h-0 flex-1 overflow-hidden rounded-md border border-border">
            <OrchestrationProgressMap
              tasks={projectedPlanItems.map((item, index) => ({ id: item.id, name: item.title, role: item.branch || item.kind, status: item.status, position: item.priority || index + 1, dependencies: item.dependsOn || [], detail: item.evidence || item.rationale }))}
              activeTaskId={projectedPlan?.currentFocus}
              height="100%"
              interactive
              ariaLabel={language === 'zh' ? '可缩放的任务分支地图' : 'Interactive task branch map'}
              emptyLabel={language === 'zh' ? '本次任务没有可用的结构化计划。' : 'No structured task plan is available for this run.'}
              statusLabels={orchestrationStatusLabels(language, t)}
              inferSequentialDependencies={false}
            />
          </div>
        </DialogContent>
      </Dialog>

      {settingsOpen && (
        <SettingsModal
          user={user}
          agents={agents}
          selectedAgentId={selectedAgentId}
          onSelectAgent={selectAgent}
          onAgentsChanged={async () => { await loadAgents(); }}
		  onPresetsChanged={refreshWorkerPresets}
          onLogout={logout}
          isDarkMode={isDarkMode}
          setIsDarkMode={setIsDarkMode}
          language={language}
          setLanguage={setLanguage}
          t={t}
          initialFocus={settingsFocus}
          close={() => setSettingsOpen(false)}
        />
      )}
    </div>
  );
}

function orchestrationRunPath(runId: string) {
  return `/orchestrate/runs/${encodeURIComponent(runId)}`;
}

function orchestrationRunIdFromPath(path: string) {
  const match = path.match(/^\/orchestrate\/runs\/([^/?#]+)/);
  return match ? decodeURIComponent(match[1]) : '';
}

function orchestrationTaskDisplayName(name: string, language: Language) {
  if (language !== 'zh') return name;
  const labels: Record<string, string> = {
    plan: '规划任务',
    'plan-review': '审核计划',
    'candidate-a': '候选方案 A',
    'candidate-b': '候选方案 B',
    integrate: '整合实现',
    review: '独立复核',
  };
  return labels[name] || name;
}

function orchestrationGraphLabel(graph: OrchestrationTaskGraph | null | undefined, graphs: OrchestrationTaskGraph[], language: Language) {
  const ordinal = Math.max(1, graphs.findIndex((item) => item.id === graph?.id) + 1);
  const total = Math.max(1, graphs.length);
  return language === 'zh' ? `第 ${ordinal}/${total} 轮` : `Round ${ordinal}/${total}`;
}

function orchestrationStatusLabels(language: Language, t: UIText) {
  if (language !== 'zh') {
    return {
      pending: 'Pending',
      ready: t.ready,
      dispatching: 'Starting',
      running: 'Running',
      in_progress: 'In progress',
      succeeded: 'Completed',
      completed: 'Completed',
      failed: 'Failed',
      blocked: 'Blocked',
      canceling: 'Canceling',
      canceled: 'Canceled',
      unknown: 'Unknown',
    };
  }
  return {
    pending: '待处理',
    ready: '可开始',
    dispatching: '启动中',
    running: '进行中',
    in_progress: '进行中',
    succeeded: '已完成',
    completed: '已完成',
    failed: '失败',
    blocked: '受阻',
    canceling: '取消中',
    canceled: '已取消',
    unknown: '状态未知',
  };
}

function maxOrchestrationEventSeq(events: OrchestrationEvent[], initial = 0) {
  return events.reduce((max, event) => {
    if (typeof event.seq !== 'number' || !Number.isFinite(event.seq)) return max;
    return Math.max(max, event.seq);
  }, initial);
}

function minOrchestrationEventSeq(events: OrchestrationEvent[], initial = 0) {
  return events.reduce((min, event) => {
    if (typeof event.seq !== 'number' || !Number.isFinite(event.seq)) return min;
    if (min <= 0) return event.seq;
    return Math.min(min, event.seq);
  }, initial);
}
