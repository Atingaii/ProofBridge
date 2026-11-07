package hub

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/tencent/codex-bridge/internal/notify"
	"github.com/tencent/codex-bridge/internal/store"
)

func (s *Server) automationMailReady() bool {
	c := s.cfg.Automation
	if !c.Enabled || !notify.NewSMTP(c.SMTP).Ready() {
		return false
	}
	_, err := s.automationPublicURL()
	return err == nil
}

func (s *Server) automationMailDeliveryMode() string {
	if !s.automationMailReady() {
		return "disabled"
	}
	return notify.NewSMTP(s.cfg.Automation.SMTP).DeliveryMode()
}

// This describes the current transport only: old outbox rows do not record its
// historical configuration. A sent row confirms SMTP acceptance, never receipt.
func (s *Server) automationNotificationStatus(rows []store.AutomationNotification) map[string]any {
	return map[string]any{
		"notifications":     rows,
		"mailDeliveryMode":  s.automationMailDeliveryMode(),
		"deliveryModeScope": "current-transport",
		"sentStatusMeaning": "smtp-accepted",
	}
}

func (s *Server) queueAutomationAttention(ctx context.Context, runID, interactionID string) {
	if !s.cfg.Automation.Enabled {
		return
	}
	if err := s.store.QueueAutomationAttention(ctx, runID, interactionID); err != nil && !errors.Is(err, store.ErrNotFound) {
		slog.Warn("[automation] could not persist attention notification", "run_id", runID, "error", err)
	}
}
func (s *Server) cancelAutomationAttention(ctx context.Context, runID, interactionID string) {
	if err := s.store.CancelAutomationAttention(ctx, runID, interactionID); err != nil {
		slog.Warn("[automation] could not cancel closed interaction notification", "run_id", runID, "error", err)
	}
}

func (s *Server) runAutomationNotifications(ctx context.Context) {
	if !s.cfg.Automation.Enabled {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	sender := notify.NewSMTP(s.cfg.Automation.SMTP)
	for {
		if ctx.Err() != nil {
			return
		}
		opCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := s.store.ReconcileAutomationNotifications(opCtx)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Warn("[automation] notification reconciliation failed", "error", err)
		}
		if s.automationMailReady() {
			base, err := s.automationPublicURL()
			if err == nil {
				timeout := s.cfg.Automation.SMTP.Timeout.Duration
				if timeout <= 0 {
					timeout = 15 * time.Second
				}
				for n := 0; n < 10 && ctx.Err() == nil; n++ {
					row, err := s.store.ClaimAutomationNotification(ctx, timeout+15*time.Second)
					if errors.Is(err, store.ErrNotFound) {
						break
					}
					if err != nil {
						slog.Warn("[automation] notification claim failed", "error", err)
						break
					}
					sendCtx, cancel := context.WithTimeout(ctx, timeout)
					err = sender.Send(sendCtx, notify.FromNotification(row, base))
					cancel()
					var retryAt time.Time
					if err != nil && row.Attempts < 5 {
						delay := time.Second * time.Duration(1<<min(row.Attempts, 9))
						retryAt = time.Now().Add(delay)
					}
					finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					finishErr := s.store.FinishAutomationNotification(finishCtx, row.ID, row.ClaimToken, err, retryAt)
					cancel()
					if finishErr != nil {
						slog.Warn("[automation] notification receipt could not be persisted", "notification_id", row.ID, "error", finishErr)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
