//go:build linux

package platform

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

//go:embed compact_linux_helper.py
var compactLinuxHelper string

type compactLinuxCloser struct {
	*compactProcessCloser
	send   func(any) error
	states chan map[string]any
	done   <-chan struct{}
}

func (c *compactLinuxCloser) ShowEntry() error {
	if c.closed.Load() {
		return errCompactClosed
	}
	select {
	case <-c.done:
		return errCompactClosed
	default:
	}
	if err := c.send(map[string]any{"action": "show_entry"}); err != nil {
		return fmt.Errorf("便签入口无法显示，入口可能已关闭: %w", err)
	}
	return nil
}

func (c *compactLinuxCloser) Show() error {
	if c.closed.Load() {
		return errCompactClosed
	}
	select {
	case <-c.done:
		return errCompactClosed
	default:
	}
	if err := c.send(map[string]any{"action": "show"}); err != nil {
		return fmt.Errorf("原生便签无法重新显示，入口可能已关闭: %w", err)
	}
	return nil
}

func startCompact(ctx context.Context, compactURL, fullURL string) (io.Closer, error) {
	client, err := newCompactClient(compactURL)
	if err != nil {
		return nil, err
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		return nil, fmt.Errorf("Linux原生便签需要python3及PyGObject/GTK3: %w", err)
	}
	if os.Getenv("DISPLAY") == "" {
		return nil, fmt.Errorf("Linux原生便签需要可访问的X11或XWayland DISPLAY；不会回退到浏览器")
	}
	childCtx, cancel := context.WithCancel(ctx)
	cmd := exec.Command(python, "-u", "-c", compactLinuxHelper)
	cmd.Env = append(os.Environ(), "GDK_BACKEND=x11")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr := &lockedCompactBuffer{}
	cmd.Stderr = stderr
	if err = cmd.Start(); err != nil {
		cancel()
		_ = stdin.Close()
		return nil, err
	}
	var sendMu sync.Mutex
	send := func(v any) error { sendMu.Lock(); defer sendMu.Unlock(); return json.NewEncoder(stdin).Encode(v) }
	done := make(chan struct{})
	var waitErr error
	go func() { waitErr = cmd.Wait(); cancel(); close(done) }()
	ready := make(chan struct{}, 1)
	states := make(chan map[string]any, 8)
	requests := make(chan compactRequest, 32)
	go func() {
		defer close(requests)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 4<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "READY GTK3-X11" {
				select {
				case ready <- struct{}{}:
				default:
				}
				continue
			}
			var r compactRequest
			if json.Unmarshal([]byte(line), &r) != nil {
				continue
			}
			if r.Action == "test_state" && os.Getenv("PROJNOTE_TEST_COMPACT_NATIVE") == "1" {
				select {
				case states <- r.Payload:
				default:
				}
				continue
			}
			select {
			case requests <- r:
			case <-childCtx.Done():
				return
			}
		}
	}()
	go func() {
		for r := range requests {
			if r.Action == "open_full" {
				if e := OpenBrowser(fullURL); e != nil {
					_ = send(compactReply{Seq: r.Seq, Action: r.Action, Error: e.Error(), Status: 503})
				}
				continue
			}
			reply := client.dispatch(childCtx, r)
			if e := send(reply); e != nil {
				select {
				case <-done:
					return
				default:
					slog.Warn("native compact response failed", "error", e)
				}
			}
		}
	}()
	closer := &compactLinuxCloser{send: send, states: states, done: done, compactProcessCloser: &compactProcessCloser{closeFn: func() error {
		select {
		case <-done:
			_ = stdin.Close()
			cancel()
			return nil
		default:
		}
		_ = send(map[string]any{"action": "close"})
		select {
		case <-done:
			_ = stdin.Close()
			cancel()
			return nil
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			_ = stdin.Close()
			cancel()
			return nil
		}
	}}}
	select {
	case <-ready:
		entry := &compactEntry{Closer: closer, state: CompactState{Backend: "gtk3-x11-native", Limitation: "GTK3原生便签；依赖Python3/PyGObject及X11/XWayland。仅显式完整工作台操作打开浏览器。"}}
		go func() {
			select {
			case <-ctx.Done():
				_ = closer.Close()
			case <-done:
			}
		}()
		return entry, nil
	case <-done:
		_ = stdin.Close()
		return nil, fmt.Errorf("GTK原生便签启动失败: %v；%s", waitErr, strings.TrimSpace(stderr.String()))
	case <-ctx.Done():
		_ = closer.Close()
		return nil, ctx.Err()
	case <-time.After(6 * time.Second):
		_ = closer.Close()
		return nil, fmt.Errorf("GTK原生便签未就绪: %s", strings.TrimSpace(stderr.String()))
	}
}

type lockedCompactBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedCompactBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.b.Len() < 8192 {
		remaining := 8192 - b.b.Len()
		part := p
		if len(part) > remaining {
			part = part[:remaining]
		}
		_, _ = b.b.Write(part)
	}
	return len(p), nil
}
func (b *lockedCompactBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }
