package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

func legacyV6(t *testing.T, root string) *sql.DB {
	t.Helper()
	db := legacyV5(t, root)
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = migrateV6(tx); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}

func meetingInput() map[string]any {
	return map[string]any{"title": "交付准备会", "meeting_date": "2026-11-05", "content": "## 会议内容\n\n核对现场事实，确认下一步负责人。\n"}
}

func TestV7MigrationFromV6PreservesDraftMonthFrozenSourceAndAudit(t *testing.T) {
	root := t.TempDir()
	old := &Store{DB: legacyV6(t, root)}
	p := mustCreate(t, old, "projects", map[string]any{"name": "纪要升级前项目"})
	log := mustCreate(t, old, "worklogs", map[string]any{"project_id": p["id"], "period_type": "month", "log_date": "2026-11-05", "content": "旧月报正文和手写计划"})
	draft, err := old.SaveDesktopDraft(map[string]any{"version": 1, "mode": "task", "project_id": p["id"], "content": "升级前未提交任务稿"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := old.CreateExportJob(map[string]any{"source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"], "format": "xlsx"})
	if err != nil {
		t.Fatal(err)
	}
	beforeSettings, _ := old.Settings()
	beforeSettings["lan_enabled"] = "0"
	beforeSettings["lan_port"] = "18680"
	var rawSnapshot, auditTail string
	var auditCount int
	old.DB.QueryRow("SELECT source_snapshot FROM export_jobs WHERE id=?", job["id"]).Scan(&rawSnapshot)
	old.DB.QueryRow("SELECT hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&auditTail)
	old.DB.QueryRow("SELECT count(*) FROM audit").Scan(&auditCount)
	old.Close()

	s, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var version, count int
	s.DB.QueryRow("PRAGMA user_version").Scan(&version)
	if version != SchemaVersion {
		t.Fatal("schema7 not applied", version)
	}
	if got := getNow(t, s, "worklogs", text(log["id"])); !reflect.DeepEqual(got, log) {
		t.Fatal("old month changed", got, log)
	}
	if got, e := s.DesktopDraft(); e != nil || !reflect.DeepEqual(got, draft) {
		t.Fatal("old task draft changed", got, e)
	}
	if got, e := s.GetExportJob(text(job["id"])); e != nil || !reflect.DeepEqual(got, job) {
		t.Fatal("old frozen export changed", got, e)
	}
	var newSnapshot, newTail string
	s.DB.QueryRow("SELECT source_snapshot FROM export_jobs WHERE id=?", job["id"]).Scan(&newSnapshot)
	s.DB.QueryRow("SELECT hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&newTail)
	s.DB.QueryRow("SELECT count(*) FROM audit").Scan(&count)
	if rawSnapshot != newSnapshot || auditTail != newTail || auditCount != count {
		t.Fatal("upgrade rewrote source JSON or audit")
	}
	if settings, e := s.Settings(); e != nil || !reflect.DeepEqual(settings, beforeSettings) {
		t.Fatal("upgrade changed settings/dataset", settings, e)
	}
	if results, e := s.SearchPage(map[string]string{"q": "旧月报正文"}); e != nil || results["total"] != 1 {
		t.Fatal("old search missing", results, e)
	}
	if items, total, e := s.List("meeting_minutes", nil); e != nil || total != 0 || len(items) != 0 {
		t.Fatal("new table not empty", items, e)
	}
	assertChain(t, s)
}

func TestV7MigrationLateIndexFailureRollsBackWholeNewTable(t *testing.T) {
	root := t.TempDir()
	db := legacyV6(t, root)
	if _, err := db.Exec("CREATE INDEX meeting_minutes_project_date ON settings(value);INSERT INTO desktop_drafts(key,content,project_id,version,updated_at,mode)VALUES('quick_note','保留原草稿','',4,'2026-11-05T00:00:00Z','record')"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(root); err == nil {
		s.Close()
		t.Fatal("colliding second index should reject migration")
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	var version, count int
	var content string
	db.QueryRow("PRAGMA user_version").Scan(&version)
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE name IN('meeting_minutes','meeting_minutes_date')").Scan(&count)
	db.QueryRow("SELECT content FROM desktop_drafts").Scan(&content)
	if version != 6 || count != 0 || content != "保留原草稿" {
		t.Fatal("partial schema7 committed", version, count, content)
	}
	if _, err = db.Exec("DROP INDEX meeting_minutes_project_date"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(root)
	if err != nil {
		t.Fatal("retry migration", err)
	}
	defer s.Close()
	if draft, e := s.DesktopDraft(); e != nil || draft["mode"] != "record" || versionOf(draft["version"]) != 4 || draft["content"] != content {
		t.Fatal("retry changed existing draft", draft, e)
	}
	mustCreate(t, s, "meeting_minutes", meetingInput())
	assertChain(t, s)
}

func TestMeetingCRUDValidationVersionsAndStableServerNumber(t *testing.T) {
	s := testStore(t)
	body := "## 原始 Markdown\n\n- 决定：保留原文 A&B <输入>。\n\n"
	input := meetingInput()
	input["title"], input["content"] = "  现场协调会  ", body
	meeting := mustCreate(t, s, "meeting_minutes", input)
	if meeting["title"] != "现场协调会" || meeting["content"] != body || meeting["project_id"] != "" || meeting["attendees"] != "" || meeting["location"] != "" {
		t.Fatal("meeting fields/defaults", meeting)
	}
	code := text(meeting["code"])
	if !regexp.MustCompile(`^MTG-[0-9]{8}-[0-9]{3,}$`).MatchString(code) {
		t.Fatal("missing generated meeting number", code)
	}
	for _, key := range []string{"created_at", "updated_at"} {
		if _, err := time.Parse(time.RFC3339Nano, text(meeting[key])); err != nil || !strings.HasSuffix(text(meeting[key]), "Z") {
			t.Fatal("non-UTC timestamp", key, meeting[key])
		}
	}
	meeting = mustUpdate(t, s, "meeting_minutes", meeting, map[string]any{"meeting_date": "2024-02-29", "location": "一号会议室", "attendees": "张工、李工", "project_id": nil})
	if meeting["code"] != code || meeting["meeting_date"] != "2024-02-29" || meeting["version"] != int64(2) {
		t.Fatal("date update changed identity", meeting)
	}
	_, err := s.Update("meeting_minutes", text(meeting["id"]), map[string]any{"version": 1, "content": "过期不得覆盖"})
	assertStatus(t, err, 409)
	for _, fields := range []map[string]any{
		{"title": " "}, {"title": strings.Repeat("字", 501)}, {"meeting_date": ""}, {"meeting_date": "2023-02-29"},
		{"meeting_date": "2026-11-31"}, {"content": "\n\t "}, {"content": nil}, {"attendees": []any{"张工"}},
		{"location": strings.Repeat("地", 501)}, {"attendees": strings.Repeat("人", 2001)}, {"content": strings.Repeat("字", 200001)},
		{"code": "伪造编号"}, {"id": "伪造ID"}, {"author": "伪造操作人"}, {"project_id": "no-project"},
	} {
		fields["version"] = meeting["version"]
		_, err = s.Update("meeting_minutes", text(meeting["id"]), fields)
		assertStatus(t, err, 400)
		if got := getNow(t, s, "meeting_minutes", text(meeting["id"])); !reflect.DeepEqual(got, meeting) {
			t.Fatal("invalid update changed existing meeting", fields)
		}
	}
	for _, missing := range []string{"title", "meeting_date", "content"} {
		fields := meetingInput()
		delete(fields, missing)
		_, err = s.Create("meeting_minutes", fields)
		assertStatus(t, err, 400)
	}
	_, err = s.Create("meeting_minutes", map[string]any{"title": "非法创建编号", "meeting_date": "2026-11-05", "content": "正文", "code": code})
	assertStatus(t, err, 400)
	second := mustCreate(t, s, "meeting_minutes", meetingInput())
	if second["code"] == code {
		t.Fatal("number reused")
	}
	assertChain(t, s)
}

func TestMeetingListDateProjectPaginationAndLiteralMetadataSearch(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "会议归属"})
	meetings := []map[string]any{}
	for _, fields := range []map[string]any{
		{"title": "初次确认会", "meeting_date": "2024-02-01", "content": "确认原始范围", "project_id": p["id"]},
		{"title": "闰日评审会", "meeting_date": "2024-02-29", "content": "纪要全文检索短语", "project_id": p["id"], "location": "二号会议室100%", "attendees": "张工_李工"},
		{"title": "跨月未归类会议", "meeting_date": "2024-03-01", "content": "跨月原文"},
	} {
		meetings = append(meetings, mustCreate(t, s, "meeting_minutes", fields))
	}
	items, total, err := s.List("meeting_minutes", map[string]string{"limit": "1"})
	if err != nil || total != 3 || len(items) != 1 || items[0]["id"] != meetings[2]["id"] {
		t.Fatal("meeting date order", items, total, err)
	}
	page, err := s.ListPage("meeting_minutes", map[string]string{"limit": "1", "offset": "1"})
	if err != nil || page["total"] != 3 || page["next_cursor"] != "" || page["items"].([]map[string]any)[0]["id"] != meetings[1]["id"] {
		t.Fatal("offset response contract", page, err)
	}
	for _, filters := range []map[string]string{
		{"project_id": text(p["id"]), "date_from": "2024-02-29", "date_to": "2024-02-29"},
		{"project_id": text(p["id"]), "from": "2024-02-29", "to": "2024-02-29"},
		{"q": "100%"}, {"q": "张工_李工"}, {"q": text(meetings[1]["code"])},
	} {
		items, total, err = s.List("meeting_minutes", filters)
		if err != nil || total != 1 || items[0]["id"] != meetings[1]["id"] {
			t.Fatal("meeting filters", filters, items, total, err)
		}
	}
	items, total, err = s.List("meeting_minutes", map[string]string{"unclassified": "1"})
	if err != nil || total != 1 || items[0]["id"] != meetings[2]["id"] {
		t.Fatal("unclassified filter", items, total, err)
	}
	items, total, err = s.List("meeting_minutes", map[string]string{"project_id": ""})
	if err != nil || total != 1 || items[0]["id"] != meetings[2]["id"] {
		t.Fatal("explicit NULL project filter", items, total, err)
	}
	for _, filters := range []map[string]string{{"date_from": "2023-02-29"}, {"date_to": "2024-02-30"}, {"date_from": "2024-03-01", "date_to": "2024-02-01"}} {
		_, _, err = s.List("meeting_minutes", filters)
		assertStatus(t, err, 400)
	}
	for _, q := range []string{"纪要全文检索", "二号会议室", "张工_李工", "100%", "2024-02-29"} {
		found, e := s.SearchPage(map[string]string{"q": q, "entity_type": "meeting_minutes"})
		if e != nil || found["total"] != 1 || found["items"].([]map[string]any)[0]["entity_id"] != meetings[1]["id"] {
			t.Fatal("global metadata search", q, found, e)
		}
	}
	found, err := s.SearchPage(map[string]string{"kind": "meeting_minutes", "date_from": "2024-02-29", "date_to": "2024-02-29"})
	if err != nil || found["total"] != 1 {
		t.Fatal("meeting search date accidentally constrained records", found, err)
	}
	assertChain(t, s)
}

func TestMeetingSoftDeleteRestoreHistoryAuditAndProjectProtection(t *testing.T) {
	s := testStore(t)
	if err := s.SetSettings(map[string]any{"operator": "真实录入人"}); err != nil {
		t.Fatal(err)
	}
	p := mustCreate(t, s, "projects", map[string]any{"name": "会议保护项目"})
	fields := meetingInput()
	fields["project_id"] = p["id"]
	meeting := mustCreate(t, s, "meeting_minutes", fields)
	meeting = mustUpdate(t, s, "meeting_minutes", meeting, map[string]any{"content": "修改后独有检索词"})
	assertStatus(t, s.Delete("meeting_minutes", text(meeting["id"]), 1), 409)
	if err := s.Delete("meeting_minutes", text(meeting["id"]), versionOf(meeting["version"])); err != nil {
		t.Fatal(err)
	}
	_, err := s.Get("meeting_minutes", text(meeting["id"]))
	assertStatus(t, err, 404)
	_, count, err := s.List("meeting_minutes", nil)
	if err != nil || count != 0 {
		t.Fatal("trash leaked to list", count, err)
	}
	found, err := s.SearchPage(map[string]string{"q": "独有检索词"})
	if err != nil || found["total"] != 0 {
		t.Fatal("trash leaked to FTS", found, err)
	}
	trash, err := s.ListTrash(map[string]string{"kind": "meeting_minutes"})
	if err != nil || trash["total"] != 1 {
		t.Fatal("meeting missing recycle bin", trash, err)
	}
	trashed := trash["items"].([]map[string]any)[0]
	if trashed["entity_type"] != "meeting_minutes" || trashed["code"] != meeting["code"] || trashed["content"] != meeting["content"] {
		t.Fatal("trash lost source", trashed)
	}
	assertStatus(t, s.Delete("projects", text(p["id"]), versionOf(p["version"])), 409)
	_, err = s.RestoreTrash("meeting_minutes", text(meeting["id"]), map[string]any{"version": meeting["version"]})
	assertStatus(t, err, 409)
	restored, err := s.RestoreTrash("meeting_minutes", text(meeting["id"]), map[string]any{"version": trashed["version"]})
	if err != nil || restored["code"] != meeting["code"] || restored["content"] != meeting["content"] || versionOf(restored["version"]) != 4 {
		t.Fatal("restore source/version", restored, err)
	}
	if found, e := s.SearchPage(map[string]string{"q": "独有检索词"}); e != nil || found["total"] != 1 {
		t.Fatal("restore missing index", found, e)
	}
	history, err := s.EntityHistory("meeting_minutes", text(meeting["id"]), map[string]string{"limit": "2"})
	if err != nil || history["total"] != 4 || history["next_cursor"] == "" {
		t.Fatal("meeting history page", history, err)
	}
	items := history["items"].([]map[string]any)
	if items[0]["action"] != "restore" || items[1]["action"] != "trash" {
		t.Fatal("history order", items)
	}
	for _, item := range items {
		if item["operator"] != "真实录入人" {
			t.Fatal("audit identity changed", item)
		}
		var after map[string]any
		if err = json.Unmarshal([]byte(text(item["after_json"])), &after); err != nil || after["id"] != meeting["id"] {
			t.Fatal("history JSON/source", item, err)
		}
	}
	history, err = s.EntityHistory("meeting_minutes", text(meeting["id"]), map[string]string{"limit": "2", "cursor": text(history["next_cursor"])})
	if err != nil || history["total"] != 4 || history["items"].([]map[string]any)[1]["action"] != "create" {
		t.Fatal("history next page", history, err)
	}
	assertChain(t, s)
}

func TestMeetingClosedArchivedRecycledProjectCannotBeEscapedOrRestored(t *testing.T) {
	for _, status := range []string{"closed", "archived"} {
		t.Run(status, func(t *testing.T) {
			s := testStore(t)
			p := mustCreate(t, s, "projects", map[string]any{"name": "只读会议项目"})
			fields := meetingInput()
			fields["project_id"] = p["id"]
			meeting := mustCreate(t, s, "meeting_minutes", fields)
			trashFields := meetingInput()
			trashFields["project_id"] = p["id"]
			toRestore := mustCreate(t, s, "meeting_minutes", trashFields)
			if err := s.Delete("meeting_minutes", text(toRestore["id"]), versionOf(toRestore["version"])); err != nil {
				t.Fatal(err)
			}
			trashed, err := getIncludingTrash(s.DB, "meeting_minutes", text(toRestore["id"]), true)
			if err != nil {
				t.Fatal(err)
			}
			p = mustUpdate(t, s, "projects", p, map[string]any{"status": status})
			_, err = s.Create("meeting_minutes", fields)
			assertStatus(t, err, 409)
			_, err = s.Update("meeting_minutes", text(meeting["id"]), map[string]any{"version": meeting["version"], "content": "被禁止的修改", "project_id": ""})
			assertStatus(t, err, 409)
			assertStatus(t, s.Delete("meeting_minutes", text(meeting["id"]), versionOf(meeting["version"])), 409)
			_, err = s.RestoreTrash("meeting_minutes", text(toRestore["id"]), map[string]any{"version": trashed["version"]})
			assertStatus(t, err, 409)
			free := mustCreate(t, s, "meeting_minutes", meetingInput())
			_, err = s.Update("meeting_minutes", text(free["id"]), map[string]any{"version": free["version"], "project_id": p["id"]})
			assertStatus(t, err, 409)
			if got := getNow(t, s, "meeting_minutes", text(meeting["id"])); !reflect.DeepEqual(got, meeting) {
				t.Fatal("read-only operation changed source", got)
			}
			assertChain(t, s)
		})
	}
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "已回收项目"})
	if err := s.Delete("projects", text(p["id"]), versionOf(p["version"])); err != nil {
		t.Fatal(err)
	}
	fields := meetingInput()
	fields["project_id"] = p["id"]
	_, err := s.Create("meeting_minutes", fields)
	assertStatus(t, err, 400)
	assertChain(t, s)
}

func TestMeetingAuditFailureRollsBackBusinessAndSearch(t *testing.T) {
	s := testStore(t)
	meeting := mustCreate(t, s, "meeting_minutes", meetingInput())
	var auditCount int
	s.DB.QueryRow("SELECT count(*) FROM audit").Scan(&auditCount)
	if _, err := s.DB.Exec("CREATE TRIGGER fail_meeting_audit BEFORE INSERT ON audit WHEN new.entity_type='meeting_minutes' BEGIN SELECT RAISE(ABORT,'meeting audit failed');END"); err != nil {
		t.Fatal(err)
	}
	_, err := s.Create("meeting_minutes", map[string]any{"title": "不应存在的会议", "meeting_date": "2026-11-05", "content": "回滚创建独有词"})
	if err == nil {
		t.Fatal("expected audit failure on create")
	}
	_, err = s.Update("meeting_minutes", text(meeting["id"]), map[string]any{"version": meeting["version"], "content": "回滚更新独有词"})
	if err == nil {
		t.Fatal("expected audit failure on update")
	}
	if err = s.Delete("meeting_minutes", text(meeting["id"]), versionOf(meeting["version"])); err == nil {
		t.Fatal("expected audit failure on trash")
	}
	if got := getNow(t, s, "meeting_minutes", text(meeting["id"])); !reflect.DeepEqual(got, meeting) {
		t.Fatal("business change escaped rollback", got)
	}
	if result, e := s.SearchPage(map[string]string{"q": "回滚"}); e != nil || result["total"] != 0 {
		t.Fatal("search change escaped rollback", result, e)
	}
	var count int
	s.DB.QueryRow("SELECT count(*) FROM meeting_minutes").Scan(&count)
	if count != 1 {
		t.Fatal("failed create survived", count)
	}
	s.DB.QueryRow("SELECT count(*) FROM audit").Scan(&count)
	if count != auditCount {
		t.Fatal("failed write appended audit", count)
	}
	if _, err = s.DB.Exec("DROP TRIGGER fail_meeting_audit"); err != nil {
		t.Fatal(err)
	}
	if err = s.Delete("meeting_minutes", text(meeting["id"]), versionOf(meeting["version"])); err != nil {
		t.Fatal(err)
	}
	trashed, _ := getIncludingTrash(s.DB, "meeting_minutes", text(meeting["id"]), true)
	if _, err = s.DB.Exec("CREATE TRIGGER fail_meeting_restore BEFORE INSERT ON audit WHEN new.entity_type='meeting_minutes' BEGIN SELECT RAISE(ABORT,'restore audit failed');END"); err != nil {
		t.Fatal(err)
	}
	_, err = s.RestoreTrash("meeting_minutes", text(meeting["id"]), map[string]any{"version": trashed["version"]})
	if err == nil {
		t.Fatal("expected audit failure on restore")
	}
	if got, e := getIncludingTrash(s.DB, "meeting_minutes", text(meeting["id"]), true); e != nil || !reflect.DeepEqual(got, trashed) {
		t.Fatal("failed restore escaped rollback", got, e)
	}
	if result, e := s.SearchPage(map[string]string{"q": "会议内容"}); e != nil || result["total"] != 0 {
		t.Fatal("failed restore search survived", result, e)
	}
	assertChain(t, s)
}

func TestMeetingOnlyDatasetDoesNotAllowDemoAndCodesNeverReuseTrash(t *testing.T) {
	s := testStore(t)
	first := mustCreate(t, s, "meeting_minutes", meetingInput())
	assertStatus(t, s.Demo(), 409)
	if err := s.Delete("meeting_minutes", text(first["id"]), versionOf(first["version"])); err != nil {
		t.Fatal(err)
	}
	second := mustCreate(t, s, "meeting_minutes", meetingInput())
	if second["code"] == first["code"] {
		t.Fatal("trash meeting number reused")
	}
	assertChain(t, s)
}
