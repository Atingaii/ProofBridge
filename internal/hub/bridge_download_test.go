package hub

import (
	"bytes"
	"context"
	"debug/elf"
	"debug/macho"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestInstallerPlatformSelectionAndPreservation(t *testing.T) {
	s, _ := newAuthTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "https://hub.test/install.sh", nil)
	w := httptest.NewRecorder()
	s.handleInstallScript(w, r)
	for _, tc := range []struct {
		os, machine, arch string
		invalid           bool
	}{
		{"Linux", "x86_64", "amd64", false},
		{"Linux", "aarch64", "arm64", false},
		{"Linux", "arm64", "arm64", false},
		{"Darwin", "arm64", "arm64", false},
		{"Darwin", "x86_64", "amd64", false},
		{"Darwin", "arm64", "arm64", true},
		{"Linux", "riscv64", "", false},
		{"Linux", "aarch64", "arm64", true},
	} {
		t.Run(tc.os+"-"+tc.machine+map[bool]string{true: "-invalid"}[tc.invalid], func(t *testing.T) {
			dir := t.TempDir()
			fake := filepath.Join(dir, "fake")
			home := filepath.Join(dir, "home")
			installed := filepath.Join(home, ".local/bin/codex-bridge")
			for _, p := range []string{fake, filepath.Dir(installed)} {
				if err := os.MkdirAll(p, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(installed, []byte("previous-binary"), 0700); err != nil {
				t.Fatal(err)
			}
			writeExecutable(t, filepath.Join(fake, "uname"), "#!/bin/sh\ncase \"$1\" in -s) echo "+shellQuote(tc.os)+";; -m) echo "+shellQuote(tc.machine)+";; esac\n")
			payload := "cp /bin/true \"$out\""
			if tc.invalid {
				payload = "printf '\\177ELFbroken' >\"$out\""
			}
			writeExecutable(t, filepath.Join(fake, "curl"), `#!/bin/sh
case " $* " in *' --version '*) exit 0 ;; esac
out=''
url=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = '-o' ]; then out=$2; shift 2; continue; fi
  url=$1
  shift
done
printf '%s' "$url" >"$DOWNLOAD_LOG"
`+payload+"\n")
			log := filepath.Join(dir, "url")
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh")
			cmd.Stdin = strings.NewReader(w.Body.String())
			cmd.Env = append(os.Environ(), "HOME="+home, "PATH="+fake+":/usr/bin:/bin", "DOWNLOAD_LOG="+log)
			output, err := cmd.CombinedOutput()
			wantSuccess := tc.arch != "" && !tc.invalid
			if (err == nil) != wantSuccess {
				t.Fatalf("installer error=%v output=%s", err, output)
			}
			got, readErr := os.ReadFile(installed)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if !wantSuccess && string(got) != "previous-binary" {
				t.Fatal("failed installation replaced old binary")
			}
			if wantSuccess && string(got) == "previous-binary" {
				t.Fatal("successful installation retained old binary")
			}
			url, err := os.ReadFile(log)
			if tc.arch == "" {
				if !os.IsNotExist(err) {
					t.Fatal("unsupported platform attempted download")
				}
			} else if err != nil || string(url) != "https://hub.test/downloads/codex-bridge-"+map[string]string{"Linux": "linux", "Darwin": "darwin"}[tc.os]+"-"+tc.arch {
				t.Fatalf("download URL %q: %v", url, err)
			}
			leftovers, _ := filepath.Glob(installed + ".tmp.*")
			if len(leftovers) != 0 {
				t.Fatal("installer leaked temporary download")
			}
		})
	}
}

func TestBridgeArchitectureDownload(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), "codex-bridge")
			path := executable
			if runtime.GOOS != "linux" || runtime.GOARCH != arch {
				path = filepath.Join(filepath.Dir(executable), "downloads", "codex-bridge-linux-"+arch)
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			request := func(rangeHeader string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodGet, "/downloads/codex-bridge-linux-"+arch, nil)
				r.Header.Set("Range", rangeHeader)
				w := httptest.NewRecorder()
				serveBridgeBinary(w, r, executable, arch)
				return w
			}
			if got := request(""); got.Code != 404 {
				t.Fatalf("missing artifact status=%d", got.Code)
			}
			data := make([]byte, 64)
			copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
			binary.LittleEndian.PutUint16(data[16:], uint16(elf.ET_EXEC))
			machine := elf.EM_X86_64
			if arch == "arm64" {
				machine = elf.EM_AARCH64
			}
			binary.LittleEndian.PutUint16(data[18:], uint16(machine))
			binary.LittleEndian.PutUint32(data[20:], 1)
			binary.LittleEndian.PutUint16(data[52:], 64)
			if err := os.WriteFile(path, data, 0700); err != nil {
				t.Fatal(err)
			}
			if got := request(""); got.Code != 200 || !bytes.Equal(got.Body.Bytes(), data) || got.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("download status=%d body=%q", got.Code, got.Body.String())
			}
			if got := request("bytes=4-7"); got.Code != 206 || !bytes.Equal(got.Body.Bytes(), data[4:8]) {
				t.Fatalf("range status=%d body=%q", got.Code, got.Body.String())
			}
			binary.LittleEndian.PutUint16(data[18:], uint16(elf.EM_386))
			if err := os.WriteFile(path, data, 0700); err != nil {
				t.Fatal(err)
			}
			if got := request(""); got.Code != 500 {
				t.Fatalf("wrong architecture status=%d", got.Code)
			}
		})
	}
}

