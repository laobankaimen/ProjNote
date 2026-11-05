package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func legacyV5(t *testing.T, root string) *sql.DB {
	t.Helper()
	db := legacyV4(t, root)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateV5(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestV6MigrationPreservesDraftLogsSnapshotsSearchAndAudit(t *testing.T) {
	root := t.TempDir()
	old := &Store{DB: legacyV5(t, root)}
	p := mustCreate(t, old, "projects", map[string]any{"name": "schema5 原始项目"})
	day := mustCreate(t, old, "worklogs", map[string]any{"project_id": p["id"], "log_date": "2026-11-05", "content": "旧日报检索证据"})
	week := mustCreate(t, old, "worklogs", map[string]any{"project_id": p["id"], "period_type": "week", "log_date": "2026-11-05", "content": "旧周报人工正文"})
	trash := mustCreate(t, old, "worklogs", map[string]any{"project_id": p["id"], "log_date": "2026-11-06", "content": "回收旧日报正文"})
	if err := old.Delete("worklogs", text(trash["id"]), versionOf(trash["version"])); err != nil {
		t.Fatal(err)
	}
	trash, err := getIncludingTrash(old.DB, "worklogs", text(trash["id"]), true)
	if err != nil {
		t.Fatal(err)
	}
	replacement := mustCreate(t, old, "worklogs", map[string]any{"project_id": p["id"], "log_date": "2026-11-06", "content": "同日有效替代日报"})
	job, err := old.CreateExportJob(map[string]any{"source_kind": "worklogs", "source_id": day["id"], "source_version": day["version"], "format": "md", "template_id": "frozen-template", "template_hash": hashString("template5")})
	if err != nil {
		t.Fatal(err)
	}
	job, err = old.ClaimExportJob(text(job["id"]), map[string]any{"version": job["version"]})
	if err != nil {
		t.Fatal(err)
	}
	job, err = old.CompleteExportJob(text(job["id"]), map[string]any{"version": job["version"], "output_name": "历史结果.md", "output_sha256": hashString("old data"), "size": 8})
	if err != nil {
		t.Fatal(err)
	}
	const updated = "2026-11-05T00:00:00.123Z"
	if _, err = old.DB.Exec("INSERT INTO desktop_drafts(key,content,project_id,version,updated_at)VALUES('quick_note','旧便签必须继续按现场记录保存',?,7,?)", p["id"], updated); err != nil {
		t.Fatal(err)
	}
	settings, _ := old.Settings()
	var snapshotRaw, auditTail string
	var auditCount int
	old.DB.QueryRow("SELECT source_snapshot FROM export_jobs WHERE id=?", job["id"]).Scan(&snapshotRaw)
	old.DB.QueryRow("SELECT hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&auditTail)
	old.DB.QueryRow("SELECT count(*) FROM audit").Scan(&auditCount)
	old.Close()

	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version, count int
	if err = s.DB.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != SchemaVersion {
		t.Fatal("schema6", version, err)
	}
	for _, original := range []map[string]any{day, week, replacement} {
		if got := getNow(t, s, "worklogs", text(original["id"])); !reflect.DeepEqual(got, original) {
			t.Fatalf("changed log: got %v want %v", got, original)
		}
	}
	if got, e := getIncludingTrash(s.DB, "worklogs", text(trash["id"]), true); e != nil || !reflect.DeepEqual(got, trash) {
		t.Fatal("changed trash", got, e)
	}
	if got, e := s.GetExportJob(text(job["id"])); e != nil || !reflect.DeepEqual(got, job) {
		t.Fatal("changed frozen export", got, e)
	}
	var afterSnapshot, afterTail string
	s.DB.QueryRow("SELECT source_snapshot FROM export_jobs WHERE id=?", job["id"]).Scan(&afterSnapshot)
	s.DB.QueryRow("SELECT hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&afterTail)
	s.DB.QueryRow("SELECT count(*) FROM audit").Scan(&count)
	if snapshotRaw != afterSnapshot || auditCount != count || auditTail != afterTail {
		t.Fatal("migration rewrote immutable JSON or audit")
	}
	gotSettings, _ := s.Settings()
	if gotSettings["dataset_id"] != settings["dataset_id"] || gotSettings["desktop_default"] != "1" {
		t.Fatal("settings migration", gotSettings)
	}
	draft, err := s.DesktopDraft()
	if err != nil || draft["mode"] != "record" || draft["content"] != "旧便签必须继续按现场记录保存" || versionOf(draft["version"]) != 7 || draft["updated_at"] != updated {
		t.Fatal("old draft conversion", draft, err)
	}
	found, err := s.SearchPage(map[string]string{"q": "旧日报检索证据"})
	if err != nil || found["total"] != 1 {
		t.Fatal("lost search index", found, err)
	}
	rows, err := s.DB.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Fatal("migration broke foreign keys")
	}
	rows.Close()
	for _, name := range []string{"worklogs_active", "worklogs_period", "export_jobs_state", "export_jobs_created"} {
		s.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name=?", name).Scan(&count)
		if count != 1 {
			t.Fatal("missing index", name)
		}
	}
	assertChain(t, s)
}

func TestV6MigrationLateFailureRollsBackBothRebuiltTablesAndDraft(t *testing.T) {
	root := t.TempDir()
	db := legacyV5(t, root)
	_, err := db.Exec(`INSERT INTO worklogs(id,period_type,log_date,content,created_at,updated_at)VALUES('keep','day','2026-11-05','不能因升级失败丢失','at','at');
INSERT INTO desktop_drafts(key,content,version,updated_at)VALUES('quick_note','旧稿',5,'at');
INSERT INTO export_jobs(id,state,format,source_kind,source_id,source_version,source_snapshot,created_at,updated_at)VALUES('job','failed','json','worklogs','keep',1,'{"content":"原冻结快照"}','at','at');
DROP INDEX export_jobs_state;
CREATE INDEX export_jobs_state ON settings(value);`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, e := Open(root); e == nil {
		s.Close()
		t.Fatal("late migration collision should fail")
	}
	db, err = sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	var version, modeColumns, count int
	var sqlDefinition, content, snapshot string
	db.QueryRow("PRAGMA user_version").Scan(&version)
	db.QueryRow("SELECT count(*) FROM pragma_table_info('desktop_drafts') WHERE name='mode'").Scan(&modeColumns)
	db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='worklogs'").Scan(&sqlDefinition)
	if version != 5 || modeColumns != 0 || strings.Contains(sqlDefinition, "month") {
		t.Fatal("partially committed schema", version, modeColumns, sqlDefinition)
	}
	db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='export_jobs'").Scan(&sqlDefinition)
	if strings.Contains(sqlDefinition, "xlsx") {
		t.Fatal("export CHECK partially upgraded")
	}
	db.QueryRow("SELECT content FROM worklogs WHERE id='keep'").Scan(&content)
	db.QueryRow("SELECT source_snapshot FROM export_jobs WHERE id='job'").Scan(&snapshot)
	if content != "不能因升级失败丢失" || snapshot != `{"content":"原冻结快照"}` {
		t.Fatal("rollback lost source", content, snapshot)
	}
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN('worklogs_v6','export_jobs_v6')").Scan(&count)
	if count != 0 {
		t.Fatal("temporary rebuilt table survived rollback")
	}
	if _, err = db.Exec("DROP INDEX export_jobs_state;CREATE INDEX export_jobs_state ON export_jobs(state)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(root)
	if err != nil {
		t.Fatal("retry upgrade", err)
	}
	defer s.Close()
	draft, err := s.DesktopDraft()
	if err != nil || draft["mode"] != "record" || draft["content"] != "旧稿" || versionOf(draft["version"]) != 5 {
		t.Fatal("retry lost draft", draft, err)
	}
}

func TestDesktopDraftModePreservationValidationConflictAndRestart(t *testing.T) {
	root := t.TempDir()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	draft, err := s.DesktopDraft()
	if err != nil || draft["mode"] != "task" || versionOf(draft["version"]) != 1 {
		t.Fatal("new empty default must be task", draft, err)
	}
	p := mustCreate(t, s, "projects", map[string]any{"name": "便签模式"})
	save := func(mode any, provide bool) (map[string]any, error) {
		input := map[string]any{"version": draft["version"], "content": "未提交的正文", "project_id": p["id"]}
		if provide {
			input["mode"] = mode
		}
		return s.SaveDesktopDraft(input)
	}
	draft, err = save(nil, false)
	if err != nil || draft["mode"] != "task" || versionOf(draft["version"]) != 2 {
		t.Fatal("omitted mode changed task", draft, err)
	}
	draft, err = save("record", true)
	if err != nil || draft["mode"] != "record" || versionOf(draft["version"]) != 3 {
		t.Fatal("switch mode", draft, err)
	}
	draft, err = save(nil, false)
	if err != nil || draft["mode"] != "record" {
		t.Fatal("omitted mode changed record", draft, err)
	}
	for _, bad := range []any{"", "TASK", "other", nil, 1, true} {
		_, err = save(bad, true)
		assertStatus(t, err, 400)
		got, e := s.DesktopDraft()
		if e != nil || !reflect.DeepEqual(got, draft) {
			t.Fatal("invalid mode changed saved draft", got, e)
		}
	}
	_, err = s.SaveDesktopDraft(map[string]any{"version": 2, "mode": "task", "content": "过期清稿", "project_id": ""})
	assertStatus(t, err, 409)
	s.Close()
	s, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.DesktopDraft()
	if err != nil || !reflect.DeepEqual(got, draft) {
		t.Fatal("restart changed mode or text", got, err)
	}
	_, total, err := s.List("records", nil)
	if err != nil || total != 0 {
		t.Fatal("mode switch submitted record", total, err)
	}
	_, total, err = s.List("tasks", nil)
	if err != nil || total != 0 {
		t.Fatal("mode switch submitted task", total, err)
	}
	assertChain(t, s)
}

func TestMonthPeriodCalendarBoundsAndLegacyAnchors(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("CST", 8*3600)
	t.Cleanup(func() { time.Local = oldLocal })
	for _, c := range []struct{ date, period, start, end string }{
		{"2024-02-29", "month", "2024-02-01", "2024-03-01"},
		{"2023-02-28", "month", "2023-02-01", "2023-03-01"},
		{"2026-12-31", "month", "2026-12-01", "2027-01-01"},
		{"2027-01-01", "month", "2027-01-01", "2027-02-01"},
		{"2026-11-05", "day", "2026-11-05", "2026-11-06"},
		{"2027-01-01", "week", "2026-12-28", "2027-01-04"},
	} {
		start, end, err := logPeriodBounds(c.date, c.period)
		if err != nil || start.Format("2006-01-02") != c.start || end.Format("2006-01-02") != c.end || start.Hour() != 0 || end.Hour() != 0 || start.Location() != time.Local {
			t.Fatalf("%+v: %v %v %v", c, start, end, err)
		}
	}
	for _, bad := range []string{"2023-02-29", "2024-02-30", "2026-13-01", "2026-11-31", "2026-1-01", ""} {
		_, _, err := logPeriodBounds(bad, "month")
		assertStatus(t, err, 400)
	}
}

func TestMonthlyGenerateUsesLocalLeapMonthAndPersonWithoutDoubleCounting(t *testing.T) {
	oldLocal := time.Local
	time.Local = time.FixedZone("CST", 8*3600)
	t.Cleanup(func() { time.Local = oldLocal })
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "月报范围"})
	for _, r := range []struct{ title, occurred string }{
		{"月首纳入", "2024-02-01T00:00:00+08:00"},
		{"闰月末纳入", "2024-02-29T23:59:59+08:00"},
		{"上月末排除", "2024-01-31T23:59:59+08:00"},
		{"下月首排除", "2024-03-01T00:00:00+08:00"},
	} {
		mustCreate(t, s, "records", map[string]any{"project_id": p["id"], "title": r.title, "occurred_at": r.occurred})
	}
	for _, entry := range []struct {
		day, person string
		minutes     int
	}{
		{"2024-02-01", "张工", 60}, {"2024-02-29", "张工", 90},
		{"2024-01-31", "张工", 80}, {"2024-03-01", "张工", 70}, {"2024-02-15", "李工", 180},
	} {
		mustCreate(t, s, "time_entries", map[string]any{"project_id": p["id"], "assignee": entry.person, "work_date": entry.day, "minutes": entry.minutes})
	}
	saved := mustCreate(t, s, "worklogs", map[string]any{"project_id": p["id"], "period_type": "month", "log_date": "2024-02-29", "content": "人工编辑月报保留，999分钟不得再相加"})
	draft, err := s.GenerateLog(map[string]any{"project_id": p["id"], "period_type": "month", "date": "2024-02-29", "assignee": "张工"})
	if err != nil || len(draft) != 4 || draft["log_date"] != "2024-02-01" || draft["period_type"] != "month" || draft["project_id"] != p["id"] {
		t.Fatal("monthly four-field draft", draft, err)
	}
	content := text(draft["content"])
	for _, included := range []string{"工作月报", "2024-02-01", "月首纳入", "闰月末纳入", "人员：张工", "合计 150 分钟", "后续计划"} {
		if !strings.Contains(content, included) {
			t.Fatal("missing monthly content", included, content)
		}
	}
	for _, excluded := range []string{"上月末排除", "下月首排除", "999", "180 分钟"} {
		if strings.Contains(content, excluded) {
			t.Fatal("wrong month/person or double counting", excluded, content)
		}
	}
	if got := getNow(t, s, "worklogs", text(saved["id"])); got["content"] != saved["content"] || got["version"] != saved["version"] {
		t.Fatal("generation overwrote saved month")
	}
	snapshot, err := s.ExportSnapshot("worklogs", text(saved["id"]))
	if err != nil || snapshot["period_type"] != "month" || snapshot["log_date"] != "2024-02-01" || snapshot["content"] != saved["content"] {
		t.Fatal("monthly export source", snapshot, err)
	}
	for _, period := range []string{"day", "week"} {
		legacy, err := s.GenerateLog(map[string]any{"date": "2024-02-29", "period_type": period})
		if err != nil || len(legacy) != 4 || legacy["period_type"] != period {
			t.Fatal("legacy draft contract", legacy, err)
		}
	}
	_, err = s.GenerateLog(map[string]any{"date": "2023-02-29", "period_type": "month"})
	assertStatus(t, err, 400)
	assertChain(t, s)
}

func TestMonthlyWorklogNormalizationUniquenessSoftDeleteAndRestore(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "月报唯一范围"})
	month := mustCreate(t, s, "worklogs", map[string]any{"project_id": p["id"], "period_type": "month", "log_date": "2026-12-31", "content": "原月报"})
	if month["log_date"] != "2026-12-01" {
		t.Fatal("month not anchored", month)
	}
	_, err := s.Create("worklogs", map[string]any{"project_id": p["id"], "period_type": "month", "log_date": "2026-12-05", "content": "重复月报"})
	assertStatus(t, err, 409)
	day := mustCreate(t, s, "worklogs", map[string]any{"project_id": p["id"], "period_type": "day", "log_date": "2026-12-01", "content": "同月首日报"})
	_, err = s.Update("worklogs", text(day["id"]), map[string]any{"version": day["version"], "period_type": "month", "log_date": "2026-12-20"})
	assertStatus(t, err, 409)
	if getNow(t, s, "worklogs", text(day["id"]))["period_type"] != "day" {
		t.Fatal("duplicate update partially committed")
	}
	if err = s.Delete("worklogs", text(month["id"]), versionOf(month["version"])); err != nil {
		t.Fatal(err)
	}
	replacement := mustCreate(t, s, "worklogs", map[string]any{"project_id": p["id"], "period_type": "month", "log_date": "2026-12-10", "content": "替代月报"})
	trashed, err := getIncludingTrash(s.DB, "worklogs", text(month["id"]), true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RestoreTrash("worklogs", text(month["id"]), map[string]any{"version": trashed["version"]})
	assertStatus(t, err, 409)
	if err = s.Delete("worklogs", text(replacement["id"]), versionOf(replacement["version"])); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RestoreTrash("worklogs", text(month["id"]), map[string]any{"version": trashed["version"]}); err != nil {
		t.Fatal(err)
	}
	month = getNow(t, s, "worklogs", text(month["id"]))
	month = mustUpdate(t, s, "worklogs", month, map[string]any{"log_date": "2027-01-31"})
	if month["log_date"] != "2027-01-01" {
		t.Fatal("cross-year update anchor", month)
	}
	_, err = s.Update("worklogs", text(month["id"]), map[string]any{"version": month["version"], "log_date": "2027-02-29"})
	assertStatus(t, err, 400)
	if getNow(t, s, "worklogs", text(month["id"]))["log_date"] != "2027-01-01" {
		t.Fatal("invalid date update committed")
	}
	assertChain(t, s)
}

func TestV6ExportNewFormatsFreezeMonthlySourceAndEnforceExtension(t *testing.T) {
	s := testStore(t)
	log := mustCreate(t, s, "worklogs", map[string]any{"period_type": "month", "log_date": "2026-11-05", "content": "导出原月报"})
	for _, format := range []string{"xlsx", "html"} {
		job, err := s.CreateExportJob(map[string]any{"source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"], "format": format, "template_id": "builtin-" + format, "template_hash": hashString(format)})
		if err != nil || job["state"] != "queued" || job["format"] != format {
			t.Fatal("new export format", job, err)
		}
		job, err = s.ClaimExportJob(text(job["id"]), map[string]any{"version": job["version"]})
		if err != nil {
			t.Fatal(err)
		}
		_, err = s.CompleteExportJob(text(job["id"]), map[string]any{"version": job["version"], "output_name": "错误.pdf", "output_sha256": hashString("data"), "size": 4})
		assertStatus(t, err, 400)
		job, err = s.CompleteExportJob(text(job["id"]), map[string]any{"version": job["version"], "output_name": "月报." + format, "output_sha256": hashString("data"), "size": 4})
		if err != nil || job["state"] != "succeeded" {
			t.Fatal("complete new format", job, err)
		}
		items, err := s.ListExportJobs(map[string]string{"format": format})
		if err != nil || items["total"] != 1 {
			t.Fatal("format list", items, err)
		}
		snapshot := job["source_snapshot"].(map[string]any)
		if snapshot["period_type"] != "month" || snapshot["log_date"] != "2026-11-01" || snapshot["content"] != "导出原月报" {
			t.Fatal("new format lost frozen source", snapshot)
		}
	}
	assertChain(t, s)
}

func TestTodayNewDesktopTaskNotHiddenByBacklogAndSettingsDefault(t *testing.T) {
	s := testStore(t)
	settings, err := s.Settings()
	if err != nil || settings["desktop_default"] != "1" {
		t.Fatal("desktop setting default", settings, err)
	}
	if err = s.SetSettings(map[string]any{"desktop_default": "0"}); err != nil {
		t.Fatal(err)
	}
	assertStatus(t, s.SetSettings(map[string]any{"desktop_default": "yes"}), 400)
	settings, _ = s.Settings()
	if settings["desktop_default"] != "0" {
		t.Fatal("invalid setting overwrote preference")
	}
	p := mustCreate(t, s, "projects", map[string]any{"name": "桌面任务可见性"})
	for i := 0; i < 105; i++ {
		mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": fmt.Sprintf("既有待办%03d", i), "plan_start": "2020-01-01", "plan_end": "2020-01-01"})
	}
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "便签刚录入的任务", "description": "正文保留", "plan_start": today(), "plan_end": today()})
	mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "未来任务不纳入", "plan_start": time.Now().AddDate(0, 0, 1).Format("2006-01-02")})
	view, err := s.Today()
	if err != nil {
		t.Fatal(err)
	}
	tasks := view["tasks"].([]map[string]any)
	if len(tasks) != 106 {
		t.Fatalf("Today silently capped applicable tasks: %d", len(tasks))
	}
	found := false
	for _, item := range tasks {
		if item["id"] == task["id"] {
			found = item["status"] == "todo" && item["has_children"] == false && item["plan_start"] == today() && item["plan_end"] == today()
		}
	}
	if !found {
		t.Fatal("desktop-created task absent after immediate Today refresh")
	}
	_, _, err = s.List("tasks", map[string]string{"limit": "1"})
	if err != nil {
		t.Fatal(err)
	}
	assertChain(t, s)
}

func TestMonthlyDraftDoesNotTruncateAtFiveHundredRecords(t *testing.T) {
	s := testStore(t)
	_, err := s.write(func(tx *sql.Tx) (map[string]any, error) {
		for i := 0; i < 501; i++ {
			if _, e := create(tx, "records", map[string]any{"title": fmt.Sprintf("月度证据%03d", i), "occurred_at": "2026-11-05T12:00:00Z"}); e != nil {
				return nil, e
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.GenerateLog(map[string]any{"date": "2026-11-30", "period_type": "month"})
	if err != nil || strings.Count(text(draft["content"]), "月度证据") != 501 {
		t.Fatal("monthly evidence truncated", err)
	}
	if _, err = json.Marshal(draft); err != nil {
		t.Fatal("draft response not JSON compatible", err)
	}
}
