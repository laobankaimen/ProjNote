package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func hashString(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func legacyV3(t *testing.T, root string) *sql.DB {
	t.Helper()
	db := legacyV2(t, root)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateV3(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestV4MigrationKeepsHistoryPersonAndRollsBackAllDDL(t *testing.T) {
	root := t.TempDir()
	db := legacyV3(t, root)
	if _, err := db.Exec("UPDATE settings SET value='周工' WHERE key='operator';INSERT INTO projects(id,code,name,created_at,updated_at)VALUES('keep','KEEP','历史项目','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z');INSERT INTO time_entries(id,project_id,work_date,minutes,note,created_at,updated_at)VALUES('old','keep','2026-11-05',120,'历史工时','2026-11-05T00:00:00Z','2026-11-05T00:00:00Z');CREATE TABLE team_daily_entries(broken TEXT)"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(root); err == nil {
		s.Close()
		t.Fatal("migration expected to fail")
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	var version, added int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	db.QueryRow("SELECT count(*) FROM pragma_table_info('time_entries') WHERE name='assignee'").Scan(&added)
	if version != 3 || added != 0 {
		t.Fatal("schema4 partially committed", version, added)
	}
	db.Exec("DROP TABLE team_daily_entries")
	db.Close()
	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entry := getNow(t, s, "time_entries", "old")
	if entry["assignee"] != "周工" || num(entry["minutes"]) != 120 || entry["note"] != "历史工时" {
		t.Fatalf("history migration %v", entry)
	}
	if err = s.SetSettings(map[string]any{"operator": "新名字"}); err != nil {
		t.Fatal(err)
	}
	if getNow(t, s, "time_entries", "old")["assignee"] != "周工" {
		t.Fatal("operator rename rewrote historical person")
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN('members','project_members','member_daily_logs')").Scan(&n)
	if n != 0 {
		t.Fatal("LAN schema unexpectedly added")
	}
	assertChain(t, s)
}
func TestTimePersonDailyCapsAndAssigneeChanges(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "多人代填"})
	a, err := s.CreateTimeEntry(map[string]any{"project_id": p["id"], "work_date": "2026-11-05", "minutes": 1440, "assignee": " 王工 "})
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateTimeEntry(map[string]any{"project_id": p["id"], "work_date": "2026-11-05", "minutes": 1440, "assignee": "李工"})
	if err != nil {
		t.Fatal(err)
	}
	if a["assignee"] != "王工" {
		t.Fatal("person not normalized")
	}
	_, err = s.CreateTimeEntry(map[string]any{"project_id": p["id"], "work_date": "2026-11-05", "minutes": 1, "assignee": "王工"})
	assertStatus(t, err, 409)
	_, err = s.UpdateTimeEntry(text(b["id"]), map[string]any{"version": b["version"], "assignee": "王工"})
	assertStatus(t, err, 409)
	summary, err := s.TimeSummary(map[string]string{"assignee": "王工"})
	if err != nil || summary["total_minutes"] != 1440 || len(summary["by_assignee"].([]map[string]any)) != 1 {
		t.Fatalf("person filter %v %v", summary, err)
	}
	if _, err = s.DeleteTimeEntry(text(a["id"]), map[string]any{"version": a["version"]}); err != nil {
		t.Fatal(err)
	}
	mustCreate(t, s, "time_entries", map[string]any{"project_id": p["id"], "work_date": "2026-11-05", "minutes": 100, "assignee": "王工"})
	trash, err := getIncludingTrash(s.DB, "time_entries", text(a["id"]), true)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RestoreTrash("time_entries", text(a["id"]), map[string]any{"version": trash["version"]})
	assertStatus(t, err, 409)
	if err = s.SetSettings(map[string]any{"operator": "当前本人"}); err != nil {
		t.Fatal(err)
	}
	entry, err := s.CreateTimeEntry(map[string]any{"project_id": p["id"], "minutes": 1})
	if err != nil || entry["assignee"] != "当前本人" {
		t.Fatal("new person default", entry, err)
	}
	assertChain(t, s)
}
func TestTeamDailyEntryValidationUniqueTrashRestoreAndAudit(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "团队条目"})
	q := mustCreate(t, s, "projects", map[string]any{"name": "异项目"})
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "研发", "assignee": "王工"})
	other := mustCreate(t, s, "tasks", map[string]any{"project_id": q["id"], "name": "其他"})
	input := map[string]any{"project_id": p["id"], "log_date": "2026-11-05", "person_name": "王工", "content": "完成接口联调", "next_plan": "明天验收", "blockers": "待设备", "task_ids": []any{task["id"]}}
	entry, err := s.CreateTeamDailyEntry(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(entry["task_ids"].([]any)) != 1 {
		t.Fatal("task ids not decoded")
	}
	_, err = s.CreateTeamDailyEntry(input)
	assertStatus(t, err, 409)
	_, err = s.UpdateTeamDailyEntry(text(entry["id"]), map[string]any{"version": entry["version"], "task_ids": []any{other["id"]}})
	assertStatus(t, err, 400)
	_, err = s.UpdateTeamDailyEntry(text(entry["id"]), map[string]any{"version": 999, "content": "覆盖"})
	assertStatus(t, err, 409)
	result, err := s.DeleteTeamDailyEntry(text(entry["id"]), map[string]any{"version": entry["version"]})
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := s.CreateTeamDailyEntry(input)
	if err != nil {
		t.Fatal(err)
	}
	trash := result["items"].([]map[string]any)[0]
	_, err = s.RestoreTrash("team_daily_entries", text(entry["id"]), map[string]any{"version": trash["version"]})
	assertStatus(t, err, 409)
	list, err := s.ListTeamDailyEntries(map[string]string{"project_id": text(p["id"]), "from": "2026-11-05", "to": "2026-11-05"})
	if err != nil || list["total"] != 1 {
		t.Fatal("team filtering", list, err)
	}
	found, err := s.SearchPage(map[string]string{"q": "接口联调", "entity_type": "team_daily_entries"})
	if err != nil || found["total"] != 1 {
		t.Fatal("team search", found, err)
	}
	p = mustUpdate(t, s, "projects", p, map[string]any{"status": "closed"})
	_, err = s.UpdateTeamDailyEntry(text(replacement["id"]), map[string]any{"version": replacement["version"], "content": "关闭后"})
	assertStatus(t, err, 409)
	_, err = s.DeleteTeamDailyEntry(text(replacement["id"]), map[string]any{"version": replacement["version"]})
	assertStatus(t, err, 409)
	assertChain(t, s)
}
func TestProjectDailyDraftSourcesVersionConflictAndExplicitSavedText(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "项目整合"})
	a := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "A任务", "assignee": "王工", "progress": 30, "status": "doing"})
	mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "B任务", "assignee": "李工"})
	entry, err := s.CreateTeamDailyEntry(map[string]any{"project_id": p["id"], "log_date": "2026-11-05", "person_name": "王工", "content": "完成阶段工作", "task_ids": []any{a["id"]}})
	if err != nil {
		t.Fatal(err)
	}
	timed, err := s.CreateTimeEntry(map[string]any{"project_id": p["id"], "work_date": "2026-11-05", "minutes": 90, "assignee": "王工"})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.ProjectDailyLog(text(p["id"]), "2026-11-05")
	if err != nil {
		t.Fatal(err)
	}
	snapshot := draft["source_snapshot"].(map[string]any)
	if len(snapshot["missing_people"].([]string)) != 1 || snapshot["missing_people"].([]string)[0] != "李工" || !strings.Contains(text(draft["content"]), "90 分钟") {
		t.Fatalf("draft incomplete %v", draft)
	}
	sources := snapshot["team_entries"].([]map[string]any)
	if sources[0]["id"] != entry["id"] || sources[0]["version"] != entry["version"] {
		t.Fatal("missing source versions")
	}
	if snapshot["time_entries"].([]map[string]any)[0]["id"] != timed["id"] || snapshot["task_snapshot_semantics"] == "" {
		t.Fatal("time source/task semantics missing")
	}
	draft["content"] = "负责人已编辑，不覆盖"
	saved, err := s.SaveProjectDailyLog(draft)
	if err != nil {
		t.Fatal(err)
	}
	if saved["content"] != "负责人已编辑，不覆盖" {
		t.Fatal("owner prose lost")
	}
	_, err = s.SaveProjectDailyLog(draft)
	assertStatus(t, err, 409)
	fresh, err := s.ProjectDailyLog(text(p["id"]), "2026-11-05")
	if err != nil {
		t.Fatal(err)
	}
	if getNow(t, s, "project_daily_logs", text(saved["id"]))["content"] != "负责人已编辑，不覆盖" {
		t.Fatal("generate overwrote saved text")
	}
	a = mustUpdate(t, s, "tasks", a, map[string]any{"progress": 50})
	fresh["version"] = saved["version"]
	_, err = s.SaveProjectDailyLog(fresh)
	assertStatus(t, err, 409)
	saved, err = s.UpdateProjectDailyLog(text(saved["id"]), map[string]any{"version": saved["version"], "content": "只改正文，保留旧来源"})
	if err != nil {
		t.Fatal(err)
	}
	if text(saved["content"]) != "只改正文，保留旧来源" {
		t.Fatal("historical source editing failed")
	}
	fresh, err = s.ProjectDailyLog(text(p["id"]), "2026-11-05")
	if err != nil {
		t.Fatal(err)
	}
	fresh["version"] = saved["version"]
	saved, err = s.SaveProjectDailyLog(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text(saved["content"]), "50%") {
		t.Fatal("explicit regeneration did not refresh current snapshot")
	}
	mustCreate(t, s, "worklogs", map[string]any{"project_id": p["id"], "log_date": "2026-11-05", "content": "个人日志独立保存"})
	list, err := s.ListProjectDailyLogs(nil)
	if err != nil || list["total"] != 1 {
		t.Fatal("project list", list, err)
	}
	assertChain(t, s)
}
func TestImportTasksValidationAtomicityIdempotencyAndDerivedParents(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "导入"})
	rows := []any{map[string]any{"row": 2, "key": "C", "parent_key": "P", "name": "先出现的子", "assignee": "王工", "estimate_hours": 2, "progress": 50, "status": "doing"}, map[string]any{"row": 3, "key": "P", "name": "父", "estimate_hours": 900, "progress": 80, "status": "done"}, map[string]any{"row": 4, "key": "D", "parent_key": "P", "name": "子二", "estimate_hours": 6, "progress": 0}}
	input := map[string]any{"rows": rows, "project_version": p["version"], "source_hash": hashString("file a")}
	preview, err := s.ImportTasksPreview(text(p["id"]), input)
	if err != nil || preview["total"] != 3 || len(preview["warnings"].([]string)) < 1 {
		t.Fatalf("preview %v %v", preview, err)
	}
	_, count, err := s.List("tasks", nil)
	if err != nil || count != 0 {
		t.Fatal("preview mutated")
	}
	input["import_id"] = preview["import_id"]
	if _, err = s.DB.Exec("CREATE TRIGGER import_audit_fail BEFORE INSERT ON audit WHEN new.action='import' BEGIN SELECT RAISE(ABORT,'import failure');END"); err != nil {
		t.Fatal(err)
	}
	_, err = s.ImportTasks(text(p["id"]), input)
	if err == nil {
		t.Fatal("import should roll back failed audit")
	}
	for _, table := range []string{"tasks", "task_imports", "search_doc"} {
		var n int
		var sqlQ string
		if table == "search_doc" {
			sqlQ = "SELECT count(*) FROM search_doc WHERE entity_type='tasks'"
		} else {
			sqlQ = "SELECT count(*) FROM " + table
		}
		if err = s.DB.QueryRow(sqlQ).Scan(&n); err != nil || n != 0 {
			t.Fatalf("import leaked %s %d %v", table, n, err)
		}
	}
	if versionOf(getNow(t, s, "projects", text(p["id"]))["version"]) != versionOf(p["version"]) {
		t.Fatal("failed import bumped project version")
	}
	s.DB.Exec("DROP TRIGGER import_audit_fail")
	result, err := s.ImportTasks(text(p["id"]), input)
	if err != nil || result["total"] != 3 {
		t.Fatalf("import %v %v", result, err)
	}
	var parent map[string]any
	for _, m := range result["items"].([]map[string]any) {
		if m["name"] == "父" {
			parent = m
		}
	}
	if parent == nil || num(parent["progress"]) != 13 || num(parent["estimate_hours"]) != 8 || parent["status"] != "doing" {
		t.Fatalf("parent not derived %v", parent)
	}
	replay, err := s.ImportTasks(text(p["id"]), input)
	if err != nil || replay["already_imported"] != true || replay["import_id"] != result["import_id"] {
		t.Fatalf("idempotent replay %v %v", replay, err)
	}
	_, count, err = s.List("tasks", nil)
	if err != nil || count != 3 {
		t.Fatal("replay duplicated tasks")
	}
	different := copyMap(input)
	different["rows"] = []any{map[string]any{"key": "other", "name": "替换"}}
	_, err = s.ImportTasks(text(p["id"]), different)
	assertStatus(t, err, 409)
	input["source_hash"] = hashString("file b")
	_, err = s.ImportTasks(text(p["id"]), input)
	assertStatus(t, err, 409)
	assertChain(t, s)
}
func TestImportInvalidRowsAndSnapshotSaveRollback(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "错误导入"})
	cases := []any{[]any{map[string]any{"key": "A", "name": "缺父", "parent_key": "missing"}}, []any{map[string]any{"key": "A", "name": "A", "parent_key": "B"}, map[string]any{"key": "B", "name": "B", "parent_key": "A"}}, []any{map[string]any{"key": "A", "name": "A"}, map[string]any{"key": "A", "name": "重复"}}, []any{map[string]any{"key": "A", "name": "A", "plan_start": "2026-02-31"}}, []any{map[string]any{"key": "A", "name": "A", "estimate_hours": -1}}}
	for i, rows := range cases {
		_, err := s.ImportTasks(text(p["id"]), map[string]any{"rows": rows, "source_hash": hashString(fmt.Sprint(i)), "project_version": p["version"]})
		assertStatus(t, err, 400)
	}
	draft, err := s.ProjectDailyLog(text(p["id"]), "2026-11-05")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.DB.Exec("CREATE TRIGGER daily_audit_fail BEFORE INSERT ON audit WHEN new.entity_type='project_daily_logs' BEGIN SELECT RAISE(ABORT,'daily failure');END"); err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveProjectDailyLog(draft)
	if err == nil {
		t.Fatal("expected save rollback")
	}
	var n int
	s.DB.QueryRow("SELECT count(*) FROM project_daily_logs").Scan(&n)
	if n != 0 {
		t.Fatal("saved log leaked")
	}
	s.DB.QueryRow("SELECT count(*) FROM search_doc WHERE entity_type='project_daily_logs'").Scan(&n)
	if n != 0 {
		t.Fatal("saved log search leaked")
	}
	assertChain(t, s)
}

