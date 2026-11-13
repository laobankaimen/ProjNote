//go:build linux

package platform

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskNotificationArgumentsAndUnavailableService(t *testing.T) {
	root := t.TempDir()
	capture := filepath.Join(root, "arguments")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PROJNOTE_TEST_NOTIFICATION_ARGS\"\n"
	if err := os.WriteFile(filepath.Join(root, "notify-send"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/test-notification-bus")
	t.Setenv("PROJNOTE_TEST_NOTIFICATION_ARGS", capture)
	if TaskNotificationStatus()["supported"] != true {
		t.Fatal("available notifier hidden")
	}
	if err := NotifyTask(context.Background(), "ProjNote 任务提醒", "<检查> & '$()'"); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "--\nProjNote 任务提醒\n&lt;检查&gt; &amp; &#39;$()&#39;\n") {
		t.Fatal("notification text or argument boundaries changed", string(got))
	}
	if err = os.WriteFile(filepath.Join(root, "notify-send"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = NotifyTask(context.Background(), "t", "b"); err == nil {
		t.Fatal("notification failure marked success")
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	if TaskNotificationStatus()["supported"] != false {
		t.Fatal("headless service presented as desktop capable")
	}
	if err = NotifyTask(context.Background(), "t", "b"); err == nil {
		t.Fatal("headless notification succeeded")
	}
}
