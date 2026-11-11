package hub

import (
	"context"
	"net/http"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/store"
)

func TestBridgeTokenOptionalMachineName(t *testing.T) {
	s, st := newAuthTestServer(t)
	cookie := loginCookie(t, s, map[string]string{"username": "admin", "password": "secret12345"})
	for _, tc := range []struct {
		id, label, inputName, name string
		omitName                   bool
	}{
		{"blank", "", "", "", false},
		{"whitespace", " \t\n\u3000 ", " \t\n\u3000 ", "", false},
		{"legacy-label-only", " wsl2-cli ", "", "", true},
		{"legacy-custom-label-only", "旧令牌标签", "", "", true},
		{"blank-name-with-label", "wsl2-cli", "", "", false},
		{"trimmed-chinese", " 独立令牌标签 ", "  我的工作电脑  ", "我的工作电脑", false},
		{"name-without-label", "", "机器名称", "机器名称", false},
		{"shell-characters", "token label", " 工作电脑 ' $(printf INJECTED) `printf INJECTED` ; printf INJECTED # \" $HOME \\ * ? ", "工作电脑 ' $(printf INJECTED) `printf INJECTED` ; printf INJECTED # \" $HOME \\ * ?", false},
		{"unicode-limit", strings.Repeat("标", 81), strings.Repeat("机", 81), strings.Repeat("机", 80), false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			for _, selected := range []string{permissionProfileReviewRequired, permissionProfileStrictWorkspace, permissionProfileAutoExecute} {
				t.Run(selected, func(t *testing.T) {
					payload := map[string]string{
						"label": tc.label, "permissionProfile": selected,
					}
					if !tc.omitName {
						payload["name"] = tc.inputName
					}
					body := authJSONRequestWithCookie(t, s, http.MethodPost, "/api/bridge-tokens", cookie, payload, http.StatusCreated)
					wantLabel := strings.TrimSpace(tc.label)
					if wantLabel == "" {
						wantLabel = "CLI endpoint"
					}
					if runes := []rune(wantLabel); len(runes) > 80 {
						wantLabel = string(runes[:80])
					}
					if body["label"] != wantLabel || body["permissionProfile"] != selected {
						t.Fatalf("label/profile = %q/%q, want %q/%q", body["label"], body["permissionProfile"], wantLabel, selected)
					}
					assertBridgeTokenNameCommands(t, body, tc.name, "", "")
					info, err := st.ConsumeEnrollTokenInfo(context.Background(), body["token"].(string), "new-machine")
					if err != nil || info.Label != wantLabel {
						t.Fatalf("persisted token label = %q, want %q: %v", info.Label, wantLabel, err)
					}
					if agents, err := st.ListAgents(context.Background()); err != nil || len(agents) != 0 {
						t.Fatalf("token creation changed agents: %#v, %v", agents, err)
					}
				})
			}
		})
	}
}

func TestBridgeRepairTokenPreservesMachineNameAndIdentity(t *testing.T) {
	s, st := newAuthTestServer(t)
	cookie := loginCookie(t, s, map[string]string{"username": "admin", "password": "secret12345"})
	user, err := st.UserByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgentForUser(context.Background(), user.ID, "已有电脑 ' $(printf INJECTED)", "existing-machine", "host", "instance", []string{"/workspace/项目 '"})
	if err != nil {
		t.Fatal(err)
	}
	for _, selected := range []string{permissionProfileReviewRequired, permissionProfileStrictWorkspace, permissionProfileAutoExecute} {
		t.Run(selected, func(t *testing.T) {
			body := authJSONRequestWithCookie(t, s, http.MethodPost, "/api/agents/"+agent.ID+"/repair-token", cookie, map[string]string{
				"permissionProfile": selected,
			}, http.StatusCreated)
			if body["agentId"] != agent.ID || body["machineId"] != agent.MachineID || body["label"] != "repair "+agent.Name {
				t.Fatalf("repair identity/label changed: %#v", body)
			}
			assertBridgeTokenNameCommands(t, body, agent.Name, agent.MachineID, agent.WorkingDirs[0])
			after, err := st.AgentByID(context.Background(), agent.ID)
			if err != nil || !reflect.DeepEqual(after, agent) {
				t.Fatalf("repair token mutated agent: %#v, %v", after, err)
			}
		})
	}
}

func TestBridgeRepairMachineNameTakesPrecedence(t *testing.T) {
	s, _ := newAuthTestServer(t)
	command := s.bridgeConnectCommand("https://hub.test", "token", permissionProfileReviewRequired, bridgeConnectOptions{
		Name: "token label must not rename machine", Agent: &store.Agent{Name: "原有名称"},
	})
	args := bridgeCommandShellArgs(t, command)
	if i := slices.Index(args, "--name"); i < 0 || args[i+1] != "原有名称" {
		t.Fatalf("repair name precedence: %#v", args)
	}
}

func assertBridgeTokenNameCommands(t *testing.T, body map[string]any, name, machineID, cwd string) {
	t.Helper()
	check := func(command, profile string) {
		t.Helper()
		want := []string{"link", "--hub", body["hubUrl"].(string), "--profile", profile}
		if machineID != "" {
			want = append(want, "--machine-id", machineID)
		}
		if cwd != "" {
			want = append(want, "--cwd", cwd)
		}
		if name != "" {
			want = append(want, "--name", name)
		}
		want = append(want, body["token"].(string))
		if args := bridgeCommandShellArgs(t, command); !slices.Equal(args, want) {
			t.Fatalf("shell arguments = %#v, want %#v", args, want)
		}
	}
	connect := body["connectCommand"].(string)
	check(connect, body["permissionProfile"].(string))
	if body["commands"].([]any)[1] != connect || body["setupCommand"] != body["installCommand"].(string)+" && "+connect {
		t.Fatal("top-level commands disagree")
	}
	profiles := body["permissionProfiles"].([]any)
	if len(profiles) != 3 {
		t.Fatalf("permission profiles = %#v", profiles)
	}
	for _, raw := range profiles {
		profile := raw.(map[string]any)
		check(profile["connectCommand"].(string), profile["id"].(string))
		if profile["setupCommand"] != body["installCommand"].(string)+" && "+profile["connectCommand"].(string) {
			t.Fatal("profile setup command disagrees")
		}
	}
}

// Run the generated argument syntax through a real shell, replacing only the
// executable with an argument capture function so no endpoint is installed.
func bridgeCommandShellArgs(t *testing.T, command string) []string {
	t.Helper()
	const executable = "~/.local/bin/codex-bridge "
	if !strings.HasPrefix(command, executable) {
		t.Fatalf("unexpected link executable: %q", command)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	script := "capture() { printf '%s\\0' \"$@\"; }; capture " + strings.TrimPrefix(command, executable)
	output, err := exec.CommandContext(ctx, "/bin/sh", "-c", script).CombinedOutput()
	if err != nil {
		t.Fatalf("shell parse: %v: %s", err, output)
	}
	return strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
}
