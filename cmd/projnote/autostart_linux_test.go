//go:build linux

package main

import (
	"os"
	"path/filepath"
	"testing"

	"projnote/internal/server"
)

func TestAutoStartStartupHooksUseActualExecutableAndDataDirectory(t *testing.T) {
	// The production callbacks are exercised against an isolated XDG root.
	// No entry is registered in the user's actual desktop configuration.
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	dataDir := filepath.Join(t.TempDir(), "项目 数据目录")
	app := &server.Server{}
	configureAutoStart(app, dataDir)
	if app.AutoStartStatus == nil || app.SetAutoStart == nil {
		t.Fatal("autostart hooks missing")
	}
	status, err := app.AutoStartStatus()
	if err != nil || status["enabled"] != false {
		t.Fatalf("initial = %#v, %v", status, err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if status["executable"] != executable || status["data_dir"] != dataDir {
		t.Fatalf("wrong fixed paths: %#v", status)
	}
	status, err = app.SetAutoStart(true)
	if err != nil || status["matches_current"] != true {
		t.Fatalf("enable = %#v, %v", status, err)
	}
	if _, err := app.SetAutoStart(false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(status["entry_path"].(string)); !os.IsNotExist(err) {
		t.Fatalf("temporary entry not removed: %v", err)
	}
}

func TestAutoStartInvalidConfigurationDoesNotPreventStartup(t *testing.T) {
	app := &server.Server{}
	configureAutoStart(app, "relative-data")
	if app.AutoStartStatus == nil || app.SetAutoStart == nil {
		t.Fatal("error hooks missing")
	}
	if _, err := app.AutoStartStatus(); err == nil {
		t.Fatal("invalid configuration was hidden")
	}
	if _, err := app.SetAutoStart(true); err == nil {
		t.Fatal("invalid configuration enabled")
	}
}
