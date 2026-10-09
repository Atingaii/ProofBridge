package store

import (
	"context"
	"testing"
)

func TestExecutionModeSurvivesMigrationAndContinuation(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	user, err := st.UpsertUser(ctx, "dynamic-owner", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgentForUser(ctx, user.ID, "agent", "machine", "host", "instance", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "dynamic"} {
		run, err := st.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{UserID: user.ID, AgentID: agent.ID, Mode: "collaboration", Prompt: "finish", ExecutionMode: mode, MaxTurns: 2})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		if err := st.UpdateOrchestrationRunSettings(ctx, run.ID, agent.ID, "collaboration", "codex-codex", "codex", "default", "agent", "/tmp", "off", 12, nil); err != nil {
			t.Fatal(err)
		}
		loaded, err := st.OrchestrationRunByID(ctx, run.ID, user.ID)
		if err != nil {
			t.Fatal(err)
		}
		wantMode, wantTurns := "bounded", 12
		if mode == "dynamic" {
			wantMode, wantTurns = "dynamic", 0
		}
		if loaded.ExecutionMode != wantMode || loaded.MaxTurns != wantTurns {
			t.Fatalf("lost policy: %+v", loaded)
		}
	}
	runs, err := st.ListOrchestrationRuns(ctx, user.ID, 10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("list regression: runs=%d err=%v", len(runs), err)
	}
}