func TestImportFiveThousandRowsAndBoundaries(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "5000行导入"})
	rows := make([]any, 5000)
	for i := range rows {
		rows[i] = map[string]any{"key": fmt.Sprint(i + 1), "name": fmt.Sprintf("任务 %d", i+1)}
	}
	input := map[string]any{"rows": rows, "project_version": p["version"], "source_hash": hashString("5000 rows")}
	preview, err := s.ImportTasksPreview(text(p["id"]), input)
	if err != nil || preview["total"] != 5000 {
		t.Fatalf("5000-row preview %v %v", preview["total"], err)
	}
	result, err := s.ImportTasks(text(p["id"]), input)
	if err != nil || result["total"] != 5000 {
		t.Fatalf("5000-row import %v %v", result["total"], err)
	}
	tree, err := s.TaskTree(text(p["id"]))
	if err != nil || tree["total"] != 5000 {
		t.Fatal("import tree incomplete", err)
	}
	input["rows"] = append(rows, map[string]any{"key": "5001", "name": "超限"})
	input["project_version"] = result["project_version"]
	input["source_hash"] = hashString("5001 rows")
	_, err = s.ImportTasks(text(p["id"]), input)
	assertStatus(t, err, 400)
	assertChain(t, s)
}
func TestProjectSourcesJSONRoundTripAndTeamNoFalseTaskHistory(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "来源JSON"})
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "任务", "assignee": "代填人"})
	entry, err := s.CreateTeamDailyEntry(map[string]any{"project_id": p["id"], "person_name": "代填人", "content": "今天完成", "task_ids": []string{text(task["id"])}})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := s.ProjectDailyLog(text(p["id"]), today())
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := json.Marshal(draft)
	if err != nil {
		t.Fatal(err)
	}
	var browser map[string]any
	if err = json.Unmarshal(bytes, &browser); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveProjectDailyLog(browser)
	if err != nil {
		t.Fatal("browser roundtrip source rejected", err)
	}
	if saved["source_snapshot"].(map[string]any)["task_snapshot_semantics"] == "" {
		t.Fatal("missing current snapshot semantics")
	}
	history, err := s.EntityHistory("team_daily_entries", text(entry["id"]), nil)
	if err != nil || history["total"] != 1 {
		t.Fatal("team audit not exposed")
	}
	taskHistory, err := s.EntityHistory("tasks", text(task["id"]), nil)
	if err != nil || taskHistory["total"] != 1 {
		t.Fatal("team entry must not mutate task progress/history")
	}
	trashed, err := s.DeleteProjectDailyLog(text(saved["id"]), map[string]any{"version": saved["version"]})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveProjectDailyLog(browser)
	if err != nil {
		t.Fatal(err)
	}
	old := trashed["items"].([]map[string]any)[0]
	_, err = s.RestoreTrash("project_daily_logs", text(old["id"]), map[string]any{"version": old["version"]})
	assertStatus(t, err, 409)
	assertChain(t, s)
}

