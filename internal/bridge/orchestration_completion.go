package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
)

// A bounded content fingerprint is a progress signal, not a proof checker.
// Missing, symlinked, unreadable or oversized trees cannot establish stagnation.
func completionSourceDigest(ctx context.Context, root string) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	h := sha256.New()
	files, bytesLeft := 0, int64(32<<20)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".codex", ".claude", ".codex-bridge", "node_modules", "__pycache__", ".cache", "_build", ".lake":
				return filepath.SkipDir
			}
			return nil
		}
		switch filepath.Ext(entry.Name()) {
		case ".vo", ".vos", ".vok", ".glob", ".aux", ".olean", ".ilean", ".pyc", ".o":
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".lia.cache") || strings.HasPrefix(entry.Name(), ".nia.cache") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular source")
		}
		files++
		if files > 2048 || info.Size() > bytesLeft {
			return fmt.Errorf("source budget exceeded")
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		content := sha256.New()
		n, err := io.Copy(content, io.LimitReader(f, bytesLeft+1))
		f.Close()
		if err != nil {
			return err
		}
		bytesLeft -= n
		if bytesLeft < 0 {
			return fmt.Errorf("source budget exceeded")
		}
		rel, _ := filepath.Rel(root, path)
		fmt.Fprintf(h, "%s\x00%d\x00%x\n", rel, n, content.Sum(nil))
		return nil
	})
	if err != nil || files == 0 {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func completionSample(payload protocol.OrchestrationStartPayload, record orchestrationTurn, digest string) protocol.CompletionObservation {
	identity := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d\x00%s\x00%s", payload.RunID, payload.PromptSeq, firstNonEmpty(payload.TaskPrompt, payload.Prompt), firstNonEmpty(payload.RunCWD, payload.CWD))))
	sample := protocol.CompletionObservation{SourceDigest: digest, TaskDigest: hex.EncodeToString(identity[:])}
	if payload.TaskGraph != nil {
		sample.Round = payload.TaskGraph.Round
	}
	u := record.Usage
	// Do not infer unknown native counts or compare routed/mixed models.
	if u.Native && !u.Estimated && u.Model != "" && u.Model != "mixed" && u.OutputTokens > 0 {
		sample.UsageKey = record.Role + "/" + record.WorkerSlot + "/" + record.CLI + "/" + u.Model
		sample.OutputTokens = u.OutputTokens
	}
	return sample
}

func completionPlateau(history []protocol.CompletionObservation, current protocol.CompletionObservation) bool {
	samples := append(append([]protocol.CompletionObservation(nil), history...), current)
	if len(samples) < 6 {
		return false
	}
	samples = samples[len(samples)-6:]
	for i, s := range samples {
		if s.UsageKey == "" || s.UsageKey != current.UsageKey || s.OutputTokens <= 0 {
			return false
		}
		if i > 0 && s.Round != samples[i-1].Round+1 {
			return false
		}
	}
	baseline := []int64{samples[0].OutputTokens, samples[1].OutputTokens, samples[2].OutputTokens}
	sort.Slice(baseline, func(i, j int) bool { return baseline[i] < baseline[j] })
	low, high := samples[3].OutputTokens, samples[3].OutputTokens
	for _, s := range samples[3:] {
		if s.OutputTokens < low {
			low = s.OutputTokens
		}
		if s.OutputTokens > high {
			high = s.OutputTokens
		}
	}
	return float64(high) <= float64(baseline[1])*0.25 && float64(high) <= float64(low)*1.5
}

func completionBlockerKey(verdict protocol.VerifierVerdict, agents []agentVerifierResult) string {
	if verdict.Status == verifierVerdictPass {
		return ""
	}
	reasons := []string{}
	concrete := []string{}
	allInfrastructure := len(agents) > 0
	for _, a := range agents {
		text := a.Reason + " " + a.Error
		for _, c := range a.Checks {
			if c.Status != verifierVerdictPass {
				text += " " + c.Reason
			}
		}
		lower := strings.ToLower(text)
		infrastructure := strings.Contains(lower, "bwrap") || strings.Contains(lower, "bubblewrap") || strings.Contains(lower, "namespace") && strings.Contains(lower, "permi")
		allInfrastructure = allInfrastructure && infrastructure
	}
	if allInfrastructure {
		return "infrastructure:sandbox-unavailable"
	}
	for _, c := range verdict.Checkers {
		if c.Status != verifierVerdictPass {
			if concreteVerifierBlocker(c) {
				name := c.Name[strings.LastIndex(c.Name, "/")+1:]
				concrete = append(concrete, name+":"+c.Code+":"+strings.TrimSpace(c.Target))
			} else {
				reasons = append(reasons, c.Name+":"+strings.Join(strings.Fields(strings.ToLower(c.Reason)), " "))
			}
		}
	}
	// Model invocation errors alone are not stable proof obligations.
	for _, a := range agents {
		if a.Error != "" {
			return ""
		}
	}
	if len(concrete) > 0 {
		// Generic task/workspace paraphrases add no distinct obligation when
		// a concrete failed target already identifies the repeated blocker.
		reasons = concrete
	}
	if len(reasons) == 0 {
		return ""
	}
	sort.Strings(reasons)
	unique := reasons[:0]
	for _, reason := range reasons {
		if len(unique) == 0 || unique[len(unique)-1] != reason {
			unique = append(unique, reason)
		}
	}
	sum := sha256.Sum256([]byte(strings.Join(unique, "\n")))
	return hex.EncodeToString(sum[:])
}

func completionStalled(history []protocol.CompletionObservation, current protocol.CompletionObservation) bool {
	if current.SourceDigest == "" || current.BlockerKey == "" || len(history) < 2 {
		return false
	}
	prev := history[len(history)-2:]
	for i, s := range prev {
		if s.TaskDigest != current.TaskDigest || s.SourceDigest != current.SourceDigest || s.BlockerKey != current.BlockerKey || s.Round != current.Round-2+i {
			return false
		}
	}
	return true
}

// A handoff requests verification; it is never evidence of task satisfaction.
func completionCandidate(payload protocol.OrchestrationStartPayload, record orchestrationTurn) bool {
	if payload.TaskGraph == nil || len(payload.TaskGraph.Tasks) != 1 {
		return false
	}
	role := payload.TaskGraph.Tasks[0].Role
	if role != "worker" && role != "integrator" {
		return false
	}
	p := record.Relay
	return p.Structured && p.Status == "resolved" && machineExplicitNone(p.Next) && machineExplicitNone(p.Risks)
}

func completedExecutionConclusion(content string, history []orchestrationTurn, verdict *protocol.VerifierVerdict, reason string) *protocol.RunConclusion {
	c := verifiedRunConclusion(content, history, verdict)
	if reason == "stalled" {
		c.Outcome = "completed"
		c.Summary = "已完成：连续三轮工作区未变且检查报告相同，已自动提前结束本次运行。"
		if verdict != nil {
			c.Summary += "\n检查记录：" + verdict.Reason
		}
	}
	return c
}
