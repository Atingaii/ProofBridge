package bridgeupdate

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const DrainedExit = 75

// ChildControl uses inherited pipes, outside any workspace, to keep a trial
// behind its registration fence until the supervisor has committed the binary.
type ChildControl struct {
	command, notice      *os.File
	requests             chan struct{}
	done                 chan struct{}
	identified, admitted sync.Once
	err                  error
}

func OpenChild() *ChildControl {
	unix.CloseOnExec(3)
	unix.CloseOnExec(4)
	return &ChildControl{command: os.NewFile(3, "release-control"), notice: os.NewFile(4, "release-readiness"), requests: make(chan struct{}, 1), done: make(chan struct{})}
}

func (c *ChildControl) exchange(request, want byte) error {
	if _, err := c.notice.Write([]byte{request}); err != nil {
		return err
	}
	var reply [1]byte
	if _, err := io.ReadFull(c.command, reply[:]); err != nil {
		return err
	}
	if reply[0] != want {
		return errors.New("invalid release supervisor response")
	}
	return nil
}

func (c *ChildControl) Registered() error {
	c.identified.Do(func() { c.err = c.exchange('R', 'G') })
	return c.err
}

func (c *ChildControl) Confirmed() error {
	c.admitted.Do(func() {
		c.err = c.exchange('S', 'A')
		if c.err != nil {
			return
		}
		go func() {
			defer close(c.done)
			defer c.command.Close()
			defer c.notice.Close()
			var b [1]byte
			for {
				if _, err := io.ReadFull(c.command, b[:]); err != nil {
					return
				}
				if b[0] == 'U' {
					select {
					case c.requests <- struct{}{}:
					default:
					}
				}
			}
		}()
	})
	return c.err
}
func (c *ChildControl) Requests() <-chan struct{} { return c.requests }
func (c *ChildControl) Done() <-chan struct{}     { return c.done }

type supervisor struct {
	*channel
	identity      Identity
	bootstrap     string
	args          []string
	instance      string
	poll, timeout time.Duration
}

