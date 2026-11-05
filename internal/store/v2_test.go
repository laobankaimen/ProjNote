package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func getNow(t *testing.T, s *Store, kind, id string) map[string]any {
	t.Helper()
	m, e := s.Get(kind, id)
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestMigrationV1KeepsDataAndSettings(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Use a frozen v1 schema fixture independent of the current migrations.
	source, err := os.ReadFile("testdata/v1.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := string(source)
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO projects(id,code,name,created_at,updated_at)VALUES('legacy','LEGACY','迁移前项目','2026-11-04T00:00:00Z','2026-11-04T00:00:00Z'); UPDATE settings SET value='王工' WHERE key='operator'"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	m := getNow(t, s, "projects", "legacy")
	if m["name"] != "迁移前项目" || m["deleted_at"] != "" || versionOf(m["version"]) != 1 {
		t.Fatalf("migration changed business: %v", m)
	}
	settings, err := s.Settings()
	if err != nil {
		t.Fatal(err)
	}
	dataset := text(settings["dataset_id"])
	if len(dataset) != 32 || settings["gjb_level"] != "2" || settings["setup_completed"] != "1" || settings["operator"] != "王工" {
		t.Fatalf("migration settings: %v", settings)
	}
	s.Close()
	s, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	settings, err = s.Settings()
	if err != nil || settings["dataset_id"] != dataset {
		t.Fatal("dataset identity changed on reopen")
	}
	var v int
	s.DB.QueryRow("PRAGMA user_version").Scan(&v)
	if v != SchemaVersion {
		t.Fatal("migration version not current")
	}
}
func TestTrashExcludesAllReadsAndRestoreConflicts(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "回收测试"})
	r := mustCreate(t, s, "records", map[string]any{"project_id": p["id"], "title": "仅此回收词", "content": "保留原始附件", "tags": []any{"回收标签"}, "attachments": []any{map[string]any{"id": "keep-file"}}})
	result, err := s.SoftDelete("records", text(r["id"]), map[string]any{"version": r["version"]})
	if err != nil {
		t.Fatal(err)
	}
	deleted := result["items"].([]map[string]any)[0]
	_, err = s.Get("records", text(r["id"]))
	assertStatus(t, err, 404)
	items, total, err := s.List("records", nil)
	if err != nil || len(items) != 0 || total != 0 {
		t.Fatalf("trash leaked List: %v %d %v", items, total, err)
	}
	search, err := s.SearchPage(map[string]string{"q": "回收词"})
	if err != nil || search["total"] != 0 {
		t.Fatalf("trash leaked search: %v %v", search, err)
	}
	oldSearch, err := s.Search("回收词")
	if err != nil || len(oldSearch) != 0 {
		t.Fatal("trash leaked legacy search")
	}
	today, err := s.Today()
	if err != nil || today["stats"].(map[string]any)["records_today"] != 0 {
		t.Fatal("trash leaked today")
	}
	draft, err := s.GenerateLog(map[string]any{"project_id": p["id"]})
	if err != nil || strings.Contains(text(draft["content"]), "仅此回收词") {
		t.Fatal("trash leaked log")
	}
	trash, err := s.ListTrash(nil)
	if err != nil || trash["total"] != 1 {
		t.Fatalf("trash absent: %v %v", trash, err)
	}
	restored, err := s.RestoreTrash("records", text(r["id"]), map[string]any{"version": deleted["version"]})
	if err != nil {
		t.Fatal(err)
	}
	if len(restored["attachments"].([]any)) != 1 || restored["deleted_at"] != "" {
		t.Fatal("restore lost record data")
	}
	search, err = s.SearchPage(map[string]string{"q": "回收词"})
	if err != nil || search["total"] != 1 {
		t.Fatal("restore missing search")
	}
	log := mustCreate(t, s, "worklogs", map[string]any{"project_id": p["id"], "content": "旧日志"})
	if err = s.Delete("worklogs", text(log["id"]), versionOf(log["version"])); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, "worklogs", map[string]any{"project_id": p["id"], "content": "新日志"})
	old, err := getIncludingTrash(s.DB, "worklogs", text(log["id"]), true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RestoreTrash("worklogs", text(log["id"]), map[string]any{"version": old["version"]})
	assertStatus(t, err, 409)
	old, err = getIncludingTrash(s.DB, "worklogs", text(log["id"]), true)
	if err != nil || old["deleted_at"] == "" {
		t.Fatal("restore conflict lost trash")
	}
	assertChain(t, s)
}
func TestTrashTaskCascadeSnapshotsAndParentFirstRestore(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "子树"})
	root := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "根"})
	a := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": root["id"], "name": "子任务"})
	b := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": a["id"], "name": "孙任务", "progress": 20, "status": "doing"})
	root = getNow(t, s, "tasks", text(root["id"]))
	a = getNow(t, s, "tasks", text(a["id"]))
	_, err := s.SoftDelete("tasks", text(root["id"]), map[string]any{"version": root["version"]})
	assertStatus(t, err, 409)
	_, err = s.SoftDelete("tasks", text(root["id"]), map[string]any{"version": root["version"], "cascade": true, "expected_versions": map[string]any{a["id"].(string): a["version"]}})
	assertStatus(t, err, 409)
	out, err := s.SoftDelete("tasks", text(root["id"]), map[string]any{"version": root["version"], "cascade": true, "expected_versions": map[string]any{a["id"].(string): a["version"], b["id"].(string): b["version"]}})
	if err != nil || out["total"] != 3 {
		t.Fatalf("cascade %v %v", out, err)
	}
	trash := out["items"].([]map[string]any)
	byID := map[string]map[string]any{}
	for _, m := range trash {
		byID[text(m["id"])] = m
	}
	_, err = s.RestoreTrash("tasks", text(b["id"]), map[string]any{"version": byID[text(b["id"])]["version"]})
	assertStatus(t, err, 409)
	for _, task := range []map[string]any{root, a, b} {
		if _, err = s.RestoreTrash("tasks", text(task["id"]), map[string]any{"version": byID[text(task["id"])]["version"]}); err != nil {
			t.Fatal(err)
		}
	}
	items, n, err := s.List("tasks", nil)
	if err != nil || n != 3 || len(items) != 3 {
		t.Fatal("subtree restoration incomplete")
	}
	_, err = s.SoftDelete("projects", text(p["id"]), map[string]any{"version": p["version"]})
	assertStatus(t, err, 409)
	assertChain(t, s)
}
func TestMoveReordersSubtreeVersionsAndCycleRollback(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "排序"})
	root := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "A"})
	other := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "B"})
	child := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": root["id"], "name": "A1"})
	root = getNow(t, s, "tasks", text(root["id"]))
	_, err := s.MoveTask(text(root["id"]), map[string]any{"version": root["version"], "parent_id": child["id"]})
	assertStatus(t, err, 400)
	moved, err := s.MoveTask(text(root["id"]), map[string]any{"version": root["version"], "parent_id": "", "before_id": other["id"], "expected_versions": map[string]any{text(other["id"]): other["version"]}})
	if err != nil {
		t.Fatal(err)
	}
	moved, err = s.MoveTask(text(other["id"]), map[string]any{"version": other["version"], "parent_id": "", "before_id": root["id"], "expected_versions": map[string]any{text(root["id"]): root["version"]}})
	if err != nil {
		t.Fatal(err)
	}
	root = getNow(t, s, "tasks", text(root["id"]))
	child = getNow(t, s, "tasks", text(child["id"]))
	if moved["wbs_code"] != "1" || root["wbs_code"] != "2" || child["wbs_code"] != "2.1" || num(root["sort_order"]) != 2 {
		t.Fatalf("bad subtree order %v %v %v", moved, root, child)
	}
	if versionOf(root["version"]) <= 1 || versionOf(child["version"]) <= 1 {
		t.Fatal("move failed version bump")
	}
	_, err = s.MoveTask(text(moved["id"]), map[string]any{"version": moved["version"], "parent_id": "", "expected_versions": map[string]any{text(root["id"]): 1}})
	assertStatus(t, err, 409)
	assertChain(t, s)
}
func TestBatchAtomicClassifyAndReopen(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "批量"})
	root := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "汇总"})
	a := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": root["id"], "name": "A"})
	b := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": root["id"], "name": "B", "status": "done"})
	_, err := s.BatchTasks(map[string]any{"items": []any{map[string]any{"id": a["id"], "version": a["version"]}, map[string]any{"id": b["id"], "version": b["version"]}}, "changes": map[string]any{"assignee": "不应提交"}})
	assertStatus(t, err, 409)
	a = getNow(t, s, "tasks", text(a["id"]))
	if a["assignee"] != "" || versionOf(a["version"]) != 1 {
		t.Fatal("partial batch committed")
	}
	a = mustUpdate(t, s, "tasks", a, map[string]any{"status": "done"})
	root = getNow(t, s, "tasks", text(root["id"]))
	_, err = s.ReopenTask(text(root["id"]), map[string]any{"version": root["version"], "reason": "调整"})
	assertStatus(t, err, 400)
	_, err = s.ReopenTask(text(a["id"]), map[string]any{"version": a["version"]})
	assertStatus(t, err, 400)
	a, err = s.ReopenTask(text(a["id"]), map[string]any{"version": a["version"], "reason": "复测未通过"})
	if err != nil {
		t.Fatal(err)
	}
	root = getNow(t, s, "tasks", text(root["id"]))
	if a["status"] != "doing" || num(a["progress"]) != 0 || a["actual_end"] != "" || root["status"] != "doing" {
		t.Fatalf("reopen invalid: %v %v", a, root)
	}
	r1 := mustCreate(t, s, "records", map[string]any{"content": "@批量 明确未归类", "project_id": ""})
	r2 := mustCreate(t, s, "records", map[string]any{"content": "另一条"})
	_, err = s.ClassifyRecords(map[string]any{"items": []any{map[string]any{"id": r1["id"], "version": r1["version"]}, map[string]any{"id": r2["id"], "version": 999}}, "project_id": p["id"]})
	assertStatus(t, err, 409)
	r1 = getNow(t, s, "records", text(r1["id"]))
	if r1["project_id"] != "" {
		t.Fatal("classification partial committed or ignored explicit empty")
	}
	out, err := s.ClassifyRecords(map[string]any{"items": []any{map[string]any{"id": r1["id"], "version": r1["version"]}, map[string]any{"id": r2["id"], "version": r2["version"]}}, "project_id": p["id"]})
	if err != nil || out["total"] != 2 {
		t.Fatalf("classify %v %v", out, err)
	}
	r1 = getNow(t, s, "records", text(r1["id"]))
	_, err = s.ClassifyRecords(map[string]any{"items": []any{map[string]any{"id": r1["id"], "version": r1["version"]}}, "project_id": ""})
	if err != nil {
		t.Fatal(err)
	}
	if getNow(t, s, "records", text(r1["id"]))["project_id"] != "" {
		t.Fatal("explicit unclassification reparsed shortcut")
	}
	assertChain(t, s)
}
func TestRecordAndSearchKeysetsTotalsFilters(t *testing.T) {
	s := testStore(t)
	for _, at := range []string{"2026-11-05T00:00:00Z", "2026-11-05T00:00:00.1Z", "2026-11-05T00:00:00.01Z", "2026-11-05T00:00:00.001Z"} {
		mustCreate(t, s, "records", map[string]any{"content": "分页匹配词", "occurred_at": at, "tags": []any{"中文标签"}})
	}
	filters := map[string]string{"limit": "2", "tag": "中文标签", "unclassified": "true"}
	page, err := s.ListPage("records", filters)
	if err != nil || page["total"] != 4 {
		t.Fatalf("page %v %v", page, err)
	}
	first := page["items"].([]map[string]any)
	if first[0]["occurred_at"] != "2026-11-05T00:00:00.1Z" || first[1]["occurred_at"] != "2026-11-05T00:00:00.01Z" {
		t.Fatalf("subsecond order %v", first)
	}
	filters["cursor"] = text(page["next_cursor"])
	mustCreate(t, s, "records", map[string]any{"content": "新插入分页匹配词", "occurred_at": "2026-11-06T00:00:00Z", "tags": []any{"中文标签"}})
	page, err = s.ListPage("records", filters)
	if err != nil || page["total"] != 5 || len(page["items"].([]map[string]any)) != 2 || page["next_cursor"] != "" {
		t.Fatalf("unstable keyset %v %v", page, err)
	}
	filters["tag"] = "改变筛选"
	_, err = s.ListPage("records", filters)
	assertStatus(t, err, 400)
	query := map[string]string{"q": "分页匹配词", "limit": "2", "entity_type": "records"}
	page, err = s.SearchPage(query)
	if err != nil || page["total"] != 5 || page["next_cursor"] == "" {
		t.Fatalf("search full totals %v %v", page, err)
	}
	seen := map[string]bool{}
	for {
		for _, m := range page["items"].([]map[string]any) {
			id := text(m["entity_id"])
			if seen[id] {
				t.Fatal("duplicate keyset item")
			}
			seen[id] = true
		}
		cursor := text(page["next_cursor"])
		if cursor == "" {
			break
		}
		query["cursor"] = cursor
		page, err = s.SearchPage(query)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 5 {
		t.Fatal("search truncated totals")
	}
	_, err = s.ListPage("records", map[string]string{"date_from": "2026-11-06", "date_to": "2026-11-05"})
	assertStatus(t, err, 400)
}
func TestTrashAuditFailureRollsBackAndArchivedRestoreRejected(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "归档"})
	r := mustCreate(t, s, "records", map[string]any{"content": "事务唯一词", "project_id": p["id"]})
	if _, err := s.DB.Exec("CREATE TRIGGER deny_audit BEFORE INSERT ON audit BEGIN SELECT RAISE(ABORT,'test failure');END"); err != nil {
		t.Fatal(err)
	}
	err := s.Delete("records", text(r["id"]), versionOf(r["version"]))
	if err == nil {
		t.Fatal("delete must rollback failed audit")
	}
	if getNow(t, s, "records", text(r["id"]))["deleted_at"] != "" {
		t.Fatal("trash leaked from failed audit")
	}
	s.DB.Exec("DROP TRIGGER deny_audit")
	if err = s.Delete("records", text(r["id"]), versionOf(r["version"])); err != nil {
		t.Fatal(err)
	}
	p = mustUpdate(t, s, "projects", p, map[string]any{"status": "archived"})
	trash, err := getIncludingTrash(s.DB, "records", text(r["id"]), true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RestoreTrash("records", text(r["id"]), map[string]any{"version": trash["version"]})
	assertStatus(t, err, 409)
	assertChain(t, s)
}
func TestBackupSettingsValidation(t *testing.T) {
	s := testStore(t)
	if err := s.SetSettings(map[string]any{"backup_keep_count": 30, "backup_max_mb": "0", "backup_auto": "0", "setup_completed": "1"}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []map[string]any{{"dataset_id": "other"}, {"gjb_level": "3"}, {"backup_keep_count": 1}, {"backup_max_mb": 99}, {"backup_auto": "yes"}} {
		assertStatus(t, s.SetSettings(input), 400)
	}
	settings, e := s.Settings()
	if e != nil || settings["backup_auto"] != "0" || settings["backup_keep_count"] != "30" {
		t.Fatal("settings invalid")
	}
}

func TestMigrationV2FailureRollsBackToV1(t *testing.T) {
	root := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile("testdata/v1.sql")
	if err != nil {
		t.Fatal(err)
	}
	schema := string(source)
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO projects(id,code,name,created_at,updated_at)VALUES('keep','KEEP','保留项目','2026-11-04T00:00:00Z','2026-11-04T00:00:00Z');CREATE TRIGGER block_upgrade BEFORE INSERT ON settings WHEN new.key='dataset_id' BEGIN SELECT RAISE(ABORT,'migration failure');END"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(root); err == nil {
		s.Close()
		t.Fatal("expected migration failure")
	}
	db, err = sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version, count int
	if err = db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 1 {
		t.Fatalf("migration version leaked: %d %v", version, err)
	}
	if err = db.QueryRow("SELECT count(*) FROM pragma_table_info('tasks') WHERE name='deleted_at'").Scan(&count); err != nil || count != 0 {
		t.Fatal("partial ALTER survived rollback")
	}
	if err = db.QueryRow("SELECT count(*) FROM projects WHERE id='keep'").Scan(&count); err != nil || count != 1 {
		t.Fatal("migration lost original data")
	}
	if _, err = db.Exec("DROP TRIGGER block_upgrade"); err != nil {
		t.Fatal(err)
	}
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}
func TestRestoreIncompleteChildReopensDerivedParentAndAvoidsWBSCollision(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "恢复汇总"})
	parent := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "汇总"})
	a := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": parent["id"], "name": "未完成"})
	b := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": parent["id"], "name": "已完成", "status": "done"})
	if err := s.Delete("tasks", text(a["id"]), versionOf(a["version"])); err != nil {
		t.Fatal(err)
	}
	parent = getNow(t, s, "tasks", text(parent["id"]))
	if parent["status"] != "done" {
		t.Fatal("parent should become done")
	}
	trashed, err := getIncludingTrash(s.DB, "tasks", text(a["id"]), true)
	if err != nil {
		t.Fatal(err)
	}
	a, err = s.RestoreTrash("tasks", text(a["id"]), map[string]any{"version": trashed["version"]})
	if err != nil {
		t.Fatal(err)
	}
	parent = getNow(t, s, "tasks", text(parent["id"]))
	if parent["status"] != "doing" || num(parent["progress"]) != 50 {
		t.Fatalf("restored child did not reopen derived parent: %v", parent)
	}
	b = getNow(t, s, "tasks", text(b["id"]))
	if a["wbs_code"] == b["wbs_code"] {
		t.Fatal("restore duplicated active WBS")
	}
	assertChain(t, s)
}
func TestPageOffsetAndDateAliasesRemainCompatible(t *testing.T) {
	s := testStore(t)
	for _, date := range []string{"2026-11-04T00:00:00Z", "2026-11-05T00:00:00Z", "2026-11-06T00:00:00Z"} {
		mustCreate(t, s, "records", map[string]any{"content": "兼容分页关键词", "occurred_at": date})
	}
	first, err := s.ListPage("records", map[string]string{"limit": "1"})
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.ListPage("records", map[string]string{"limit": "1", "offset": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if first["items"].([]map[string]any)[0]["id"] == next["items"].([]map[string]any)[0]["id"] {
		t.Fatal("record offset ignored")
	}
	query := map[string]string{"q": "兼容分页关键词", "limit": "1"}
	first, err = s.SearchPage(query)
	if err != nil {
		t.Fatal(err)
	}
	query["offset"] = "1"
	next, err = s.SearchPage(query)
	if err != nil {
		t.Fatal(err)
	}
	if first["items"].([]map[string]any)[0]["entity_id"] == next["items"].([]map[string]any)[0]["entity_id"] {
		t.Fatal("search offset ignored")
	}
	result, err := s.ListPage("records", map[string]string{"from": "2026-11-04", "to": "2026-11-06"})
	if err != nil || result["total"] != 3 {
		t.Fatalf("date aliases %v %v", result, err)
	}
}

func TestSearchPageCountsBeyondLegacyCapAndCascadeRejectsCompletedDescendant(t *testing.T) {
	s := testStore(t)
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 503; i++ {
		if _, err = create(tx, "records", map[string]any{"content": "超过五百完整结果"}); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	result, err := s.SearchPage(map[string]string{"q": "完整结果", "limit": "500"})
	if err != nil || result["total"] != 503 || len(result["items"].([]map[string]any)) != 500 || result["next_cursor"] == "" {
		t.Fatalf("search counts capped: %v %v", result["total"], err)
	}
	result, err = s.SearchPage(map[string]string{"q": "完整结果", "limit": "500", "cursor": text(result["next_cursor"])})
	if err != nil || len(result["items"].([]map[string]any)) != 3 || result["total"] != 503 {
		t.Fatalf("search tail %v %v", result, err)
	}
	p := mustCreate(t, s, "projects", map[string]any{"name": "终态限制"})
	parent := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "汇总"})
	b := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": parent["id"], "name": "待办项"})
	a := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": parent["id"], "name": "完成项", "status": "done"})
	parent = getNow(t, s, "tasks", text(parent["id"]))
	_, err = s.SoftDelete("tasks", text(parent["id"]), map[string]any{"version": parent["version"], "cascade": true, "expected_versions": map[string]any{text(a["id"]): a["version"], text(b["id"]): b["version"]}})
	assertStatus(t, err, 409)
	if getNow(t, s, "tasks", text(b["id"]))["deleted_at"] != "" {
		t.Fatal("terminal rejection partially recycled subtree")
	}
	assertChain(t, s)
}

func TestTagsExcludeTrashAndEscapeWildcard(t *testing.T) {
	s := testStore(t)
	a := mustCreate(t, s, "records", map[string]any{"content": "第一条", "tags": []any{"中文", "100%", "A_B", "共享"}})
	mustCreate(t, s, "records", map[string]any{"content": "第二条", "tags": []any{"共享", "其他"}})
	tags, err := s.Tags("")
	if err != nil || len(tags) != 5 {
		t.Fatalf("tag dedup %v %v", tags, err)
	}
	for _, q := range []string{"%", "_"} {
		tags, err = s.Tags(q)
		if err != nil || len(tags) != 1 {
			t.Fatalf("tag wildcard %q: %v %v", q, tags, err)
		}
	}
	if err = s.Delete("records", text(a["id"]), versionOf(a["version"])); err != nil {
		t.Fatal(err)
	}
	tags, err = s.Tags("")
	if err != nil || len(tags) != 2 {
		t.Fatalf("deleted tags leaked %v %v", tags, err)
	}
	_, err = s.Tags(strings.Repeat("字", 101))
	assertStatus(t, err, 400)
}

func TestAddingTaskToDerivedDoneParentRollsUp(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "追加工作"})
	parent := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "汇总"})
	mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": parent["id"], "name": "原工作", "status": "done"})
	parent = getNow(t, s, "tasks", text(parent["id"]))
	if parent["status"] != "done" {
		t.Fatal("parent not complete")
	}
	mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": parent["id"], "name": "新增工作"})
	parent = getNow(t, s, "tasks", text(parent["id"]))
	if parent["status"] != "doing" || num(parent["progress"]) != 50 {
		t.Fatal("new work did not update derived completion")
	}
	assertChain(t, s)
}
