package store

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func legacyV2(t *testing.T, root string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/v1.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(raw)); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateV2(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestV3MigrationFromV2KeepsDatasetAndRollsBackDDL(t *testing.T) {
	root := t.TempDir()
	db := legacyV2(t, root)
	var dataset string
	db.QueryRow("SELECT value FROM settings WHERE key='dataset_id'").Scan(&dataset)
	if _, err := db.Exec("INSERT INTO projects(id,code,name,created_at,updated_at)VALUES('keep','KEEP','升级保留','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z');CREATE TABLE reminders(broken TEXT)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(root); err == nil {
		s.Close()
		t.Fatal("expected transactional migration failure")
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	var version, count int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name='time_entries'").Scan(&count)
	if version != 2 || count != 0 {
		t.Fatalf("partial migration survived %d %d", version, count)
	}
	db.Exec("DROP TABLE reminders")
	db.Close()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	settings, err := s.Settings()
	if err != nil || settings["dataset_id"] != dataset {
		t.Fatal("migration changed dataset identity")
	}
	if getNow(t, s, "projects", "keep")["name"] != "升级保留" {
		t.Fatal("migration lost business data")
	}
	s.DB.QueryRow("PRAGMA user_version").Scan(&version)
	if version != SchemaVersion {
		t.Fatal("wrong schema version")
	}
}
func TestTimeEntriesCapsCrossProjectsUpdateRestoreAndReadonly(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "工时甲"})
	q := mustCreate(t, s, "projects", map[string]any{"name": "工时乙"})
	parent := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "阶段"})
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": parent["id"], "name": "已做完", "status": "done"})
	other := mustCreate(t, s, "tasks", map[string]any{"project_id": q["id"], "name": "异项目"})
	entry, err := s.CreateTimeEntry(map[string]any{"project_id": p["id"], "task_id": task["id"], "work_date": "2026-11-05", "minutes": 900, "note": "完成后补录"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateTimeEntry(map[string]any{"project_id": q["id"], "work_date": "2026-11-05", "minutes": 540})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateTimeEntry(map[string]any{"project_id": q["id"], "work_date": "2026-11-05", "minutes": 1})
	assertStatus(t, err, 409)
	_, err = s.UpdateTimeEntry(text(second["id"]), map[string]any{"version": second["version"], "minutes": 541})
	assertStatus(t, err, 409)
	for _, input := range []map[string]any{{"project_id": p["id"], "task_id": other["id"], "minutes": 1}, {"project_id": p["id"], "task_id": parent["id"], "minutes": 1}, {"project_id": p["id"], "minutes": 1.5}, {"project_id": p["id"], "minutes": 0}, {"project_id": p["id"], "minutes": 1, "work_date": "2026-02-31"}} {
		_, err = s.CreateTimeEntry(input)
		assertStatus(t, err, 400)
	}
	_, err = s.DeleteTimeEntry(text(second["id"]), map[string]any{"version": second["version"]})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := s.CreateTimeEntry(map[string]any{"project_id": q["id"], "work_date": "2026-11-05", "minutes": 500})
	if err != nil {
		t.Fatal(err)
	}
	trashed, err := getIncludingTrash(s.DB, "time_entries", text(second["id"]), true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RestoreTrash("time_entries", text(second["id"]), map[string]any{"version": trashed["version"]})
	assertStatus(t, err, 409)
	if _, err = s.DeleteTimeEntry(text(replacement["id"]), map[string]any{"version": replacement["version"]}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RestoreTrash("time_entries", text(second["id"]), map[string]any{"version": trashed["version"]}); err != nil {
		t.Fatal(err)
	}
	_, err = s.SoftDelete("tasks", text(task["id"]), map[string]any{"version": task["version"]})
	assertStatus(t, err, 409)
	q = mustUpdate(t, s, "projects", q, map[string]any{"status": "closed"})
	_, err = s.CreateTimeEntry(map[string]any{"project_id": q["id"], "minutes": 1})
	assertStatus(t, err, 409)
	p = mustUpdate(t, s, "projects", p, map[string]any{"status": "archived"})
	_, err = s.UpdateTimeEntry(text(entry["id"]), map[string]any{"version": entry["version"], "note": "非法"})
	assertStatus(t, err, 409)
	_, err = s.DeleteTimeEntry(text(entry["id"]), map[string]any{"version": entry["version"]})
	assertStatus(t, err, 409)
	assertChain(t, s)
}
func TestTimeSummarySingleSourceLogsAndTrashSearch(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "汇总工时"})
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "现场工作"})
	a, err := s.CreateTimeEntry(map[string]any{"project_id": p["id"], "task_id": task["id"], "work_date": "2026-11-05", "minutes": 481, "note": "工时独特检索词"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateTimeEntry(map[string]any{"project_id": p["id"], "work_date": "2026-11-06", "minutes": 119})
	if err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, "worklogs", map[string]any{"project_id": p["id"], "period_type": "day", "log_date": "2026-11-05", "content": "人工日志 999分钟，不计入汇总"})
	summary, err := s.TimeSummary(map[string]string{"date_from": "2026-11-02", "date_to": "2026-11-08"})
	if err != nil || summary["total_minutes"] != 600 {
		t.Fatalf("summary %v %v", summary, err)
	}
	days := summary["by_day"].([]map[string]any)
	if len(days) != 2 || days[0]["over_eight_hours"] != true || days[1]["over_eight_hours"] != false {
		t.Fatalf("daily warnings %v", days)
	}
	if len(summary["by_project"].([]map[string]any)) != 1 || len(summary["by_task"].([]map[string]any)) != 2 {
		t.Fatal("dimensions incorrect")
	}
	draft, err := s.GenerateLog(map[string]any{"date": "2026-11-05", "period_type": "week", "project_id": p["id"]})
	if err != nil || !strings.Contains(text(draft["content"]), "10.00 小时") || strings.Contains(text(draft["content"]), "999") {
		t.Fatalf("weekly log %v %v", draft, err)
	}
	found, err := s.SearchPage(map[string]string{"q": "独特检索词", "entity_type": "time_entries"})
	if err != nil || found["total"] != 1 {
		t.Fatal("entry not searchable")
	}
	_, err = s.DeleteTimeEntry(text(a["id"]), map[string]any{"version": a["version"]})
	if err != nil {
		t.Fatal(err)
	}
	summary, err = s.TimeSummary(nil)
	if err != nil || summary["total_minutes"] != 119 {
		t.Fatal("trash counted in summary")
	}
	found, err = s.SearchPage(map[string]string{"q": "独特检索词"})
	if err != nil || found["total"] != 0 {
		t.Fatal("trash entry search leaked")
	}
	list, err := s.ListTimeEntries(map[string]string{"project_id": text(p["id"]), "from": "2026-11-06", "to": "2026-11-06"})
	if err != nil || list["total"] != 1 {
		t.Fatalf("time filters %v %v", list, err)
	}
	assertChain(t, s)
}
func TestReminderParserLocalBoundariesAndStrictSyntax(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("CST", 8*3600)
	t.Cleanup(func() { time.Local = old })
	clock := time.Date(2026, 11, 6, 10, 0, 0, 0, time.Local)
	cases := []struct{ input, want string }{{"原文 !今天", "2026-11-06T02:01:00Z"}, {"原文 !明天", "2026-11-07T01:00:00Z"}, {"!周五", "2026-11-13T01:00:00Z"}, {"!2026-11-08 13:30", "2026-11-08T05:30:00Z"}, {"!2026-02-31", ""}, {"!明天 25:00", ""}, {"!后天", ""}}
	for _, c := range cases {
		got, hint := parseReminder(c.input, clock)
		if got != c.want || hint == "" {
			t.Fatalf("parse %q got %q hint %q want %q", c.input, got, hint, c.want)
		}
	}
}
func TestRecordReminderAtomicNoDuplicateCompleteTrashAndRestore(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "提醒项目"})
	content := "原始正文 !2026-11-08 13:30"
	r := mustCreate(t, s, "records", map[string]any{"project_id": p["id"], "content": content})
	rem := r["reminder"].(map[string]any)
	rid := text(rem["id"])
	if r["content"] != content || r["reminder_parse_hint"] == "" {
		t.Fatal("reminder parse lost text or hint")
	}
	r = mustUpdate(t, s, "records", r, map[string]any{"content": "改正文 !明天"})
	if text(r["reminder"].(map[string]any)["id"]) != rid {
		t.Fatal("edit recreated reminder")
	}
	list, err := s.ListReminders(map[string]string{"status": "all"})
	if err != nil || list["total"] != 1 {
		t.Fatal("duplicate reminder")
	}
	reminder := getNow(t, s, "reminders", rid)
	reminder, err = s.CompleteReminder(rid, map[string]any{"version": reminder["version"]})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CompleteReminder(rid, map[string]any{"version": reminder["version"]})
	assertStatus(t, err, 409)
	r = getNow(t, s, "records", text(r["id"]))
	_, err = s.Update("records", text(r["id"]), map[string]any{"version": r["version"], "reminder_due_at": "2026-11-09T09:00:00+08:00"})
	assertStatus(t, err, 409)
	out, err := s.SoftDelete("records", text(r["id"]), map[string]any{"version": r["version"]})
	if err != nil {
		t.Fatal(err)
	}
	list, err = s.ListReminders(map[string]string{"status": "all"})
	if err != nil || list["total"] != 0 {
		t.Fatal("record trash leaked reminder")
	}
	trashed := out["items"].([]map[string]any)[0]
	r, err = s.RestoreTrash("records", text(r["id"]), map[string]any{"version": trashed["version"]})
	if err != nil {
		t.Fatal(err)
	}
	reminder = getNow(t, s, "reminders", rid)
	if reminder["done_at"] == "" || text(r["reminder"].(map[string]any)["id"]) != rid {
		t.Fatal("restore resurrected completed reminder")
	}
	list, err = s.ListReminders(nil)
	if err != nil || list["total"] != 0 {
		t.Fatal("completed reminder woke up")
	}
	r = mustUpdate(t, s, "records", r, map[string]any{"reminder_due_at": nil})
	if r["reminder"] != nil || r["reminder_due_at"] != "" {
		t.Fatal("clear failed")
	}
	r = mustUpdate(t, s, "records", r, map[string]any{"reminder_due_at": "2026-11-10T09:00:00+08:00"})
	if text(r["reminder"].(map[string]any)["id"]) == rid {
		t.Fatal("clear and new did not preserve old completion")
	}
	assertChain(t, s)
}
func TestReminderSaveFailureRollsBackRecordAndAudit(t *testing.T) {
	s := testStore(t)
	if _, err := s.DB.Exec("CREATE TRIGGER reminder_failure BEFORE INSERT ON reminders BEGIN SELECT RAISE(ABORT,'reminder failure');END"); err != nil {
		t.Fatal(err)
	}
	_, err := s.Create("records", map[string]any{"content": "提醒事务唯一词 !明天"})
	if err == nil {
		t.Fatal("expected record transaction failure")
	}
	for _, table := range []string{"records", "reminders", "audit", "search_doc"} {
		var count int
		if err = s.DB.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("rollback leaked %s: %d %v", table, count, err)
		}
	}
	s.DB.Exec("DROP TRIGGER reminder_failure")
	r := mustCreate(t, s, "records", map[string]any{"content": "无效日期 !2026-02-31"})
	if r["reminder"] != nil || r["content"] != "无效日期 !2026-02-31" {
		t.Fatal("invalid shortcut changed text/created reminder")
	}
	_, err = s.Update("records", text(r["id"]), map[string]any{"version": r["version"], "content": "不应更改", "reminder_due_at": "bad"})
	assertStatus(t, err, 400)
	if getNow(t, s, "records", text(r["id"]))["content"] != "无效日期 !2026-02-31" {
		t.Fatal("invalid explicit reminder partially saved record")
	}
}
func TestTodayReminderLocalDueCutoffAndTimeMinutes(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("CST", 8*3600)
	t.Cleanup(func() { time.Local = old })
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "今日"})
	start, _ := time.ParseInLocation("2006-01-02", today(), time.Local)
	for _, offset := range []time.Duration{-time.Second, 0, 24 * time.Hour} {
		_, err := s.CreateReminder(map[string]any{"title": "边界提醒", "project_id": p["id"], "due_at": start.Add(offset).Format(time.RFC3339Nano)})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.CreateTimeEntry(map[string]any{"project_id": p["id"], "minutes": 75, "work_date": today()})
	if err != nil {
		t.Fatal(err)
	}
	todayView, err := s.Today()
	if err != nil || todayView["time_minutes_today"] != 75 || len(todayView["reminders"].([]map[string]any)) != 2 {
		t.Fatalf("today %v %v", todayView, err)
	}
	list, err := s.ListReminders(map[string]string{"date_from": today(), "date_to": today()})
	if err != nil || list["total"] != 1 {
		t.Fatalf("local reminder date %v %v", list, err)
	}
}
func TestFullTaskTreeHistoryPaginationAndTrashHistory(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "完整树"})
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var firstID string
	for i := 0; i < 5005; i++ {
		m := defaults("tasks")
		m["project_id"] = p["id"]
		m["name"] = "完整任务"
		m["sort_order"] = i + 1
		m["wbs_code"] = text(i + 1)
		if i == 0 {
			firstID = text(m["id"])
		} else if i == 1 {
			m["parent_id"] = firstID
			m["sort_order"] = 1
			m["wbs_code"] = "1.1"
		}
		if err = insert(tx, "tasks", m); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	tree, err := s.TaskTree(text(p["id"]))
	if err != nil || tree["total"] != 5005 {
		t.Fatalf("tree truncated %v %v", tree["total"], err)
	}
	items := tree["items"].([]map[string]any)
	if items[0]["depth"] != 0 || items[0]["has_children"] != true || items[1]["depth"] != 1 || text(items[1]["parent_id"]) != firstID {
		t.Fatal("tree not DFS/depth")
	}
	r := mustCreate(t, s, "records", map[string]any{"content": "历史"})
	r = mustUpdate(t, s, "records", r, map[string]any{"content": "历史2"})
	r = mustUpdate(t, s, "records", r, map[string]any{"content": "历史3"})
	if err = s.Delete("records", text(r["id"]), versionOf(r["version"])); err != nil {
		t.Fatal(err)
	}
	history, err := s.EntityHistory("records", text(r["id"]), map[string]string{"limit": "2"})
	if err != nil || history["total"] != 4 || history["next_cursor"] == "" {
		t.Fatalf("history %v %v", history, err)
	}
	histItems := history["items"].([]map[string]any)
	if histItems[0]["action"] != "trash" || histItems[0]["after"] == nil || histItems[0]["after_json"] == "" {
		t.Fatal("history fields/order")
	}
	history, err = s.EntityHistory("records", text(r["id"]), map[string]string{"limit": "2", "cursor": text(history["next_cursor"])})
	if err != nil || len(history["items"].([]map[string]any)) != 2 || history["next_cursor"] != "" {
		t.Fatal("history pagination")
	}
}

