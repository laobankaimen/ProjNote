package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func legacyV8(t *testing.T, root string) *sql.DB {
	t.Helper()
	db := legacyV7(t, root)
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	if e = migrateV8(tx); e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(); e != nil {
		t.Fatal(e)
	}
	return db
}
func mustLANMember(t *testing.T, s *Store, name string, projects, tasks []any) map[string]any {
	t.Helper()
	m, e := s.CreateLANMember(map[string]any{"name": name, "project_ids": projects, "task_ids": tasks})
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func mustLANSession(t *testing.T, s *Store, m map[string]any) (string, string) {
	t.Helper()
	pair, e := s.CreateLANPairing(text(m["id"]), map[string]any{"version": m["version"]})
	if e != nil {
		t.Fatal(e)
	}
	session, e := s.ExchangeLANPairing(text(pair["code"]), "测试设备")
	if e != nil {
		t.Fatal(e)
	}
	return text(pair["code"]), text(session["token"])
}
func lanEntryInput(pid any, date, content string, minutes int) map[string]any {
	return map[string]any{"project_id": pid, "work_date": date, "content": content, "minutes": minutes}
}
func mustLANEntry(t *testing.T, s *Store, member any, input map[string]any) map[string]any {
	t.Helper()
	m, e := s.SaveLANDailyEntry(text(member), input)
	if e != nil {
		t.Fatal(e)
	}
	return m
}
func TestV9MigrationFromV8PreservesSavedVersionsDraftExportsAndIdentity(t *testing.T) {
	root := t.TempDir()
	old := &Store{DB: legacyV8(t, root)}
	p := mustCreate(t, old, "projects", map[string]any{"name": "局域网升级前项目"})
	task := mustCreate(t, old, "tasks", map[string]any{"project_id": p["id"], "name": "保留旧任务"})
	in := requirementInput(p["id"])
	in["task_ids"] = []any{task["id"]}
	r := mustCreate(t, old, "requirements", in)
	plan := savePlan(t, old, p, "旧版独立计划")
	draft, e := old.SaveDesktopDraft(map[string]any{"version": 1, "mode": "task", "content": "未提交草稿", "project_id": p["id"]})
	if e != nil {
		t.Fatal(e)
	}
	job, e := old.CreateExportJob(map[string]any{"source_kind": "plan_versions", "source_id": plan["id"], "source_version": 1, "format": "json"})
	if e != nil {
		t.Fatal(e)
	}
	settings, _ := old.Settings()
	count := sqlCount(t, old, "audit")
	var tail string
	old.DB.QueryRow("SELECT hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&tail)
	old.Close()
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	var version int
	s.DB.QueryRow("PRAGMA user_version").Scan(&version)
	if version != 9 {
		t.Fatal(version)
	}
	if got := getNow(t, s, "requirements", text(r["id"])); !reflect.DeepEqual(got, r) {
		t.Fatal("requirements changed")
	}
	if got, e := s.GetPlanVersion(text(plan["id"])); e != nil || !reflect.DeepEqual(got, plan) {
		t.Fatal("plan changed", got, e)
	}
	if got, e := s.DesktopDraft(); e != nil || !reflect.DeepEqual(got, draft) {
		t.Fatal(got, e)
	}
	if got, e := s.GetExportJob(text(job["id"])); e != nil || !reflect.DeepEqual(got, job) {
		t.Fatal("export source changed", got, e)
	}
	gotSettings, _ := s.Settings()
	if gotSettings["lan_enabled"] != "0" || gotSettings["lan_port"] != "18680" || gotSettings["dataset_id"] != settings["dataset_id"] {
		t.Fatal(gotSettings)
	}
	var afterTail string
	s.DB.QueryRow("SELECT hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&afterTail)
	if tail != afterTail || count != sqlCount(t, s, "audit") {
		t.Fatal("upgrade changed audit")
	}
	assertChain(t, s)
}
func TestV9MigrationLateFailureRollsBackAllIdentityTablesAndSettings(t *testing.T) {
	root := t.TempDir()
	db := legacyV8(t, root)
	if _, e := db.Exec("CREATE INDEX lan_daily_project_day ON settings(value)"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if s, e := Open(root); e == nil {
		s.Close()
		t.Fatal("late collision accepted")
	}
	db, e := sql.Open("sqlite", filepath.Join(root, "app.db"))
	if e != nil {
		t.Fatal(e)
	}
	var version, tables, settings int
	db.QueryRow("PRAGMA user_version").Scan(&version)
	db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name LIKE 'lan_%'").Scan(&tables)
	db.QueryRow("SELECT count(*) FROM settings WHERE key IN ('lan_port','lan_enabled')").Scan(&settings)
	if version != 8 || tables != 0 || settings != 0 {
		t.Fatal("partial migration committed", version, tables, settings)
	}
	db.Exec("DROP INDEX lan_daily_project_day")
	db.Close()
	s, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.CreateLANMember(map[string]any{"name": "重试成功"}); e != nil {
		t.Fatal(e)
	}
	for _, settings := range []map[string]any{{"lan_enabled": "1", "lan_port": 18681}, {"lan_enabled": "0", "lan_port": "65436"}} {
		if e = s.SetSettings(settings); e != nil {
			t.Fatal(e)
		}
	}
	for _, settings := range []map[string]any{{"lan_port": 1023}, {"lan_port": 65437}, {"lan_enabled": "yes"}} {
		assertStatus(t, s.SetSettings(settings), 400)
	}
	assertChain(t, s)
}
func TestLANPairingSingleUseExpiryRevocationAndHashOnlyStorage(t *testing.T) {
	s := testStore(t)
	m := mustLANMember(t, s, "成员同名", []any{}, []any{})
	pair, e := s.CreateLANPairing(text(m["id"]), map[string]any{"version": m["version"]})
	if e != nil {
		t.Fatal(e)
	}
	code := text(pair["code"])
	if len(code) != 64 {
		t.Fatal(code)
	}
	var stored string
	s.DB.QueryRow("SELECT hash FROM lan_pairings").Scan(&stored)
	if stored == code || stored != shaText(code) {
		t.Fatal("pairing not hashed")
	}
	var wg sync.WaitGroup
	results := make(chan map[string]any, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := s.ExchangeLANPairing(code, "同一设备")
			if e != nil {
				errs <- e
			} else {
				results <- v
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	if len(results) != 1 || len(errs) != 1 {
		t.Fatal("pairing not single use", len(results), len(errs))
	}
	for e := range errs {
		assertStatus(t, e, 401)
	}
	session := <-results
	token := text(session["token"])
	if len(token) != 64 {
		t.Fatal(token)
	}
	s.DB.QueryRow("SELECT hash FROM lan_sessions").Scan(&stored)
	if stored == token || stored != shaText(token) {
		t.Fatal("session not hashed")
	}
	if got, e := s.LANSession(token); e != nil || got["id"] != m["id"] {
		t.Fatal(got, e)
	}
	pair, e = s.CreateLANPairing(text(m["id"]), map[string]any{"version": m["version"]})
	if e != nil {
		t.Fatal(e)
	}
	s.DB.Exec("UPDATE lan_pairings SET expires_at=?", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano))
	_, e = s.ExchangeLANPairing(text(pair["code"]), "")
	assertStatus(t, e, 401)
	s.DB.Exec("UPDATE lan_sessions SET expires_at=?", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano))
	_, e = s.LANSession(token)
	assertStatus(t, e, 401)
	_, token = mustLANSession(t, s, m)
	updated, e := s.UpdateLANMember(text(m["id"]), map[string]any{"version": m["version"], "name": "成员新名称"})
	if e != nil {
		t.Fatal(e)
	}
	if num(updated["auth_version"]) != num(m["auth_version"])+1 {
		t.Fatal(updated)
	}
	_, e = s.LANSession(token)
	assertStatus(t, e, 401)
	m = updated
	code, token = mustLANSession(t, s, m)
	m, e = s.RevokeLANMember(text(m["id"]), map[string]any{"version": m["version"]})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.LANSession(token)
	assertStatus(t, e, 401)
	_, e = s.ExchangeLANPairing(code, "")
	assertStatus(t, e, 401)
	_, token = mustLANSession(t, s, m)
	if e = s.LogOutLAN(token); e != nil {
		t.Fatal(e)
	}
	if e = s.LogOutLAN(token); e != nil {
		t.Fatal("logout should be idempotent", e)
	}
	_, e = s.LANSession(token)
	assertStatus(t, e, 401)
	_, token = mustLANSession(t, s, m)
	pair, e = s.CreateLANPairing(text(m["id"]), map[string]any{"version": m["version"]})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.RevokeAllLANSessions(); e != nil {
		t.Fatal(e)
	}
	_, e = s.LANSession(token)
	assertStatus(t, e, 401)
	_, e = s.ExchangeLANPairing(text(pair["code"]), "")
	assertStatus(t, e, 401)
	rows, e := s.DB.Query("SELECT before_json,after_json FROM audit")
	if e != nil {
		t.Fatal(e)
	}
	audit, e := rawRows(rows)
	if e != nil {
		t.Fatal(e)
	}
	all, _ := json.Marshal(audit)
	for _, secret := range []string{code, token, text(pair["code"])} {
		if strings.Contains(string(all), secret) || strings.Contains(string(all), shaText(secret)) {
			t.Fatal("secret or credential hash leaked into audit")
		}
	}
	assertChain(t, s)
}
func TestLANStableIdentityLeafPermissionsScopeChangesAndPrivateFields(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "成员可见", "description": "项目私密描述", "customer": "私密客户"})
	other := mustCreate(t, s, "projects", map[string]any{"name": "成员不可见"})
	parent := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "共同父项", "description": "私密父描述"})
	a := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": parent["id"], "name": "成员甲任务", "description": "私密任务正文"})
	b := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": parent["id"], "name": "成员乙任务"})
	foreign := mustCreate(t, s, "tasks", map[string]any{"project_id": other["id"], "name": "跨项目任务"})
	m1 := mustLANMember(t, s, "王工", []any{p["id"]}, []any{a["id"]})
	m2 := mustLANMember(t, s, "王工", []any{p["id"]}, []any{b["id"]})
	_, e := s.CreateLANMember(map[string]any{"name": "冲突成员", "project_ids": []any{p["id"]}, "task_ids": []any{a["id"]}})
	assertStatus(t, e, 409)
	_, e = s.CreateLANMember(map[string]any{"name": "跨项目", "project_ids": []any{p["id"]}, "task_ids": []any{foreign["id"]}})
	assertStatus(t, e, 400)
	_, e = s.CreateLANMember(map[string]any{"name": "父项", "project_ids": []any{p["id"]}, "task_ids": []any{parent["id"]}})
	assertStatus(t, e, 400)
	ws, e := s.LANWorkspace(text(m1["id"]))
	if e != nil {
		t.Fatal(e)
	}
	settings, _ := s.Settings()
	if ws["dataset_id"] != settings["dataset_id"] {
		t.Fatal("workspace dataset isolation missing", ws)
	}
	if len(mapItems(ws["projects"])) != 1 || ws["total"] != 3 {
		t.Fatal(ws)
	}
	if coverageFind(t, ws["tasks"], a["id"])["can_update"] != true || coverageFind(t, ws["tasks"], b["id"])["can_update"] != false || coverageFind(t, ws["tasks"], parent["id"])["can_update"] != false {
		t.Fatal(ws)
	}
	raw, _ := json.Marshal(ws)
	if strings.Contains(string(raw), "私密") || strings.Contains(string(raw), text(foreign["id"])) || strings.Contains(string(raw), "description") {
		t.Fatal("workspace leaked private data", string(raw))
	}
	a = getNow(t, s, "tasks", text(a["id"]))
	_, e = s.UpdateLANTask(text(m2["id"]), text(a["id"]), map[string]any{"version": a["version"], "progress": 50})
	assertStatus(t, e, 403)
	_, e = s.UpdateLANTask(text(m1["id"]), text(foreign["id"]), map[string]any{"version": foreign["version"], "progress": 50})
	assertStatus(t, e, 403)
	_, e = s.UpdateLANTask(text(m1["id"]), text(a["id"]), map[string]any{"version": a["version"], "assignee": "冒充"})
	assertStatus(t, e, 400)
	after, e := s.UpdateLANTask(text(m1["id"]), text(a["id"]), map[string]any{"version": a["version"], "status": "doing", "progress": 50})
	if e != nil || num(after["progress"]) != 50 {
		t.Fatal(after, e)
	}
	_, e = s.UpdateLANTask(text(m1["id"]), text(a["id"]), map[string]any{"version": a["version"], "progress": 60})
	assertStatus(t, e, 409)
	var actor string
	s.DB.QueryRow("SELECT operator FROM audit WHERE entity_id=? AND action='update' ORDER BY id DESC LIMIT 1", a["id"]).Scan(&actor)
	if actor != lanActor(m1) {
		t.Fatal("member task audit borrowed local owner", actor)
	}
	s.DB.QueryRow("SELECT operator FROM audit WHERE entity_id=? AND action='rollup' ORDER BY id DESC LIMIT 1", parent["id"]).Scan(&actor)
	if actor != lanActor(m1) {
		t.Fatal("parent rollup wrong actor", actor)
	}
	a = getNow(t, s, "tasks", text(a["id"]))
	_, e = s.UpdateLANTask(text(m1["id"]), text(a["id"]), map[string]any{"version": a["version"], "status": "done"})
	if e != nil {
		t.Fatal(e)
	}
	a = getNow(t, s, "tasks", text(a["id"]))
	_, e = s.UpdateLANTask(text(m1["id"]), text(a["id"]), map[string]any{"version": a["version"], "status": "doing"})
	assertStatus(t, e, 409)
	// Scope removal revokes sessions and drops old task grants even when not sent.
	_, token := mustLANSession(t, s, m1)
	m1, e = s.UpdateLANMember(text(m1["id"]), map[string]any{"version": m1["version"], "project_ids": []any{}})
	if e != nil {
		t.Fatal(e)
	}
	if len(m1["task_ids"].([]any)) != 0 {
		t.Fatal("scope removal retained old task grant", m1)
	}
	_, e = s.LANSession(token)
	assertStatus(t, e, 401)
	_, e = s.SaveLANDailyEntry(text(m1["id"]), lanEntryInput(p["id"], "2026-11-05", "无权限", 1))
	assertStatus(t, e, 403)
	// A formerly assigned task becoming a parent must not prevent disabling member.
	b = getNow(t, s, "tasks", text(b["id"]))
	mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": b["id"], "name": "后来新增子项"})
	b = getNow(t, s, "tasks", text(b["id"]))
	_, e = s.UpdateLANTask(text(m2["id"]), text(b["id"]), map[string]any{"version": b["version"], "progress": 50})
	assertStatus(t, e, 403)
	p = mustUpdate(t, s, "projects", p, map[string]any{"status": "closed"})
	m2, e = s.UpdateLANMember(text(m2["id"]), map[string]any{"version": m2["version"], "enabled": false})
	if e != nil || m2["enabled"] != false {
		t.Fatal("cannot disable closed/derived-parent member", m2, e)
	}
	_, e = s.LANWorkspace(text(m2["id"]))
	assertStatus(t, e, 403)
	assertChain(t, s)
}
func TestLANDailyEntriesStableMemberQuotaIsolationVersionsAndAudit(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "跨项目一"})
	p2 := mustCreate(t, s, "projects", map[string]any{"name": "跨项目二"})
	m1 := mustLANMember(t, s, "同名王工", []any{p["id"], p2["id"]}, []any{})
	m2 := mustLANMember(t, s, "同名王工", []any{p["id"], p2["id"]}, []any{})
	a := mustLANEntry(t, s, m1["id"], lanEntryInput(p["id"], "2026-11-05", "成员一只属于本人_%", 1000))
	mustLANEntry(t, s, m1["id"], lanEntryInput(p2["id"], "2026-11-05", "成员一第二项目", 440))
	b := mustLANEntry(t, s, m2["id"], lanEntryInput(p["id"], "2026-11-05", "同名成员二独立额度", 1440))
	for _, in := range []map[string]any{lanEntryInput(p["id"], "2026-11-05", "重复创建不能覆盖", 1), {"id": a["id"], "version": a["version"], "project_id": p["id"], "work_date": "2026-11-05", "content": "增加超限", "minutes": 1001}} {
		_, e := s.SaveLANDailyEntry(text(m1["id"]), in)
		if nonempty(in["id"]) {
			assertStatus(t, e, 400)
		} else {
			assertStatus(t, e, 409)
		}
	}
	_, e := s.SaveLANDailyEntry(text(m2["id"]), map[string]any{"id": a["id"], "version": a["version"], "project_id": p["id"], "work_date": "2026-11-05", "content": "同名冒充", "minutes": 1})
	assertStatus(t, e, 403)
	a2, e := s.SaveLANDailyEntry(text(m1["id"]), map[string]any{"id": a["id"], "version": a["version"], "project_id": p["id"], "work_date": "2026-11-05", "content": "本人更新_%", "minutes": 999})
	if e != nil || num(a2["version"]) != 2 {
		t.Fatal(a2, e)
	}
	_, e = s.SaveLANDailyEntry(text(m1["id"]), map[string]any{"id": a["id"], "version": a["version"], "project_id": p["id"], "work_date": "2026-11-05", "content": "旧表", "minutes": 1})
	assertStatus(t, e, 409)
	own, e := s.LANDailyEntries(text(m1["id"]), map[string]string{"member_id": text(m2["id"]), "q": "_%", "limit": "1"})
	if e != nil || own["total"] != 1 || mapItems(own["items"])[0]["member_id"] != m1["id"] {
		t.Fatal("member filter escaped identity", own, e)
	}
	all, e := s.LANDailyEntries("", nil)
	if e != nil || all["total"] != 3 {
		t.Fatal(all, e)
	}
	other, e := s.LANDailyEntries(text(m2["id"]), nil)
	if e != nil || other["total"] != 1 || mapItems(other["items"])[0]["id"] != b["id"] {
		t.Fatal(other, e)
	}
	var actor string
	s.DB.QueryRow("SELECT operator FROM audit WHERE entity_id=? ORDER BY id DESC LIMIT 1", a["id"]).Scan(&actor)
	if actor != lanActor(m1) {
		t.Fatal("wrong member daily actor", actor)
	}
	for _, bad := range []map[string]any{{"minutes": -1}, {"minutes": 1.1}, {"minutes": 1441}, {"work_date": "2026-02-31"}, {"member_id": m2["id"]}, {"minutes": 0, "content": " "}} {
		in := lanEntryInput(p["id"], "2026-11-06", "测试", 0)
		for k, v := range bad {
			in[k] = v
		}
		_, e := s.SaveLANDailyEntry(text(m1["id"]), in)
		assertStatus(t, e, 400)
	}
	p = mustUpdate(t, s, "projects", p, map[string]any{"status": "closed"})
	_, e = s.SaveLANDailyEntry(text(m1["id"]), lanEntryInput(p["id"], "2026-11-06", "关闭项目", 1))
	assertStatus(t, e, 409)
	if got, e := s.LANDailyEntries(text(m1["id"]), nil); e != nil || got["total"] != 2 {
		t.Fatal("closed project history unreadable", got, e)
	}
	assertChain(t, s)
}
func TestLANDailyProjectSummaryUsesMemberIDsFrozenSourcesAndNoDoubleCounting(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "LAN来源汇总"})
	t1 := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "王工甲任务"})
	t2 := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "王工乙任务"})
	m1 := mustLANMember(t, s, "王工", []any{p["id"]}, []any{t1["id"]})
	m2 := mustLANMember(t, s, "王工", []any{p["id"]}, []any{t2["id"]})
	a := mustLANEntry(t, s, m1["id"], lanEntryInput(p["id"], "2026-11-05", "成员甲完成接口", 90))
	b := mustLANEntry(t, s, m2["id"], lanEntryInput(p["id"], "2026-11-05", "成员乙完成联调", 30))
	mustCreate(t, s, "time_entries", map[string]any{"project_id": p["id"], "work_date": "2026-11-05", "minutes": 60, "assignee": "本机负责人"})
	draft, e := s.ProjectDailyLog(text(p["id"]), "2026-11-05")
	if e != nil {
		t.Fatal(e)
	}
	snap := draft["source_snapshot"].(map[string]any)
	if num(snap["total_minutes"]) != 180 || num(snap["lan_minutes"]) != 120 || len(mapItems(snap["lan_entries"])) != 2 || len(mapItems(snap["lan_people"])) != 2 {
		t.Fatal(snap)
	}
	coverageFind(t, snap["lan_entries"], a["id"])
	coverageFind(t, snap["lan_entries"], b["id"])
	for _, task := range mapItems(snap["tasks"]) {
		if !nonempty(task["lan_member_id"]) {
			t.Fatal("stable LAN task ownership missing", task)
		}
	}
	if !strings.Contains(text(draft["content"]), "合计 180 分钟") || !strings.Contains(text(draft["content"]), text(m1["id"])) || !strings.Contains(text(draft["content"]), text(m2["id"])) {
		t.Fatal(draft)
	}
	saved, e := s.SaveProjectDailyLog(draft)
	if e != nil {
		t.Fatal(e)
	}
	original, _ := json.Marshal(saved)
	_, e = s.SaveLANDailyEntry(text(m1["id"]), map[string]any{"id": a["id"], "version": a["version"], "project_id": p["id"], "work_date": "2026-11-05", "content": "后续成员修改", "minutes": 100})
	if e != nil {
		t.Fatal(e)
	}
	got := getNow(t, s, "project_daily_logs", text(saved["id"]))
	after, _ := json.Marshal(got)
	if string(after) != string(original) {
		t.Fatal("saved owner prose or sources overwritten")
	}
	_, e = s.Create("project_daily_logs", map[string]any{"project_id": p["id"], "log_date": "2026-11-06", "content": "试图复用旧快照", "source_snapshot": snap})
	assertStatus(t, e, 409)
	// Explicitly sending an old same-date snapshot also triggers source conflict.
	_, e = s.UpdateProjectDailyLog(text(saved["id"]), map[string]any{"version": saved["version"], "content": "不能伪称重新汇总", "source_snapshot": snap})
	assertStatus(t, e, 409)
	updated, e := s.UpdateProjectDailyLog(text(saved["id"]), map[string]any{"version": saved["version"], "content": "负责人只改已冻结正文"})
	if e != nil || updated["content"] != "负责人只改已冻结正文" {
		t.Fatal(updated, e)
	}
	if sqlCount(t, s, "time_entries") != 1 {
		t.Fatal("LAN entry duplicated into single-user time entries")
	}
	assertChain(t, s)
}
func TestLANAuditFailureRollsBackPairingMemberAssignmentDailyAndTaskRollup(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "LAN事务"})
	parent := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "父项"})
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "parent_id": parent["id"], "name": "叶子"})
	m := mustLANMember(t, s, "成员", []any{p["id"]}, []any{task["id"]})
	pair, e := s.CreateLANPairing(text(m["id"]), map[string]any{"version": m["version"]})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DB.Exec("CREATE TRIGGER fail_lan_audit BEFORE INSERT ON audit WHEN NEW.operator LIKE '%[成员:%' BEGIN SELECT RAISE(ABORT,'member audit failed');END"); e != nil {
		t.Fatal(e)
	}
	_, e = s.ExchangeLANPairing(text(pair["code"]), "设备")
	if e == nil {
		t.Fatal("audit failure hidden")
	}
	if sqlCount(t, s, "lan_pairings") != 1 || sqlCount(t, s, "lan_sessions") != 0 {
		t.Fatal("pairing consumed despite audit failure")
	}
	_, e = s.SaveLANDailyEntry(text(m["id"]), lanEntryInput(p["id"], "2026-11-05", "失败日报", 1))
	if e == nil || sqlCount(t, s, "lan_daily_entries") != 0 {
		t.Fatal("partial daily write")
	}
	before := getNow(t, s, "tasks", text(task["id"]))
	_, e = s.UpdateLANTask(text(m["id"]), text(task["id"]), map[string]any{"version": before["version"], "progress": 50, "status": "doing"})
	if e == nil {
		t.Fatal("task audit failure hidden")
	}
	after := getNow(t, s, "tasks", text(task["id"]))
	if !reflect.DeepEqual(before, after) || num(getNow(t, s, "tasks", text(parent["id"]))["progress"]) != 0 {
		t.Fatal("partial member progress/rollup")
	}
	s.DB.Exec("DROP TRIGGER fail_lan_audit")
	if _, e = s.ExchangeLANPairing(text(pair["code"]), "重试设备"); e != nil {
		t.Fatal(e)
	}
	var owner string
	s.DB.QueryRow("SELECT value FROM settings WHERE key='operator'").Scan(&owner)
	if owner != "本人" {
		t.Fatal("member audit changed global operator")
	}
	assertChain(t, s)
}

