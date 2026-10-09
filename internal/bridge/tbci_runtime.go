package bridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

const tbciProbeTimeout = 5 * time.Second

var tbciInstallMu sync.Mutex

type tbciRuntime struct {
	path       string
	capability protocol.TBCICapability
}

type tbciArtifact struct {
	data     []byte
	digest   string
	platform string
	version  string
}

func prepareTBCIRuntime(ctx context.Context, cfg *config.Config) *tbciRuntime {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	if cfg == nil {
		return unavailableTBCIRuntime("error", platform, "Bridge configuration is unavailable")
	}
	if !cfg.Bridge.TBCIEnabled {
		return unavailableTBCIRuntime("disabled", platform, "TBCI was disabled by the Bridge administrator")
	}

	if configured := strings.TrimSpace(cfg.Bridge.TBCIPath); configured != "" && !strings.EqualFold(configured, "auto") {
		path, err := exec.LookPath(configured)
		if err != nil {
			return unavailableTBCIRuntime("error", platform, "configured TBCI executable was not found")
		}
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		if err := probeTBCI(ctx, path); err != nil {
			return unavailableTBCIRuntime("error", platform, "configured TBCI executable failed its startup probe: "+boundedTBCIReason(err.Error()))
		}
		cfg.Bridge.TBCIPath = path
		return &tbciRuntime{
			path: path,
			capability: protocol.TBCICapability{
				Available: true,
				State:     "ready",
				Platform:  platform,
			},
		}
	}

	artifact := embeddedTBCIArtifact()
	if len(artifact.data) == 0 || artifact.digest == "" {
		return unavailableTBCIRuntime("unsupported", platform, "this Bridge build does not carry a TBCI artifact for "+platform)
	}
	path, err := installManagedTBCI(tbciManagedToolRoot(), artifact)
	if err != nil {
		return unavailableTBCIRuntime("error", platform, "managed TBCI installation failed: "+boundedTBCIReason(err.Error()))
	}
	if err := probeTBCI(ctx, path); err != nil {
		return unavailableTBCIRuntime("error", platform, "managed TBCI failed its startup probe: "+boundedTBCIReason(err.Error()))
	}
	cfg.Bridge.TBCIPath = path
	return &tbciRuntime{
		path: path,
		capability: protocol.TBCICapability{
			Available:      true,
			Managed:        true,
			State:          "ready",
			Platform:       artifact.platform,
			Version:        artifact.version,
			ArtifactSHA256: artifact.digest,
		},
	}
}

func unavailableTBCIRuntime(state, platform, reason string) *tbciRuntime {
	return &tbciRuntime{capability: protocol.TBCICapability{
		Available: false,
		State:     state,
		Platform:  platform,
		Reason:    boundedTBCIReason(reason),
	}}
}

func tbciManagedToolRoot() string {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		home = "."
	}
	return filepath.Join(home, ".codex-bridge", "tools", "tbci")
}

func installManagedTBCI(root string, artifact tbciArtifact) (string, error) {
	if len(artifact.data) == 0 {
		return "", errors.New("embedded artifact is empty")
	}
	digest := sha256.Sum256(artifact.data)
	actualDigest := hex.EncodeToString(digest[:])
	if !strings.EqualFold(actualDigest, artifact.digest) {
		return "", errors.New("embedded artifact digest does not match build metadata")
	}

	tbciInstallMu.Lock()
	defer tbciInstallMu.Unlock()

	dir := filepath.Join(root, strings.ToLower(artifact.digest))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(dir, "tbci")
	if ok, err := fileHasSHA256(target, artifact.digest); err != nil {
		return "", err
	} else if ok {
		if err := os.Chmod(target, 0o700); err != nil {
			return "", err
		}
		return target, nil
	}

	tmp, err := os.CreateTemp(dir, ".tbci-install-*")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}
	if err := tmp.Chmod(0o700); err != nil {
		cleanup()
		return "", err
	}
	if _, err := tmp.Write(artifact.data); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}
	if ok, err := fileHasSHA256(target, artifact.digest); err != nil {
		return "", err
	} else if !ok {
		return "", errors.New("installed artifact failed digest verification")
	}
	return target, nil
}

func fileHasSHA256(path, want string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, file)
	closeErr := file.Close()
	if copyErr != nil {
		return false, copyErr
	}
	if closeErr != nil {
		return false, closeErr
	}
	return strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), want), nil
}

func probeTBCI(parent context.Context, path string) error {
	ctx, cancel := context.WithTimeout(parent, tbciProbeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--help")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return errors.New("probe timed out")
		}
		return fmt.Errorf("probe command: %w", err)
	}
	return nil
}