func TestStandaloneReminderChangesInvalidateRecordSnapshot(t *testing.T) {
	s := testStore(t)
	r := mustCreate(t, s, "records", map[string]any{"content": "提醒并发 !明天"})
	rem := r["reminder"].(map[string]any)
	id := text(rem["id"])
	reminder := getNow(t, s, "reminders", id)
	_, err := s.UpdateReminder(id, map[string]any{"version": reminder["version"], "due_at": "2026-11-11T09:00:00+08:00"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Update("records", text(r["id"]), map[string]any{"version": r["version"], "reminder_due_at": "2026-11-12T09:00:00+08:00"})
	assertStatus(t, err, 409)
	r = getNow(t, s, "records", text(r["id"]))
	reminder = getNow(t, s, "reminders", id)
	_, err = s.CompleteReminder(id, map[string]any{"version": reminder["version"]})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Update("records", text(r["id"]), map[string]any{"version": r["version"], "content": "不能覆盖"})
	assertStatus(t, err, 409)
	r = getNow(t, s, "records", text(r["id"]))
	reminder = getNow(t, s, "reminders", id)
	_, err = s.SoftDelete("reminders", id, map[string]any{"version": reminder["version"]})
	if err != nil {
		t.Fatal(err)
	}
	fresh := getNow(t, s, "records", text(r["id"]))
	if versionOf(fresh["version"]) <= versionOf(r["version"]) || fresh["reminder"] != nil {
		t.Fatal("reminder recycle did not invalidate record")
	}
	assertChain(t, s)
}
func TestTimeEntryCannotEscapeClosedProjectByReassigning(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "关闭前"})
	q := mustCreate(t, s, "projects", map[string]any{"name": "活动"})
	entry, err := s.CreateTimeEntry(map[string]any{"project_id": p["id"], "minutes": 30})
	if err != nil {
		t.Fatal(err)
	}
	p = mustUpdate(t, s, "projects", p, map[string]any{"status": "closed"})
	_, err = s.UpdateTimeEntry(text(entry["id"]), map[string]any{"version": entry["version"], "project_id": q["id"]})
	assertStatus(t, err, 409)
}

