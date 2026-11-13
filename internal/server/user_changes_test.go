package server

import (
	"encoding/json"
	"testing"
	"time"
)

func userChangeError(t *testing.T, s *Server, method, path string, input map[string]any, status int, code string) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	response := call(s, method, path, string(raw), map[string]string{"Content-Type": "application/json", "X-ProjNote-Request": "1"})
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if response.Code != status || code != "" && body.Error.Code != code {
		t.Fatalf("%s %s: want %d %s, got %d %s", method, path, status, code, response.Code, response.Body.String())
	}
}
func userChangeCopy(input map[string]any) map[string]any {
	result := map[string]any{}
	for key, value := range input {
		result[key] = value
	}
	return result
}
func userChangeItem(t *testing.T, page map[string]any, id string) map[string]any {
	t.Helper()
	for _, value := range page["items"].([]any) {
		item := value.(map[string]any)
		if item["id"] == id {
			return item
		}
	}
	t.Fatalf("missing item %s: %v", id, page)
	return nil
}
func userChangeHasID(values any, id string) bool {
	for _, value := range values.([]any) {
		if value == id {
			return true
		}
	}
	return false
}

func TestUserChangesLogDuplicateOverwriteAndSamePeriodSaveAs(t *testing.T) {
	s := testServer(t)
	p := exportJSON(t, s, "POST", "/api/projects", map[string]any{"name": "日志选择项目"}, 201)
	pid := p["id"].(string)
	payload := map[string]any{"project_id": pid, "period_type": "day", "log_date": "2026-11-13", "content": "原始个人日志"}
	original := exportJSON(t, s, "POST", "/api/worklogs", payload, 201)
	userChangeError(t, s, "POST", "/api/worklogs", payload, 409, "conflict")
	current := exportJSON(t, s, "GET", "/api/worklogs/"+original["id"].(string), nil, 200)
	changed := exportJSON(t, s, "PATCH", "/api/worklogs/"+original["id"].(string), map[string]any{"version": current["version"], "title": "个人新标题", "content": "明确覆盖正文"}, 200)
	userChangeError(t, s, "PATCH", "/api/worklogs/"+original["id"].(string), map[string]any{"version": current["version"], "content": "过期版本不可覆盖"}, 409, "version_conflict")
	separate := userChangeCopy(payload)
	separate["save_as_new"] = true
	separate["title"] = "评审补充日志"
	separate["content"] = "另存个人正文"
	copy := exportJSON(t, s, "POST", "/api/worklogs", separate, 201)
	if copy["id"] == original["id"] || copy["variant"] == "" || copy["log_date"] != original["log_date"] || copy["project_id"] != pid || copy["title"] != "评审补充日志" {
		t.Fatal("personal save-as changed scope or identity", copy)
	}
	retained := exportJSON(t, s, "GET", "/api/worklogs/"+original["id"].(string), nil, 200)
	if retained["content"] != changed["content"] || retained["version"] != changed["version"] {
		t.Fatal("save-as overwrote original", retained)
	}
	userChangeError(t, s, "POST", "/api/worklogs", map[string]any{"project_id": pid, "period_type": "day", "log_date": "2026-11-13", "content": "无标题另存", "save_as_new": true}, 400, "validation")

	task := exportJSON(t, s, "POST", "/api/tasks", map[string]any{"project_id": pid, "name": "日报来源任务"}, 201)
	draft := exportJSON(t, s, "POST", "/api/project-daily-logs/generate", map[string]any{"project_id": pid, "log_date": "2026-11-13"}, 200)
	projectLog := exportJSON(t, s, "POST", "/api/project-daily-logs", draft, 201)
	userChangeError(t, s, "POST", "/api/project-daily-logs", draft, 409, "version_conflict")
	projectID := projectLog["id"].(string)
	current = exportJSON(t, s, "GET", "/api/project-daily-logs/"+projectID, nil, 200)
	projectChanged := exportJSON(t, s, "PATCH", "/api/project-daily-logs/"+projectID, map[string]any{"version": current["version"], "title": "项目新标题", "content": "人工整理项目正文"}, 200)
	userChangeError(t, s, "PATCH", "/api/project-daily-logs/"+projectID, map[string]any{"version": current["version"], "content": "过期覆盖"}, 409, "version_conflict")
	separate = userChangeCopy(draft)
	separate["save_as_new"] = true
	separate["title"] = "项目评审补充"
	separate["content"] = "另存项目正文"
	projectCopy := exportJSON(t, s, "POST", "/api/project-daily-logs", separate, 201)
	if projectCopy["id"] == projectID || projectCopy["variant"] == "" || projectCopy["log_date"] != draft["log_date"] || projectCopy["project_id"] != pid {
		t.Fatal("project save-as changed period", projectCopy)
	}
	retained = exportJSON(t, s, "GET", "/api/project-daily-logs/"+projectID, nil, 200)
	if retained["content"] != projectChanged["content"] || retained["version"] != projectChanged["version"] {
		t.Fatal("project save-as overwrote original", retained)
	}
	exportJSON(t, s, "PATCH", "/api/tasks/"+task["id"].(string), map[string]any{"version": task["version"], "progress": 40, "status": "doing"}, 200)
	separate["title"] = "过期来源另存不应绕过核验"
	userChangeError(t, s, "POST", "/api/project-daily-logs", separate, 409, "source_conflict")
	stale := userChangeCopy(draft)
	stale["version"] = projectChanged["version"]
	stale["content"] = "来源过期明确拒绝"
	userChangeError(t, s, "PATCH", "/api/project-daily-logs/"+projectID, stale, 409, "source_conflict")
	retained = exportJSON(t, s, "GET", "/api/project-daily-logs/"+projectID, nil, 200)
	if retained["content"] != projectChanged["content"] {
		t.Fatal("stale source replaced manual content", retained)
	}
	list := exportJSON(t, s, "GET", "/api/project-daily-logs?project_id="+pid+"&log_date=2026-11-13", nil, 200)
	if list["total"] != float64(2) {
		t.Fatal("source conflict created extra copy", list)
	}
}

