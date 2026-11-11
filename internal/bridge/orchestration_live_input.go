package bridge

import (
	"errors"
	"sync"

	"github.com/tencent/codex-bridge/internal/protocol"
)

type liveInputMailbox struct {
	mu      sync.Mutex
	pending []protocol.OrchestrationInputPayload
	seen    map[string]protocol.OrchestrationInputStatusPayload
	wake    chan struct{}
	closed  bool
	manager *OrchestrationManager
}

func (m *OrchestrationManager) liveInputFor(execution string) *liveInputMailbox {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.liveInputs == nil {
		m.liveInputs = make(map[string]*liveInputMailbox)
	}
	box := m.liveInputs[execution]
	if box == nil {
		box = &liveInputMailbox{manager: m, wake: make(chan struct{}, 1), seen: make(map[string]protocol.OrchestrationInputStatusPayload)}
		m.liveInputs[execution] = box
	}
	return box
}
func (m *OrchestrationManager) dispatchLiveInput(p protocol.OrchestrationInputPayload) {
	if p.RunID == "" || p.MessageID == "" || p.ExecutionID == "" || p.Prompt == "" || (p.DeliveryMode != "queue" && p.DeliveryMode != "immediate") {
		return
	}
	m.mu.Lock()
	execution, exists := m.executions[p.ExecutionID]
	var box *liveInputMailbox
	if exists && execution.runID == p.RunID {
		if m.liveInputs == nil {
			m.liveInputs = make(map[string]*liveInputMailbox)
		}
		box = m.liveInputs[p.ExecutionID]
		if box == nil {
			box = &liveInputMailbox{manager: m, wake: make(chan struct{}, 1), seen: make(map[string]protocol.OrchestrationInputStatusPayload)}
			m.liveInputs[p.ExecutionID] = box
		}
	}
	m.mu.Unlock()
	if !exists || execution.runID != p.RunID {
		m.send(protocol.MustEnvelope(protocol.TypeOrchestrationInputStatus, "", protocol.OrchestrationInputStatusPayload{RunID: p.RunID, MessageID: p.MessageID, ExecutionID: p.ExecutionID, Status: "failed", Error: "target execution has ended; instruction was not sent"}))
		return
	}
	box.mu.Lock()
	if receipt, ok := box.seen[p.MessageID]; ok {
		box.mu.Unlock()
		if receipt.Status != "" {
			m.send(protocol.MustEnvelope(protocol.TypeOrchestrationInputStatus, "", receipt))
		}
		return
	}
	if box.closed || len(box.pending) >= 64 {
		box.mu.Unlock()
		box.receipt(p, errors.New("native input queue unavailable; instruction was not sent"))
		return
	}
	box.seen[p.MessageID] = protocol.OrchestrationInputStatusPayload{}
	box.pending = append(box.pending, p)
	box.mu.Unlock()
	select {
	case box.wake <- struct{}{}:
	default:
	}
}
func (b *liveInputMailbox) pop(immediateOnly bool) (protocol.OrchestrationInputPayload, bool) {
	if b == nil {
		return protocol.OrchestrationInputPayload{}, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, p := range b.pending {
		if immediateOnly && p.DeliveryMode != "immediate" {
			continue
		}
		b.pending = append(b.pending[:i], b.pending[i+1:]...)
		return p, true
	}
	return protocol.OrchestrationInputPayload{}, false
}
func (b *liveInputMailbox) receipt(p protocol.OrchestrationInputPayload, err error) {
	if b == nil {
		return
	}
	status := protocol.OrchestrationInputStatusPayload{RunID: p.RunID, ExecutionID: p.ExecutionID, MessageID: p.MessageID, Status: "delivered"}
	if err != nil {
		status.Status = "failed"
		status.Error = err.Error()
	}
	b.mu.Lock()
	b.seen[p.MessageID] = status
	b.mu.Unlock()
	b.manager.send(protocol.MustEnvelope(protocol.TypeOrchestrationInputStatus, "", status))
}
func (m *OrchestrationManager) closeLiveInput(execution string) {
	m.mu.Lock()
	box := m.liveInputs[execution]
	delete(m.liveInputs, execution)
	m.mu.Unlock()
	if box == nil {
		return
	}
	box.mu.Lock()
	box.closed = true
	pending := box.pending
	box.pending = nil
	box.mu.Unlock()
	for _, p := range pending {
		box.receipt(p, errors.New("execution ended before native input delivery"))
	}
}
func liveInputWake(box *liveInputMailbox) <-chan struct{} {
	if box == nil {
		return nil
	}
	return box.wake
}
