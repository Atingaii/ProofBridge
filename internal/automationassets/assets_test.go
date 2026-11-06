package automationassets

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHandlerServesSkillManifestAndConditionalGet(t *testing.T) {
	handler := Handler("https://lab.example")
	for _, name := range []string{"SKILL.md", "references/tools.md", "checksums.txt"} {
		r := httptest.NewRequest(http.MethodGet, "/skills/proofbridge/"+name, nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK || w.Body.Len() == 0 || w.Header().Get("ETag") == "" {
			t.Fatalf("asset %s: status=%d body=%q", name, w.Code, w.Body.String())
		}
		cached := httptest.NewRecorder()
		r.Header.Set("If-None-Match", w.Header().Get("ETag"))
		handler.ServeHTTP(cached, r)
		if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
			t.Fatalf("conditional %s: %d", name, cached.Code)
		}
	}
	for _, path := range []string{"/skills/proofbridge/../../go.mod", "/skills/proofbridge/auth.json", "/other/SKILL.md"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("unexpected file served: %s", path)
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/skills/install.sh", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("mutating asset request status=%d", w.Code)
	}
	head := httptest.NewRecorder()
	handler.ServeHTTP(head, httptest.NewRequest(http.MethodHead, "/skills/install.sh", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 {
		t.Fatal("HEAD must not return installer body")
	}
}

type installation struct {
	home, project, installer string
}

func newInstallation(t *testing.T) installation {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("installer integration requires curl")
	}
	base := t.TempDir()
	i := installation{home: filepath.Join(base, "user home"), project: filepath.Join(base, "project"), installer: filepath.Join(base, "install.sh")}
	for _, dir := range []string{i.home, i.project} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(i.installer, []byte(Installer), 0o700); err != nil {
		t.Fatal(err)
	}
	return i
}

func (i installation) run(t *testing.T, hub, client, scope string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", i.installer, "--hub", hub, "--client", client, "--scope", scope)
	command.Dir = i.project
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "HOME=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "HOME="+i.home)
	output, err := command.CombinedOutput()
	return string(output), err
}

func (i installation) target(client, scope string) string {
	base := i.home
	if scope == "project" {
		base = i.project
	}
	hidden := ".agents"
	if client == "claude" {
		hidden = ".claude"
	}
	return filepath.Join(base, hidden, "skills", "proofbridge")
}

func TestInstallerClientsScopesAndManagedUpdate(t *testing.T) {
	server := httptest.NewServer(Handler(""))
	defer server.Close()
	for _, client := range []string{"codex", "claude"} {
		for _, scope := range []string{"user", "project"} {
			t.Run(client+"-"+scope, func(t *testing.T) {
				i := newInstallation(t)
				// A real credential/config sentinel must remain unchanged.
				configDir := filepath.Join(i.home, ".codex")
				if err := os.MkdirAll(configDir, 0o700); err != nil {
					t.Fatal(err)
				}
				original := []byte("existing user login configuration\n")
				config := filepath.Join(configDir, "config.toml")
				if err := os.WriteFile(config, original, 0o600); err != nil {
					t.Fatal(err)
				}
				output, err := i.run(t, server.URL, client, scope)
				if err != nil {
					t.Fatalf("install: %v: %s", err, output)
				}
				target := i.target(client, scope)
				for name, expected := range SkillFiles {
					actual, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(name)))
					if err != nil || string(actual) != expected {
						t.Fatalf("incorrect installed %s: %v", name, err)
					}
				}
				note := filepath.Join(target, "personal-notes.md")
				if err := os.WriteFile(note, []byte("preserve this note"), 0o600); err != nil {
					t.Fatal(err)
				}
				output, err = i.run(t, server.URL, client, scope)
				if err != nil {
					t.Fatalf("update: %v: %s", err, output)
				}
				actual, err := os.ReadFile(config)
				if err != nil || !bytes.Equal(actual, original) {
					t.Fatal("installer changed native CLI configuration")
				}
				if actual, err := os.ReadFile(note); err != nil || string(actual) != "preserve this note" {
					t.Fatal("managed update removed a user's unrelated file")
				}
				staging, _ := filepath.Glob(filepath.Join(filepath.Dir(target), ".proofbridge-install.*"))
				if len(staging) != 0 {
					t.Fatalf("installer left staging directories: %v", staging)
				}
			})
		}
	}
}

func TestInstallerPreservesExistingSkillOnCorruptDownload(t *testing.T) {
	handler := Handler("")
	var corrupt atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if corrupt.Load() && r.URL.Path == "/skills/proofbridge/references/tools.md" {
			_, _ = fmt.Fprint(w, "corrupt download")
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	i := newInstallation(t)
	if output, err := i.run(t, server.URL, "codex", "user"); err != nil {
		t.Fatalf("initial install: %v %s", err, output)
	}
	corrupt.Store(true)
	output, err := i.run(t, server.URL, "codex", "user")
	if err == nil || !strings.Contains(output, "SHA-256 mismatch") {
		t.Fatalf("corrupt update accepted: %v %s", err, output)
	}
	for name, expected := range SkillFiles {
		actual, err := os.ReadFile(filepath.Join(i.target("codex", "user"), filepath.FromSlash(name)))
		if err != nil || string(actual) != expected {
			t.Fatalf("failed update changed installed %s", name)
		}
	}
}

func TestInstallerRefusesUnmanagedOrSymlinkTarget(t *testing.T) {
	server := httptest.NewServer(Handler(""))
	defer server.Close()
	for _, symlink := range []bool{false, true} {
		t.Run(fmt.Sprint("symlink=", symlink), func(t *testing.T) {
			i := newInstallation(t)
			target := i.target("claude", "project")
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			if symlink {
				if err := os.Symlink(i.home, target); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(target, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(target, "SKILL.md"), []byte("user owned"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			output, err := i.run(t, server.URL, "claude", "project")
			if err == nil {
				t.Fatalf("unsafe target accepted: %s", output)
			}
			if !symlink {
				data, _ := os.ReadFile(filepath.Join(target, "SKILL.md"))
				if string(data) != "user owned" {
					t.Fatal("unmanaged skill was overwritten")
				}
			}
		})
	}
}

func TestSkillHasAllPublicToolsAndWorkerGuard(t *testing.T) {
	entry := SkillFiles["SKILL.md"]
	reference := SkillFiles["references/tools.md"]
	for _, important := range []string{"PROOFBRIDGE_MANAGED_WORKER=1", "background-mail", "clientRequestId", "submissionId", "not_sent"} {
		if !strings.Contains(entry, important) {
			t.Fatalf("skill missing operational invariant: %s", important)
		}
	}
	lines := strings.Split(reference, "\n")
	count := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "| `proofbridge_") {
			count++
		}
	}
	if count != 20 {
		t.Fatalf("public tool map contains %d tools", count)
	}
}
