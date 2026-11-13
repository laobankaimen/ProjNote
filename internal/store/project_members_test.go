package store

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"
)

func TestV10ProjectRosterBackfillsStableGrantsWithoutRewritingAuditOrSnapshots(t *testing.T) {
	root := t.TempDir()
	db := legacyV8(t, root)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateV9(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	old := &Store{DB: db}
	p := mustCreate(t, old, "projects", map[string]any{"name": "schema9项目"})
	task := mustCreate(t, old, "tasks", map[string]any{"project_id": p["id"], "name": "稳定ID回填", "assignee": "旧显示姓名"})
	plan := savePlan(t, old, p, "schema9原样保留")
	memberID := uid()
	if _, err = db.Exec("INSERT INTO lan_members(id,name,enabled,created_at,updated_at)VALUES(?,?,1,?,?)", memberID, "王工", now(), now()); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO lan_member_projects(member_id,project_id)VALUES(?,?)", memberID, p["id"]); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO lan_member_tasks(task_id,member_id)VALUES(?,?)", task["id"], memberID); err != nil {
		t.Fatal(err)
	}
	var tail string
	if err = db.QueryRow("SELECT hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&tail); err != nil {
		t.Fatal(err)
	}
	count := sqlCount(t, old, "audit")
	old.Close()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	after := assertTaskMember(t, s, task["id"], memberID, "旧显示姓名")
	if after["version"] != task["version"] || after["updated_at"] != task["updated_at"] {
		t.Fatal("migration changed task version", after, task)
	}
	gotPlan, err := s.GetPlanVersion(text(plan["id"]))
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(plan)
	gotJSON, _ := json.Marshal(gotPlan)
	if string(wantJSON) != string(gotJSON) {
		t.Fatal("migration rewrote immutable plan JSON")
	}
	var gotTail string
	s.DB.QueryRow("SELECT hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&gotTail)
	if gotTail != tail || count != sqlCount(t, s, "audit") {
		t.Fatal("migration rewrote audit history")
	}
	list, err := s.ProjectMembers(text(p["id"]), nil)
	if err != nil || list["total"] != 1 || mapItems(list["items"])[0]["id"] != memberID {
		t.Fatal("existing LAN member not visible in project roster", list, err)
	}
	assertChain(t, s)
}

func mustProjectMember(t *testing.T, s *Store, projectID any, name string) map[string]any {
	t.Helper()
	m, err := s.AddProjectMember(text(projectID), map[string]any{"name": name})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func memberNow(t *testing.T, s *Store, id any) map[string]any {
	t.Helper()
	m, err := lanMember(s.DB, text(id), false)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func assertTaskMember(t *testing.T, s *Store, taskID, memberID any, name string) map[string]any {
	t.Helper()
	task := getNow(t, s, "tasks", text(taskID))
	if text(task["assignee_member_id"]) != text(memberID) || text(task["assignee"]) != name {
		t.Fatalf("task stable owner mismatch: %v", task)
	}
	var grant string
	err := s.DB.QueryRow("SELECT member_id FROM lan_member_tasks WHERE task_id=?", taskID).Scan(&grant)
	if text(memberID) == "" && err != sql.ErrNoRows || text(memberID) != "" && (err != nil || grant != text(memberID)) {
		t.Fatalf("grant mismatch: %s %v", grant, err)
	}
	return task
}

func TestProjectMembersSingleUserStableIdentityJoinListAndRemoval(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "名单项目"})
	p2 := mustCreate(t, s, "projects", map[string]any{"name": "另一项目"})
	m1 := mustProjectMember(t, s, p["id"], "同名成员")
	m2 := mustProjectMember(t, s, p["id"], "同名成员")
	if m1["id"] == m2["id"] {
		t.Fatal("same names merged identities")
	}
	settings, _ := s.Settings()
	if settings["lan_enabled"] != "0" {
		t.Fatal("single-user roster enabled LAN")
	}
	list, err := s.ProjectMembers(text(p["id"]), map[string]string{"limit": "1"})
	if err != nil || list["total"] != 2 || len(mapItems(list["items"])) != 1 {
		t.Fatal(list, err)
	}
	_, token := mustLANSession(t, s, m1)
	m1, err = s.AddProjectMember(text(p2["id"]), map[string]any{"member_id": m1["id"], "version": m1["version"]})
	if err != nil || len(m1["project_ids"].([]any)) != 2 {
		t.Fatal(m1, err)
	}
	_, err = s.LANSession(token)
	assertStatus(t, err, 401)
	_, err = s.AddProjectMember(text(p2["id"]), map[string]any{"member_id": m1["id"], "version": m1["version"]})
	assertStatus(t, err, 409)
	_, err = s.UpdateProjectMember(text(p["id"]), text(m1["id"]), map[string]any{"version": 1, "remove": true})
	assertStatus(t, err, 409)
	m1, err = s.UpdateProjectMember(text(p["id"]), text(m1["id"]), map[string]any{"version": m1["version"], "remove": true})
	if err != nil || len(m1["project_ids"].([]any)) != 1 {
		t.Fatal(m1, err)
	}
	if _, err = lanScope(s.DB, text(m1["id"]), text(p["id"]), false); err == nil {
		t.Fatal("former project permission retained")
	}
	if _, err = lanScope(s.DB, text(m1["id"]), text(p2["id"]), false); err != nil {
		t.Fatal("unrelated project permission removed", err)
	}
	assertChain(t, s)
}

func TestTaskStableAssignmentSynchronizesLANBothDirectionsAndRejectsForeignMembers(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "共同指派"})
	p2 := mustCreate(t, s, "projects", map[string]any{"name": "不可串权"})
	m1 := mustProjectMember(t, s, p["id"], "王工")
	m2 := mustProjectMember(t, s, p["id"], "王工")
	foreign := mustProjectMember(t, s, p2["id"], "王工")
	_, token1 := mustLANSession(t, s, m1)
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "按ID分配", "assignee_member_id": m1["id"], "assignee": "客户端旧名"})
	task = assertTaskMember(t, s, task["id"], m1["id"], "王工")
	_, err := s.LANSession(token1)
	assertStatus(t, err, 401)
	_, err = s.UpdateLANTask(text(m2["id"]), text(task["id"]), map[string]any{"version": task["version"], "progress": 20})
	assertStatus(t, err, 403)
	_, err = s.Update("tasks", text(task["id"]), map[string]any{"version": task["version"], "assignee_member_id": foreign["id"]})
	assertStatus(t, err, 400)
	task = assertTaskMember(t, s, task["id"], m1["id"], "王工")
	// Reassigning between equal names must still withdraw the old stable grant.
	task = mustUpdate(t, s, "tasks", task, map[string]any{"assignee_member_id": m2["id"]})
	assertTaskMember(t, s, task["id"], m2["id"], "王工")
	_, err = s.UpdateLANTask(text(m1["id"]), text(task["id"]), map[string]any{"version": task["version"], "progress": 20})
	assertStatus(t, err, 403)
	task, err = s.UpdateLANTask(text(m2["id"]), text(task["id"]), map[string]any{"version": task["version"], "progress": 20, "status": "doing"})
	if err != nil {
		t.Fatal(err)
	}
	assertTaskMember(t, s, task["id"], m2["id"], "王工")
	m2 = memberNow(t, s, m2["id"])
	m2, err = s.UpdateLANMember(text(m2["id"]), map[string]any{"version": m2["version"], "task_ids": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	assertTaskMember(t, s, task["id"], "", "")
	m1 = memberNow(t, s, m1["id"])
	m1, err = s.UpdateLANMember(text(m1["id"]), map[string]any{"version": m1["version"], "task_ids": []any{task["id"]}})
	if err != nil {
		t.Fatal(err)
	}
	task = assertTaskMember(t, s, task["id"], m1["id"], "王工")
	task = mustUpdate(t, s, "tasks", task, map[string]any{"assignee_member_id": ""})
	assertTaskMember(t, s, task["id"], "", "")
	assertChain(t, s)
}

func TestCompletedTaskCannotBeNewlyAssignedThroughAnyMemberEntry(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "结束历史"})
	m1 := mustProjectMember(t, s, p["id"], "原负责人")
	m2 := mustProjectMember(t, s, p["id"], "新负责人")
	for _, status := range []string{"done", "canceled"} {
		unassigned := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "已结束未分配" + status, "status": status})
		_, err := s.UpdateLANMember(text(m2["id"]), map[string]any{"version": m2["version"], "task_ids": []any{unassigned["id"]}})
		assertStatus(t, err, 409)
		_, err = s.Update("tasks", text(unassigned["id"]), map[string]any{"version": unassigned["version"], "assignee_member_id": m2["id"]})
		assertStatus(t, err, 409)
		_, err = s.Update("tasks", text(unassigned["id"]), map[string]any{"version": unassigned["version"], "assignee": "新负责人"})
		assertStatus(t, err, 409)
		assigned := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "保留历史负责人" + status, "assignee_member_id": m1["id"]})
		assigned = mustUpdate(t, s, "tasks", assigned, map[string]any{"status": status})
		assigned = getNow(t, s, "tasks", text(assigned["id"]))
		m1 = memberNow(t, s, m1["id"])
		m1, err = s.UpdateLANMember(text(m1["id"]), map[string]any{"version": m1["version"], "name": "修改全局姓名"})
		if err != nil {
			t.Fatal(err)
		}
		if got := getNow(t, s, "tasks", text(assigned["id"])); !reflect.DeepEqual(got, assigned) {
			t.Fatal("ended task history changed with global name", got, assigned)
		}
	}
	assertChain(t, s)
}

