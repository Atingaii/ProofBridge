package bridgeupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func signTest(t *testing.T, k ed25519.PrivateKey, m manifest) signed {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return signed{Payload: raw, Signature: ed25519.Sign(k, raw)}
}
func fixtureManifest(data []byte, platform string) manifest {
	h := sha256.Sum256(data)
	return manifest{Schema: 1, Sequence: 2, Version: "v2", Platform: platform, Size: int64(len(data)), Digest: hex.EncodeToString(h[:])}
}

func TestManagedReleaseTrustAndReplay(t *testing.T) {
	key, k, _ := ed25519.GenerateKey(rand.Reader)
	for _, name := range []string{"signature", "digest", "short", "long", "platform", "replay", "unsigned", "redirect", "valid"} {
		t.Run(name, func(t *testing.T) {
			var body, artifact []byte
			fetched := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ".json") {
					w.Write(body)
					return
				}
				fetched = true
				if name == "redirect" {
					http.Redirect(w, r, "http://invalid.example/release", http.StatusTemporaryRedirect)
					return
				}
				w.Write(artifact)
			}))
			defer server.Close()
			c, err := newChannel(t.TempDir(), server.URL, "id", "/workspace", key)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.MkdirAll(c.root, 0700); err != nil {
				t.Fatal(err)
			}
			artifact = []byte("test executable")
			m := fixtureManifest(artifact, c.platform)
			switch name {
			case "platform":
				m.Platform = "other-amd64"
			case "digest":
				artifact[0] = 'B'
			case "short":
				artifact = artifact[1:]
			case "long":
				artifact = append(artifact, '!')
			}
			receipt := signTest(t, k, m)
			if name == "signature" {
				receipt.Signature[0] ^= 1
			}
			if name == "unsigned" {
				receipt.Signature = nil
			}
			body, _ = json.Marshal(receipt)
			floor := int64(1)
			if name == "replay" {
				floor = 2
			}
			next, err := c.stage(context.Background(), floor)
			switch name {
			case "valid":
				if err != nil || next == nil {
					t.Fatalf("valid rejected: %v", err)
				}
				if err := verifyFile(c.binary(m), m); err != nil {
					t.Fatal(err)
				}
			case "replay":
				if err != nil || next != nil || fetched {
					t.Fatal("replay was fetched")
				}
			default:
				if err == nil || next != nil {
					t.Fatal("invalid update staged")
				}
				if _, err = os.Stat(c.binary(m)); !os.IsNotExist(err) {
					t.Fatal("invalid artifact persisted")
				}
			}
		})
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition was not observed")
}

func TestSupervisorIdleTrialRollbackAndSeparateEndpoints(t *testing.T) {
	for _, bad := range []string{"", "bad-start", "bad-register", "bad-build", "crash-after-hello", "crash-after-live"} {
		t.Run("release-"+bad, func(t *testing.T) {
			key, priv, _ := ed25519.GenerateKey(rand.Reader)
			home := t.TempDir()
			activity := filepath.Join(home, "busy")
			log := filepath.Join(home, "workers")
			var signedData, artifact []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, ".json") {
					w.Write(signedData)
				} else {
					w.Write(artifact)
				}
			}))
			defer server.Close()
			ch, err := newChannel(home, server.URL, "machineA", "/ws", key)
			if err != nil {
				t.Fatal(err)
			}
			other, err := newChannel(home, server.URL, "machineB", "/sibling", key)
			if err != nil || ch.root == other.root {
				t.Fatal("shared endpoint state")
			}
			baseline := filepath.Join(home, "bootstrap")
			setup := func(version string, seq int, bad string) []byte {
				id := Identity{Version: version, Platform: ch.platform, Sequence: int64(seq), PublicKey: base64.StdEncoding.EncodeToString(key)}
				meta, _ := json.Marshal(id)
				script := "#!/bin/sh\nset -eu\nif [ \"$1\" = release-info ]; then printf '%s\\n' '" + string(meta) + "'; exit 0; fi\n[ \"$1\" = managed-connect ]\n"
				script += fmt.Sprintf("echo 'start %s' >> '%s'\n", version, log)
				if bad == "bad-start" {
					script += "exit 9\n"
				}
				if bad == "bad-register" {
					script += "exec sleep 10\n"
				}
				script += "printf R >&4\n[ \"$(dd bs=1 count=1 <&3 2>/dev/null)\" = G ]\n"
				if bad == "crash-after-hello" {
					script += "exit 8\n"
				}
				script += "printf S >&4\n[ \"$(dd bs=1 count=1 <&3 2>/dev/null)\" = A ]\n"
				if bad == "crash-after-live" {
					script += "exit 7\n"
				}
				script += fmt.Sprintf("echo 'live %s' >> '%s'\n", version, log)
				script += "while [ \"$(dd bs=1 count=1 <&3 2>/dev/null)\" = U ]; do\n"
				script += fmt.Sprintf("  if [ -f '%s' ]; then echo busy >> '%s'; else exit 75; fi\n", activity, log)
				return []byte(script + "done\n")
			}
			if err = os.WriteFile(baseline, setup("v1", 1, ""), 0700); err != nil {
				t.Fatal(err)
			}
			ver := "v2"
			if bad == "bad-build" {
				ver = "wrong-version"
			}
			artifact = setup(ver, 2, bad)
			m := fixtureManifest(artifact, ch.platform)
			signedData, _ = json.Marshal(signTest(t, priv, m))
			s := &supervisor{channel: ch, identity: Identity{Version: "v1", Sequence: 1, PublicKey: base64.StdEncoding.EncodeToString(key), Platform: ch.platform}, bootstrap: baseline, instance: "test", poll: 20 * time.Millisecond, timeout: 250 * time.Millisecond}
			if err = os.WriteFile(activity, nil, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- s.run(ctx) }()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Errorf("unexpected supervisor exit: %v", err)
					}
				case <-time.After(3 * time.Second):
					t.Error("supervisor did not exit")
				}
			}()
			logText := func() string { b, _ := os.ReadFile(log); return string(b) }
			if bad == "bad-build" {
				waitFor(t, func() bool { st, _ := s.readState(); return st.Floor == 2 })
				if strings.Contains(logText(), "start wrong-version") || strings.Count(logText(), "live v1") != 1 {
					t.Fatal(logText())
				}
				return
			}
			waitFor(t, func() bool { return strings.Contains(logText(), "busy") })
			if strings.Contains(logText(), "start v2") {
				t.Fatal("busy endpoint interrupted")
			}
			if err = os.Remove(activity); err != nil {
				t.Fatal(err)
			}
			if bad != "" {
				waitFor(t, func() bool { return strings.Count(logText(), "live v1") == 2 })
				st, _ := s.readState()
				if st.Floor != 2 || st.Active != nil || strings.Count(logText(), "start v2") != 1 {
					t.Fatalf("rollback failed: %+v %s", st, logText())
				}
			} else {
				waitFor(t, func() bool { return strings.Contains(logText(), "live v2") })
				st, err := s.readState()
				if err != nil || st.Active == nil || st.Floor != 2 {
					t.Fatalf("release not persisted: %+v %v", st, err)
				}
				receipt, _ := s.verify(*st.Active)
				if residue, err := verifyPathIdentity(s.binary(receipt)); err != nil || residue != m.Digest {
					t.Fatal("installed binary differed")
				}
			}
			if _, err = os.Stat(other.statePath()); !os.IsNotExist(err) {
				t.Fatal("sibling endpoint was modified")
			}
		})
	}
}

func verifyPathIdentity(path string) (string, error) {
	b, err := os.ReadFile(path)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), err
}
