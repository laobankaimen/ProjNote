//go:build linux

package platform

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestAutoStartLinuxGLibLaunchPreservesLiteralPaths(t *testing.T) {
	python := "/usr/bin/python3"
	probe := exec.Command(python, "-c", "from gi.repository import Gio; assert hasattr(Gio, 'DesktopAppInfo')")
	if err := probe.Run(); err != nil {
		t.Skip("Python GObject/Gio desktop launcher is unavailable")
	}
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	outputPath := filepath.Join(root, "actual-argv.json")
	t.Setenv("PROJNOTE_AUTOSTART_ARGV_OUTPUT", outputPath)
	// This executable only records its literal argv in the temporary directory.
	// It creates no window, opens no browser and never launches ProjNote itself.
	executable := filepath.Join(root, "工具 \\ $HOME `date` \" & ; >.py")
	dataDir := filepath.Join(root, "项目 %U \\ $USER `id` \" & ; >")
	helper := "#!/usr/bin/python3\nimport json, os, sys\nwith open(os.environ['PROJNOTE_AUTOSTART_ARGV_OUTPUT'], 'w', encoding='utf-8') as stream:\n    json.dump(sys.argv, stream, ensure_ascii=False)\n"
	if err := os.WriteFile(executable, []byte(helper), 0700); err != nil {
		t.Fatal(err)
	}
	manager, err := NewAutoStartManager(executable, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	status, err := manager.SetEnabled(true)
	if err != nil {
		t.Fatal(err)
	}
	entryPath := status["entry_path"].(string)
	if validator, err := exec.LookPath("desktop-file-validate"); err == nil {
		if out, err := exec.Command(validator, entryPath).CombinedOutput(); err != nil {
			t.Fatalf("desktop entry validation: %v\n%s", err, out)
		}
	}
	// The real Gio/GLib implementation performs desktop-value unescaping,
	// Exec quoting and percent-field expansion before starting the helper.
	launcher := `import json, os, sys, time
from gi.repository import Gio
info = Gio.DesktopAppInfo.new_from_filename(sys.argv[1])
assert info is not None, 'GLib rejected the generated desktop entry'
assert info.launch([], None), 'GLib could not launch the helper'
deadline = time.monotonic() + 4
while time.monotonic() < deadline:
    try:
        with open(os.environ['PROJNOTE_AUTOSTART_ARGV_OUTPUT'], encoding='utf-8') as stream:
            actual = json.load(stream)
        print(json.dumps(actual, ensure_ascii=False))
        break
    except (FileNotFoundError, json.JSONDecodeError):
        time.sleep(0.02)
else:
    raise RuntimeError('temporary helper did not record its argv')
`
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, python, "-c", launcher, entryPath)
	output, err := command.Output()
	if err != nil {
		if failure, ok := err.(*exec.ExitError); ok {
			t.Fatalf("GLib launch: %v\n%s", err, failure.Stderr)
		}
		t.Fatal(err)
	}
	var actual []string
	if err := json.Unmarshal(output, &actual); err != nil {
		t.Fatalf("invalid helper output %q: %v", output, err)
	}
	want := []string{executable, "--data-dir", dataDir}
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("GLib argv = %#v, want %#v", actual, want)
	}
}