func TestProjectMemberRemovalRequiresOutstandingWorkResolvedAndPreservesHistory(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "退出项目"})
	m := mustProjectMember(t, s, p["id"], "项目成员")
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "先处理待办", "assignee_member_id": m["id"]})
	m = memberNow(t, s, m["id"])
	_, err := s.UpdateProjectMember(text(p["id"]), text(m["id"]), map[string]any{"version": m["version"], "remove": true})
	assertStatus(t, err, 409)
	task = mustUpdate(t, s, "tasks", task, map[string]any{"status": "done"})
	task = getNow(t, s, "tasks", text(task["id"]))
	_, token := mustLANSession(t, s, m)
	m, err = s.UpdateProjectMember(text(p["id"]), text(m["id"]), map[string]any{"version": m["version"], "remove": true})
	if err != nil || len(m["project_ids"].([]any)) != 0 {
		t.Fatal(m, err)
	}
	_, err = s.LANSession(token)
	assertStatus(t, err, 401)
	if got := getNow(t, s, "tasks", text(task["id"])); !reflect.DeepEqual(got, task) {
		t.Fatal("project leave rewrote completed ownership history", got, task)
	}
	var grants int
	s.DB.QueryRow("SELECT count(*) FROM lan_member_tasks WHERE task_id=?", task["id"]).Scan(&grants)
	if grants != 0 {
		t.Fatal("project leave retained a task grant")
	}
	assertChain(t, s)
}

