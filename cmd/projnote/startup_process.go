package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type desktopBackgroundOptions struct{ foreground, noBrowser, noDesktop, maintenance bool }

func shouldDetachDesktop(goos string, display bool, options desktopBackgroundOptions) bool {
	return goos == "linux" && display && !options.foreground && !options.noBrowser && !options.noDesktop && !options.maintenance
}

var startupLogRoot string

// Startup diagnostics never include runtime.json or the instance token. Keep
// seven daily files, with at most one rotated predecessor per day.
func openStartupLog(root string) (*os.File, error) {
	logDir := filepath.Join(root, "logs")
	if err := os.MkdirAll(logDir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(logDir, "startup-"+time.Now().Format("20060102")+".log")
	if info, err := os.Stat(path); err == nil && info.Size() >= 1<<20 {
		if err := os.Rename(path, path+".1"); err != nil {
			return nil, err
		}
	}
	entries, _ := os.ReadDir(logDir)
	cutoff := time.Now().AddDate(0, 0, -7)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "startup-") || !(strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".log.1")) {
			continue
		}
		if info, err := entry.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(filepath.Join(logDir, name))
		}
	}
	return os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
}
func reportStartupFailure(err error) {
	message := "ProjNote: " + err.Error()
	fmt.Fprintln(os.Stderr, message)
	root := startupLogRoot
	if root == "" {
		root = defaultDataDir()
	}
	if log, openErr := openStartupLog(root); openErr == nil {
		_, _ = fmt.Fprintf(log, "%s %s\n", time.Now().Format(time.RFC3339), message)
		_ = log.Close()
	}
	presentStartupFailure(message + "\n\n诊断日志：" + filepath.Join(root, "logs"))
}
func maintenanceConsoleRequested(args []string) bool {
	for _, arg := range args {
		name := strings.TrimLeft(strings.SplitN(arg, "=", 2)[0], "-")
		switch name {
		case "stop", "restore", "verify-backup", "relocate", "foreground", "help", "h":
			return true
		}
	}
	return false
}