func TestAutoStartLinuxUserEntryAndExplicitRepair(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	executable := filepath.Join(t.TempDir(), "工具 中文", "ProjNote")
	dataDir := filepath.Join(t.TempDir(), "项目 100%", "data & notes")
	manager, err := NewAutoStartManager(executable, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	entryPath := filepath.Join(config, "autostart", autoStartDesktopName)
	status, err := manager.Status()
	if err != nil || status["enabled"] != false || status["entry_path"] != entryPath {
		t.Fatalf("initial = %#v, %v", status, err)
	}
	if _, err := os.Stat(filepath.Dir(entryPath)); !os.IsNotExist(err) {
		t.Fatal("reading state created the config directory")
	}
	status, err = manager.SetEnabled(true)
	if err != nil || status["matches_current"] != true {
		t.Fatalf("enable = %#v, %v", status, err)
	}
	contents, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "100%%") || !strings.Contains(string(contents), "--data-dir") || strings.Contains(string(contents), "--browser") || strings.Contains(string(contents), "--no-browser") {
		t.Fatalf("unexpected desktop entry: %s", contents)
	}
	info, err := os.Stat(entryPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("file permissions = %v, %v", info.Mode(), err)
	}
	other, err := NewAutoStartManager(executable+"-moved", dataDir+"-relocated")
	if err != nil {
		t.Fatal(err)
	}
	status, err = other.Status()
	if err != nil || status["needs_repair"] != true || status["enabled"] != true {
		t.Fatalf("moved = %#v, %v", status, err)
	}
	status, err = other.SetEnabled(true)
	if err != nil || status["needs_repair"] != false {
		t.Fatalf("repair = %#v, %v", status, err)
	}
	status, err = manager.Status()
	if err != nil || status["needs_repair"] != true {
		t.Fatalf("old process must see actual new entry = %#v, %v", status, err)
	}
	contents, _ = os.ReadFile(entryPath)
	contents = []byte(strings.ReplaceAll(string(contents), "Hidden=false", "Hidden=true"))
	if err := os.WriteFile(entryPath, contents, 0600); err != nil {
		t.Fatal(err)
	}
	status, err = other.Status()
	if err != nil || status["enabled"] != false || status["matches_current"] != true || status["needs_repair"] != true {
		t.Fatalf("externally disabled = %#v, %v", status, err)
	}
	if _, err := other.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	status, err = other.SetEnabled(false)
	if err != nil || status["enabled"] != false {
		t.Fatalf("disable = %#v, %v", status, err)
	}
	if _, err := os.Stat(entryPath); !os.IsNotExist(err) {
		t.Fatalf("entry not removed: %v", err)
	}
	if _, err := other.SetEnabled(false); err != nil {
		t.Fatal(err)
	}
}

func TestAutoStartLinuxExecQuotesSpecialPaths(t *testing.T) {
	value := "/tmp/目录 %f \\ \" $HOME `x` & ; < > '"
	quoted := desktopExecArgument(value)
	// Four backslashes represent one literal backslash after both desktop
	// unescaping stages; $, backtick and quote each need two-file-backslash escapes.
	want := `"/tmp/目录 %%f \\\\ \\" \\$HOME \\` + "`x\\\\`" + ` & ; < > '"`
	if quoted != want {
		t.Fatalf("quoted = %q, want %q", quoted, want)
	}
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	if _, err := NewAutoStartManager("/tmp/has=equals/ProjNote", "/tmp/data"); err == nil {
		t.Fatal("desktop forbidden executable equals accepted")
	}
	if _, err := NewAutoStartManager("/tmp/has%percent/ProjNote", "/tmp/data"); err == nil {
		t.Fatal("GLib-incompatible executable percentage accepted")
	}
	t.Setenv("XDG_CONFIG_HOME", "relative-config")
	if _, err := NewAutoStartManager("/tmp/ProjNote", "/tmp/data"); err == nil {
		t.Fatal("relative config root accepted")
	}
}

func TestAutoStartLinuxDoesNotFollowLinksOrOverwriteForeignEntries(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	manager, err := NewAutoStartManager("/tmp/ProjNote", "/tmp/data")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "autostart", autoStartDesktopName)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	foreign := []byte("[Desktop Entry]\nType=Application\nName=Another\nExec=/tmp/other\n")
	if err := os.WriteFile(path, foreign, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetEnabled(true); err == nil {
		t.Fatal("foreign entry overwritten")
	}
	if _, err := manager.SetEnabled(false); err == nil {
		t.Fatal("foreign entry removed")
	}
	contents, _ := os.ReadFile(path)
	if string(contents) != string(foreign) {
		t.Fatal("foreign entry changed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(target, []byte("do not change"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Status(); err == nil {
		t.Fatal("symlink read accepted")
	}
	if _, err := manager.SetEnabled(true); err == nil {
		t.Fatal("symlink replaced")
	}
	if _, err := manager.SetEnabled(false); err == nil {
		t.Fatal("symlink removed")
	}
	contents, _ = os.ReadFile(target)
	if string(contents) != "do not change" {
		t.Fatal("symlink target changed")
	}
}

func TestAutoStartLinuxMalformedRestrictionsRequireRepair(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	manager, err := NewAutoStartManager("/tmp/ProjNote", "/tmp/data")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "autostart", autoStartDesktopName)
	contents, _ := os.ReadFile(path)
	for _, extra := range []string{"Exec=/tmp/evil\n", "OnlyShowIn=Other;\n", "Terminal=true\n"} {
		if err := os.WriteFile(path, append(append([]byte{}, contents...), extra...), 0600); err != nil {
			t.Fatal(err)
		}
		status, err := manager.Status()
		if err != nil || status["needs_repair"] != true {
			t.Fatalf("malformed %q = %#v, %v", extra, status, err)
		}
		if _, err := manager.SetEnabled(true); err != nil {
			t.Fatal(err)
		}
	}
}
