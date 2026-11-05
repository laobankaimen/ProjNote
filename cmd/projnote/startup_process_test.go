package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDesktopBackgroundOnlyForNormalLinuxGraphicalStartup(t *testing.T) {
	for _, tc := range []struct {
		name, goos string
		display    bool
		options    desktopBackgroundOptions
		want       bool
	}{
		{"normal graphical", "linux", true, desktopBackgroundOptions{}, true},
		{"foreground debugging", "linux", true, desktopBackgroundOptions{foreground: true}, false},
		{"headless compatibility", "linux", true, desktopBackgroundOptions{noBrowser: true}, false},
		{"no desktop", "linux", true, desktopBackgroundOptions{noDesktop: true}, false},
		{"maintenance", "linux", true, desktopBackgroundOptions{maintenance: true}, false},
		{"without display", "linux", false, desktopBackgroundOptions{}, false},
		{"windows GUI subsystem", "windows", true, desktopBackgroundOptions{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldDetachDesktop(tc.goos, tc.display, tc.options); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
	for _, args := range [][]string{{"--stop"}, {"--restore=a.zip"}, {"-verify-backup", "a.zip"}, {"--relocate", "target"}, {"--foreground"}, {"--help"}} {
		if !maintenanceConsoleRequested(args) {
			t.Fatal("maintenance output needs a console", args)
		}
	}
	if maintenanceConsoleRequested([]string{"--compact", "--data-dir=x"}) {
		t.Fatal("normal desktop launch should not attach the caller console")
	}
}
func TestStartupLogRotationAndRetention(t *testing.T) {
	root := t.TempDir()
	log, err := openStartupLog(root)
	if err != nil {
		t.Fatal(err)
	}
	name := log.Name()
	if _, err = log.WriteString(strings.Repeat("x", 1<<20)); err != nil {
		t.Fatal(err)
	}
	_ = log.Close()
	old := filepath.Join(root, "logs", "startup-20000101.log")
	if err = os.WriteFile(old, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().AddDate(0, 0, -8)
	if err = os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	log, err = openStartupLog(root)
	if err != nil {
		t.Fatal(err)
	}
	_ = log.Close()
	if info, err := os.Stat(name); err != nil || info.Size() != 0 {
		t.Fatal("current log not rotated", info, err)
	}
	if info, err := os.Stat(name + ".1"); err != nil || info.Size() != 1<<20 {
		t.Fatal("preceding log not retained", info, err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("stale startup log retained", err)
	}
}
