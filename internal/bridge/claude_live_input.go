package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

// All JSONL writers share this lock, including the long-command observer.
type lockedClaudeInput struct {
	mu sync.Mutex
	io.WriteCloser
}

func (w *lockedClaudeInput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if deadlineWriter, ok := w.WriteCloser.(interface{ SetWriteDeadline(time.Time) error }); ok {
		_ = deadlineWriter.SetWriteDeadline(time.Now().Add(10 * time.Second))
		defer deadlineWriter.SetWriteDeadline(time.Time{})
	}
	return w.WriteCloser.Write(p)
}
func (w *lockedClaudeInput) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.WriteCloser.Close()
}

type claudeLiveInput struct {
	mu                   sync.Mutex
	pending              map[string]protocol.OrchestrationInputPayload
	resultSeen           bool
	closed               bool
	box                  *liveInputMailbox
	input                io.Writer
	controls             map[string]chan error
	wakeAfterFailedInput func()
}

func newClaudeLiveInput(box *liveInputMailbox, input io.Writer) *claudeLiveInput {
	return &claudeLiveInput{box: box, input: input, pending: make(map[string]protocol.OrchestrationInputPayload), controls: make(map[string]chan error)}
}
func (l *claudeLiveInput) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.box.wake:
		}
		for {
			l.mu.Lock()
			if l.closed {
				l.mu.Unlock()
				return
			}
			p, ok := l.box.pop(false)
			if !ok {
				l.mu.Unlock()
				break
			}
			id := nativeLiveInputUUID(p.RunID, p.MessageID)
			l.pending[id] = p
			l.resultSeen = false
			l.mu.Unlock()
			var err error
			if p.DeliveryMode == "immediate" {
				err = l.interrupt(ctx, id)
			}
			l.mu.Lock()
			if err == nil {
				l.resultSeen = false
				raw, marshalErr := json.Marshal(map[string]any{"type": "user", "uuid": id, "priority": "next", "message": map[string]any{"role": "user", "content": []map[string]string{{"type": "text", "text": p.Prompt}}}})
				err = marshalErr
				if err == nil {
					var n int
					n, err = l.input.Write(append(raw, '\n'))
					if err == nil && n != len(raw)+1 {
						err = io.ErrShortWrite
					}
				}
			}
			if err != nil {
				delete(l.pending, id)
			}
			wake := err != nil && l.resultSeen && len(l.pending) == 0
			l.box.receipt(p, err)
			l.mu.Unlock()
			// The CLI may have emitted its final result before the control request
			// timed out. Close only the finished turn's stdin so its blocked stdout
			// reader receives EOF; never leave it waiting for a nonexistent frame.
			if wake && l.wakeAfterFailedInput != nil {
				l.wakeAfterFailedInput()
			}
		}
	}
}

// The official headless control protocol interrupts the active turn while
// retaining its native session. It does not terminate the CLI process.
func (l *claudeLiveInput) interrupt(ctx context.Context, id string) error {
	requestID := "live-interrupt-" + id
	ch := make(chan error, 1)
	l.mu.Lock()
	l.controls[requestID] = ch
	l.mu.Unlock()
	defer func() { l.mu.Lock(); delete(l.controls, requestID); l.mu.Unlock() }()
	raw, _ := json.Marshal(map[string]any{"type": "control_request", "request_id": requestID, "request": map[string]any{"subtype": "interrupt"}})
	if n, err := l.input.Write(append(raw, '\n')); err != nil {
		return err
	} else if n != len(raw)+1 {
		return io.ErrShortWrite
	}
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return errors.New("Claude interrupt receipt timed out; instruction was not sent")
	}
}

// Observe runs in the sole stdout reader. A result belonging to the original
// turn cannot complete a node while native queued instructions are outstanding.
func (l *claudeLiveInput) observe(msg map[string]any) (bool, error) {
	if l == nil {
		return false, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	typ := firstString(msg, "type")
	if typ == "control_response" {
		response, _ := msg["response"].(map[string]any)
		if ch := l.controls[firstString(response, "request_id")]; ch != nil {
			var err error
			if firstString(response, "subtype") != "success" {
				err = errors.New("Claude interrupt rejected: " + firstString(response, "error"))
				id := strings.TrimPrefix(firstString(response, "request_id"), "live-interrupt-")
				if p, ok := l.pending[id]; ok {
					delete(l.pending, id)
					l.box.receipt(p, err)
				}
			}
			select {
			case ch <- err:
			default:
			}
		}
	}

	if typ == "command_lifecycle" {
		id := firstString(msg, "command_uuid")
		p, ok := l.pending[id]
		if !ok {
			return false, nil
		}
		switch firstString(msg, "state") {
		case "started":
			l.resultSeen = false
		case "completed":
			delete(l.pending, id)
		case "cancelled", "discarded", "refused":
			delete(l.pending, id)
			return false, errors.New("Claude did not process runtime instruction " + p.MessageID + ": " + firstString(msg, "state"))
		}
	}
	if typ == "result" {
		l.resultSeen = true
	}
	if l.resultSeen && len(l.pending) == 0 {
		// Close under the same lock used to dequeue/write new input. New messages
		// remain in the mailbox for the next turn instead of escaping this reader.
		l.box.mu.Lock()
		waiting := len(l.box.pending) > 0
		l.box.mu.Unlock()
		if !waiting {
			l.closed = true
			return true, nil
		}
	}
	return false, nil
}
func (l *claudeLiveInput) close() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
}

func nativeLiveInputUUID(runID, messageID string) string {
	h := sha256.Sum256([]byte(runID + "\x00" + messageID))
	h[6] = (h[6] & 15) | 64
	h[8] = (h[8] & 63) | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", h[:4], h[4:6], h[6:8], h[8:10], h[10:16])
}