func TestExternalBridgeDownloadURLArchitectures(t *testing.T) {
	s, _ := newAuthTestServer(t)
	r := httptest.NewRequest(http.MethodGet, "https://hub.test/install.sh", nil)
	s.cfg.Hub.BridgeDownloadURL = "https://release.test/bridge-linux-amd64"
	if got := s.bridgeDownloadURLForArch(r, "amd64"); got != s.cfg.Hub.BridgeDownloadURL {
		t.Fatal(got)
	}
	if got := s.bridgeDownloadURLForArch(r, "arm64"); got != "https://hub.test/downloads/codex-bridge-linux-arm64" {
		t.Fatal(got)
	}
	s.cfg.Hub.BridgeDownloadURL = "https://release.test/bridge-linux-{arch}"
	if got := s.bridgeDownloadURLForArch(r, "arm64"); got != "https://release.test/bridge-linux-arm64" {
		t.Fatal(got)
	}
	for _, legacy := range []string{"https://release.test/bridge-linux-amd64", "https://release.test/bridge-linux-{arch}"} {
		s.cfg.Hub.BridgeDownloadURL = legacy
		if got := s.bridgeDownloadURLForPlatform(r, "darwin", "arm64"); got != "https://hub.test/downloads/codex-bridge-darwin-arm64" {
			t.Fatal(got)
		}
	}
	s.cfg.Hub.BridgeDownloadURL = "https://release.test/bridge-{os}-{arch}"
	for _, platform := range []string{"linux", "darwin"} {
		for _, arch := range []string{"amd64", "arm64"} {
			if got := s.bridgeDownloadURLForPlatform(r, platform, arch); got != "https://release.test/bridge-"+platform+"-"+arch {
				t.Fatal(got)
			}
		}
	}
}

func TestBridgeDarwinDownload(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), "codex-bridge")
			path := filepath.Join(filepath.Dir(executable), "downloads", "codex-bridge-darwin-"+arch)
			if runtime.GOOS == "darwin" && runtime.GOARCH == arch {
				path = executable
			}
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			request := func(rangeHeader string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodGet, "/downloads/codex-bridge-darwin-"+arch, nil)
				r.Header.Set("Range", rangeHeader)
				w := httptest.NewRecorder()
				serveBridgePlatformBinary(w, r, executable, "darwin", arch)
				return w
			}
			if got := request(""); got.Code != 404 {
				t.Fatalf("missing artifact status=%d", got.Code)
			}
			cpu := macho.CpuAmd64
			if arch == "arm64" {
				cpu = macho.CpuArm64
			}
			data := make([]byte, 32)
			binary.LittleEndian.PutUint32(data, macho.Magic64)
			binary.LittleEndian.PutUint32(data[4:], uint32(cpu))
			binary.LittleEndian.PutUint32(data[12:], uint32(macho.TypeExec))
			write := func() {
				if err := os.WriteFile(path, data, 0700); err != nil {
					t.Fatal(err)
				}
			}
			write()
			if got := request(""); got.Code != 200 || !bytes.Equal(got.Body.Bytes(), data) || got.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("download status=%d body=%q", got.Code, got.Body.String())
			}
			if got := request("bytes=4-7"); got.Code != 206 || !bytes.Equal(got.Body.Bytes(), data[4:8]) {
				t.Fatalf("range status=%d body=%q", got.Code, got.Body.String())
			}
			binary.LittleEndian.PutUint32(data[4:], uint32(macho.Cpu386))
			write()
			if got := request(""); got.Code != 500 {
				t.Fatalf("wrong CPU status=%d", got.Code)
			}
			binary.LittleEndian.PutUint32(data[4:], uint32(cpu))
			binary.LittleEndian.PutUint32(data[12:], uint32(macho.TypeDylib))
			write()
			if got := request(""); got.Code != 500 {
				t.Fatalf("library status=%d", got.Code)
			}
		})
	}
}
