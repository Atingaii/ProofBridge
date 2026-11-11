import React, { useEffect, useRef, useState } from 'react';
import { X } from 'lucide-react';
import { Button } from './ui';
import { cn } from '../lib/utils';

// Presentation only: this breakpoint also covers short, touch-driven landscape phones.
const phoneQuery = '(max-width: 767px), (max-height: 500px) and (pointer: coarse)';
const topVisibleDialog = () => Array.from(document.querySelectorAll<HTMLElement>('[role="dialog"]'))
  .filter((dialog) => dialog.getClientRects().length > 0 && getComputedStyle(dialog).visibility !== 'hidden').at(-1);

/** Keep keyboard navigation in the visible dialog, without touching its inputs. */
export function useDialogFocus<T extends HTMLElement>(ref: React.RefObject<T | null>, enabled = true) {
  useEffect(() => {
    if (!enabled) return;
    const query = window.matchMedia(phoneQuery);
    const enter = () => {
      const panel = ref.current;
      let previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      const targets = () => Array.from(panel?.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex="0"]') || [])
        .filter((item) => item.getClientRects().length > 0);
      const focusFrame = window.requestAnimationFrame(() => {
        if (panel?.contains(document.activeElement)) return;
        previous = document.activeElement instanceof HTMLElement ? document.activeElement : null;
        (panel?.querySelector<HTMLButtonElement>('[data-mobile-close]') || targets()[0])?.focus({ preventScroll: true });
      });
      const onKey = (event: KeyboardEvent) => {
        if (event.key !== 'Tab' || !panel || (event.target instanceof Element && event.target.closest('[role="dialog"]') !== panel)) return;
        const items = targets(), first = items[0], last = items.at(-1);
        if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
        else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
      };
      const onEscape = (event: KeyboardEvent) => {
        if (event.key !== 'Escape' || event.defaultPrevented || !panel || topVisibleDialog() !== panel || (event.target instanceof Node && panel.contains(event.target))) return;
        // Capture before a background dialog can handle a key from stale focus.
        // The forwarded event uses this dialog's existing close/saving guard.
        event.preventDefault();
        event.stopImmediatePropagation();
        panel.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }));
      };
      panel?.addEventListener('keydown', onKey);
      document.addEventListener('keydown', onEscape, true);
      return () => {
        window.cancelAnimationFrame(focusFrame);
        panel?.removeEventListener('keydown', onKey);
        document.removeEventListener('keydown', onEscape, true);
        if (previous?.isConnected && previous.getClientRects().length) previous.focus({ preventScroll: true });
      };
    };
    let leave: (() => void) | undefined;
    const update = () => {
      leave?.();
      leave = query.matches ? enter() : undefined;
    };
    update();
    query.addEventListener('change', update);
    return () => {
      query.removeEventListener('change', update);
      leave?.();
    };
  }, [enabled, ref]);
}

export function MobileViewport() {
  useEffect(() => {
    const viewport = window.visualViewport;
    const update = () => {
      document.documentElement.style.setProperty('--phone-viewport-height', `${viewport?.height || window.innerHeight}px`);
      document.documentElement.style.setProperty('--phone-viewport-top', `${viewport?.offsetTop || 0}px`);
    };
    update();
    viewport?.addEventListener('resize', update);
    viewport?.addEventListener('scroll', update);
    window.addEventListener('resize', update);
    return () => {
      viewport?.removeEventListener('resize', update);
      viewport?.removeEventListener('scroll', update);
      window.removeEventListener('resize', update);
      document.documentElement.style.removeProperty('--phone-viewport-height');
      document.documentElement.style.removeProperty('--phone-viewport-top');
    };
  }, []);
  return null;
}

/** The same children stay mounted when a phone panel closes or the device rotates. */
export function MobileSurface({ open, onClose, title, closeLabel, children, className, bodyClassName, id }: {
  open: boolean; onClose: () => void; title: string; closeLabel: string;
  children: React.ReactNode; className?: string; bodyClassName?: string; id?: string;
}) {
  const [phone, setPhone] = useState(() => window.matchMedia(phoneQuery).matches);
  const ref = useRef<HTMLElement>(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  useDialogFocus(ref, phone && open);
  useEffect(() => {
    const query = window.matchMedia(phoneQuery);
    const update = () => setPhone(query.matches);
    query.addEventListener('change', update);
    return () => query.removeEventListener('change', update);
  }, []);
  useEffect(() => {
    if (!phone || !open) return;
    const panel = ref.current;
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || event.defaultPrevented || !panel || topVisibleDialog() !== panel) return;
      event.preventDefault(); event.stopPropagation(); closeRef.current();
    };
    panel?.addEventListener('keydown', onKey);
    return () => { panel?.removeEventListener('keydown', onKey); };
  }, [phone, open]);
  return <aside ref={ref} id={id} role={phone && open ? 'dialog' : undefined} aria-modal={phone && open ? true : undefined} aria-label={phone && open ? title : undefined}
    className={cn('mobile-surface', open && 'mobile-surface-open', className)}>
    <div className="mobile-surface-heading">
      <h2 className="min-w-0 flex-1 text-sm font-semibold">{title}</h2>
      <Button data-mobile-close type="button" variant="ghost" size="icon" onClick={onClose} aria-label={closeLabel}><X className="h-5 w-5" /></Button>
    </div>
    <div className={cn('mobile-surface-body', bodyClassName)}>{children}</div>
  </aside>;
}
