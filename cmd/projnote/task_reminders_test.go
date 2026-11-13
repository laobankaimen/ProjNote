package main

import (
	"context"
	"projnote/internal/store"
	"strings"
	"testing"
	"time"
)

func TestBackgroundTaskReminderDeliveryWithoutBrowser(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.Create("projects", map[string]any{"name": "后台验证项目"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := st.Create("tasks", map[string]any{"project_id": p["id"], "name": "今日现场检查"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := st.CreateTaskReminder(map[string]any{"task_id": task["id"], "due_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	delivered := make(chan string, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runTaskReminders(ctx, st, func(_ context.Context, title, body string) error { delivered <- title + body; return nil })
	}()
	select {
	case message := <-delivered:
		if !strings.Contains(message, "今日现场检查") || !strings.Contains(message, "后台验证项目") {
			t.Fatal(message)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("background reminder not delivered")
	}
	// Finish is persisted before the worker waits for another tick.
	deadline := time.Now().Add(time.Second)
	for {
		saved, err := st.GetTaskReminder(r["id"].(string))
		if err != nil {
			t.Fatal(err)
		}
		if saved["notified_at"] != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delivery not persisted")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("notification worker did not stop")
	}
}