func TestPersonalGenerateLogFiltersPersonAndKeepsLegacyFourFields(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "两种日志口径"})
	for _, person := range []string{"张三", "李四"} {
		mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": person + "专属任务", "assignee": person})
		mustCreate(t, s, "issues", map[string]any{"project_id": p["id"], "title": person + "负责问题", "owner": person})
		minutes := 90
		if person == "李四" {
			minutes = 210
		}
		if _, err := s.CreateTimeEntry(map[string]any{"project_id": p["id"], "work_date": today(), "minutes": minutes, "assignee": person}); err != nil {
			t.Fatal(err)
		}
	}
	mustCreate(t, s, "records", map[string]any{"project_id": p["id"], "title": "本机现场记录", "content": "个人记录保留，不推断他人作者"})
	for _, period := range []string{"day", "week"} {
		for _, person := range []string{"张三", "李四"} {
			draft, err := s.GenerateLog(map[string]any{"project_id": p["id"], "date": today(), "period_type": period, "assignee": person})
			if err != nil {
				t.Fatal(err)
			}
			if len(draft) != 4 {
				t.Fatalf("draft contract changed: %v", draft)
			}
			content := text(draft["content"])
			other, minutes := "李四", 90
			if person == "李四" {
				other, minutes = "张三", 210
			}
			for _, expected := range []string{"人员：" + person + "（个人日志）", person + "专属任务", person + "负责问题", "本机现场记录", fmt.Sprintf("合计 %d 分钟", minutes)} {
				if !strings.Contains(content, expected) {
					t.Fatalf("personal draft missing %q: %s", expected, content)
				}
			}
			for _, excluded := range []string{other + "专属任务", other + "负责问题", "合计 300 分钟", "本机录入的个人记录", "待补充"} {
				if strings.Contains(content, excluded) {
					t.Fatalf("personal draft leaked %q: %s", excluded, content)
				}
			}
		}
	}
	for _, input := range []map[string]any{{"project_id": p["id"], "date": today(), "period_type": "day"}, {"project_id": p["id"], "date": today(), "period_type": "day", "assignee": ""}} {
		draft, err := s.GenerateLog(input)
		if err != nil {
			t.Fatal(err)
		}
		if len(draft) != 4 {
			t.Fatal("legacy draft response changed")
		}
		content := text(draft["content"])
		for _, expected := range []string{"张三专属任务", "李四专属任务", "张三负责问题", "李四负责问题", "合计 300 分钟"} {
			if !strings.Contains(content, expected) {
				t.Fatalf("legacy draft missing %q: %s", expected, content)
			}
		}
		if strings.Contains(content, "（个人日志）") {
			t.Fatal("omitted person changed legacy prose")
		}
	}
	draft, err := s.GenerateLog(map[string]any{"project_id": p["id"], "assignee": "张三"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create("worklogs", draft); err != nil {
		t.Fatal("four-field personal draft cannot be saved directly", err)
	}
	_, err = s.GenerateLog(map[string]any{"assignee": 123})
	assertStatus(t, err, 400)
}
