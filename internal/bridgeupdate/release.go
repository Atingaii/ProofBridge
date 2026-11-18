// Package bridgeupdate manages signed endpoint releases separately for each
// linked workspace. The installed bootstrap and other endpoints stay live.
package bridgeupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

const maxArtifact = 256 << 20

var validDigest = regexp.MustCompile("^[a-f0-9]{64}$")

type Identity struct {
	Version   string `json:"version"`
	Sequence  int64  `json:"sequence"`
	Platform  string `json:"platform"`
	PublicKey string `json:"publicKey"`
}

type manifest struct {
	Schema   int    `json:"schema"`
	Sequence int64  `json:"sequence"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Size     int64  `json:"size"`
	Digest   string `json:"sha256"`
}

type signed struct {
	Payload   []byte `json:"payload"`
	Signature []byte `json:"signature"`
}

type localState struct {
	Floor    int64   `json:"floor"`
	Active   *signed `json:"active,omitempty"`
	Previous *signed `json:"previous,omitempty"`
}

type channel struct {
	root, base, platform string
	key                  ed25519.PublicKey
	client               *http.Client
}

func newChannel(home, hub, machine, cwd string, key ed25519.PublicKey) (*channel, error) {
	u, err := url.Parse(hub)
	if err != nil || u.Host == "" {
		return nil, errors.New("invalid managed release origin")
	}
	switch u.Scheme {
	case "ws":
		u.Scheme = "http"
	case "wss":
		u.Scheme = "https"
	case "http", "https":
	default:
		return nil, errors.New("invalid managed release scheme")
	}
	// Enrollment/connection parameters never travel with release downloads.
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.User = nil
	if !filepath.IsAbs(home) {
		return nil, errors.New("managed release storage requires an absolute home")
	}
	if len(key) != ed25519.PublicKeySize {
		return nil, errors.New("invalid managed release public key")
	}
	base := strings.TrimRight(u.String(), "/") + "/downloads/releases/"
	id := sha256.Sum256([]byte(base + "\x00" + machine + "\x00" + cwd + "\x00" + string(key)))
	root := filepath.Join(home, ".codex-bridge", "updates", hex.EncodeToString(id[:16]))
	return &channel{root: root, base: base, platform: runtime.GOOS + "-" + runtime.GOARCH, key: key, client: &http.Client{
		Timeout:       5 * time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

// PublishedArtifact selects the ordinary installer download from the immutable
// files in the operator's local publication directory. Initial installation
// relies on the trusted installer transport. Subsequent releases are verified
// at the endpoint using its pinned key.
func PublishedArtifact(dir, platform string) (string, error) {
	f, err := os.Open(filepath.Join(dir, platform+".json"))
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (16<<10)+1))
	if err != nil {
		return "", err
	}
	var s signed
	var m manifest
	if len(raw) > 16<<10 || json.Unmarshal(raw, &s) != nil || len(s.Signature) != ed25519.SignatureSize || json.Unmarshal(s.Payload, &m) != nil ||
		m.Schema != 1 || m.Platform != platform || m.Sequence <= 0 || !validDigest.MatchString(m.Digest) {
		return "", errors.New("invalid published Bridge manifest")
	}
	return filepath.Join(dir, m.Digest+".bin"), nil
}

func (c *channel) verify(s signed) (manifest, error) {
	var m manifest
	if !ed25519.Verify(c.key, s.Payload, s.Signature) {
		return m, errors.New("invalid Bridge release signature")
	}
	if err := json.Unmarshal(s.Payload, &m); err != nil {
		return m, err
	}
	if m.Schema != 1 || m.Sequence <= 0 || m.Platform != c.platform || m.Version == "" || len(m.Version) > 128 || m.Size <= 0 || m.Size > maxArtifact || !validDigest.MatchString(m.Digest) {
		return m, errors.New("invalid Bridge release metadata")
	}
	return m, nil
}

func (c *channel) get(ctx context.Context, name string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+name, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound && strings.HasSuffix(name, ".json") {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("Bridge release %s returned HTTP %d", name, resp.StatusCode)
	}
	return resp.Body, nil
}

func (c *channel) statePath() string        { return filepath.Join(c.root, "state.json") }
func (c *channel) binary(m manifest) string { return filepath.Join(c.root, m.Digest) }

func (c *channel) readState() (localState, error) {
	var st localState
	raw, err := os.ReadFile(c.statePath())
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	if err = json.Unmarshal(raw, &st); err != nil {
		return st, err
	}
	if st.Floor < 0 {
		return st, errors.New("invalid locally recorded release sequence")
	}
	for _, release := range []*signed{st.Active, st.Previous} {
		if release == nil {
			continue
		}
		m, err := c.verify(*release)
		if err != nil {
			return st, err
		}
		if m.Sequence > st.Floor {
			return st, errors.New("active release exceeds durable sequence floor")
		}
	}
	return st, nil
}

// Only digest-named executables belong to the managed cache. Retain one
// confirmed fallback; the bootstrap can always resume a first failed update.
func (c *channel) prune(st localState) {
	keep := make(map[string]bool, 2)
	for _, r := range []*signed{st.Active, st.Previous} {
		if r != nil {
			if m, err := c.verify(*r); err == nil {
				keep[m.Digest] = true
			}
		}
	}
	files, err := os.ReadDir(c.root)
	if err != nil {
		return
	}
	for _, f := range files {
		if validDigest.MatchString(f.Name()) && !keep[f.Name()] {
			_ = os.Remove(filepath.Join(c.root, f.Name()))
		}
	}
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (c *channel) save(st localState) error {
	f, err := os.CreateTemp(c.root, ".state-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = json.NewEncoder(f).Encode(st); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), c.statePath()); err != nil {
		return err
	}
	return syncDir(c.root)
}

func verifyFile(path string, m manifest) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != m.Size {
		return errors.New("invalid staged binary size or type")
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err = io.Copy(sum, io.LimitReader(f, m.Size+1)); err != nil {
		return err
	}
	if hex.EncodeToString(sum.Sum(nil)) != m.Digest {
		return errors.New("Bridge release checksum mismatch")
	}
	return nil
}

func (c *channel) stage(ctx context.Context, floor int64) (*signed, error) {
	r, err := c.get(ctx, c.platform+".json")
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(r, (16<<10)+1))
	r.Close()
	if err != nil {
		return nil, err
	}
	if len(raw) > 16<<10 {
		return nil, errors.New("oversized Bridge release manifest")
	}
	var s signed
	if err = json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	m, err := c.verify(s)
	if err != nil {
		return nil, err
	}
	if m.Sequence <= floor {
		return nil, nil
	}
	path := c.binary(m)
	if verifyFile(path, m) == nil {
		return &s, nil
	}
	r, err = c.get(ctx, m.Digest+".bin")
	if err != nil {
		return nil, err
	}
	defer r.Close()
	f, err := os.CreateTemp(c.root, ".staging-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, sum), io.LimitReader(r, m.Size+1))
	if err != nil {
		return nil, err
	}
	if n != m.Size || hex.EncodeToString(sum.Sum(nil)) != m.Digest {
		return nil, errors.New("Bridge release size or checksum mismatch")
	}
	if err = f.Chmod(0700); err != nil {
		return nil, err
	}
	if err = f.Sync(); err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return nil, err
	}
	if err = syncDir(c.root); err != nil {
		return nil, err
	}
	return &s, nil
}

func checkBuild(ctx context.Context, path string, want Identity) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// The caller has already verified the signing key, binary digest and size.
	cmd := exec.CommandContext(ctx, path, "release-info")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("release startup check: %w", err)
	}
	var actual Identity
	if err := json.Unmarshal(out.Bytes(), &actual); err != nil || actual != want {
		return errors.New("candidate build identity does not match signed release and installed key")
	}
	return nil
}
