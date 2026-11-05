//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

type detachedTestProcess struct{ PID, Session int }

func TestDetachedDesktopHelper(t *testing.T) {
	mode := os.Getenv("PROJNOTE_TEST_DETACHED_MODE")
	if mode == "" {
		t.Skip("subprocess helper")
	}
	if err := prepareDesktopProcess(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer cancel()
	sid, _, _ := syscall.Syscall(syscall.SYS_GETSID, 0, 0, 0)
	raw, _ := json.Marshal(detachedTestProcess{os.Getpid(), int(sid)})
	if err := os.WriteFile(os.Getenv("PROJNOTE_TEST_DETACHED_PID"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "ready":
		desktopServiceReady("http://127.0.0.1:18600")
		<-ctx.Done()
	case "failure":
		desktopProcessDone(errors.New("模拟数据库启动失败"))
	case "empty":
		desktopProcessDone(nil)
	case "early":
		return
	case "timeout":
		<-ctx.Done()
	default:
		t.Fatal(mode)
	}
}
func TestDetachedDesktopHandshakeFailureTimeoutAndSession(t *testing.T) {
	for _, mode := range []string{"ready", "failure", "empty", "early", "timeout"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			pidPath := filepath.Join(root, "child.json")
			env := append(os.Environ(), "PROJNOTE_TEST_DETACHED_MODE="+mode, "PROJNOTE_TEST_DETACHED_PID="+pidPath)
			timeout := 4 * time.Second
			if mode == "timeout" {
				timeout = 400 * time.Millisecond
			}
			message, err := launchDetachedDesktop(os.Args[0], []string{"-test.run=^TestDetachedDesktopHelper$"}, env, root, timeout)
			var child detachedTestProcess
			raw, readErr := os.ReadFile(pidPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if err := json.Unmarshal(raw, &child); err != nil {
				t.Fatal(err)
			}
			if child.PID == os.Getpid() || child.Session != child.PID {
				t.Fatal("child did not detach its terminal session", child)
			}
			if mode == "ready" {
				if err != nil || message.URL != "http://127.0.0.1:18600" {
					t.Fatal(message, err)
				}
				if err = syscall.Kill(child.PID, 0); err != nil {
					t.Fatal("successful startup did not stay alive", err)
				}
				_ = syscall.Kill(child.PID, syscall.SIGTERM)
				done := make(chan error, 1)
				go func() {
					var status syscall.WaitStatus
					_, err := syscall.Wait4(child.PID, &status, 0, nil)
					done <- err
				}()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					_ = syscall.Kill(child.PID, syscall.SIGKILL)
					t.Fatal("background child did not exit")
				}
			} else {
				if err == nil {
					t.Fatal("startup without ready was reported successful", mode)
				}
				if mode == "failure" && !strings.Contains(err.Error(), "模拟数据库启动失败") {
					t.Fatal(err)
				}
				if err = syscall.Kill(child.PID, 0); err == nil {
					t.Fatal("failed startup left a child process", child)
				}
			}
		})
	}
}