func TestUserChangesProjectMembersStableTaskAssignmentAndCompletedGuard(t *testing.T) {
	s := testServer(t)
	p := exportJSON(t, s, "POST", "/api/projects", map[string]any{"name": "成员选择项目"}, 201)
	q := exportJSON(t, s, "POST", "/api/projects", map[string]any{"name": "其他项目"}, 201)
	pid := p["id"].(string)
	membersPath := "/api/projects/" + pid + "/members"
	first := exportJSON(t, s, "POST", membersPath, map[string]any{"name": "同名成员"}, 201)
	second := exportJSON(t, s, "POST", membersPath, map[string]any{"name": "同名成员"}, 201)
	stranger := exportJSON(t, s, "POST", "/api/projects/"+q["id"].(string)+"/members", map[string]any{"name": "其他成员"}, 201)
	if first["id"] == second["id"] {
		t.Fatal("same-name people share identity")
	}
	task := exportJSON(t, s, "POST", "/api/tasks", map[string]any{"project_id": pid, "name": "稳定成员任务", "assignee_member_id": first["id"], "assignee": "不应采用的手输文本"}, 201)
	tid := task["id"].(string)
	if task["assignee_member_id"] != first["id"] || task["assignee"] != "同名成员" {
		t.Fatal("task failed to resolve project identity", task)
	}
	roster := exportJSON(t, s, "GET", membersPath, nil, 200)
	if roster["total"] != float64(2) || !userChangeHasID(userChangeItem(t, roster, first["id"].(string))["task_ids"], tid) || userChangeHasID(userChangeItem(t, roster, second["id"].(string))["task_ids"], tid) {
		t.Fatal("assignment did not match stable member", roster)
	}
	userChangeError(t, s, "PATCH", "/api/tasks/"+tid, map[string]any{"version": task["version"], "assignee_member_id": stranger["id"]}, 400, "validation")
	task = exportJSON(t, s, "PATCH", "/api/tasks/"+tid, map[string]any{"version": task["version"], "assignee_member_id": second["id"]}, 200)
	roster = exportJSON(t, s, "GET", membersPath, nil, 200)
	if userChangeHasID(userChangeItem(t, roster, first["id"].(string))["task_ids"], tid) || !userChangeHasID(userChangeItem(t, roster, second["id"].(string))["task_ids"], tid) {
		t.Fatal("LAN assignment diverged from task assignee", roster)
	}
	task = exportJSON(t, s, "PATCH", "/api/tasks/"+tid, map[string]any{"version": task["version"], "status": "done", "progress": 100}, 200)
	userChangeError(t, s, "PATCH", "/api/tasks/"+tid, map[string]any{"version": task["version"], "assignee_member_id": first["id"]}, 409, "read_only")
	userChangeError(t, s, "PATCH", "/api/tasks/"+tid, map[string]any{"version": task["version"], "assignee": "新的手输负责人"}, 409, "read_only")
	userChangeError(t, s, "POST", "/api/lan/members", map[string]any{"name": "不可新派已完成任务", "project_ids": []any{pid}, "task_ids": []any{tid}}, 409, "read_only")
	retained := exportJSON(t, s, "GET", "/api/tasks/"+tid, nil, 200)
	if retained["assignee_member_id"] != second["id"] || retained["status"] != "done" || retained["version"] != task["version"] {
		t.Fatal("completed assignment mutated", retained)
	}
}