func TestTodayHidesClosedArchivedProjectRemindersButKeepsHistory(t *testing.T) {
	s := testStore(t)
	closed := mustCreate(t, s, "projects", map[string]any{"name": "关闭提醒"})
	archived := mustCreate(t, s, "projects", map[string]any{"name": "归档提醒"})
	a, err := s.CreateReminder(map[string]any{"title": "关闭前提醒", "project_id": closed["id"], "due_at": time.Now().Add(-time.Hour).Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateReminder(map[string]any{"title": "归档前提醒", "project_id": archived["id"], "due_at": time.Now().Add(-time.Hour).Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	closed = mustUpdate(t, s, "projects", closed, map[string]any{"status": "closed"})
	archived = mustUpdate(t, s, "projects", archived, map[string]any{"status": "archived"})
	view, err := s.Today()
	if err != nil || len(view["reminders"].([]map[string]any)) != 0 {
		t.Fatal("nonactionable reminders leaked today")
	}
	history, err := s.ListReminders(map[string]string{"status": "all"})
	if err != nil || history["total"] != 2 {
		t.Fatal("closed/archived reminder history missing")
	}
	_, err = s.CreateReminder(map[string]any{"title": "关闭后新增", "project_id": closed["id"], "due_at": now()})
	assertStatus(t, err, 409)
	_, err = s.CompleteReminder(text(a["id"]), map[string]any{"version": a["version"]})
	if err != nil {
		t.Fatal("closed project completion should allow cleanup", err)
	}
	_, err = s.CompleteReminder(text(b["id"]), map[string]any{"version": b["version"]})
	assertStatus(t, err, 409)
	assertChain(t, s)
}

func TestTaskPagedListAndDetailExposeActiveChildrenWithoutTruncation(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "行内叶子"})
	parent := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "父"})
	leaf := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": parent["id"], "name": "叶"})
	page, err := s.ListPage("tasks", map[string]string{"project_id": text(p["id"])})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range page["items"].([]map[string]any) {
		if m["has_children"] != (m["id"] == parent["id"]) {
			t.Fatalf("invalid has_children: %v", m)
		}
	}
	if getNow(t, s, "tasks", text(parent["id"]))["has_children"] != true || getNow(t, s, "tasks", text(leaf["id"]))["has_children"] != false {
		t.Fatal("detail child derivation missing")
	}
	if err = s.Delete("tasks", text(leaf["id"]), versionOf(leaf["version"])); err != nil {
		t.Fatal(err)
	}
	if getNow(t, s, "tasks", text(parent["id"]))["has_children"] != false {
		t.Fatal("recycled child counted as active")
	}
}

