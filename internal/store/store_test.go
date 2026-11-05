package store

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func mustCreate(t *testing.T, s *Store, k string, m map[string]any) map[string]any {
	t.Helper()
	v, err := s.Create(k, m)
	if err != nil {
		t.Fatalf("create %s: %v", k, err)
	}
	return v
}
func mustUpdate(t *testing.T, s *Store, k string, m map[string]any, in map[string]any) map[string]any {
	t.Helper()
	in["version"] = m["version"]
	v, err := s.Update(k, text(m["id"]), in)
	if err != nil {
		t.Fatalf("update %s: %v", k, err)
	}
	return v
}
func assertStatus(t *testing.T, err error, status int) {
	t.Helper()
	var app *AppError
	if !errors.As(err, &app) || app.Status != status {
		t.Fatalf("want status %d, got %v", status, err)
	}
}
func assertChain(t *testing.T, s *Store) {
	t.Helper()
	v, err := s.VerifyAudit()
	if err != nil || v["ok"] != true {
		t.Fatalf("broken audit %v: %v", v, err)
	}
}

func TestTaskTreeWeightedProgressAndStableWBS(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "工程"})
	pid := p["id"]
	root := mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "name": "交付"})
	a := mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "parent_id": root["id"], "name": "设计", "estimate_hours": 2, "status": "doing", "progress": 50})
	b := mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "parent_id": root["id"], "name": "实施", "estimate_hours": 6})
	parent, err := s.Get("tasks", text(root["id"]))
	if err != nil {
		t.Fatal(err)
	}
	if num(parent["progress"]) != 13 || num(parent["estimate_hours"]) != 8 {
		t.Fatalf("weighted rollup = %v", parent)
	}
	_, err = s.Update("tasks", text(parent["id"]), map[string]any{"version": parent["version"], "progress": 90})
	assertStatus(t, err, 400)
	_, err = s.Update("tasks", text(parent["id"]), map[string]any{"version": parent["version"], "parent_id": a["id"]})
	assertStatus(t, err, 400)
	other := mustCreate(t, s, "projects", map[string]any{"name": "其他"})
	_, err = s.Create("tasks", map[string]any{"project_id": other["id"], "parent_id": a["id"], "name": "跨项目"})
	assertStatus(t, err, 400)
	oldWBS := text(b["wbs_code"])
	if err = s.Delete("tasks", text(a["id"]), versionOf(a["version"])); err != nil {
		t.Fatal(err)
	}
	b, err = s.Get("tasks", text(b["id"]))
	if err != nil {
		t.Fatal(err)
	}
	if text(b["wbs_code"]) != oldWBS {
		t.Fatalf("WBS changed after sibling deletion: %s", b["wbs_code"])
	}
	b = mustUpdate(t, s, "tasks", b, map[string]any{"status": "done", "progress": 2})
	if num(b["progress"]) != 100 {
		t.Fatal("done leaf must be 100%")
	}
	parent, err = s.Get("tasks", text(root["id"]))
	if err != nil {
		t.Fatal(err)
	}
	if text(parent["status"]) != "done" || num(parent["progress"]) != 100 {
		t.Fatalf("parent did not complete: %v", parent)
	}
	_, err = s.Update("tasks", text(b["id"]), map[string]any{"version": b["version"], "name": "修改"})
	assertStatus(t, err, 409)
	assertChain(t, s)
}
func TestIssueStateHistoryValidationAndRollback(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "工程"})
	i := mustCreate(t, s, "issues", map[string]any{"project_id": p["id"], "title": "焊点异常"})
	_, err := s.Update("issues", text(i["id"]), map[string]any{"version": i["version"], "status": "closed"})
	assertStatus(t, err, 400)
	i = mustUpdate(t, s, "issues", i, map[string]any{"status": "analyzing"})
	i = mustUpdate(t, s, "issues", i, map[string]any{"status": "fixing", "root_cause": "工艺参数偏差", "solution": "修正温度"})
	i = mustUpdate(t, s, "issues", i, map[string]any{"status": "verifying"})
	_, err = s.Update("issues", text(i["id"]), map[string]any{"version": i["version"], "status": "closed"})
	assertStatus(t, err, 400)
	if _, err = s.DB.Exec("CREATE TRIGGER audit_fail BEFORE INSERT ON audit BEGIN SELECT RAISE(ABORT,'test audit failure'); END"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Update("issues", text(i["id"]), map[string]any{"version": i["version"], "status": "closed", "verify_result": "复测合格"})
	if err == nil {
		t.Fatal("audit failure must fail business write")
	}
	current, err := s.Get("issues", text(i["id"]))
	if err != nil {
		t.Fatal(err)
	}
	if text(current["status"]) != "verifying" || len(current["history"].([]map[string]any)) != 4 || versionOf(current["version"]) != versionOf(i["version"]) {
		t.Fatalf("rollback leaked state/history: %v", current)
	}
	if _, err = s.DB.Exec("DROP TRIGGER audit_fail"); err != nil {
		t.Fatal(err)
	}
	i = mustUpdate(t, s, "issues", i, map[string]any{"status": "closed", "verify_result": "复测合格"})
	if len(i["history"].([]map[string]any)) != 5 || !nonempty(i["closed_at"]) {
		t.Fatal("missing close history")
	}
	_, err = s.Update("issues", text(i["id"]), map[string]any{"version": i["version"], "description": "更改"})
	assertStatus(t, err, 409)
	assertChain(t, s)
}
func TestArchiveClosedVersionsAndAtomicCreate(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "工程"})
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "测试"})
	_, err := s.Update("tasks", text(task["id"]), map[string]any{"version": 999, "name": "覆盖"})
	assertStatus(t, err, 409)
	p = mustUpdate(t, s, "projects", p, map[string]any{"status": "closed"})
	_, err = s.Create("tasks", map[string]any{"project_id": p["id"], "name": "非法新增"})
	assertStatus(t, err, 409)
	_, err = s.Create("issues", map[string]any{"project_id": p["id"], "title": "非法新增"})
	assertStatus(t, err, 409)
	p = mustUpdate(t, s, "projects", p, map[string]any{"status": "archived"})
	_, err = s.Update("tasks", text(task["id"]), map[string]any{"version": task["version"], "name": "非法修改"})
	assertStatus(t, err, 409)
	err = s.Delete("tasks", text(task["id"]), versionOf(task["version"]))
	assertStatus(t, err, 409)
	_, err = s.Update("projects", text(p["id"]), map[string]any{"version": p["version"], "status": "active"})
	assertStatus(t, err, 409)
	if _, err = s.DB.Exec("CREATE TRIGGER audit_fail BEFORE INSERT ON audit BEGIN SELECT RAISE(ABORT,'test audit failure'); END"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Create("records", map[string]any{"content": "应回滚"})
	if err == nil {
		t.Fatal("expected rollback")
	}
	var count int
	if err = s.DB.QueryRow("SELECT count(*) FROM records").Scan(&count); err != nil || count != 0 {
		t.Fatalf("business leaked: %d %v", count, err)
	}
	if err = s.DB.QueryRow("SELECT count(*) FROM search_doc WHERE entity_type='records'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("search leaked: %d %v", count, err)
	}
	assertChain(t, s)
}
func TestChineseSearchQuickTagsAndEscapedWildcards(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "现场工程"})
	original := "巡检发现电源连接异常 #质量 @现场工程 @未知项目 100% A_B"
	r := mustCreate(t, s, "records", map[string]any{"content": original, "occurred_at": "2026-11-04T08:30:00+08:00"})
	if r["project_id"] != p["id"] || r["content"] != original || text(r["occurred_at"]) != "2026-11-04T00:30:00Z" {
		t.Fatalf("quick parse lost data or occurrence timezone: %v", r)
	}
	tags := r["tags"].([]any)
	if len(tags) != 1 || tags[0] != "质量" {
		t.Fatalf("tags %v", tags)
	}
	for _, q := range []string{"电源连接", "异常", "质量", "%", "_"} {
		items, err := s.Search(q)
		if err != nil || len(items) != 1 {
			t.Fatalf("search %q got %v %v", q, items, err)
		}
	}
	for _, q := range []string{"%_", "不存在", "\" OR *"} {
		items, err := s.Search(q)
		if err != nil || len(items) != 0 {
			t.Fatalf("safe search %q got %v %v", q, items, err)
		}
	}
	r = mustUpdate(t, s, "records", r, map[string]any{"content": "传感器复测正常", "tags": []any{}})
	items, err := s.Search("电源连接")
	if err != nil || len(items) != 0 {
		t.Fatalf("stale index: %v %v", items, err)
	}
	if err = s.Delete("records", text(r["id"]), versionOf(r["version"])); err != nil {
		t.Fatal(err)
	}
	items, err = s.Search("传感器")
	if err != nil || len(items) != 0 {
		t.Fatalf("deleted index: %v %v", items, err)
	}
	assertChain(t, s)
}
func TestWorklogsUniqueDraftAndLocalDay(t *testing.T) {
	oldLocation := time.Local
	time.Local = time.FixedZone("CST", 8*3600)
	t.Cleanup(func() { time.Local = oldLocation })
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "日报工程"})
	d := today()
	local, _ := time.ParseInLocation("2006-01-02", d, time.Local)
	mustCreate(t, s, "records", map[string]any{"project_id": p["id"], "title": "日初工作", "occurred_at": local.Add(1 * time.Second).Format(time.RFC3339Nano)})
	mustCreate(t, s, "records", map[string]any{"project_id": p["id"], "title": "前日工作", "occurred_at": local.Add(-1 * time.Second).Format(time.RFC3339Nano)})
	v, err := s.Today()
	if err != nil {
		t.Fatal(err)
	}
	if len(v["records"].([]map[string]any)) != 1 || v["stats"].(map[string]any)["records_today"] != 1 {
		t.Fatalf("local day boundary: %v", v)
	}
	draft, err := s.GenerateLog(map[string]any{"date": d, "period_type": "day", "project_id": p["id"]})
	if err != nil {
		t.Fatal(err)
	}
	content := text(draft["content"])
	if !strings.Contains(content, "日初工作") || strings.Contains(content, "前日工作") {
		t.Fatalf("bad date range: %s", content)
	}
	saved := mustCreate(t, s, "worklogs", map[string]any{"log_date": d, "project_id": p["id"], "content": "人工修改的日志"})
	if _, err = s.GenerateLog(map[string]any{"date": d, "period_type": "day", "project_id": p["id"]}); err != nil {
		t.Fatal(err)
	}
	saved, err = s.Get("worklogs", text(saved["id"]))
	if err != nil || saved["content"] != "人工修改的日志" {
		t.Fatalf("draft overwrote saved log: %v %v", saved, err)
	}
	_, err = s.Create("worklogs", map[string]any{"log_date": d, "project_id": p["id"], "content": "重复"})
	assertStatus(t, err, 409)
	mustCreate(t, s, "worklogs", map[string]any{"log_date": "2026-11-04", "period_type": "week", "content": "周报"})
	_, err = s.Create("worklogs", map[string]any{"log_date": "2026-11-06", "period_type": "week", "content": "同周重复"})
	assertStatus(t, err, 409)
	assertChain(t, s)
}
func TestAuditOperatorSystemEventsAndTampering(t *testing.T) {
	s := testStore(t)
	if err := s.SetSettings(map[string]any{"operator": "张工"}); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, "projects", map[string]any{"name": "审计"})
	if err := s.RecordEvent("backup", "system", "b.zip", map[string]any{"size": 1024}); err != nil {
		t.Fatal(err)
	}
	assertChain(t, s)
	var actor string
	if err := s.DB.QueryRow("SELECT operator FROM audit ORDER BY id DESC LIMIT 1").Scan(&actor); err != nil || actor != "张工" {
		t.Fatalf("server actor: %s %v", actor, err)
	}
	if _, err := s.DB.Exec("UPDATE audit SET after_json='{}' WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	v, err := s.VerifyAudit()
	if err != nil || v["ok"] != false || v["broken_at"] != 2 {
		t.Fatalf("tamper undetected %v %v", v, err)
	}
}
func TestDemoOnlyExplicitEmptyStore(t *testing.T) {
	s := testStore(t)
	items, n, err := s.List("projects", nil)
	if err != nil || n != 0 || len(items) != 0 {
		t.Fatalf("implicit demo: %v %d %v", items, n, err)
	}
	if err = s.Demo(); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, s.Demo(), 409)
	assertChain(t, s)
}