func TestLANUnsubmittedMemberTasksAndProjectReferenceProtection(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "未填写成员项目"})
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "未填写也保留当前任务"})
	m := mustLANMember(t, s, "未提交成员", []any{p["id"]}, []any{task["id"]})
	draft, e := s.ProjectDailyLog(text(p["id"]), "2026-11-05")
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(text(draft["content"]), "当天未提交") || !strings.Contains(text(draft["content"]), text(task["name"])) || len(mapItems(draft["source_snapshot"].(map[string]any)["lan_people"])) != 1 {
		t.Fatal(draft)
	}
	empty := mustCreate(t, s, "projects", map[string]any{"name": "仅成员项目归属"})
	membership := mustLANMember(t, s, "只读成员", []any{empty["id"]}, []any{})
	_, e = s.SoftDelete("projects", text(empty["id"]), map[string]any{"version": empty["version"]})
	assertStatus(t, e, 409)
	_, e = s.UpdateLANMember(text(membership["id"]), map[string]any{"version": membership["version"], "project_ids": []any{}})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Delete("projects", text(empty["id"]), versionOf(empty["version"])); e != nil {
		t.Fatal(e)
	}
	// A scope removal cannot remove the project while historical LAN work remains.
	onlyLog := mustCreate(t, s, "projects", map[string]any{"name": "仅成员日志历史"})
	writer := mustLANMember(t, s, "历史成员", []any{onlyLog["id"]}, []any{})
	mustLANEntry(t, s, writer["id"], lanEntryInput(onlyLog["id"], "2026-11-05", "历史不能孤立", 0))
	_, e = s.UpdateLANMember(text(writer["id"]), map[string]any{"version": writer["version"], "project_ids": []any{}})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.SoftDelete("projects", text(onlyLog["id"]), map[string]any{"version": onlyLog["version"]})
	assertStatus(t, e, 409)
	// Project membership removal hides former project log data from this member.
	m, e = s.UpdateLANMember(text(m["id"]), map[string]any{"version": m["version"], "enabled": false})
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.CreateLANPairing(text(m["id"]), map[string]any{"version": m["version"]})
	assertStatus(t, e, 403)
	assertChain(t, s)
}

