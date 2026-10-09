package bridge

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

type blockingUserInputPipe struct {
	started chan struct{}
	closed  chan struct{}
	release chan struct{}
	once    sync.Once
}

type shortUserInputPipe struct{}

func (shortUserInputPipe) Write(frame []byte) (int, error) { return len(frame) - 1, nil }
func (shortUserInputPipe) Close() error                    { return nil }

func TestCodexUserInputShortWriteIsNotDelivered(t *testing.T) {
	client := &appServerClient{stdin: shortUserInputPipe{}, stop: make(chan struct{})}
	if err := client.respondUserInput(context.Background(), 1, nil); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short native write = %v", err)
	}
}

func TestCodexUserInputResolvedLeavesChildAvailableForOrdinaryRPC(t *testing.T) {
	cmd := exec.Command("sh", "-c", "IFS= read -r first; printf '%s\\n' \"$first\"; IFS= read -r second; printf '%s\\n' \"$second\"")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdin.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	client := &appServerClient{cmd: cmd, stdin: stdin, stop: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	if err := client.respondUserInput(ctx, 1, map[string]any{"answers": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	cancel() // Models the native resolved notification after the complete write.
	reader := bufio.NewReader(stdout)
	first, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(first, `"id":1`) {
		t.Fatalf("first frame = %q, %v", first, err)
	}
	if err := client.respond(2, map[string]any{"ok": true}); err != nil {
		t.Fatalf("ordinary RPC after resolved: %v", err)
	}
	second, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(second, `"id":2`) {
		t.Fatalf("second frame = %q, %v", second, err)
	}
}

func (p *blockingUserInputPipe) Write(frame []byte) (int, error) {
	close(p.started)
	if p.release == nil {
		<-p.closed
		return 0, io.ErrClosedPipe
	}
	select {
	case <-p.release:
		return len(frame), nil
	case <-p.closed:
		return 0, io.ErrClosedPipe
	}
}

func (p *blockingUserInputPipe) Close() error {
	p.once.Do(func() { close(p.closed) })
	return nil
}

func TestCodexUserInputResolvedDuringWriteKeepsTransport(t *testing.T) {
	pipe := &blockingUserInputPipe{started: make(chan struct{}), closed: make(chan struct{}), release: make(chan struct{})}
	client := &appServerClient{stdin: pipe, stop: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.respondUserInput(ctx, 1, map[string]any{"answers": map[string]any{}}) }()
	select {
	case <-pipe.started:
	case <-time.After(time.Second):
		t.Fatal("native pipe write did not start")
	}
	cancel()
	close(pipe.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("blocked write result = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("native pipe write outlived request cancellation")
	}
	select {
	case <-pipe.closed:
		t.Fatal("normal resolved closed a healthy native transport")
	default:
	}
}

func TestCodexUserInputBlockedPipeTimesOut(t *testing.T) {
	pipe := &blockingUserInputPipe{started: make(chan struct{}), closed: make(chan struct{})}
	client := &appServerClient{stdin: pipe, stop: make(chan struct{}), userInputWriteTimeout: 30 * time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- client.respondUserInput(context.Background(), 1, nil) }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("blocked transport result = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked transport did not converge")
	}
	select {
	case <-pipe.closed:
	default:
		t.Fatal("timed-out transport was not closed")
	}
}

func TestCodexUserInputWaitingForWriterLockIsCancelable(t *testing.T) {
	pipe := &blockingUserInputPipe{started: make(chan struct{}), closed: make(chan struct{})}
	client := &appServerClient{stdin: pipe, stop: make(chan struct{})}
	client.writeMu.Lock()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.respondUserInput(ctx, 1, nil) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("waiting write result = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("answer stayed blocked behind another writer's lock")
	}
	client.writeMu.Unlock()
	select {
	case <-pipe.started:
		t.Fatal("canceled answer wrote after lock became available")
	default:
	}
	select {
	case <-pipe.closed:
		t.Fatal("canceling before Write closed a healthy native transport")
	default:
	}
}
