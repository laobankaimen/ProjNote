package platform

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type fakeAutoStartBackend struct {
	entry           autoStartEntry
	err             error
	writes, removes int
}

func (b *fakeAutoStartBackend) path() string                  { return "test-current-user-entry" }
func (b *fakeAutoStartBackend) read() (autoStartEntry, error) { return b.entry, b.err }
func (b *fakeAutoStartBackend) write(command string) error {
	if b.err != nil {
		return b.err
	}
	b.writes++
	b.entry = autoStartEntry{exists: true, enabled: true, valid: true, command: command}
	return nil
}
func (b *fakeAutoStartBackend) remove() error {
	if b.err != nil {
		return b.err
	}
	b.removes++
	b.entry = autoStartEntry{}
	return nil
}

func TestAutoStartManagerDoesNotRegisterUntilExplicitAction(t *testing.T) {
	backend := &fakeAutoStartBackend{}
	manager := &AutoStartManager{executable: "current-program", dataDir: "current-data", command: "current-command", backend: backend}
	for range 2 {
		status, err := manager.Status()
		if err != nil || status["enabled"] != false || status["needs_repair"] != false {
			t.Fatalf("default status = %#v, %v", status, err)
		}
	}
	if backend.writes != 0 || backend.removes != 0 {
		t.Fatal("status mutated login entry")
	}
	status, err := manager.SetEnabled(true)
	if err != nil || status["enabled"] != true || status["matches_current"] != true || status["needs_repair"] != false {
		t.Fatalf("enabled status = %#v, %v", status, err)
	}
	backend.entry.command = "old-program --data-dir other-data"
	status, err = manager.Status()
	if err != nil || status["enabled"] != true || status["matches_current"] != false || status["needs_repair"] != true || status["executable"] != "current-program" || status["data_dir"] != "current-data" {
		t.Fatalf("stale status = %#v, %v", status, err)
	}
	if backend.entry.command == manager.command {
		t.Fatal("reading stale entry silently repaired it")
	}
	status, err = manager.SetEnabled(true)
	if err != nil || status["needs_repair"] != false || backend.entry.command != manager.command {
		t.Fatalf("repair status = %#v, %v", status, err)
	}
	status, err = manager.SetEnabled(false)
	if err != nil || status["enabled"] != false || status["needs_repair"] != false || backend.entry.exists {
		t.Fatalf("disabled status = %#v, %v", status, err)
	}
	if _, err := manager.SetEnabled(false); err != nil {
		t.Fatalf("idempotent disable: %v", err)
	}
}

func TestAutoStartManagerDisabledMalformedAndReadFailures(t *testing.T) {
	for _, entry := range []autoStartEntry{
		{exists: true, enabled: false, valid: true, command: "expected"},
		{exists: true, enabled: true, valid: false, command: "expected"},
	} {
		backend := &fakeAutoStartBackend{entry: entry}
		manager := &AutoStartManager{command: "expected", backend: backend}
		status, err := manager.Status()
		if err != nil || status["needs_repair"] != true {
			t.Fatalf("status = %#v, %v", status, err)
		}
		if _, err := manager.SetEnabled(true); err != nil {
			t.Fatal(err)
		}
		status, err = manager.Status()
		if err != nil || status["matches_current"] != true || status["needs_repair"] != false {
			t.Fatalf("repair = %#v, %v", status, err)
		}
	}
	backend := &fakeAutoStartBackend{err: errors.New("denied")}
	manager := &AutoStartManager{backend: backend}
	if _, err := manager.Status(); err == nil {
		t.Fatal("read error hidden")
	}
	if _, err := manager.SetEnabled(true); err == nil {
		t.Fatal("write error hidden")
	}
	if _, err := manager.SetEnabled(false); err == nil {
		t.Fatal("delete error hidden")
	}
	unsupported := &AutoStartManager{}
	status, err := unsupported.Status()
	if err != nil || status["supported"] != false {
		t.Fatalf("unsupported = %#v, %v", status, err)
	}
	if _, err := unsupported.SetEnabled(true); err == nil {
		t.Fatal("unsupported enabled")
	}
}

func TestAutoStartRejectsRelativeAndControlPaths(t *testing.T) {
	base := t.TempDir()
	for _, pair := range [][2]string{{"relative", base}, {base, "relative"}, {base + "\nExec=evil", base}, {base, base + "\r--browser"}, {base + "\x00", base}, {base, base + "\t"}} {
		if _, err := NewAutoStartManager(pair[0], pair[1]); err == nil {
			t.Fatalf("accepted unsafe paths %q", pair)
		}
	}
	if !filepath.IsAbs(base) {
		t.Fatal("test requires absolute temporary path")
	}
}

func TestAutoStartWindowsCommandQuotesWithoutShellExpansion(t *testing.T) {
	command, err := windowsAutoStartCommand(`C:\工具 100%\ProjNote.exe`, `D:\项目 100%\logs & notes\`)
	if err != nil {
		t.Fatal(err)
	}
	want := `"C:\工具 100%\ProjNote.exe" --data-dir "D:\项目 100%\logs & notes\\"`
	if command != want {
		t.Fatalf("command = %q, want %q", command, want)
	}
	if strings.Contains(command, "--browser") || strings.Contains(command, "--no-browser") || strings.Contains(command, "cmd.exe") {
		t.Fatalf("unexpected launcher: %s", command)
	}
	if _, err := windowsAutoStartCommand(`C:\bad"path\p.exe`, `C:\data`); err == nil {
		t.Fatal("invalid quoted Windows path accepted")
	}
	if _, err := windowsAutoStartCommand(`C:\p.exe`, `C:\`+strings.Repeat("长", 260)); err == nil {
		t.Fatal("oversized Run command accepted")
	}
	if _, err := windowsAutoStartCommand(`C:\p.exe`, `C:\`+strings.Repeat("😀", 130)); err == nil {
		t.Fatal("UTF-16 length limit not enforced")
	}
}
