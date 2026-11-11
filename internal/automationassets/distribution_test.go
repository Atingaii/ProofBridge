package automationassets

import (
	"context"
	"net"
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

func TestGitHubManifestAndReleaseConsistency(t *testing.T) {
	if got := readAsset("skill/checksums.txt"); got != manifest() {
		t.Fatal("committed GitHub checksums differ from the embedded Skill; regenerate skill/checksums.txt")
	}
	if !strings.Contains(Installer, "asset_base='"+GitHubAssetRoot+"/skill'") {
		t.Fatal("installer files and generated bootstrap script use different GitHub releases")
	}
	for _, client := range []string{"codex", "claude"} {
		command := InstallCommand(client)
		if !strings.Contains(command, GitHubAssetRoot+"/install.sh") || strings.Contains(command, "--hub") || strings.Contains(command, " | ") {
			t.Fatalf("%s does not use the safe independent GitHub bootstrap", client)
		}
	}
	if InstallCommand("unknown") != "" {
		t.Fatal("unsupported client generated an install command")
	}
}

// A transport fixture captures the actual shell's two download layers without
// making ordinary unit tests depend on GitHub or the not-yet-published tag.
// TLS arguments remain visible in the trace; production TLS is tested on release.
func githubTransportFixture(t *testing.T) []string {
	t.Helper()
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	repo := filepath.Join(root, "repository")
	tmp := filepath.Join(root, "temporary")
	for _, dir := range []string{bin, filepath.Join(repo, "skill", "references"), tmp} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"install.sh": Installer, "skill/checksums.txt": manifest()}
	for name, body := range SkillFiles {
		files["skill/"+name] = body
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(repo, filepath.FromSlash(name)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	shim := `#!/bin/sh
set -eu
printf 'CALL\n' >> "$PB_TEST_CURL_LOG"
printf 'ARG %s\n' "$@" >> "$PB_TEST_CURL_LOG"
output=''
url=''
while [ "$#" -gt 0 ]; do
  case "$1" in
    --output) output=$2; shift 2 ;;
    https://*) url=$1; shift ;;
    --insecure|-k|--header|-H) exit 97 ;;
    *) shift ;;
  esac
done
case "$url" in "$PB_TEST_GITHUB_ROOT"/*) ;; *) exit 98 ;; esac
[ -n "$output" ] || exit 99
relative=${url#"$PB_TEST_GITHUB_ROOT/"}
case "$relative" in install.sh|skill/checksums.txt|skill/SKILL.md|skill/references/tools.md) ;; *) exit 96 ;; esac
if [ "${PB_TEST_FAIL_BOOTSTRAP:-0}" = 1 ] && [ "$relative" = install.sh ]; then
  printf '%s\n' 'touch "$PB_TEST_PARTIAL_EXECUTED"' > "$output"
  exit 22
fi
cp "$PB_TEST_REPOSITORY/$relative" "$output"
`
	if err := os.WriteFile(filepath.Join(bin, "curl"), []byte(shim), 0o700); err != nil {
		t.Fatal(err)
	}
	return []string{
		"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH"),
		"TMPDIR=" + tmp,
		"PB_TEST_GITHUB_ROOT=" + GitHubAssetRoot,
		"PB_TEST_REPOSITORY=" + repo,
		"PB_TEST_CURL_LOG=" + filepath.Join(root, "curl.log"),
		"PB_TEST_PARTIAL_EXECUTED=" + filepath.Join(root, "partial-executed"),
	}
}

func environmentValue(entries []string, key string) string {
	for _, entry := range entries {
		if value, ok := strings.CutPrefix(entry, key+"="); ok {
			return value
		}
	}
	return ""
}

func runGitHubCommand(t *testing.T, i installation, env []string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "sh", args...)
	command.Dir = i.project
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "HOME=") && !strings.HasPrefix(entry, "CLAUDE_CONFIG_DIR=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "HOME="+i.home)
	command.Env = append(command.Env, env...)
	out, err := command.CombinedOutput()
	return string(out), err
}

func assertInstalledGitHubFiles(t *testing.T, i installation, client string) {
	t.Helper()
	for name, expected := range SkillFiles {
		body, err := os.ReadFile(filepath.Join(i.target(client, "user"), filepath.FromSlash(name)))
		if err != nil || string(body) != expected {
			t.Fatalf("GitHub install did not publish %s: %v", name, err)
		}
	}
}