func Run(ctx context.Context, id Identity, hub, machine, cwd string, args []string) error {
	key, err := base64.StdEncoding.DecodeString(id.PublicKey)
	if err != nil || id.Sequence <= 0 {
		return errors.New("invalid installed managed release identity")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	ch, err := newChannel(home, hub, machine, cwd, key)
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	s := &supervisor{channel: ch, identity: id, bootstrap: exe, args: args, instance: "bin_" + rand.Text(), poll: time.Minute, timeout: 2 * time.Minute}
	return s.run(ctx)
}

func (s *supervisor) run(ctx context.Context) error {
	if err := os.MkdirAll(s.root, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(s.root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return errors.New("managed release directory must be private to its owner")
	}
	lock, err := os.OpenFile(filepath.Join(s.root, "supervisor.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("managed endpoint already supervised: %w", err)
	}
	st, err := s.readState()
	if err != nil {
		return err
	}
	if st.Floor < s.identity.Sequence {
		st.Floor = s.identity.Sequence
		if err = s.save(st); err != nil {
			return err
		}
	}
	rollback := func(cause error) error {
		slog.Warn("[bridge] restoring previous confirmed release", "error", cause)
		st.Active = st.Previous
		st.Previous = nil
		return s.save(st)
	}
	var trial *signed
	for {
		current := s.bootstrap
		if st.Active != nil {
			m, err := s.verify(*st.Active)
			if err != nil {
				return err
			}
			if m.Sequence > s.identity.Sequence {
				current = s.binary(m)
				if err = verifyFile(current, m); err != nil {
					if err = rollback(err); err != nil {
						return err
					}
					continue
				}
			}
		}
		if trial != nil {
			m, err := s.verify(*trial)
			if err != nil {
				return err
			}
			current = s.binary(m)
		}
		previous := st
		next, registered, err := s.worker(ctx, current, &st, trial)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if trial != nil && !registered {
			slog.Warn("[bridge] release trial failed; resuming previous endpoint", "error", err, "failed_sequence", st.Floor)
			st = previous
			if err = s.save(st); err != nil {
				return err
			}
			s.prune(st)
			trial = nil
			continue
		}
		if next == nil {
			if !registered && st.Active != nil && current != s.bootstrap {
				if err = rollback(err); err != nil {
					return err
				}
				continue
			}
			return err
		}
		m, err := s.verify(*next)
		if err != nil {
			return err
		}
		st.Floor = m.Sequence
		// Persist a rejected/crashed trial's floor before executing it.
		if err = s.save(st); err != nil {
			return err
		}
		trial = next
	}
}

type workerProcess struct {
	command *os.File
	cmd     *exec.Cmd
	ready   chan byte
	exit    chan error
}

func (s *supervisor) start(ctx context.Context, path string) (*workerProcess, error) {
	commandR, commandW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	defer commandR.Close()
	noticeR, noticeW, err := os.Pipe()
	if err != nil {
		commandW.Close()
		return nil, err
	}
	defer noticeW.Close()
	args := append([]string{"managed-connect", s.instance}, s.args...)
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.ExtraFiles = []*os.File{commandR, noticeW}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Start(); err != nil {
		commandW.Close()
		noticeR.Close()
		return nil, err
	}
	p := &workerProcess{cmd: cmd, command: commandW, ready: make(chan byte, 2), exit: make(chan error, 1)}
	go func() {
		defer noticeR.Close()
		var b [1]byte
		for _, want := range []byte{'R', 'S'} {
			if _, err := io.ReadFull(noticeR, b[:]); err != nil || b[0] != want {
				return
			}
			p.ready <- b[0]
		}
	}()
	go func() { p.exit <- cmd.Wait() }()
	return p, nil
}

type staged struct {
	s       *signed
	err     error
	invalid bool
}

func (s *supervisor) candidate(ctx context.Context, floor int64) staged {
	signed, err := s.stage(ctx, floor)
	if err != nil || signed == nil {
		return staged{err: err}
	}
	m, _ := s.verify(*signed)
	expected := Identity{Version: m.Version, Sequence: m.Sequence, Platform: m.Platform, PublicKey: s.identity.PublicKey}
	if err = checkBuild(ctx, s.binary(m), expected); err != nil {
		return staged{s: signed, err: err, invalid: true}
	}
	return staged{s: signed}
}

func (s *supervisor) worker(ctx context.Context, path string, st *localState, trial *signed) (next *signed, registered bool, err error) {
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	p, err := s.start(work, path)
	if err != nil {
		return nil, false, err
	}
	// Every return other than observing process exit first cancels and joins it.
	joined := false
	defer func() {
		p.command.Close()
		if !joined {
			cancel()
			<-p.exit
		}
	}()
	var deadline <-chan time.Time
	if trial != nil {
		timer := time.NewTimer(s.timeout)
		defer timer.Stop()
		deadline = timer.C
	}
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()
	fetched := make(chan staged, 1)
	var candidate *signed
	fetching := false
	poll := func() {
		if !registered {
			return
		}
		if candidate != nil {
			_, _ = p.command.Write([]byte{'U'})
			return
		}
		if !fetching {
			fetching = true
			floor := st.Floor
			go func() { fetched <- s.candidate(work, floor) }()
		}
	}
	activatedAt := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return nil, registered, ctx.Err()
		case <-deadline:
			return nil, false, errors.New("release trial did not register by the startup deadline")
		case signal := <-p.ready:
			if signal == 'R' {
				if _, err = p.command.Write([]byte{'G'}); err != nil {
					return nil, false, err
				}
				continue
			}
			if trial != nil {
				confirmed := *st
				confirmed.Previous = st.Active
				confirmed.Active = trial
				if err = s.save(confirmed); err != nil {
					return nil, false, err
				}
				*st = confirmed
			}
			if _, err = p.command.Write([]byte{'A'}); err != nil {
				// The worker may have exited immediately after its greeting.
				return nil, false, err
			}
			registered = true
			activatedAt = time.Now()
			s.prune(*st)
			deadline = nil
			poll()
		case err = <-p.exit:
			joined = true
			var exitErr *exec.ExitError
			if candidate != nil && errors.As(err, &exitErr) && exitErr.ExitCode() == DrainedExit {
				return candidate, registered, nil
			}
			if trial != nil && registered && err != nil && time.Since(activatedAt) < 15*time.Second {
				// The worker exited on its own during startup. Keep its old
				// confirmed executable and require a later signed publication.
				return nil, false, err
			}
			return nil, registered, err
		case <-ticker.C:
			poll()
		case result := <-fetched:
			fetching = false
			if result.err != nil {
				slog.Warn("[bridge] release check", "error", result.err)
				if result.invalid && work.Err() == nil {
					m, _ := s.verify(*result.s)
					rejected := *st
					rejected.Floor = m.Sequence
					if err = s.save(rejected); err != nil {
						slog.Error("[bridge] persist rejected release", "error", err)
					} else {
						*st = rejected
						s.prune(*st)
					}
				}
				continue
			}
			if result.s != nil {
				candidate = result.s
				m, _ := s.verify(*candidate)
				slog.Info("[bridge] release staged; waiting for idle handoff", "version", m.Version, "sequence", m.Sequence)
				poll()
			}
		}
	}
}
