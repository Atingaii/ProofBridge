package store

import (
	"context"
	"errors"
	"testing"
)

func TestOrchestrationMessageIdempotencyAndReceiptFence(t *testing.T) {
	st := openTestStore(t)
	graph := createTestTaskGraph(t, st)
	ctx := context.Background()
	m := OrchestrationMessage{ID: "m1", RunID: graph.RunID, Prompt: "add a check", DeliveryMode: "queue"}
	got, created, err := st.CreateOrchestrationMessage(ctx, m)
	if err != nil || !created || got.Status != "queued" {
		t.Fatalf("create: %+v %v %v", got, created, err)
	}
	_, created, err = st.CreateOrchestrationMessage(ctx, m)
	if err != nil || created {
		t.Fatalf("repeat: %v %v", created, err)
	}
	conflict := m
	conflict.Prompt = "different"
	if _, _, err = st.CreateOrchestrationMessage(ctx, conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict %v", err)
	}
	ok, err := st.ClaimOrchestrationMessage(ctx, m.RunID, m.ID, "attempt-new")
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	ok, err = st.ClaimOrchestrationMessage(ctx, m.RunID, m.ID, "attempt-old")
	if err != nil || ok {
		t.Fatal("duplicate claim", ok, err)
	}
	ok, err = st.FinishOrchestrationMessage(ctx, m.RunID, m.ID, "attempt-old", "delivered", "")
	if err != nil || ok {
		t.Fatal("stale ack", ok, err)
	}
	ok, err = st.FinishOrchestrationMessage(ctx, m.RunID, m.ID, "attempt-new", "delivered", "")
	if err != nil || !ok {
		t.Fatal("ack", ok, err)
	}
	if err = st.FailOrchestrationMessages(ctx, m.RunID, "attempt-new", "ended"); err != nil {
		t.Fatal(err)
	}
	got, err = st.OrchestrationMessageByID(ctx, m.RunID, m.ID)
	if err != nil || got.Status != "delivered" {
		t.Fatal(got, err)
	}
	rows, err := st.ListOrchestrationMessages(ctx, m.RunID)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
}
func TestOrchestrationMessageUncertainDeliveryNeverReplays(t *testing.T) {
	st := openTestStore(t)
	g := createTestTaskGraph(t, st)
	ctx := context.Background()
	m := OrchestrationMessage{ID: "uncertain", RunID: g.RunID, Prompt: "once", DeliveryMode: "immediate"}
	_, _, _ = st.CreateOrchestrationMessage(ctx, m)
	_, _ = st.ClaimOrchestrationMessage(ctx, m.RunID, m.ID, "attempt")
	if err := st.FailOrchestrationMessages(ctx, m.RunID, "attempt", "delivery uncertain"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := st.ClaimOrchestrationMessage(ctx, m.RunID, m.ID, "retry"); ok {
		t.Fatal("uncertain instruction replayed")
	}
	if ok, _ := st.FinishOrchestrationMessage(ctx, m.RunID, m.ID, "attempt", "delivered", ""); ok {
		t.Fatal("late ack rewrote terminal receipt")
	}
}