func TestLANOwnerAssigneeChangeAndBatchWithdrawGrantWithoutNameAuthorization(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "本机改派权限"})
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "本机可改派"})
	batchTask := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "批量可改派"})
	m1 := mustLANMember(t, s, "原成员", []any{p["id"]}, []any{task["id"], batchTask["id"]})
	m2 := mustLANMember(t, s, "新成员", []any{p["id"]}, []any{})
	task = getNow(t, s, "tasks", text(task["id"]))
	task = mustUpdate(t, s, "tasks", task, map[string]any{"assignee": "新成员"})
	for _, m := range []map[string]any{m1, m2} {
		_, e := s.UpdateLANTask(text(m["id"]), text(task["id"]), map[string]any{"version": task["version"], "progress": 30})
		assertStatus(t, e, 403)
	}
	ws, e := s.LANWorkspace(text(m1["id"]))
	if e != nil || coverageFind(t, ws["tasks"], task["id"])["can_update"] != false {
		t.Fatal(ws, e)
	}
	batchTask = getNow(t, s, "tasks", text(batchTask["id"]))
	_, e = s.BatchTasks(map[string]any{"items": []any{map[string]any{"id": batchTask["id"], "version": batchTask["version"]}}, "changes": map[string]any{"assignee": "新成员"}})
	if e != nil {
		t.Fatal(e)
	}
	batchTask = getNow(t, s, "tasks", text(batchTask["id"]))
	_, e = s.UpdateLANTask(text(m1["id"]), text(batchTask["id"]), map[string]any{"version": batchTask["version"], "progress": 30})
	assertStatus(t, e, 403)
	// Granting explicitly by stable ID works; an unchanged name PATCH preserves it.
	m2, e = s.UpdateLANMember(text(m2["id"]), map[string]any{"version": m2["version"], "task_ids": []any{task["id"]}})
	if e != nil {
		t.Fatal(e)
	}
	task = getNow(t, s, "tasks", text(task["id"]))
	task = mustUpdate(t, s, "tasks", task, map[string]any{"assignee": "新成员"})
	_, e = s.UpdateLANTask(text(m2["id"]), text(task["id"]), map[string]any{"version": task["version"], "progress": 30})
	if e != nil {
		t.Fatal(e)
	}
	assertChain(t, s)
}
