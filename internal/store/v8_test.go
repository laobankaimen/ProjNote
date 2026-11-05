package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func legacyV7(t *testing.T, root string) *sql.DB {
	t.Helper()
	db := legacyV6(t, root)
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = migrateV7(tx); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return db
}
func requirementInput(pid any) map[string]any {
	return map[string]any{"project_id": pid, "title": "温控告警需求", "description": "现场温度超过阈值时发出告警并保留记录。", "acceptance_criteria": "模拟超温，核对告警和记录各出现一次。"}
}
func savePlan(t *testing.T, s *Store, p map[string]any, name string) map[string]any {
	t.Helper()
	p = getNow(t, s, "projects", text(p["id"]))
	m, e := s.CreatePlanVersion(text(p["id"]), map[string]any{"project_version": p["version"], "name": name})
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func frozenTasks(t *testing.T, m map[string]any) []map[string]any {
	t.Helper()
	snap, ok := m["snapshot"].(map[string]any)
	if !ok {
		t.Fatal("snapshot must be object", m)
	}
	return mapItems(snap["tasks"])
}
func sqlCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if e := s.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}

func TestV8MigrationPreservesV7DataDraftSearchFrozenExportAndAudit(t *testing.T) {
	root := t.TempDir()
	old := &Store{DB: legacyV7(t, root)}
	p := mustCreate(t, old, "projects", map[string]any{"name": "升级前中文项目"})
	input := meetingInput()
	input["project_id"] = p["id"]
	meeting := mustCreate(t, old, "meeting_minutes", input)
	draft, e := old.SaveDesktopDraft(map[string]any{"version": 1, "mode": "task", "content": "保留未提交计划", "project_id": p["id"]})
	if e != nil {
		t.Fatal(e)
	}
	log := mustCreate(t, old, "worklogs", map[string]any{"project_id": p["id"], "period_type": "month", "log_date": "2026-11-05", "content": "既有月报不可重写"})
	job, e := old.CreateExportJob(map[string]any{"source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"], "format": "md"})
	if e != nil {
		t.Fatal(e)
	}
	var hash string
	old.DB.QueryRow("SELECT hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&hash)
	count := sqlCount(t, old, "audit")
	settings, _ := old.Settings()
	settings["lan_enabled"] = "0"
	settings["lan_port"] = "18680"
	old.Close()
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var schema int
	s.DB.QueryRow("PRAGMA user_version").Scan(&schema)
	if schema != SchemaVersion {
		t.Fatal(schema)
	}
	for _, pair := range []struct {
		k string
		m map[string]any
	}{{"meeting_minutes", meeting}, {"worklogs", log}, {"projects", p}} {
		if got := getNow(t, s, pair.k, text(pair.m["id"])); !reflect.DeepEqual(got, pair.m) {
			t.Fatal("migration changed old entity", got, pair.m)
		}
	}
	if got, e := s.DesktopDraft(); e != nil || !reflect.DeepEqual(got, draft) {
		t.Fatal(got, e)
	}
	if got, e := s.GetExportJob(text(job["id"])); e != nil || !reflect.DeepEqual(got, job) {
		t.Fatal(got, e)
	}
	if got, e := s.Settings(); e != nil || !reflect.DeepEqual(got, settings) {
		t.Fatal(got, e)
	}
	var tail string
	s.DB.QueryRow("SELECT hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&tail)
	if tail != hash || sqlCount(t, s, "audit") != count {
		t.Fatal("audit history rewritten")
	}
	if got, e := s.SearchPage(map[string]string{"q": "既有月报不可重写"}); e != nil || got["total"] != 1 {
		t.Fatal(got, e)
	}
	assertChain(t, s)
}
func TestV8MigrationLateFailureIsAtomicAndRetryable(t *testing.T) {
	root := t.TempDir()
	db := legacyV7(t, root)
	if _, e := db.Exec("CREATE INDEX requirement_task_links_task ON settings(value);INSERT INTO desktop_drafts(key,content,project_id,version,updated_at,mode)VALUES('quick_note','草稿不丢','',9,'2026-11-05T00:00:00Z','task')"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if s, e := Open(root); e == nil {
		s.Close()
		t.Fatal("migration collision accepted")
	}
	db, e := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if e != nil {
		t.Fatal(e)
	}
	var version, n int
	var draft string
	db.QueryRow("PRAGMA user_version").Scan(&version)
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN ('plan_versions','requirements','requirement_task_links','plan_versions_immutable_update')").Scan(&n)
	db.QueryRow("SELECT content FROM desktop_drafts").Scan(&draft)
	if version != 7 || n != 0 || draft != "草稿不丢" {
		t.Fatal("partial upgrade", version, n, draft)
	}
	if _, e = db.Exec("DROP INDEX requirement_task_links_task"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	got, e := s.DesktopDraft()
	if e != nil || got["content"] != draft || versionOf(got["version"]) != 9 || got["mode"] != "task" {
		t.Fatal(got, e)
	}
}
func TestPlanVersionFullFrozenTreeReadOnlySearchHistoryAndProjectProtection(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "冻结项目名称"})
	root := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "完整WBS"})
	// More than the default List page proves snapshot capture reads the full tree.
	rows := []any{}
	for i := 0; i < 106; i++ {
		rows = append(rows, map[string]any{"key": fmt.Sprint(i), "name": fmt.Sprintf("计划任务%03d", i), "estimate_hours": 1})
	}
	_, e := s.ImportTasks(text(p["id"]), map[string]any{"project_version": p["version"], "source_hash": strings.Repeat("a", 64), "rows": rows})
	if e != nil {
		t.Fatal(e)
	}
	child := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": root["id"], "name": "冻结子任务", "assignee": "张三", "plan_start": "2026-11-05", "plan_end": "2026-11-10", "estimate_hours": 3, "description": "快照描述"})
	if e = s.SetSettings(map[string]any{"operator": "计划负责人"}); e != nil {
		t.Fatal(e)
	}
	pv := savePlan(t, s, p, "首次安排_%")
	if versionOf(pv["number"]) != 1 || versionOf(pv["version"]) != 1 || pv["created_by"] != "计划负责人" || versionOf(pv["task_count"]) != 108 {
		t.Fatal(pv)
	}
	tasks := frozenTasks(t, pv)
	if len(tasks) != 108 || tasks[0]["id"] != root["id"] || tasks[1]["id"] != child["id"] || num(tasks[1]["depth"]) != 1 || tasks[0]["has_children"] != true {
		t.Fatal("not complete DFS", tasks[:2])
	}
	for _, task := range tasks {
		for _, k := range []string{"progress", "actual_hours", "actual_start", "status"} {
			if _, ok := task[k]; ok {
				t.Fatal("actual field in plan", k)
			}
		}
	}
	raw, _ := json.Marshal(pv)
	p = mustUpdate(t, s, "projects", getNow(t, s, "projects", text(p["id"])), map[string]any{"name": "现在项目名"})
	child = mustUpdate(t, s, "tasks", child, map[string]any{"plan_end": "2026-11-12", "assignee": "李四"})
	got, e := s.GetPlanVersion(text(pv["id"]))
	if e != nil {
		t.Fatal(e)
	}
	after, _ := json.Marshal(got)
	if string(after) != string(raw) || got["snapshot"].(map[string]any)["project"].(map[string]any)["name"] != "冻结项目名称" {
		t.Fatal("frozen plan changed")
	}
	for _, statement := range []string{"UPDATE plan_versions SET name='篡改' WHERE id=?", "DELETE FROM plan_versions WHERE id=?"} {
		if _, e = s.DB.Exec(statement, pv["id"]); e == nil {
			t.Fatal("database allowed immutable plan mutation")
		}
	}
	_, e = s.Create("plan_versions", nil)
	assertStatus(t, e, 409)
	_, e = s.Update("plan_versions", text(pv["id"]), map[string]any{"version": 1})
	assertStatus(t, e, 409)
	_, e = s.SoftDelete("plan_versions", text(pv["id"]), map[string]any{"version": 1})
	assertStatus(t, e, 409)
	_, e = s.RestoreTrash("plan_versions", text(pv["id"]), map[string]any{"version": 1})
	assertStatus(t, e, 409)
	_, e = s.CreatePlanVersion(text(p["id"]), map[string]any{"name": "旧项目版本", "project_version": 1})
	assertStatus(t, e, 409)
	other := mustCreate(t, s, "projects", map[string]any{"name": "第二项目"})
	savePlan(t, s, other, "空计划")
	all, e := s.ListPlanVersions("", map[string]string{"limit": "1", "offset": "1"})
	if e != nil || all["total"] != 2 || len(mapItems(all["items"])) != 1 {
		t.Fatal(all, e)
	}
	page, e := s.ListPage("plan_versions", map[string]string{"project_id": text(p["id"]), "q": "_%"})
	if e != nil || page["total"] != 1 {
		t.Fatal(page, e)
	}
	if _, exists := mapItems(page["items"])[0]["snapshot"]; exists {
		t.Fatal("list must omit large snapshot")
	}
	_, e = s.ListPlanVersions("missing", nil)
	assertStatus(t, e, 404)
	found, e := s.SearchPage(map[string]string{"q": "首次安排_%", "kind": "plan_versions"})
	if e != nil || found["total"] != 1 {
		t.Fatal(found, e)
	}
	history, e := s.EntityHistory("plan_versions", text(pv["id"]), nil)
	if e != nil || history["total"] != 1 {
		t.Fatal(history, e)
	}
	_, e = s.SoftDelete("projects", text(other["id"]), map[string]any{"version": other["version"]})
	assertStatus(t, e, 409)
	assertChain(t, s)
}
func TestPlanComparisonStableIDsMovesAndActualsDoNotChangePlan(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "计划对比"})
	a := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "工作包"})
	b := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "拟调整", "assignee": "张三", "estimate_hours": 2})
	gone := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "待删除"})
	first := savePlan(t, s, p, "调整前")
	b = mustUpdate(t, s, "tasks", b, map[string]any{"status": "doing", "progress": 25})
	_, e := s.CreateTimeEntry(map[string]any{"project_id": p["id"], "task_id": b["id"], "minutes": 30, "work_date": "2026-11-05"})
	if e != nil {
		t.Fatal(e)
	}
	cmp, e := s.ComparePlanVersions(text(p["id"]), text(first["id"]), "")
	if e != nil || num(cmp["summary"].(map[string]any)["total"]) != 0 {
		t.Fatal("actual state appeared in planning comparison", cmp, e)
	}
	b = mustUpdate(t, s, "tasks", b, map[string]any{"name": "已调整", "plan_end": "2026-11-20", "assignee": "李四", "estimate_hours": 5})
	_, e = s.MoveTask(text(b["id"]), map[string]any{"version": b["version"], "parent_id": a["id"], "expected_versions": map[string]any{text(a["id"]): getNow(t, s, "tasks", text(a["id"]))["version"]}})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Delete("tasks", text(gone["id"]), versionOf(getNow(t, s, "tasks", text(gone["id"]))["version"])); e != nil {
		t.Fatal(e)
	}
	added := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "新工作"})
	second := savePlan(t, s, p, "调整后")
	cmp, e = s.ComparePlanVersions(text(p["id"]), text(first["id"]), text(second["id"]))
	if e != nil {
		t.Fatal(e)
	}
	summary := cmp["summary"].(map[string]any)
	if num(summary["added"]) != 1 || num(summary["removed"]) != 1 || num(summary["moved"]) != 1 {
		t.Fatal(cmp)
	}
	if mapItems(cmp["added"])[0]["id"] != added["id"] || mapItems(cmp["removed"])[0]["id"] != gone["id"] {
		t.Fatal("compared by row rather than stable ID", cmp)
	}
	matched := false
	for _, change := range mapItems(cmp["changed"]) {
		if change["id"] == b["id"] {
			matched = change["moved"] == true
			fields := map[string]bool{}
			for _, field := range mapItems(change["fields"]) {
				fields[text(field["field"])] = true
			}
			for _, key := range []string{"parent_id", "name", "plan_end", "assignee", "estimate_hours"} {
				if !fields[key] {
					t.Fatal("missing changed field", key, change)
				}
			}
		}
	}
	if !matched {
		t.Fatal(cmp)
	}
	same, e := s.ComparePlanVersions(text(p["id"]), text(second["id"]), text(second["id"]))
	if e != nil || num(same["summary"].(map[string]any)["total"]) != 0 {
		t.Fatal(same, e)
	}
	other := mustCreate(t, s, "projects", map[string]any{"name": "异项目"})
	foreign := savePlan(t, s, other, "异项目计划")
	_, e = s.ComparePlanVersions(text(p["id"]), text(first["id"]), text(foreign["id"]))
	assertStatus(t, e, 400)
	assertChain(t, s)
}
func TestRequirementRevisionLinksAcceptanceAndImmutableHistory(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "需求项目"})
	a := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "关联原名称"})
	b := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "第二关联"})
	input := requirementInput(p["id"])
	input["task_ids"] = []any{a["id"]}
	r := mustCreate(t, s, "requirements", input)
	if versionOf(r["revision"]) != 1 || !strings.HasPrefix(text(r["code"]), "REQ-") || len(mapItems(r["linked_tasks"])) != 1 {
		t.Fatal(r)
	}
	first, e := s.GetRequirementRevision(text(r["id"]), 1)
	if e != nil {
		t.Fatal(e)
	}
	firstRaw, _ := json.Marshal(first)
	_, e = s.Update("requirements", text(r["id"]), map[string]any{"version": r["version"], "title": "未说明"})
	assertStatus(t, e, 400)
	_, e = s.Update("requirements", text(r["id"]), map[string]any{"version": r["version"], "change_note": "  ", "title": "空原因"})
	assertStatus(t, e, 400)
	a = mustUpdate(t, s, "tasks", a, map[string]any{"name": "关联现名称", "status": "done"})
	current := getNow(t, s, "requirements", text(r["id"]))
	if current["status"] != "pending" || versionOf(current["revision"]) != 1 || mapItems(current["linked_tasks"])[0]["name"] != "关联现名称" {
		t.Fatal("task status auto accepted requirement", current)
	}
	got, e := s.GetRequirementRevision(text(r["id"]), 1)
	gotRaw, _ := json.Marshal(got)
	if e != nil || string(firstRaw) != string(gotRaw) || mapItems(got["snapshot"].(map[string]any)["linked_tasks"])[0]["name"] != "关联原名称" {
		t.Fatal("revision was not frozen", got, e)
	}
	r = mustUpdate(t, s, "requirements", r, map[string]any{"title": "温控和记录需求", "task_ids": []any{a["id"], b["id"]}, "change_note": "增加第二执行任务"})
	if versionOf(r["revision"]) != 2 {
		t.Fatal(r)
	}
	_, e = s.Update("requirements", text(r["id"]), map[string]any{"version": 1, "title": "并发旧表", "change_note": "旧版本"})
	assertStatus(t, e, 409)
	_, e = s.Update("requirements", text(r["id"]), map[string]any{"version": r["version"], "status": "accepted", "change_note": "准备验收"})
	assertStatus(t, e, 400)
	r = mustUpdate(t, s, "requirements", r, map[string]any{"status": "accepted", "acceptance_note": "已按验收准则核对通过", "change_note": "完成验收"})
	_, e = s.Update("requirements", text(r["id"]), map[string]any{"version": r["version"], "title": "偷偷修改已验收项", "change_note": "修订"})
	assertStatus(t, e, 409)
	r = mustUpdate(t, s, "requirements", r, map[string]any{"status": "doing", "title": "重开后的需求", "change_note": "现场增加告警复位用例，明确重开"})
	_, e = s.Update("requirements", text(r["id"]), map[string]any{"version": r["version"], "status": "accepted", "change_note": "再次验收但没有本次说明"})
	assertStatus(t, e, 400)
	if versionOf(r["revision"]) != 4 {
		t.Fatal(r)
	}
	if _, e = s.DB.Exec("UPDATE requirement_revisions SET change_note='篡改' WHERE requirement_id=?", r["id"]); e == nil {
		t.Fatal("revision UPDATE allowed")
	}
	if _, e = s.DB.Exec("DELETE FROM requirement_revisions WHERE requirement_id=?", r["id"]); e == nil {
		t.Fatal("revision DELETE allowed")
	}
	history, e := s.RequirementRevisions(text(r["id"]), map[string]string{"limit": "2", "offset": "1"})
	if e != nil || history["total"] != 4 || len(mapItems(history["items"])) != 2 || num(mapItems(history["items"])[0]["revision"]) != 3 {
		t.Fatal(history, e)
	}
	diff, e := s.CompareRequirementRevisions(text(r["id"]), 1, 2)
	if e != nil || diff["changed"] != true || len(diff["task_links"].(map[string]any)["added"].([]string)) != 1 {
		t.Fatal(diff, e)
	}
	same, e := s.CompareRequirementRevisions(text(r["id"]), 4, 4)
	if e != nil || same["changed"] != false {
		t.Fatal(same, e)
	}
	assertChain(t, s)
}
func TestRequirementValidationRecycleLinksAndProjectWriteBoundary(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "保护需求"})
	other := mustCreate(t, s, "projects", map[string]any{"name": "其他项目"})
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "可回收关联"})
	foreign := mustCreate(t, s, "tasks", map[string]any{"project_id": other["id"], "name": "不可跨项目"})
	for _, extra := range []map[string]any{{"title": " "}, {"description": ""}, {"acceptance_criteria": ""}, {"code": "手写编号"}, {"revision": 22}, {"task_ids": []any{foreign["id"]}}, {"task_ids": []any{"missing"}}, {"task_ids": []any{task["id"], task["id"]}}} {
		in := requirementInput(p["id"])
		for k, v := range extra {
			in[k] = v
		}
		_, e := s.Create("requirements", in)
		assertStatus(t, e, 400)
	}
	input := requirementInput(p["id"])
	input["task_ids"] = []any{task["id"]}
	r := mustCreate(t, s, "requirements", input)
	if e := s.Delete("tasks", text(task["id"]), versionOf(task["version"])); e != nil {
		t.Fatal(e)
	}
	r = getNow(t, s, "requirements", text(r["id"]))
	if len(mapItems(r["linked_tasks"])) != 1 || !nonempty(mapItems(r["linked_tasks"])[0]["deleted_at"]) {
		t.Fatal("task recycle silently removed requirement link", r)
	}
	r = mustUpdate(t, s, "requirements", r, map[string]any{"owner": "张三", "change_note": "调整负责人", "task_ids": r["task_ids"]})
	newInput := requirementInput(p["id"])
	newInput["task_ids"] = []any{task["id"]}
	_, e := s.Create("requirements", newInput)
	assertStatus(t, e, 400)
	_, e = s.Update("requirements", text(r["id"]), map[string]any{"version": r["version"], "project_id": other["id"], "change_note": "移项目"})
	assertStatus(t, e, 400)
	if results, e := s.SearchPage(map[string]string{"q": "模拟超温", "kind": "requirements"}); e != nil || results["total"] != 1 {
		t.Fatal(results, e)
	}
	if e = s.Delete("requirements", text(r["id"]), versionOf(r["version"])); e != nil {
		t.Fatal(e)
	}
	_, e = s.Get("requirements", text(r["id"]))
	assertStatus(t, e, 404)
	if results, e := s.SearchPage(map[string]string{"q": "模拟超温", "kind": "requirements"}); e != nil || results["total"] != 0 {
		t.Fatal(results, e)
	}
	trash, e := s.ListTrash(map[string]string{"kind": "requirements"})
	if e != nil || trash["total"] != 1 || len(mapItems(mapItems(trash["items"])[0]["linked_tasks"])) != 1 {
		t.Fatal(trash, e)
	}
	tr := mapItems(trash["items"])[0]
	rev := tr["revision"]
	r, e = s.RestoreTrash("requirements", text(r["id"]), map[string]any{"version": tr["version"]})
	if e != nil || r["revision"] != rev {
		t.Fatal("recycle must not create business revision", r, e)
	}
	p = mustUpdate(t, s, "projects", p, map[string]any{"status": "closed"})
	for _, fn := range []func() error{func() error { _, e := s.Create("requirements", requirementInput(p["id"])); return e }, func() error {
		_, e := s.Update("requirements", text(r["id"]), map[string]any{"version": r["version"], "project_id": other["id"], "change_note": "不能逃离关闭项目"})
		return e
	}, func() error { return s.Delete("requirements", text(r["id"]), versionOf(r["version"])) }, func() error {
		_, e := s.CreatePlanVersion(text(p["id"]), map[string]any{"name": "不能保存关闭项目", "project_version": p["version"]})
		return e
	}} {
		assertStatus(t, fn(), 409)
	}
	p = mustUpdate(t, s, "projects", p, map[string]any{"status": "active"})
	if e = s.Delete("requirements", text(r["id"]), versionOf(r["version"])); e != nil {
		t.Fatal(e)
	}
	tr, _ = getIncludingTrash(s.DB, "requirements", text(r["id"]), true)
	p = mustUpdate(t, s, "projects", p, map[string]any{"status": "closed"})
	_, e = s.RestoreTrash("requirements", text(r["id"]), map[string]any{"version": tr["version"]})
	assertStatus(t, e, 409)
	assertChain(t, s)
}
func TestPlanAndRequirementFailuresRollbackSnapshotsLinksSearchAndAudit(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "事务失败"})
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "链接"})
	baseAudit := sqlCount(t, s, "audit")
	if _, e := s.DB.Exec("CREATE TRIGGER fail_new_audit BEFORE INSERT ON audit WHEN NEW.entity_type IN ('requirements','plan_versions') BEGIN SELECT RAISE(ABORT,'simulate audit failure');END;"); e != nil {
		t.Fatal(e)
	}
	_, e := s.CreatePlanVersion(text(p["id"]), map[string]any{"name": "失败计划", "project_version": p["version"]})
	if e == nil {
		t.Fatal("audit failure hidden")
	}
	in := requirementInput(p["id"])
	in["task_ids"] = []any{task["id"]}
	_, e = s.Create("requirements", in)
	if e == nil {
		t.Fatal("audit failure hidden")
	}
	for _, table := range []string{"plan_versions", "requirements", "requirement_task_links", "requirement_revisions"} {
		if sqlCount(t, s, table) != 0 {
			t.Fatal("partial failed create", table)
		}
	}
	if sqlCount(t, s, "audit") != baseAudit {
		t.Fatal("partial audit")
	}
	var docs int
	s.DB.QueryRow("SELECT count(*) FROM search_doc WHERE entity_type IN ('requirements','plan_versions')").Scan(&docs)
	if docs != 0 {
		t.Fatal("partial search")
	}
	s.DB.Exec("DROP TRIGGER fail_new_audit")
	r := mustCreate(t, s, "requirements", in)
	savePlan(t, s, p, "成功计划")
	before, _ := json.Marshal(r)
	baseAudit = sqlCount(t, s, "audit")
	if _, e = s.DB.Exec("CREATE TRIGGER fail_revision_insert BEFORE INSERT ON requirement_revisions WHEN NEW.revision=2 BEGIN SELECT RAISE(ABORT,'simulate snapshot failure');END;"); e != nil {
		t.Fatal(e)
	}
	_, e = s.Update("requirements", text(r["id"]), map[string]any{"version": r["version"], "title": "失败标题", "task_ids": []any{}, "change_note": "触发失败"})
	if e == nil {
		t.Fatal("snapshot failure hidden")
	}
	got := getNow(t, s, "requirements", text(r["id"]))
	after, _ := json.Marshal(got)
	if string(before) != string(after) || sqlCount(t, s, "requirement_revisions") != 1 || sqlCount(t, s, "audit") != baseAudit {
		t.Fatal("partial requirement update", got)
	}
	if result, e := s.SearchPage(map[string]string{"q": "失败标题"}); e != nil || result["total"] != 0 {
		t.Fatal(result, e)
	}
	s.DB.Exec("DROP TRIGGER fail_revision_insert")
	mustUpdate(t, s, "requirements", r, map[string]any{"title": "重试成功", "change_note": "重试"})
	assertChain(t, s)
}

func TestRequirementOnlyProjectProtectionAndArchivedPlanBoundary(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "只有需求的项目"})
	r := mustCreate(t, s, "requirements", requirementInput(p["id"]))
	if e := s.Delete("requirements", text(r["id"]), versionOf(r["version"])); e != nil {
		t.Fatal(e)
	}
	_, e := s.SoftDelete("projects", text(p["id"]), map[string]any{"version": p["version"]})
	assertStatus(t, e, 409)
	trashed, _ := getIncludingTrash(s.DB, "requirements", text(r["id"]), true)
	p = mustUpdate(t, s, "projects", p, map[string]any{"status": "archived"})
	_, e = s.RestoreTrash("requirements", text(r["id"]), map[string]any{"version": trashed["version"]})
	assertStatus(t, e, 409)
	_, e = s.CreatePlanVersion(text(p["id"]), map[string]any{"name": "归档禁止保存", "project_version": p["version"]})
	assertStatus(t, e, 409)
	if count := sqlCount(t, s, "requirement_revisions"); count != 1 {
		t.Fatal("recycle/failed writes appended business revisions", count)
	}
	assertChain(t, s)
}
