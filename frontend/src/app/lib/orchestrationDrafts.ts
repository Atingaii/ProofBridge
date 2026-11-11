import { useCallback, useSyncExternalStore } from 'react';

const namespace = 'codexBridge.orchestrationDraft.v1:';
type Draft = { text: string; persisted: boolean };
const drafts = new Map<string, Draft>();
const listeners = new Set<() => void>();
const notify = () => listeners.forEach((listener) => listener());
const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
};

function accountPrefix(userId: string) {
  return `${namespace}${encodeURIComponent(userId)}:`;
}

function readDraft(key: string): Draft {
  const cached = drafts.get(key);
  if (cached) return cached;
  let draft: Draft;
  try {
    draft = { text: localStorage.getItem(key) || '', persisted: true };
  } catch {
    draft = { text: '', persisted: false };
  }
  drafts.set(key, draft);
  return draft;
}

function writeDraft(key: string, text: string) {
  let persisted = true;
  try {
    if (text) localStorage.setItem(key, text);
    else localStorage.removeItem(key);
  } catch {
    persisted = false;
  }
  // A new object records an edit, even if the user types the same text again.
  drafts.set(key, { text, persisted });
  notify();
}

export function clearAccountOrchestrationDrafts(userId: string) {
  const prefix = accountPrefix(userId);
  const keys = new Set([...drafts.keys()].filter((key) => key.startsWith(prefix)));
  try {
    for (let index = 0; index < localStorage.length; index += 1) {
      const key = localStorage.key(index);
      if (key?.startsWith(prefix)) keys.add(key);
    }
  } catch { /* Memory-only drafts must still be cleared when storage is unavailable. */ }
  keys.forEach((key) => writeDraft(key, ''));
}

export function useOrchestrationDraft(userId: string, target: string) {
  const key = `${accountPrefix(userId)}${target}`;
  const snapshot = useCallback(() => readDraft(key), [key]);
  const draft = useSyncExternalStore(subscribe, snapshot);
  const setText = useCallback((text: string) => writeDraft(key, text), [key]);
  // This callback belongs to the exact render submitted by the caller. It stays
  // safe after switching targets, editing again, unmounting, or logging out.
  const clearSubmitted = useCallback(() => {
    if (readDraft(key) !== draft) return false;
    writeDraft(key, '');
    return true;
  }, [draft, key]);
  return { text: draft.text, setText, clearSubmitted, persistenceFailed: !draft.persisted };
}