func TestGitHubCommandBothDownloadLayersAndCleanup(t *testing.T) {
	for _, client := range []string{"codex", "claude"} {
		t.Run(client, func(t *testing.T) {
			i := newInstallation(t)
			env := githubTransportFixture(t)
			if output, err := runGitHubCommand(t, i, env, "-c", InstallCommand(client)); err != nil {
				t.Fatalf("two-layer install: %v: %s", err, output)
			}
			assertInstalledGitHubFiles(t, i, client)
			trace, err := os.ReadFile(environmentValue(env, "PB_TEST_CURL_LOG"))
			if err != nil {
				t.Fatal(err)
			}
			text := string(trace)
			if strings.Count(text, "CALL\n") != 4 || strings.Count(text, "ARG =https\n") != 8 {
				t.Fatalf("expected bootstrap and three TLS-only asset downloads: %s", text)
			}
			for _, file := range []string{"install.sh", "skill/checksums.txt", "skill/SKILL.md", "skill/references/tools.md"} {
				if !strings.Contains(text, "ARG "+GitHubAssetRoot+"/"+file+"\n") {
					t.Fatalf("wrong GitHub download source for %s", file)
				}
			}
			leftovers, _ := filepath.Glob(filepath.Join(environmentValue(env, "TMPDIR"), "proofbridge-install.*"))
			if len(leftovers) != 0 {
				t.Fatalf("bootstrap left temporary script: %v", leftovers)
			}
		})
	}
}

func TestGitHubInstallerIgnoresUntrustedHubForDownloads(t *testing.T) {
	var hubConnections atomic.Int32
	hub := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("Skill installation requested the private Hub")
	}))
	hub.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			hubConnections.Add(1)
		}
	}
	hub.StartTLS()
	defer hub.Close()
	i := newInstallation(t)
	env := githubTransportFixture(t)
	if output, err := runGitHubCommand(t, i, env, i.installer, "--hub", hub.URL, "--client", "codex"); err != nil {
		t.Fatalf("legacy Hub argument interfered with GitHub installation: %v: %s", err, output)
	}
	assertInstalledGitHubFiles(t, i, "codex")
	if hubConnections.Load() != 0 {
		t.Fatal("installer contacted the untrusted-CA Hub")
	}
	trace, err := os.ReadFile(environmentValue(env, "PB_TEST_CURL_LOG"))
	if err != nil || strings.Contains(string(trace), hub.URL) || strings.Count(string(trace), "CALL\n") != 3 {
		t.Fatal("legacy Hub was used instead of the fixed GitHub assets")
	}
}

func TestGitHubBootstrapDownloadFailureDoesNotExecutePartialScript(t *testing.T) {
	i := newInstallation(t)
	env := githubTransportFixture(t)
	env = append(env, "PB_TEST_FAIL_BOOTSTRAP=1")
	if output, err := runGitHubCommand(t, i, env, "-c", InstallCommand("codex")); err == nil {
		t.Fatalf("failed first-layer download returned success: %s", output)
	}
	if _, err := os.Stat(environmentValue(env, "PB_TEST_PARTIAL_EXECUTED")); !os.IsNotExist(err) {
		t.Fatal("partial downloaded script was executed")
	}
	leftovers, _ := filepath.Glob(filepath.Join(environmentValue(env, "TMPDIR"), "proofbridge-install.*"))
	if len(leftovers) != 0 {
		t.Fatal("failed bootstrap left a temporary script")
	}
}

func TestInstallerRejectsUnsafeAssetSources(t *testing.T) {
	for _, source := range []string{"http://lab.example/skill", "file:///tmp/skill", "https://user:secret@example.test/skill", "https://example.test/skill?token=secret", "https://example.test/skill#fragment", ""} {
		i := newInstallation(t)
		if output, err := runGitHubCommand(t, i, nil, i.installer, "--asset-base", source, "--client", "codex"); err == nil {
			t.Fatalf("unsafe resource source accepted: %s", output)
		}
		if _, err := os.Stat(i.target("codex", "user")); !os.IsNotExist(err) {
			t.Fatal("invalid source changed the installed skill")
		}
	}
}

func TestInstallerRejectsUntrustedAssetCertificateAndPreservesSkill(t *testing.T) {
	trustedFixture := httptest.NewServer(Handler(""))
	defer trustedFixture.Close()
	i := newInstallation(t)
	if output, err := i.run(t, trustedFixture.URL, "claude", "user"); err != nil {
		t.Fatalf("initial installation: %v: %s", err, output)
	}
	untrusted := httptest.NewTLSServer(Handler(""))
	defer untrusted.Close()
	if output, err := i.run(t, untrusted.URL, "claude", "user"); err == nil || !strings.Contains(output, "Could not download checksums.txt") {
		t.Fatalf("untrusted asset certificate was not rejected: %v: %s", err, output)
	}
	assertInstalledGitHubFiles(t, i, "claude")
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(i.target("claude", "user")), ".proofbridge-install.*"))
	if len(leftovers) != 0 {
		t.Fatal("untrusted source left staging data")
	}
}
