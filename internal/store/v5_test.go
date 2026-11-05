package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func legacyV4(t *testing.T, root string) *sql.DB {
	t.Helper()
	db := legacyV3(t, root)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateV4(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestV5MigrationRollbackAndBusinessPreservation(t *testing.T) {
	root := t.TempDir()
	db := legacyV4(t, root)
	if _, err := db.Exec("INSERT INTO projects(id,code,name,created_at,updated_at)VALUES('keep','KEEP','便签升级前','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z');CREATE TABLE export_jobs(invalid TEXT)"); err != nil {
		t.Fatal(err)
	}
	var dataset string
	db.QueryRow("SELECT value FROM settings WHERE key='dataset_id'").Scan(&dataset)
	db.Close()
	if s, err := Open(root); err == nil {
		s.Close()
		t.Fatal("schema5 collision should fail transaction")
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	var version, count int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='desktop_drafts'").Scan(&count)
	if version != 4 || count != 0 {
		t.Fatalf("partial schema5 committed: %d %d", version, count)
	}
	db.Exec("DROP TABLE export_jobs")
	db.Close()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if getNow(t, s, "projects", "keep")["name"] != "便签升级前" {
		t.Fatal("upgrade lost business data")
	}
	settings, err := s.Settings()
	if err != nil || settings["dataset_id"] != dataset {
		t.Fatal("upgrade changed dataset")
	}
	s.DB.QueryRow("PRAGMA user_version").Scan(&version)
	if version != SchemaVersion {
		t.Fatal("wrong migrated version")
	}
	draft, err := s.DesktopDraft()
	if err != nil || versionOf(draft["version"]) != 1 || draft["content"] != "" {
		t.Fatal("initial draft", draft, err)
	}
	jobs, err := s.ListExportJobs(nil)
	if err != nil || jobs["total"] != 0 {
		t.Fatal("initial export jobs", jobs, err)
	}
}
func TestDesktopDraftVersionedScratchAndUnavailableProjectPreserved(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "便签项目"})
	draft, err := s.DesktopDraft()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveDesktopDraft(map[string]any{"content": "缺版本", "project_id": ""}); err == nil {
		t.Fatal("draft write without version")
	}
	draft, err = s.SaveDesktopDraft(map[string]any{"version": draft["version"], "content": "便签独有检索词", "project_id": p["id"]})
	if err != nil || versionOf(draft["version"]) != 2 {
		t.Fatal("draft save", draft, err)
	}
	_, err = s.SaveDesktopDraft(map[string]any{"version": 1, "content": "旧页覆盖", "project_id": p["id"]})
	assertStatus(t, err, 409)
	_, n, err := s.List("records", nil)
	if err != nil || n != 0 {
		t.Fatal("scratch became business record")
	}
	results, err := s.SearchPage(map[string]string{"q": "便签独有检索词"})
	if err != nil || results["total"] != 0 {
		t.Fatal("scratch leaked into search")
	}
	view, err := s.Today()
	if err != nil || view["stats"].(map[string]any)["records_today"] != 0 {
		t.Fatal("scratch counted as saved record")
	}
	if err = s.Delete("projects", text(p["id"]), versionOf(p["version"])); err != nil {
		t.Fatal("draft must not prevent recycling empty project", err)
	}
	draft, err = s.DesktopDraft()
	if err != nil || draft["project_available"] != false || draft["content"] != "便签独有检索词" {
		t.Fatal("project recycle lost scratch", draft, err)
	}
	draft, err = s.SaveDesktopDraft(map[string]any{"version": draft["version"], "content": "项目已回收，正文还可以接着写", "project_id": p["id"]})
	if err != nil {
		t.Fatal("unavailable existing project blocked draft preservation", err)
	}
	_, err = s.SaveDesktopDraft(map[string]any{"version": draft["version"], "content": "错误关联不得覆盖原稿", "project_id": "no-project"})
	assertStatus(t, err, 400)
	preserved, err := s.DesktopDraft()
	if err != nil || preserved["content"] != draft["content"] {
		t.Fatal("invalid project wiped draft")
	}
	stale := draft["version"]
	draft, err = s.SaveDesktopDraft(map[string]any{"version": stale, "content": "新页正在编辑", "project_id": ""})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveDesktopDraft(map[string]any{"version": stale, "content": "", "project_id": ""})
	assertStatus(t, err, 409)
	if got, e := s.DesktopDraft(); e != nil || got["content"] != "新页正在编辑" {
		t.Fatal("post-record stale clear erased newer scratch")
	}
	assertChain(t, s)
}
func TestNextPlanPersonLeafDateReasonsAndLocalRecordWindow(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("CST", 8*3600)
	t.Cleanup(func() { time.Local = old })
	s := testStore(t)
	if err := s.SetSettings(map[string]any{"operator": "本人甲"}); err != nil {
		t.Fatal(err)
	}
	p := mustCreate(t, s, "projects", map[string]any{"name": "次日候选"})
	other := mustCreate(t, s, "projects", map[string]any{"name": "另一项目"})
	parent := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "不能选父项", "assignee": "本人甲"})
	cases := []struct{ name, start, end, status, person, parent string }{{"目标日开始", "2026-11-06", "2026-11-07", "todo", "本人甲", text(parent["id"])}, {"跨目标日", "2026-11-04", "2026-11-07", "doing", "本人甲", ""}, {"逾期未完成", "2026-11-01", "2026-11-04", "doing", "本人甲", ""}, {"待排程任务", "", "", "todo", "本人甲", ""}, {"别人任务", "2026-11-06", "2026-11-07", "todo", "其他人", ""}, {"未来任务", "2026-11-07", "2026-11-08", "todo", "本人甲", ""}, {"已完任务", "2026-11-05", "2026-11-06", "done", "本人甲", ""}, {"取消任务", "2026-11-05", "2026-11-06", "canceled", "本人甲", ""}}
	for _, c := range cases {
		mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": c.parent, "name": c.name, "plan_start": c.start, "plan_end": c.end, "status": c.status, "assignee": c.person})
	}
	mustCreate(t, s, "tasks", map[string]any{"project_id": other["id"], "name": "另一项目任务", "assignee": "本人甲"})
	mustCreate(t, s, "records", map[string]any{"project_id": p["id"], "title": "当日本地零点记录", "occurred_at": "2026-11-05T00:00:00+08:00"})
	mustCreate(t, s, "records", map[string]any{"project_id": p["id"], "title": "前日记录不选", "occurred_at": "2026-11-04T23:59:59+08:00"})
	mustCreate(t, s, "records", map[string]any{"project_id": p["id"], "title": "次日零点不选", "occurred_at": "2026-11-06T00:00:00+08:00"})
	var before int
	s.DB.QueryRow("SELECT count(*) FROM audit").Scan(&before)
	out, err := s.NextPlan(map[string]any{"date": "2026-11-05", "project_id": p["id"]})
	if err != nil || out["total"] != 5 || out["target_date"] != "2026-11-06" || out["assignee"] != "本人甲" {
		t.Fatalf("candidate selection %v %v", out, err)
	}
	reasons := map[string]string{}
	for _, c := range out["candidates"].([]map[string]any) {
		if !nonempty(c["key"]) || versionOf(c["version"]) < 1 {
			t.Fatal("candidate source not versioned")
		}
		reasons[text(c["title"])] = text(c["reason"])
	}
	for title, reason := range map[string]string{"目标日开始": "计划在目标日开始", "跨目标日": "计划跨目标日", "逾期未完成": "逾期未完成", "待排程任务": "待排程", "当日本地零点记录": "供手动选取"} {
		if !strings.Contains(reasons[title], reason) {
			t.Fatalf("missing %s reason %q", title, reasons[title])
		}
	}
	var after int
	s.DB.QueryRow("SELECT count(*) FROM audit").Scan(&after)
	if before != after {
		t.Fatal("candidate read mutated source/audit")
	}
	_, err = s.NextPlan(map[string]any{"date": "2026-02-31"})
	assertStatus(t, err, 400)
	_, err = s.NextPlan(map[string]any{"assignee": 123})
	assertStatus(t, err, 400)
	out, err = s.NextPlan(map[string]any{"date": "2026-11-05", "project_id": p["id"], "q": "目标日开始"})
	if err != nil || out["total"] != 1 {
		t.Fatal("candidate search", out, err)
	}
}
func TestNextPlanPaginationBeyondFiveHundredAndEscapedWildcards(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "候选分页"})
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 503; i++ {
		m := defaults("tasks")
		m["project_id"] = p["id"]
		m["name"] = fmt.Sprintf("任务 %d", i)
		if i == 0 {
			m["name"] = "进展100%"
		}
		m["assignee"] = "本人"
		m["sort_order"] = i + 1
		if err = insert(tx, "tasks", m); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	page, err := s.NextPlan(map[string]any{"project_id": p["id"], "limit": 500})
	if err != nil || page["total"] != 503 || len(page["candidates"].([]map[string]any)) != 500 || page["has_more"] != true {
		t.Fatal("candidate totals truncated", page["total"], err)
	}
	page, err = s.NextPlan(map[string]any{"project_id": p["id"], "limit": 500, "offset": 500})
	if err != nil || len(page["candidates"].([]map[string]any)) != 3 || page["has_more"] != false {
		t.Fatal("candidate tail", page, err)
	}
	page, err = s.NextPlan(map[string]any{"project_id": p["id"], "q": "%"})
	if err != nil || page["total"] != 1 {
		t.Fatal("wildcard not escaped", page, err)
	}
	_, err = s.NextPlan(map[string]any{"limit": 2.5})
	assertStatus(t, err, 400)
}
func TestExportSnapshotsFreezeSourcesAndRejectPayloadTampering(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "导出来源"})
	log := mustCreate(t, s, "worklogs", map[string]any{"project_id": p["id"], "log_date": "2026-11-05", "content": "原已保存正文"})
	snapshot, err := s.ExportSnapshot("worklogs", text(log["id"]))
	if err != nil || snapshot["content"] != "原已保存正文" || snapshot["project_name"] != "导出来源" || snapshot["person_name"] != "本人" {
		t.Fatalf("export snapshot %v %v", snapshot, err)
	}
	created, err := s.CreateExportJob(map[string]any{"format": "md", "source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"], "template_id": "builtin-markdown", "template_hash": hashString("template")})
	if err != nil {
		t.Fatal(err)
	}
	if created["state"] != "queued" || created["source_snapshot"].(map[string]any)["content"] != "原已保存正文" {
		t.Fatal("job not queued with saved source")
	}
	log = mustUpdate(t, s, "worklogs", log, map[string]any{"content": "后续变化正文"})
	frozen, err := s.GetExportJob(text(created["id"]))
	if err != nil || frozen["source_snapshot"].(map[string]any)["content"] != "原已保存正文" {
		t.Fatal("job source changed after source edit")
	}
	_, err = s.CreateExportJob(map[string]any{"format": "md", "source_kind": "worklogs", "source_id": log["id"], "source_version": 1})
	assertStatus(t, err, 409)
	_, err = s.CreateExportJob(map[string]any{"format": "md", "source_kind": "worklogs", "source_id": log["id"]})
	assertStatus(t, err, 409)
	_, err = s.CreateExportJob(map[string]any{"format": "md", "source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"], "source_snapshot": map[string]any{"content": "用户伪造"}})
	assertStatus(t, err, 400)
	_, err = s.ExportSnapshot("tasks", text(log["id"]))
	assertStatus(t, err, 400)
	if err = s.Delete("worklogs", text(log["id"]), versionOf(log["version"])); err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateExportJob(map[string]any{"format": "md", "source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"]})
	assertStatus(t, err, 404)
	jobs, err := s.ListExportJobs(nil)
	if err != nil || jobs["total"] != 1 {
		t.Fatal("failed creates leaked jobs")
	}
	if _, leaks := jobs["items"].([]map[string]any)[0]["source_snapshot"]; leaks {
		t.Fatal("list unnecessarily returns full snapshots")
	}
	assertChain(t, s)
}
func TestProjectLogExportKeepsSavedHistoricalSourceAndIssueFields(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "历史来源"})
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "任务", "progress": 20})
	draft, err := s.ProjectDailyLog(text(p["id"]), today())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveProjectDailyLog(draft)
	if err != nil {
		t.Fatal(err)
	}
	mustUpdate(t, s, "tasks", task, map[string]any{"progress": 80})
	snapshot, err := s.ExportSnapshot("project-daily-logs", text(saved["id"]))
	if err != nil {
		t.Fatal(err)
	}
	source := snapshot["source_snapshot"].(map[string]any)
	tasks := source["tasks"].([]any)
	if num(tasks[0].(map[string]any)["progress"]) != 20 {
		t.Fatal("export refreshed historical project sources")
	}
	issue := mustCreate(t, s, "issues", map[string]any{"project_id": p["id"], "title": "待分析问题", "description": "真实问题描述", "owner": "测试负责人"})
	snapshot, err = s.ExportSnapshot("issues", text(issue["id"]))
	if err != nil || snapshot["person_name"] != "测试负责人" || !strings.Contains(text(snapshot["content"]), "真实问题描述") {
		t.Fatal("issue export", snapshot, err)
	}
	if len(snapshot["rows"].([]map[string]any)) < 8 {
		t.Fatal("issue metadata table missing")
	}
	b, err := json.Marshal(snapshot)
	if err != nil || len(b) == 0 {
		t.Fatal("snapshot not JSON serializable")
	}
}
func TestExportJobStateMachineResultsAndStartupInterrupt(t *testing.T) {
	s := testStore(t)
	log := mustCreate(t, s, "worklogs", map[string]any{"content": "用于导出"})
	makeJob := func(format string) map[string]any {
		t.Helper()
		j, e := s.CreateExportJob(map[string]any{"format": format, "source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"]})
		if e != nil {
			t.Fatal(e)
		}
		return j
	}
	j := makeJob("docx")
	id := text(j["id"])
	_, err := s.CompleteExportJob(id, map[string]any{"version": j["version"], "output_name": "result.docx", "output_sha256": hashString("result"), "size": 5})
	assertStatus(t, err, 409)
	j, err = s.ClaimExportJob(id, map[string]any{"version": j["version"]})
	if err != nil || j["state"] != "running" {
		t.Fatal("claim", j, err)
	}
	_, err = s.ClaimExportJob(id, map[string]any{"version": 1})
	assertStatus(t, err, 409)
	for _, bad := range []map[string]any{{"output_name": "../escape.docx", "output_sha256": hashString("result"), "size": 5}, {"output_name": "wrong.md", "output_sha256": hashString("result"), "size": 5}, {"output_name": "result.docx", "output_sha256": "bad", "size": 5}, {"output_name": "result.docx", "output_sha256": hashString("result"), "size": 0}} {
		bad["version"] = j["version"]
		_, err = s.CompleteExportJob(id, bad)
		assertStatus(t, err, 400)
	}
	j, err = s.CompleteExportJob(id, map[string]any{"version": j["version"], "output_name": "result.docx", "output_sha256": hashString("result"), "size": 5})
	if err != nil || j["state"] != "succeeded" || j["output_name"] != "result.docx" {
		t.Fatal("complete", j, err)
	}
	_, err = s.FailExportJob(id, map[string]any{"version": j["version"], "error": "不能覆盖成功"})
	assertStatus(t, err, 409)
	queued := makeJob("md")
	running := makeJob("json")
	running, err = s.ClaimExportJob(text(running["id"]), map[string]any{"version": running["version"]})
	if err != nil {
		t.Fatal(err)
	}
	failed := makeJob("md")
	failed, err = s.FailExportJob(text(failed["id"]), map[string]any{"version": failed["version"], "error": "模板处理失败"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.InterruptExportJobs(); err != nil {
		t.Fatal(err)
	}
	for _, old := range []map[string]any{queued, running} {
		got, e := s.GetExportJob(text(old["id"]))
		if e != nil || got["state"] != "failed" || got["error"] != "服务中断，请重新导出" || versionOf(got["version"]) != versionOf(old["version"])+1 {
			t.Fatal("interrupted pending job", got, e)
		}
	}
	unchanged, err := s.GetExportJob(id)
	if err != nil || unchanged["state"] != "succeeded" || versionOf(unchanged["version"]) != versionOf(j["version"]) {
		t.Fatal("interrupt corrupted completed result")
	}
	unchanged, err = s.GetExportJob(text(failed["id"]))
	if err != nil || unchanged["error"] != "模板处理失败" {
		t.Fatal("interrupt rewrote failure")
	}
	jobs, err := s.ListExportJobs(map[string]string{"state": "failed", "limit": "2", "offset": "1"})
	if err != nil || jobs["total"] != 3 || len(jobs["items"].([]map[string]any)) != 2 {
		t.Fatal("job filter pagination", jobs, err)
	}
	assertChain(t, s)
}
func TestExportJobAuditFailureRollsBackCreationCompletionAndInterrupt(t *testing.T) {
	s := testStore(t)
	log := mustCreate(t, s, "worklogs", map[string]any{"content": "事务导出"})
	input := map[string]any{"format": "md", "source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"]}
	if _, err := s.DB.Exec("CREATE TRIGGER deny_export_audit BEFORE INSERT ON audit WHEN new.entity_type='export_jobs' BEGIN SELECT RAISE(ABORT,'test failure');END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateExportJob(input); err == nil {
		t.Fatal("create ignored audit failure")
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM export_jobs").Scan(&n)
	if n != 0 {
		t.Fatal("job creation leaked")
	}
	s.DB.Exec("DROP TRIGGER deny_export_audit")
	j, err := s.CreateExportJob(input)
	if err != nil {
		t.Fatal(err)
	}
	j, err = s.ClaimExportJob(text(j["id"]), map[string]any{"version": j["version"]})
	if err != nil {
		t.Fatal(err)
	}
	s.DB.Exec("CREATE TRIGGER deny_export_audit BEFORE INSERT ON audit WHEN new.entity_type='export_jobs' BEGIN SELECT RAISE(ABORT,'test failure');END")
	_, err = s.CompleteExportJob(text(j["id"]), map[string]any{"version": j["version"], "output_name": "result.md", "output_sha256": hashString("x"), "size": 1})
	if err == nil {
		t.Fatal("result ignored audit failure")
	}
	if err = s.InterruptExportJobs(); err == nil {
		t.Fatal("interrupt ignored audit failure")
	}
	current, err := s.GetExportJob(text(j["id"]))
	if err != nil || current["state"] != "running" || versionOf(current["version"]) != versionOf(j["version"]) || current["output_name"] != "" {
		t.Fatal("state/audit transaction leaked", current, err)
	}
	assertChain(t, s)
}

func TestPersistedExportJobsInterruptAndMissingArtifactInvalidation(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	log := mustCreate(t, s, "worklogs", map[string]any{"content": "可恢复的原始内容"})
	input := map[string]any{"format": "md", "source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"]}
	queued, err := s.CreateExportJob(input)
	if err != nil {
		t.Fatal(err)
	}
	succeeded, err := s.CreateExportJob(input)
	if err != nil {
		t.Fatal(err)
	}
	succeeded, err = s.ClaimExportJob(text(succeeded["id"]), map[string]any{"version": succeeded["version"]})
	if err != nil {
		t.Fatal(err)
	}
	succeeded, err = s.CompleteExportJob(text(succeeded["id"]), map[string]any{"version": succeeded["version"], "output_name": "result.md", "output_sha256": hashString("output"), "size": 6})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.InterruptExportJobs(); err != nil {
		t.Fatal(err)
	}
	pending, err := s.GetExportJob(text(queued["id"]))
	if err != nil || pending["state"] != "failed" {
		t.Fatal("pending job not recovered after reopen", pending, err)
	}
	restored, err := s.GetExportJob(text(succeeded["id"]))
	if err != nil || restored["state"] != "succeeded" || restored["source_snapshot"].(map[string]any)["content"] != "可恢复的原始内容" {
		t.Fatal("successful persistent job lost source", restored, err)
	}
	_, err = s.InvalidateExportJob(text(succeeded["id"]), map[string]any{"version": 1, "error": "过期检查"})
	assertStatus(t, err, 409)
	invalid, err := s.InvalidateExportJob(text(succeeded["id"]), map[string]any{"version": restored["version"], "error": "备份未含生成结果，请重新导出"})
	if err != nil || invalid["state"] != "failed" || invalid["output_name"] != "" || num(invalid["size"]) != 0 || invalid["source_snapshot"].(map[string]any)["content"] != "可恢复的原始内容" {
		t.Fatal("missing artifact invalidation", invalid, err)
	}
	var action string
	s.DB.QueryRow("SELECT action FROM audit WHERE entity_type='export_jobs' AND entity_id=? ORDER BY id DESC LIMIT 1", succeeded["id"]).Scan(&action)
	if action != "artifact_unavailable" {
		t.Fatal("artifact invalidation not audited")
	}
	_, err = s.InvalidateExportJob(text(succeeded["id"]), map[string]any{"version": invalid["version"], "error": "不能二次覆盖"})
	assertStatus(t, err, 409)
	assertChain(t, s)
}
