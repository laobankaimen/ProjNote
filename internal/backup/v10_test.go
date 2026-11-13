package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"projnote/internal/store"
)

func TestV10BackupRestoresRosterLogVariantsAndDeliveredTaskReminder(t *testing.T) {
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	create := func(kind string, input map[string]any) map[string]any {
		t.Helper()
		value, err := st.Create(kind, input)
		if err != nil {
			t.Fatal(kind, err)
		}
		return value
	}
	p := create("projects", map[string]any{"name": "独立备份新功能"})
	pid := p["id"].(string)
	member, err := st.AddProjectMember(pid, map[string]any{"name": "单机名单成员"})
	if err != nil {
		t.Fatal(err)
	}
	task := create("tasks", map[string]any{"project_id": pid, "name": "保留稳定负责人", "assignee_member_id": member["id"]})
	personal := create("worklogs", map[string]any{"project_id": pid, "period_type": "day", "log_date": "2026-11-13", "content": "个人默认日志"})
	personalCopy := create("worklogs", map[string]any{"project_id": pid, "period_type": "day", "log_date": "2026-11-13", "content": "同日个人补充", "title": "个人另存标题", "save_as_new": true})
	draft, err := st.ProjectDailyLog(pid, "2026-11-13")
	if err != nil {
		t.Fatal(err)
	}
	projectLog, err := st.SaveProjectDailyLog(draft)
	if err != nil {
		t.Fatal(err)
	}
	projectInput := map[string]any{}
	for key, value := range draft {
		projectInput[key] = value
	}
	projectInput["content"], projectInput["title"], projectInput["save_as_new"] = "同日项目补充", "项目另存标题", true
	projectCopy, err := st.SaveProjectDailyLog(projectInput)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Now().UTC()
	reminder, err := st.CreateTaskReminder(map[string]any{"task_id": task["id"], "due_at": clock.Add(-time.Minute).Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := st.ClaimTaskReminder(clock)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	if err = st.FinishTaskReminder(claim, clock, nil); err != nil {
		t.Fatal(err)
	}
	reminder, err = st.GetTaskReminder(reminder["id"].(string))
	if err != nil || reminder["notified_at"] == "" {
		t.Fatal(reminder, err)
	}
	settings, err := st.Settings()
	if err != nil || settings["lan_enabled"] != "0" {
		t.Fatal("roster unexpectedly enabled LAN", settings, err)
	}
	info, err := Create(root, st.DB)
	if err != nil {
		t.Fatal(err)
	}
	stage, _, err := Extract(filepath.Join(root, "backups", info.Name), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	restoredRoot := t.TempDir()
	if err = Install(restoredRoot, stage); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(restoredRoot)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	for _, item := range []struct {
		kind string
		data map[string]any
	}{{"tasks", task}, {"worklogs", personal}, {"worklogs", personalCopy}, {"project_daily_logs", projectLog}, {"project_daily_logs", projectCopy}} {
		got, err := restored.Get(item.kind, item.data["id"].(string))
		if err != nil {
			t.Fatal(item.kind, err)
		}
		for key, want := range item.data {
			gotJSON, _ := json.Marshal(got[key])
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("restored %s %s changed %s: got %s want %s", item.kind, item.data["id"], key, gotJSON, wantJSON)
			}
		}
	}
	for _, kind := range []string{"worklogs", "project_daily_logs"} {
		_, total, err := restored.List(kind, map[string]string{"project_id": pid})
		if err != nil || total != 2 {
			t.Fatal("default and same-date named copy not both restored", kind, total, err)
		}
	}
	roster, err := restored.ProjectMembers(pid, nil)
	if err != nil || roster["total"] != 1 {
		t.Fatal(roster, err)
	}
	gotMember := roster["items"].([]map[string]any)[0]
	if gotMember["id"] != member["id"] || gotMember["name"] != member["name"] || len(gotMember["task_ids"].([]any)) != 1 || gotMember["task_ids"].([]any)[0] != task["id"] {
		t.Fatal("restored stable roster diverged from task grant", gotMember)
	}
	gotReminder, err := restored.GetTaskReminder(reminder["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(reminder)
	gotJSON, _ := json.Marshal(gotReminder)
	if string(wantJSON) != string(gotJSON) {
		t.Fatal("reminder delivery state changed in restore", string(gotJSON), string(wantJSON))
	}
	if claim, err = restored.ClaimTaskReminder(clock.Add(24 * time.Hour)); err != nil || claim != nil {
		t.Fatal("restored delivered reminder was sent again", claim, err)
	}
	settings, err = restored.Settings()
	if err != nil || settings["lan_enabled"] != "0" {
		t.Fatal(settings, err)
	}
	verification, err := restored.VerifyAudit()
	if err != nil || verification["ok"] != true {
		t.Fatal("backup restore broke audit", verification, err)
	}
}