func TestTaskActualTimeConsistentAcrossReadPathsTrashAndMove(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "实际工时"})
	a := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "历史叶子"})
	b := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "另一阶段"})
	own, err := s.CreateTimeEntry(map[string]any{"project_id": p["id"], "task_id": a["id"], "minutes": 90, "work_date": today()})
	if err != nil {
		t.Fatal(err)
	}
	child := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": a["id"], "name": "后加子工作"})
	work, err := s.CreateTimeEntry(map[string]any{"project_id": p["id"], "task_id": child["id"], "minutes": 30, "work_date": today()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateTimeEntry(map[string]any{"project_id": p["id"], "minutes": 60, "work_date": today()}); err != nil {
		t.Fatal(err)
	}
	verify := func(want map[string]int) {
		t.Helper()
		for id, minutes := range want {
			m := getNow(t, s, "tasks", id)
			if num(m["actual_minutes"]) != float64(minutes) || num(m["actual_hours"]) != float64(minutes)/60 {
				t.Fatalf("Get actuals: %v want %d", m, minutes)
			}
		}
		list, total, e := s.List("tasks", map[string]string{"project_id": text(p["id"])})
		if e != nil || total != 3 {
			t.Fatal("List actuals failed", e)
		}
		page, e := s.ListPage("tasks", map[string]string{"project_id": text(p["id"]), "limit": "1"})
		if e != nil {
			t.Fatal(e)
		}
		tree, e := s.TaskTree(text(p["id"]))
		if e != nil {
			t.Fatal(e)
		}
		today, e := s.Today()
		if e != nil {
			t.Fatal(e)
		}
		for _, items := range [][]map[string]any{list, page["items"].([]map[string]any), tree["items"].([]map[string]any), today["tasks"].([]map[string]any)} {
			for _, m := range items {
				if num(m["actual_minutes"]) != float64(want[text(m["id"])]) || num(m["actual_hours"]) != float64(want[text(m["id"])])/60 {
					t.Fatalf("read paths disagree actuals %v want %v", m, want)
				}
			}
		}
	}
	verify(map[string]int{text(a["id"]): 120, text(b["id"]): 0, text(child["id"]): 30})
	if _, err = s.DeleteTimeEntry(text(work["id"]), map[string]any{"version": work["version"]}); err != nil {
		t.Fatal(err)
	}
	verify(map[string]int{text(a["id"]): 90, text(b["id"]): 0, text(child["id"]): 0})
	trash, err := getIncludingTrash(s.DB, "time_entries", text(work["id"]), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RestoreTrash("time_entries", text(work["id"]), map[string]any{"version": trash["version"]}); err != nil {
		t.Fatal(err)
	}
	child = getNow(t, s, "tasks", text(child["id"]))
	b = getNow(t, s, "tasks", text(b["id"]))
	if _, err = s.MoveTask(text(child["id"]), map[string]any{"version": child["version"], "parent_id": b["id"], "expected_versions": map[string]any{text(b["id"]): b["version"]}}); err != nil {
		t.Fatal(err)
	}
	verify(map[string]int{text(a["id"]): 90, text(b["id"]): 30, text(child["id"]): 30})
	if _, err = s.DeleteTimeEntry(text(own["id"]), map[string]any{"version": own["version"]}); err != nil {
		t.Fatal(err)
	}
	verify(map[string]int{text(a["id"]): 0, text(b["id"]): 30, text(child["id"]): 30})
	summary, err := s.TimeSummary(nil)
	if err != nil || summary["total_minutes"] != 90 {
		t.Fatalf("project-only entry double counted/allocated to task: %v %v", summary, err)
	}
	assertChain(t, s)
}