func TestStableAssignmentBatchVersionAndAuditFailureAreAtomic(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "原子分配"})
	m := mustProjectMember(t, s, p["id"], "成员")
	a := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "任务一"})
	b := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "任务二"})
	_, err := s.BatchTasks(map[string]any{"items": []any{map[string]any{"id": a["id"], "version": a["version"]}, map[string]any{"id": b["id"], "version": 999}}, "changes": map[string]any{"assignee_member_id": m["id"]}})
	assertStatus(t, err, 409)
	assertTaskMember(t, s, a["id"], "", "")
	if _, err = s.DB.Exec("CREATE TRIGGER reject_assignment_audit BEFORE INSERT ON audit WHEN NEW.action='task_scope' BEGIN SELECT RAISE(ABORT,'scope audit failed');END"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Update("tasks", text(a["id"]), map[string]any{"version": a["version"], "assignee_member_id": m["id"]})
	if err == nil {
		t.Fatal("audit failure hidden")
	}
	assertTaskMember(t, s, a["id"], "", "")
	if got := memberNow(t, s, m["id"]); !reflect.DeepEqual(got, m) {
		t.Fatal("partial member scope version", got, m)
	}
	s.DB.Exec("DROP TRIGGER reject_assignment_audit")
	_, err = s.BatchTasks(map[string]any{"items": []any{map[string]any{"id": a["id"], "version": a["version"]}, map[string]any{"id": b["id"], "version": b["version"]}}, "changes": map[string]any{"assignee_member_id": m["id"]}})
	if err != nil {
		t.Fatal(err)
	}
	assertTaskMember(t, s, a["id"], m["id"], "成员")
	assertTaskMember(t, s, b["id"], m["id"], "成员")
	assertChain(t, s)
}

func TestRetainedDisabledAssigneeDoesNotBlockOwnerEditsOrRegrantHistory(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "保留停用人员"})
	m := mustProjectMember(t, s, p["id"], "停用前负责人")
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "原有任务", "assignee_member_id": m["id"]})
	m = memberNow(t, s, m["id"])
	m, err := s.UpdateLANMember(text(m["id"]), map[string]any{"version": m["version"], "enabled": false})
	if err != nil {
		t.Fatal(err)
	}
	task = mustUpdate(t, s, "tasks", task, map[string]any{"description": "本机负责人继续整理", "assignee_member_id": m["id"]})
	assertTaskMember(t, s, task["id"], m["id"], "停用前负责人")
	task = mustUpdate(t, s, "tasks", task, map[string]any{"status": "canceled"})
	m, err = s.UpdateProjectMember(text(p["id"]), text(m["id"]), map[string]any{"version": m["version"], "remove": true})
	if err != nil {
		t.Fatal(err)
	}
	task = mustUpdate(t, s, "tasks", task, map[string]any{"description": "结束后留档", "assignee_member_id": m["id"]})
	if task["assignee_member_id"] != m["id"] {
		t.Fatal(task)
	}
	var grants int
	s.DB.QueryRow("SELECT count(*) FROM lan_member_tasks WHERE task_id=?", task["id"]).Scan(&grants)
	if grants != 0 {
		t.Fatal("retaining historical owner recreated a grant")
	}
	assertChain(t, s)
}