func TestUserChangesTaskReminderHTTPRequiresOpenTaskAndVersion(t *testing.T) {
	s := testServer(t)
	p := exportJSON(t, s, "POST", "/api/projects", map[string]any{"name": "任务提醒项目"}, 201)
	pid := p["id"].(string)
	parent := exportJSON(t, s, "POST", "/api/tasks", map[string]any{"project_id": pid, "name": "汇总任务"}, 201)
	task := exportJSON(t, s, "POST", "/api/tasks", map[string]any{"project_id": pid, "parent_id": parent["id"], "name": "桌面通知任务"}, 201)
	tid := task["id"].(string)
	due := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	userChangeError(t, s, "POST", "/api/task-reminders", map[string]any{"title": "不能单独新增的提醒正文", "due_at": due}, 400, "validation")
	userChangeError(t, s, "POST", "/api/task-reminders", map[string]any{"task_id": parent["id"], "due_at": due}, 409, "read_only")
	reminder := exportJSON(t, s, "POST", "/api/task-reminders", map[string]any{"task_id": tid, "due_at": due}, 201)
	rid := reminder["id"].(string)
	if reminder["task_name"] != "桌面通知任务" || reminder["project_id"] != pid {
		t.Fatal("reminder does not project current task", reminder)
	}
	userChangeError(t, s, "POST", "/api/task-reminders", map[string]any{"task_id": tid, "due_at": due}, 409, "conflict")
	userChangeError(t, s, "PATCH", "/api/task-reminders/"+rid, map[string]any{"version": reminder["version"], "due_at": due, "title": "不可改为独立标题"}, 400, "validation")
	nextDue := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339)
	updated := exportJSON(t, s, "PATCH", "/api/task-reminders/"+rid, map[string]any{"version": reminder["version"], "due_at": nextDue}, 200)
	userChangeError(t, s, "PATCH", "/api/task-reminders/"+rid, map[string]any{"version": reminder["version"], "due_at": due}, 409, "version_conflict")
	today := exportJSON(t, s, "GET", "/api/today", nil, 200)
	if len(today["task_reminders"].([]any)) != 1 || today["stats"].(map[string]any)["task_reminders_due"] != float64(1) {
		t.Fatal("today omits task reminder", today)
	}
	state := exportJSON(t, s, "GET", "/api/task-reminders/status", nil, 200)
	if _, ok := state["supported"].(bool); !ok || state["description"] == "" {
		t.Fatal("missing desktop delivery capability", state)
	}
	ended := exportJSON(t, s, "POST", "/api/task-reminders/"+rid+"/done", map[string]any{"version": updated["version"]}, 200)
	if ended["done_at"] == "" {
		t.Fatal("reminder not ended", ended)
	}
	unchanged := exportJSON(t, s, "GET", "/api/tasks/"+tid, nil, 200)
	if unchanged["status"] != task["status"] || unchanged["progress"] != task["progress"] || unchanged["version"] != task["version"] {
		t.Fatal("ending reminder completed the task", unchanged)
	}
	reminder = exportJSON(t, s, "POST", "/api/task-reminders", map[string]any{"task_id": tid, "due_at": due}, 201)
	task = exportJSON(t, s, "PATCH", "/api/tasks/"+tid, map[string]any{"version": task["version"], "status": "done", "progress": 100}, 200)
	pending := exportJSON(t, s, "GET", "/api/task-reminders?status=pending", nil, 200)
	if pending["total"] != float64(0) {
		t.Fatal("completed task still pending reminder", pending)
	}
	userChangeError(t, s, "POST", "/api/task-reminders", map[string]any{"task_id": tid, "due_at": due}, 409, "read_only")
	history := exportJSON(t, s, "GET", "/api/task-reminders?status=done", nil, 200)
	if history["total"] != float64(2) {
		t.Fatal("ended reminder history lost", history)
	}
	if userChangeItem(t, history, reminder["id"].(string))["task_status"] != "done" {
		t.Fatal("completed history failed current task projection")
	}
}

func stringMustJSON(value any) string { raw, _ := json.Marshal(value); return string(raw) }
