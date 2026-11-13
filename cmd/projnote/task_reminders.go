package main

import (
	"context"
	"fmt"
	"log/slog"
	"projnote/internal/platform"
	"projnote/internal/store"
	"time"
)

func runTaskReminders(ctx context.Context, st *store.Store, deliver func(context.Context, string, string) error) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		claim, err := st.ClaimTaskReminder(time.Now())
		if err != nil {
			slog.Error("task reminder claim failed", "error", err)
		} else if claim != nil {
			due, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(claim["due_at"]))
			body := fmt.Sprintf("%s\n项目：%s\n提醒时间：%s\n完成后可在便签或任务列表标记完成。", claim["task_name"], claim["project_name"], due.Local().Format("01-02 15:04"))
			err = deliver(ctx, "ProjNote 任务提醒", body)
			if ctx.Err() != nil {
				return
			}
			if saveErr := st.FinishTaskReminder(claim, time.Now(), err); saveErr != nil {
				slog.Error("task reminder delivery state failed", "error", saveErr)
			}
			// Acknowledged reminders are sent once. Drain other due tasks on the next tick.
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func startTaskReminders(parent context.Context, st *store.Store) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() { defer close(done); runTaskReminders(ctx, st, platform.NotifyTask) }()
	return func() { cancel(); <-done }
}
