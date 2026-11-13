package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

func migrateV9(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE lan_members(id TEXT PRIMARY KEY,name TEXT NOT NULL,enabled INTEGER NOT NULL DEFAULT 1 CHECK(enabled IN(0,1)),auth_version INTEGER NOT NULL DEFAULT 1,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1);
CREATE TABLE lan_member_projects(member_id TEXT NOT NULL REFERENCES lan_members(id),project_id TEXT NOT NULL REFERENCES projects(id),PRIMARY KEY(member_id,project_id));
CREATE INDEX lan_project_members ON lan_member_projects(project_id,member_id);
CREATE TABLE lan_member_tasks(task_id TEXT PRIMARY KEY REFERENCES tasks(id),member_id TEXT NOT NULL REFERENCES lan_members(id));
CREATE INDEX lan_tasks_member ON lan_member_tasks(member_id,task_id);
CREATE TABLE lan_pairings(hash TEXT PRIMARY KEY,member_id TEXT NOT NULL REFERENCES lan_members(id),auth_version INTEGER NOT NULL,expires_at TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE INDEX lan_pairings_member ON lan_pairings(member_id);
CREATE TABLE lan_sessions(hash TEXT PRIMARY KEY,member_id TEXT NOT NULL REFERENCES lan_members(id),auth_version INTEGER NOT NULL,device TEXT NOT NULL DEFAULT '',expires_at TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE INDEX lan_sessions_member ON lan_sessions(member_id);
CREATE TABLE lan_daily_entries(id TEXT PRIMARY KEY,member_id TEXT NOT NULL REFERENCES lan_members(id),member_name TEXT NOT NULL,project_id TEXT NOT NULL REFERENCES projects(id),work_date TEXT NOT NULL,content TEXT NOT NULL DEFAULT '',next_plan TEXT NOT NULL DEFAULT '',blockers TEXT NOT NULL DEFAULT '',minutes INTEGER NOT NULL DEFAULT 0 CHECK(minutes BETWEEN 0 AND 1440),created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,UNIQUE(member_id,project_id,work_date));
CREATE INDEX lan_daily_member_day ON lan_daily_entries(member_id,work_date);
CREATE INDEX lan_daily_project_day ON lan_daily_entries(project_id,work_date,id);
INSERT OR IGNORE INTO settings(key,value)VALUES('lan_enabled','0'),('lan_port','18680');
PRAGMA user_version=9;`)
	return err
}
func lanForbidden() error { return fail("forbidden", "成员无权访问此项目或任务", 403) }
func lanUnauthenticated() error {
	return fail("unauthorized", "配对码或会话已失效，请重新配对", 401)
}
func lanToken() (string, error) {
	var b [32]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	return hex.EncodeToString(b[:]), nil
}
func lanMember(q queryer, id string, enabled bool) (map[string]any, error) {
	rows, e := q.Query("SELECT * FROM lan_members WHERE id=?", id)
	if e != nil {
		return nil, e
	}
	items, e := rawRows(rows)
	if e != nil {
		return nil, e
	}
	if len(items) == 0 {
		return nil, fail("not_found", "成员不存在", 404)
	}
	m := items[0]
	m["enabled"] = num(m["enabled"]) == 1
	if enabled && m["enabled"] != true {
		return nil, lanForbidden()
	}
	for _, spec := range []struct{ k, sql string }{{"project_ids", "SELECT project_id AS id FROM lan_member_projects WHERE member_id=? ORDER BY project_id"}, {"task_ids", "SELECT task_id AS id FROM lan_member_tasks WHERE member_id=? ORDER BY task_id"}} {
		rows, e = q.Query(spec.sql, id)
		if e != nil {
			return nil, e
		}
		list, e := rawRows(rows)
		if e != nil {
			return nil, e
		}
		ids := []any{}
		for _, x := range list {
			ids = append(ids, x["id"])
		}
		m[spec.k] = ids
	}
	return m, nil
}
func lanActor(m map[string]any) string { return text(m["name"]) + " [成员:" + text(m["id"]) + "]" }
func lanScope(q queryer, memberID, projectID string, write bool) (map[string]any, error) {
	m, e := lanMember(q, memberID, true)
	if e != nil {
		return nil, lanForbidden()
	}
	var count int
	if e = q.QueryRow("SELECT count(*) FROM lan_member_projects WHERE member_id=? AND project_id=?", memberID, projectID).Scan(&count); e != nil {
		return nil, e
	}
	if count == 0 {
		return nil, lanForbidden()
	}
	if _, e = get(q, "projects", projectID); e != nil {
		return nil, lanForbidden()
	}
	if write {
		if e = planningProjectWritable(q, projectID); e != nil {
			return nil, e
		}
	}
	return m, nil
}
func lanMemberInput(input map[string]any, create bool) error {
	for k := range input {
		if !oneOf(k, "name", "enabled", "project_ids", "task_ids", "version") {
			return invalid("未知成员字段: " + k)
		}
	}
	if create {
		if _, ok := input["version"]; ok {
			return invalid("新成员不接受版本")
		}
	}
	if v, ok := input["enabled"]; ok {
		if _, ok = v.(bool); !ok {
			return invalid("enabled必须为布尔值")
		}
	}
	return nil
}
func lanRevoke(tx *sql.Tx, id string) error {
	for _, table := range []string{"lan_sessions", "lan_pairings"} {
		if _, e := tx.Exec("DELETE FROM "+table+" WHERE member_id=?", id); e != nil {
			return e
		}
	}
	return nil
}
func lanApplyScope(tx *sql.Tx, m, before map[string]any, input map[string]any) error {
	projects, err := stringIDs(m["project_ids"])
	if err != nil {
		return err
	}
	tasks, err := stringIDs(m["task_ids"])
	if err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, pid := range projects {
		if _, err = get(tx, "projects", text(pid)); err != nil {
			return invalid("成员项目不存在或已回收")
		}
		allowed[text(pid)] = true
	}
	existing := map[string]bool{}
	if before != nil {
		for _, id := range before["task_ids"].([]any) {
			existing[text(id)] = true
		}
	}
	_, explicitTasks := input["task_ids"]
	taskRows := []map[string]any{}
	for _, id := range tasks {
		retained := existing[text(id)]
		task, e := getIncludingTrash(tx, "tasks", text(id), retained)
		if e != nil {
			return invalid("分配任务不存在或已回收")
		}
		if !allowed[text(task["project_id"])] {
			if retained && !explicitTasks {
				continue
			}
			return invalid("分配任务必须属于成员项目")
		}
		if !retained && oneOf(task["status"], "done", "canceled") {
			return fail("read_only", "已结束任务不能新指派负责人", 409)
		}
		if !retained && task["has_children"] == true {
			return invalid("只能向成员分配有效叶子任务")
		}
		if !retained {
			if e = planningProjectWritable(tx, text(task["project_id"])); e != nil {
				return e
			}
		}
		var owner string
		e = tx.QueryRow("SELECT member_id FROM lan_member_tasks WHERE task_id=?", id).Scan(&owner)
		if e != nil && e != sql.ErrNoRows {
			return e
		}
		if owner != "" && owner != text(m["id"]) {
			return fail("assignment_conflict", "任务已分配给另一成员，请先解除原分配", 409)
		}
		taskRows = append(taskRows, task)
	}
	if _, err = tx.Exec("DELETE FROM lan_member_projects WHERE member_id=?", m["id"]); err != nil {
		return err
	}
	for _, id := range projects {
		if _, err = tx.Exec("INSERT INTO lan_member_projects(member_id,project_id)VALUES(?,?)", m["id"], id); err != nil {
			return err
		}
	}
	retainedTasks := map[string]bool{}
	for _, task := range taskRows {
		retainedTasks[text(task["id"])] = true
	}
	for taskID := range existing {
		if retainedTasks[taskID] {
			continue
		}
		task, e := getIncludingTrash(tx, "tasks", taskID, true)
		if e != nil {
			return e
		}
		// Completed/removed work retains its historical person and ID while
		// its editable grant is withdrawn with project membership.
		if !nonempty(task["deleted_at"]) && !oneOf(task["status"], "done", "canceled") && planningProjectWritable(tx, text(task["project_id"])) == nil {
			if _, e = tx.Exec("UPDATE tasks SET assignee='',assignee_member_id=NULL,version=version+1,updated_at=? WHERE id=?", now(), taskID); e != nil {
				return e
			}
			after, e := get(tx, "tasks", taskID)
			if e != nil {
				return e
			}
			if e = searchSync(tx, "tasks", after); e != nil {
				return e
			}
			if e = audit(tx, "tasks", taskID, "lan_unassign", task, after); e != nil {
				return e
			}
		}
	}
	if _, err = tx.Exec("DELETE FROM lan_member_tasks WHERE member_id=?", m["id"]); err != nil {
		return err
	}
	for _, task := range taskRows {
		if _, err = tx.Exec("INSERT INTO lan_member_tasks(task_id,member_id)VALUES(?,?)", task["id"], m["id"]); err != nil {
			return err
		}
		if (text(task["assignee"]) != text(m["name"]) || text(task["assignee_member_id"]) != text(m["id"])) && !nonempty(task["deleted_at"]) && !oneOf(task["status"], "done", "canceled") && planningProjectWritable(tx, text(task["project_id"])) == nil {
			if _, err = tx.Exec("UPDATE tasks SET assignee=?,assignee_member_id=?,version=version+1,updated_at=? WHERE id=?", m["name"], m["id"], now(), task["id"]); err != nil {
				return err
			}
			after, e := get(tx, "tasks", text(task["id"]))
			if e != nil {
				return e
			}
			if e = searchSync(tx, "tasks", after); e != nil {
				return e
			}
			if e = audit(tx, "tasks", text(task["id"]), "lan_assign", task, after); e != nil {
				return e
			}
		}
	}
	return nil
}
func (s *Store) CreateLANMember(input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		if e := lanMemberInput(input, true); e != nil {
			return nil, e
		}
		name, e := inputText(input, "name", 100, false)
		if e != nil {
			return nil, e
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, invalid("成员姓名不能为空")
		}
		at := now()
		m := map[string]any{"id": uid(), "name": name, "enabled": true, "project_ids": []any{}, "task_ids": []any{}}
		for _, key := range []string{"enabled", "project_ids", "task_ids"} {
			if v, ok := input[key]; ok {
				m[key] = v
			}
		}
		if _, e = tx.Exec("INSERT INTO lan_members(id,name,enabled,created_at,updated_at)VALUES(?,?,?,?,?)", m["id"], name, m["enabled"], at, at); e != nil {
			return nil, e
		}
		if e = lanApplyScope(tx, m, nil, input); e != nil {
			return nil, e
		}
		after, e := lanMember(tx, text(m["id"]), false)
		if e != nil {
			return nil, e
		}
		return after, audit(tx, "lan_members", text(m["id"]), "create", nil, after)
	})
}
func (s *Store) ListLANMembers(filters map[string]string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	where := " WHERE 1=1"
	args := []any{}
	if q := filters["q"]; q != "" {
		if len([]rune(q)) > 200 {
			return nil, invalid("查询内容过长")
		}
		where += " AND name LIKE ? ESCAPE '\\'"
		args = append(args, like(q))
	}
	var total int
	if e := s.DB.QueryRow("SELECT count(*) FROM lan_members"+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	args = append(args, pageLimit(filters, 100), offset)
	rows, e := s.DB.Query("SELECT id FROM lan_members"+where+" ORDER BY created_at,id LIMIT ? OFFSET ?", args...)
	if e != nil {
		return nil, e
	}
	ids, e := rawRows(rows)
	if e != nil {
		return nil, e
	}
	items := []map[string]any{}
	for _, id := range ids {
		m, e := lanMember(s.DB, text(id["id"]), false)
		if e != nil {
			return nil, e
		}
		items = append(items, m)
	}
	return map[string]any{"items": items, "total": total, "next_cursor": ""}, nil
}
func (s *Store) UpdateLANMember(id string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		if e := lanMemberInput(input, false); e != nil {
			return nil, e
		}
		before, e := lanMember(tx, id, false)
		if e != nil {
			return nil, e
		}
		if e = checkVersion(before, input["version"]); e != nil {
			return nil, e
		}
		m := copyMap(before)
		for _, key := range []string{"name", "enabled", "project_ids", "task_ids"} {
			if v, ok := input[key]; ok {
				m[key] = v
			}
		}
		name, ok := m["name"].(string)
		if !ok || strings.TrimSpace(name) == "" || len([]rune(name)) > 100 {
			return nil, invalid("成员姓名须为1至100字符")
		}
		m["name"] = strings.TrimSpace(name)
		if _, e = tx.Exec("UPDATE lan_members SET name=?,enabled=?,auth_version=auth_version+1,version=version+1,updated_at=? WHERE id=?", m["name"], m["enabled"], now(), id); e != nil {
			return nil, e
		}
		if e = lanApplyScope(tx, m, before, input); e != nil {
			return nil, e
		}
		if e = lanRevoke(tx, id); e != nil {
			return nil, e
		}
		after, e := lanMember(tx, id, false)
		if e != nil {
			return nil, e
		}
		return after, audit(tx, "lan_members", id, "update", before, after)
	})
}
func (s *Store) RevokeLANMember(id string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		for k := range input {
			if k != "version" {
				return nil, invalid("仅接受成员版本")
			}
		}
		before, e := lanMember(tx, id, false)
		if e != nil {
			return nil, e
		}
		if e = checkVersion(before, input["version"]); e != nil {
			return nil, e
		}
		if _, e = tx.Exec("UPDATE lan_members SET auth_version=auth_version+1,version=version+1,updated_at=? WHERE id=?", now(), id); e != nil {
			return nil, e
		}
		if e = lanRevoke(tx, id); e != nil {
			return nil, e
		}
		after, e := lanMember(tx, id, false)
		if e != nil {
			return nil, e
		}
		return after, audit(tx, "lan_members", id, "revoke", before, after)
	})
}
func (s *Store) RevokeAllLANSessions() error {
	_, e := s.write(func(tx *sql.Tx) (map[string]any, error) {
		if _, e := tx.Exec("DELETE FROM lan_sessions;DELETE FROM lan_pairings"); e != nil {
			return nil, e
		}
		return nil, audit(tx, "system", "lan", "revoke_all", nil, map[string]any{"revoked": true})
	})
	return e
}
func (s *Store) CreateLANPairing(id string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		for k := range input {
			if k != "version" {
				return nil, invalid("仅接受成员版本")
			}
		}
		m, e := lanMember(tx, id, true)
		if e != nil {
			return nil, e
		}
		if e = checkVersion(m, input["version"]); e != nil {
			return nil, e
		}
		code, e := lanToken()
		if e != nil {
			return nil, e
		}
		expires := time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339Nano)
		if _, e = tx.Exec("DELETE FROM lan_pairings WHERE member_id=?", id); e != nil {
			return nil, e
		}
		if _, e = tx.Exec("INSERT INTO lan_pairings(hash,member_id,auth_version,expires_at,created_at)VALUES(?,?,?,?,?)", shaText(code), id, m["auth_version"], expires, now()); e != nil {
			return nil, e
		}
		if e = audit(tx, "lan_members", id, "pairing_created", nil, map[string]any{"expires_at": expires}); e != nil {
			return nil, e
		}
		return map[string]any{"code": code, "expires_at": expires, "member": m}, nil
	})
}
func (s *Store) ExchangeLANPairing(code, device string) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		if len(code) != 64 || len([]rune(device)) > 200 {
			return nil, lanUnauthenticated()
		}
		var id, expires string
		var version int
		e := tx.QueryRow("SELECT member_id,auth_version,expires_at FROM lan_pairings WHERE hash=?", shaText(code)).Scan(&id, &version, &expires)
		if e != nil {
			return nil, lanUnauthenticated()
		}
		until, e := time.Parse(time.RFC3339Nano, expires)
		if e != nil || !time.Now().Before(until) {
			return nil, lanUnauthenticated()
		}
		m, e := lanMember(tx, id, true)
		if e != nil || version != versionOf(m["auth_version"]) {
			return nil, lanUnauthenticated()
		}
		token, e := lanToken()
		if e != nil {
			return nil, e
		}
		expiry := time.Now().Add(7 * 24 * time.Hour).UTC().Format(time.RFC3339Nano)
		if _, e = tx.Exec("DELETE FROM lan_pairings WHERE hash=?", shaText(code)); e != nil {
			return nil, e
		}
		if _, e = tx.Exec("INSERT INTO lan_sessions(hash,member_id,auth_version,device,expires_at,created_at)VALUES(?,?,?,?,?,?)", shaText(token), id, version, device, expiry, now()); e != nil {
			return nil, e
		}
		if e = auditAs(tx, lanActor(m), "lan_members", id, "paired", nil, map[string]any{"device": device, "expires_at": expiry}); e != nil {
			return nil, e
		}
		return map[string]any{"token": token, "expires_at": expiry, "member": m}, nil
	})
}
func (s *Store) LANSession(token string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	if len(token) != 64 {
		return nil, lanUnauthenticated()
	}
	tx, e := s.DB.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var id, expires string
	var version int
	e = tx.QueryRow("SELECT member_id,auth_version,expires_at FROM lan_sessions WHERE hash=?", shaText(token)).Scan(&id, &version, &expires)
	if e != nil {
		return nil, lanUnauthenticated()
	}
	until, e := time.Parse(time.RFC3339Nano, expires)
	if e != nil || !time.Now().Before(until) {
		return nil, lanUnauthenticated()
	}
	m, e := lanMember(tx, id, true)
	if e != nil || version != versionOf(m["auth_version"]) {
		return nil, lanUnauthenticated()
	}
	return m, nil
}
func (s *Store) LogOutLAN(token string) error {
	_, e := s.write(func(tx *sql.Tx) (map[string]any, error) {
		var id string
		if e := tx.QueryRow("SELECT member_id FROM lan_sessions WHERE hash=?", shaText(token)).Scan(&id); e != nil {
			if e == sql.ErrNoRows {
				return nil, nil
			}
			return nil, e
		}
		m, e := lanMember(tx, id, false)
		if e != nil {
			return nil, e
		}
		if _, e = tx.Exec("DELETE FROM lan_sessions WHERE hash=?", shaText(token)); e != nil {
			return nil, e
		}
		return nil, auditAs(tx, lanActor(m), "lan_members", id, "logout", nil, map[string]any{"logged_out": true})
	})
	return e
}
func lanTask(q queryer, task map[string]any, memberID string) map[string]any {
	out := map[string]any{}
	for _, key := range strings.Fields("id project_id parent_id wbs_code name assignee plan_start plan_end status progress version depth has_children") {
		out[key] = task[key]
	}
	var assigned string
	_ = q.QueryRow("SELECT member_id FROM lan_member_tasks WHERE task_id=?", task["id"]).Scan(&assigned)
	var status string
	_ = q.QueryRow("SELECT status FROM projects WHERE id=? AND deleted_at=''", task["project_id"]).Scan(&status)
	out["can_update"] = assigned == memberID && task["has_children"] != true && !oneOf(task["status"], "done", "canceled") && !oneOf(status, "closed", "archived", "")
	return out
}
func (s *Store) LANWorkspace(memberID string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	tx, e := s.DB.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	m, e := lanMember(tx, memberID, true)
	if e != nil {
		return nil, lanForbidden()
	}
	projects := []map[string]any{}
	tasks := []map[string]any{}
	for _, id := range m["project_ids"].([]any) {
		p, e := get(tx, "projects", text(id))
		if e != nil {
			continue
		}
		projects = append(projects, map[string]any{"id": p["id"], "name": p["name"], "status": p["status"], "version": p["version"]})
		rows, e := tx.Query("SELECT t.id,t.project_id,t.parent_id,t.wbs_code,t.name,t.assignee,t.plan_start,t.plan_end,t.status,t.progress,t.version,COALESCE(a.member_id,'') AS assigned_member_id FROM tasks t LEFT JOIN lan_member_tasks a ON a.task_id=t.id WHERE t.project_id=? AND t.deleted_at='' ORDER BY t.sort_order,t.id", id)
		if e != nil {
			return nil, e
		}
		all, e := rawRows(rows)
		if e != nil {
			return nil, e
		}
		children := map[string][]map[string]any{}
		for _, task := range all {
			normalize("tasks", task)
			children[text(task["parent_id"])] = append(children[text(task["parent_id"])], task)
		}
		type node struct {
			m     map[string]any
			depth int
		}
		stack := []node{}
		for i := len(children[""]) - 1; i >= 0; i-- {
			stack = append(stack, node{children[""][i], 0})
		}
		seen := map[string]bool{}
		for len(stack) > 0 {
			v := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			tid := text(v.m["id"])
			if seen[tid] {
				return nil, invalid("任务树存在环")
			}
			seen[tid] = true
			v.m["depth"] = v.depth
			v.m["has_children"] = len(children[tid]) > 0
			out := map[string]any{}
			for _, key := range strings.Fields("id project_id parent_id wbs_code name assignee plan_start plan_end status progress version depth has_children") {
				out[key] = v.m[key]
			}
			out["can_update"] = text(v.m["assigned_member_id"]) == memberID && len(children[tid]) == 0 && !oneOf(v.m["status"], "done", "canceled") && !oneOf(p["status"], "closed", "archived")
			tasks = append(tasks, out)
			for i := len(children[tid]) - 1; i >= 0; i-- {
				stack = append(stack, node{children[tid][i], v.depth + 1})
			}
		}
		if len(seen) != len(all) {
			return nil, invalid("任务树存在孤立节点")
		}

	}
	var datasetID string
	if e = tx.QueryRow("SELECT value FROM settings WHERE key='dataset_id'").Scan(&datasetID); e != nil {
		return nil, e
	}
	return map[string]any{"member": m, "projects": projects, "tasks": tasks, "total": len(tasks), "dataset_id": datasetID}, nil
}
func (s *Store) SaveLANDailyEntry(memberID string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		for k := range input {
			if !oneOf(k, "id", "version", "project_id", "work_date", "content", "next_plan", "blockers", "minutes") {
				return nil, invalid("未知成员日报字段: " + k)
			}
		}
		pid, date := text(input["project_id"]), text(input["work_date"])
		member, e := lanScope(tx, memberID, pid, true)
		if e != nil {
			return nil, e
		}
		if date == "" || !validDate(date) {
			return nil, invalid("工作日期须为真实YYYY-MM-DD")
		}
		minutes, ok := numeric(input["minutes"])
		if !ok || minutes < 0 || minutes > 1440 || math.Trunc(minutes) != minutes {
			return nil, invalid("分钟须为0至1440整数")
		}
		in := map[string]any{}
		for _, key := range []string{"content", "next_plan", "blockers"} {
			value, e := inputText(input, key, 20000, true)
			if e != nil {
				return nil, e
			}
			in[key] = value
		}
		if !nonempty(in["content"]) && !nonempty(in["next_plan"]) && !nonempty(in["blockers"]) && minutes == 0 {
			return nil, invalid("日报至少填写一项工作、计划、阻碍或工时")
		}
		id := text(input["id"])
		var before map[string]any
		if id != "" {
			before, e = lanDaily(tx, id)
			if e != nil {
				return nil, e
			}
			if text(before["member_id"]) != memberID {
				return nil, lanForbidden()
			}
			if text(before["project_id"]) != pid || text(before["work_date"]) != date {
				return nil, invalid("成员日报不能修改项目或工作日期")
			}
			if e = checkVersion(before, input["version"]); e != nil {
				return nil, e
			}
		} else {
			if _, ok := input["version"]; ok {
				return nil, fail("version_conflict", "新日报不接受版本，请读取已保存日报", 409)
			}
			var count int
			if e = tx.QueryRow("SELECT count(*) FROM lan_daily_entries WHERE member_id=? AND project_id=? AND work_date=?", memberID, pid, date).Scan(&count); e != nil {
				return nil, e
			}
			if count > 0 {
				return nil, fail("version_conflict", "该项目当天日报已存在，请读取当前版本", 409)
			}
			id = uid()
		}
		var total int
		if e = tx.QueryRow("SELECT COALESCE(sum(minutes),0) FROM lan_daily_entries WHERE member_id=? AND work_date=? AND id!=?", memberID, date, id).Scan(&total); e != nil {
			return nil, e
		}
		if total+int(minutes) > 1440 {
			return nil, invalid("同一成员同日跨项目工时不能超过1440分钟")
		}
		if before == nil {
			_, e = tx.Exec("INSERT INTO lan_daily_entries(id,member_id,member_name,project_id,work_date,content,next_plan,blockers,minutes,created_at,updated_at)VALUES(?,?,?,?,?,?,?,?,?,?,?)", id, memberID, member["name"], pid, date, in["content"], in["next_plan"], in["blockers"], int(minutes), now(), now())
		} else {
			_, e = tx.Exec("UPDATE lan_daily_entries SET content=?,next_plan=?,blockers=?,minutes=?,member_name=?,updated_at=?,version=version+1 WHERE id=?", in["content"], in["next_plan"], in["blockers"], int(minutes), member["name"], now(), id)
		}
		if e != nil {
			return nil, dbErr(e)
		}
		after, e := lanDaily(tx, id)
		if e != nil {
			return nil, e
		}
		action := "create"
		if before != nil {
			action = "update"
		}
		return after, auditAs(tx, lanActor(member), "lan_daily_entries", id, action, before, after)
	})
}
func lanDaily(q queryer, id string) (map[string]any, error) {
	rows, e := q.Query("SELECT * FROM lan_daily_entries WHERE id=?", id)
	if e != nil {
		return nil, e
	}
	items, e := rawRows(rows)
	if e != nil {
		return nil, e
	}
	if len(items) == 0 {
		return nil, fail("not_found", "成员日报不存在", 404)
	}
	return items[0], nil
}
func (s *Store) LANDailyEntries(memberID string, filters map[string]string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	where := " WHERE 1=1"
	args := []any{}
	if memberID != "" {
		if _, e := lanMember(s.DB, memberID, true); e != nil {
			return nil, lanForbidden()
		}
		where += " AND member_id=? AND project_id IN(SELECT project_id FROM lan_member_projects WHERE member_id=?)"
		args = append(args, memberID, memberID)
	} else if id := filters["member_id"]; id != "" {
		where += " AND member_id=?"
		args = append(args, id)
	}
	filters = canonicalFilters(filters)
	for _, key := range []string{"project_id", "work_date"} {
		if value := filters[key]; value != "" {
			if key == "work_date" && !validDate(value) {
				return nil, invalid("日期无效")
			}
			where += " AND " + key + "=?"
			args = append(args, value)
		}
	}
	for _, key := range []string{"date_from", "date_to"} {
		if value := filters[key]; value != "" {
			if !validDate(value) {
				return nil, invalid("日期无效")
			}
			op := ">="
			if key == "date_to" {
				op = "<="
			}
			where += " AND work_date" + op + "?"
			args = append(args, value)
		}
	}
	if filters["date_from"] != "" && filters["date_to"] != "" && filters["date_from"] > filters["date_to"] {
		return nil, invalid("日期范围无效")
	}
	if q := filters["q"]; q != "" {
		if len([]rune(q)) > 200 {
			return nil, invalid("查询内容过长")
		}
		where += " AND (content LIKE ? ESCAPE '\\' OR next_plan LIKE ? ESCAPE '\\' OR blockers LIKE ? ESCAPE '\\')"
		args = append(args, like(q), like(q), like(q))
	}
	var total int
	if e := s.DB.QueryRow("SELECT count(*) FROM lan_daily_entries"+where, args...).Scan(&total); e != nil {
		return nil, e
	}
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	args = append(args, pageLimit(filters, 100), offset)
	rows, e := s.DB.Query("SELECT * FROM lan_daily_entries"+where+" ORDER BY work_date DESC,created_at DESC,id DESC LIMIT ? OFFSET ?", args...)
	if e != nil {
		return nil, e
	}
	items, e := rawRows(rows)
	return map[string]any{"items": items, "total": total, "next_cursor": ""}, e
}
func (s *Store) UpdateLANTask(memberID, taskID string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		for k := range input {
			if !oneOf(k, "version", "status", "progress") {
				return nil, invalid("成员仅可修改任务状态和进度")
			}
		}
		task, e := get(tx, "tasks", taskID)
		if e != nil {
			return nil, e
		}
		member, e := lanScope(tx, memberID, text(task["project_id"]), true)
		if e != nil {
			return nil, e
		}
		var assigned string
		if e = tx.QueryRow("SELECT member_id FROM lan_member_tasks WHERE task_id=?", taskID).Scan(&assigned); e != nil || assigned != memberID {
			return nil, lanForbidden()
		}
		if task["has_children"] == true {
			return nil, lanForbidden()
		}
		if oneOf(task["status"], "done", "canceled") {
			return nil, fail("read_only", "已结束任务须由负责人处理", 409)
		}
		if status, ok := input["status"]; ok && !oneOf(status, "todo", "doing", "done") {
			return nil, invalid("成员任务状态无效")
		}
		after, e := updateAs(tx, "tasks", taskID, input, lanActor(member))
		if e != nil {
			return nil, e
		}
		return lanTask(tx, after, memberID), nil
	})
}

// Add LAN sources only when present, preserving old single-user snapshot shapes.
func projectDailyLAN(q queryer, pid, date string, snapshot map[string]any, content *strings.Builder) error {
	var exists int
	if e := q.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='lan_daily_entries'").Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		return nil
	}
	rows, e := q.Query("SELECT * FROM lan_daily_entries WHERE project_id=? AND work_date=? ORDER BY member_id,id", pid, date)
	if e != nil {
		return e
	}
	entries, e := rawRows(rows)
	if e != nil {
		return e
	}
	rows, e = q.Query("SELECT m.id,m.name,m.enabled FROM lan_members m JOIN lan_member_projects p ON p.member_id=m.id WHERE p.project_id=? ORDER BY m.id", pid)
	if e != nil {
		return e
	}
	members, e := rawRows(rows)
	if e != nil {
		return e
	}
	if len(entries) == 0 && len(members) == 0 {
		return nil
	}
	entryByID := map[string]map[string]any{}
	for _, entry := range entries {
		entryByID[text(entry["member_id"])] = entry
	}
	people := []map[string]any{}
	seen := map[string]bool{}
	extra := 0
	content.WriteString("\n## 局域网成员日报（按稳定成员ID）\n\n")
	render := func(id, name string) {
		seen[id] = true
		entry := entryByID[id]
		minutes := 0
		if entry != nil {
			minutes = int(num(entry["minutes"]))
			extra += minutes
		}
		people = append(people, map[string]any{"member_id": id, "person_name": name, "minutes": minutes, "submitted": entry != nil})
		fmt.Fprintf(content, "### %s（成员ID：%s）\n\n", name, id)
		if entry == nil {
			content.WriteString("- 当天未提交\n")
		} else {
			fmt.Fprintf(content, "- 工时：%d分钟\n", minutes)
			for _, field := range []struct{ k, label string }{{"content", "完成工作"}, {"next_plan", "后续计划"}, {"blockers", "阻碍"}} {
				if nonempty(entry[field.k]) {
					fmt.Fprintf(content, "- %s：%s\n", field.label, text(entry[field.k]))
				}
			}
		}
		for _, task := range mapItems(snapshot["tasks"]) {
			if text(task["lan_member_id"]) == id {
				fmt.Fprintf(content, "- 任务 [%s] %s：%s，进度%.0f%%\n", text(task["wbs_code"]), text(task["name"]), text(task["status"]), num(task["progress"]))
			}
		}
		content.WriteString("\n")
	}
	for _, m := range members {
		render(text(m["id"]), text(m["name"]))
	}
	for _, entry := range entries {
		if !seen[text(entry["member_id"])] {
			render(text(entry["member_id"]), text(entry["member_name"]))
		}
	}
	snapshot["lan_entries"] = entries
	snapshot["lan_people"] = people
	snapshot["lan_minutes"] = extra
	snapshot["total_minutes"] = int(num(snapshot["total_minutes"])) + extra
	fmt.Fprintf(content, "\n合计（单机工时条目 + 成员日报工时，各计一次）：%d分钟。\n", int(num(snapshot["total_minutes"])))
	return nil
}

// A local free-text reassignment withdraws the stable LAN grant. Names never
// grant a different member permission, even when another member has that name.
func lanUnassignTask(tx *sql.Tx, taskID string) error {
	var exists int
	if e := tx.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='lan_member_tasks'").Scan(&exists); e != nil {
		return e
	}
	if exists == 0 {
		return nil
	}
	var memberID string
	e := tx.QueryRow("SELECT member_id FROM lan_member_tasks WHERE task_id=?", taskID).Scan(&memberID)
	if e == sql.ErrNoRows {
		return nil
	}
	if e != nil {
		return e
	}
	if _, e = tx.Exec("DELETE FROM lan_member_tasks WHERE task_id=?", taskID); e != nil {
		return e
	}
	return audit(tx, "tasks", taskID, "lan_unassign", map[string]any{"member_id": memberID}, map[string]any{"member_id": "", "reason": "负责人通过单机任务入口调整署名"})
}
