package store

import (
	"errors"
	"testing"
	"time"
)

func TestTaskRemindersDeliveryLeaseVersionAndFinishedTasks(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "提醒项目"})
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "现场检查"})
	clock := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)
	input := map[string]any{"task_id": task["id"], "due_at": clock.Add(-time.Minute).Format(time.RFC3339)}
	r, err := s.CreateTaskReminder(input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTaskReminder(input); err == nil {
		t.Fatal("duplicate reminder accepted")
	}
	claim, err := s.ClaimTaskReminder(clock)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	second, err := s.ClaimTaskReminder(clock)
	if err != nil || second != nil {
		t.Fatal("duplicate in flight", second, err)
	}
	// An edit while an old delivery is in flight must not mark the new time delivered.
	r, err = s.UpdateTaskReminder(text(r["id"]), map[string]any{"version": r["version"], "due_at": clock.Add(time.Hour).Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishTaskReminder(claim, clock, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetTaskReminder(text(r["id"]))
	if nonempty(got["notified_at"]) {
		t.Fatal("stale claim acknowledged newer schedule")
	}
	claim, err = s.ClaimTaskReminder(clock.Add(2 * time.Hour))
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	if err = s.FinishTaskReminder(claim, clock.Add(2*time.Hour), errors.New("notification service unavailable")); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetTaskReminder(text(r["id"]))
	if !nonempty(got["last_error"]) || nonempty(got["notified_at"]) {
		t.Fatal("failed delivery lost", got)
	}
	retry, _ := s.ClaimTaskReminder(clock.Add(2*time.Hour + 30*time.Second))
	if retry != nil {
		t.Fatal("failure retry too fast")
	}
	retry, _ = s.ClaimTaskReminder(clock.Add(2*time.Hour + time.Minute))
	if retry == nil {
		t.Fatal("failed delivery never retried")
	}
	if err = s.FinishTaskReminder(retry, clock.Add(2*time.Hour+time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	retry, _ = s.ClaimTaskReminder(clock.Add(24 * time.Hour))
	if retry != nil {
		t.Fatal("delivered reminder repeated")
	}
	if _, err = s.UpdateTaskReminder(text(r["id"]), map[string]any{"version": 1, "due_at": input["due_at"]}); err == nil {
		t.Fatal("stale edit accepted")
	}
	r, err = s.DismissTaskReminder(text(r["id"]), map[string]any{"version": r["version"]})
	if err != nil {
		t.Fatal(err)
	}
	gotTask := getNow(t, s, "tasks", text(task["id"]))
	if gotTask["status"] != "todo" {
		t.Fatal("dismissing completed task")
	}
	r, err = s.CreateTaskReminder(input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Update("tasks", text(task["id"]), map[string]any{"version": gotTask["version"], "status": "done", "progress": 100})
	if err != nil {
		t.Fatal(err)
	}
	retry, _ = s.ClaimTaskReminder(clock.Add(48 * time.Hour))
	if retry != nil {
		t.Fatal("completed task notified")
	}
	page, err := s.ListTaskReminders(map[string]string{"status": "pending"})
	if err != nil || num(page["total"]) != 0 {
		t.Fatal(page, err)
	}
	if _, err = s.UpdateTaskReminder(text(r["id"]), map[string]any{"version": r["version"], "due_at": input["due_at"]}); err == nil {
		t.Fatal("completed task rescheduled")
	}

	finished := getNow(t, s, "tasks", text(task["id"]))
	if _, err = s.ReopenTask(text(task["id"]), map[string]any{"version": finished["version"], "reason": "继续后续工作"}); err != nil {
		t.Fatal(err)
	}
	retry, _ = s.ClaimTaskReminder(clock.Add(96 * time.Hour))
	if retry != nil {
		t.Fatal("reopening revived a terminated reminder")
	}
	if _, err = s.CreateTaskReminder(input); err != nil {
		t.Fatal("reopened task cannot receive a fresh reminder", err)
	}
	assertChain(t, s)
}
func TestTaskRemindersLeaseRecoveryAndEligibility(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "重启补发"})
	parent := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "汇总"})
	leaf := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": parent["id"], "name": "叶任务"})
	at := time.Now().UTC()
	due := at.Add(-time.Minute).Format(time.RFC3339Nano)
	if _, err := s.CreateTaskReminder(map[string]any{"task_id": parent["id"], "due_at": due}); err == nil {
		t.Fatal("parent reminder accepted")
	}
	if _, err := s.CreateTaskReminder(map[string]any{"task_id": leaf["id"], "due_at": due, "title": "独立事项"}); err == nil {
		t.Fatal("independent content accepted")
	}
	if _, err := s.CreateTaskReminder(map[string]any{"task_id": leaf["id"], "due_at": due}); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimTaskReminder(at)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	retry, err := s.ClaimTaskReminder(at.Add(6 * time.Minute))
	if err != nil || retry == nil {
		t.Fatal("interrupted delivery not reclaimed", err)
	}
	if err = s.FinishTaskReminder(claim, at, nil); err != nil {
		t.Fatal(err)
	}
	got, _ := s.GetTaskReminder(text(claim["id"]))
	if nonempty(got["notified_at"]) {
		t.Fatal("expired lease acknowledged a newer claim")
	}
	if err = s.FinishTaskReminder(retry, at, nil); err != nil {
		t.Fatal(err)
	}
}
