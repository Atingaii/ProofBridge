package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tencent/codex-bridge/internal/config"
)

func TestInstallManagedTBCIInstallsReusesAndRepairs(t *testing.T) {
	data := []byte("managed-tbci-test-artifact")
	sum := sha256.Sum256(data)
	artifact := tbciArtifact{
		data:     data,
		digest:   hex.EncodeToString(sum[:]),
		platform: runtime.GOOS + "/" + runtime.GOARCH,
		version:  "test",
	}
	root := t.TempDir()
	path, err := installManagedTBCI(root, artifact)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(data) {
		t.Fatalf("installed bytes = %q, err = %v", got, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("installed mode = %v, err = %v", info, err)
	}
	firstInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := installManagedTBCI(root, artifact)
	if err != nil || reused != path {
		t.Fatalf("reuse path = %q, err = %v", reused, err)
	}
	secondInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !secondInfo.ModTime().Equal(firstInfo.ModTime()) {
		t.Fatalf("valid artifact was rewritten: %v -> %v", firstInfo.ModTime(), secondInfo.ModTime())
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o700); err != nil {
		t.Fatal(err)
	}
	repaired, err := installManagedTBCI(root, artifact)
	if err != nil || repaired != path {
		t.Fatalf("repair path = %q, err = %v", repaired, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(data) {
		t.Fatalf("repaired bytes = %q, err = %v", got, err)
	}
}

func TestInstallManagedTBCIRejectsDigestMismatch(t *testing.T) {
	_, err := installManagedTBCI(t.TempDir(), tbciArtifact{data: []byte("x"), digest: "00"})
	if err == nil {
		t.Fatal("digest mismatch was accepted")
	}
}

func TestPrepareTBCIRuntimeHonorsAdministrativeDisable(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.TBCIEnabled = false
	runtime := prepareTBCIRuntime(context.Background(), &cfg)
	if runtime.capability.Available || runtime.capability.State != "disabled" {
		t.Fatalf("disabled runtime = %#v", runtime)
	}
}

func TestPrepareTBCIRuntimeAcceptsExplicitExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test executable invocation differs on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "tbci")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.TBCIPath = path
	prepared := prepareTBCIRuntime(context.Background(), &cfg)
	if !prepared.capability.Available || prepared.capability.Managed || prepared.path != path {
		t.Fatalf("external runtime = %#v", prepared)
	}
}

func TestPrepareTBCIRuntimeInstallsAndReportsEmbeddedArtifact(t *testing.T) {
	artifact := embeddedTBCIArtifact()
	if len(artifact.data) == 0 {
		t.Skip("platform has no managed artifact")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := config.Default()
	cfg.Bridge.TBCIPath = ""

	prepared := prepareTBCIRuntime(context.Background(), &cfg)
	if !prepared.capability.Available || !prepared.capability.Managed || prepared.capability.State != "ready" {
		t.Fatalf("managed runtime = %#v", prepared)
	}
	if prepared.capability.ArtifactSHA256 != artifact.digest || prepared.capability.Platform != artifact.platform {
		t.Fatalf("managed capability = %#v", prepared.capability)
	}
	wantPath := filepath.Join(home, ".codex-bridge", "tools", "tbci", artifact.digest, "tbci")
	if prepared.path != wantPath || cfg.Bridge.TBCIPath != wantPath {
		t.Fatalf("managed path = %q, config path = %q, want %q", prepared.path, cfg.Bridge.TBCIPath, wantPath)
	}
	if ok, err := fileHasSHA256(prepared.path, artifact.digest); err != nil || !ok {
		t.Fatalf("installed digest ok = %v, err = %v", ok, err)
	}
}

func TestEmbeddedTBCIArtifactDigest(t *testing.T) {
	artifact := embeddedTBCIArtifact()
	if len(artifact.data) == 0 {
		t.Skip("platform has no managed artifact")
	}
	sum := sha256.Sum256(artifact.data)
	if got := hex.EncodeToString(sum[:]); got != artifact.digest {
		t.Fatalf("embedded digest = %s, want %s", got, artifact.digest)
	}
}