func TestPhotoOnlyRecordAndUnfixableIssue(t *testing.T) {
	s := testStore(t)
	r := mustCreate(t, s, "records", map[string]any{"type": "photo", "attachments": []any{map[string]any{"id": "uploaded-test-id", "filename": "photo.png"}}})
	if len(r["attachments"].([]any)) != 1 {
		t.Fatal("photo-only record missing attachments")
	}
	p := mustCreate(t, s, "projects", map[string]any{"name": "工程"})
	i := mustCreate(t, s, "issues", map[string]any{"project_id": p["id"], "title": "无法修复"})
	i = mustUpdate(t, s, "issues", i, map[string]any{"status": "analyzing"})
	i = mustUpdate(t, s, "issues", i, map[string]any{"status": "fixing"})
	_, err := s.Update("issues", text(i["id"]), map[string]any{"version": i["version"], "status": "rejected"})
	assertStatus(t, err, 400)
	i = mustUpdate(t, s, "issues", i, map[string]any{"status": "rejected", "rejection_reason": "验证为外部设备限制"})
	if i["status"] != "rejected" {
		t.Fatal("unfixable issue not rejected")
	}
	assertChain(t, s)
}

func TestTaskGroupingDoesNotChangeLeafWeights(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "按叶子汇总"})
	pid := p["id"]
	root := mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "name": "根"})
	branch := mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "parent_id": root["id"], "name": "分组"})
	mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "parent_id": branch["id"], "name": "叶子A", "status": "doing", "progress": 100})
	mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "parent_id": branch["id"], "name": "叶子B", "status": "doing", "progress": 100})
	mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "parent_id": root["id"], "name": "叶子C", "progress": 0})
	got, err := s.Get("tasks", text(root["id"]))
	if err != nil {
		t.Fatal(err)
	}
	if num(got["progress"]) != 67 {
		t.Fatalf("grouping changed equal leaf weights: %v", got)
	}
	assertChain(t, s)
}
