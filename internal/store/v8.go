package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func migrateV8(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE plan_versions(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,number INTEGER NOT NULL CHECK(number>=1),name TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',created_by TEXT NOT NULL,created_at TEXT NOT NULL,snapshot TEXT NOT NULL CHECK(json_valid(snapshot)),hash TEXT NOT NULL,task_count INTEGER NOT NULL CHECK(task_count>=0),version INTEGER NOT NULL DEFAULT 1 CHECK(version=1),UNIQUE(project_id,number));
CREATE INDEX plan_versions_project ON plan_versions(project_id,number DESC);
CREATE TRIGGER plan_versions_immutable_update BEFORE UPDATE ON plan_versions BEGIN SELECT RAISE(ABORT,'plan version is immutable');END;
CREATE TRIGGER plan_versions_immutable_delete BEFORE DELETE ON plan_versions BEGIN SELECT RAISE(ABORT,'plan version is immutable');END;
CREATE TABLE requirements(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,code TEXT NOT NULL UNIQUE,title TEXT NOT NULL,description TEXT NOT NULL,acceptance_criteria TEXT NOT NULL,owner TEXT NOT NULL DEFAULT '',source TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'pending' CHECK(status IN('pending','doing','accepted','void')),revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>=1),acceptance_note TEXT NOT NULL DEFAULT '',change_note TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,deleted_at TEXT NOT NULL DEFAULT '');
CREATE INDEX requirements_project ON requirements(project_id,deleted_at,status,updated_at DESC,id);
CREATE TABLE requirement_task_links(requirement_id TEXT NOT NULL REFERENCES requirements(id) ON DELETE RESTRICT,task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE RESTRICT,PRIMARY KEY(requirement_id,task_id));
CREATE INDEX requirement_task_links_task ON requirement_task_links(task_id,requirement_id);
CREATE TABLE requirement_revisions(requirement_id TEXT NOT NULL REFERENCES requirements(id) ON DELETE RESTRICT,revision INTEGER NOT NULL CHECK(revision>=1),created_by TEXT NOT NULL,created_at TEXT NOT NULL,change_note TEXT NOT NULL DEFAULT '',snapshot TEXT NOT NULL CHECK(json_valid(snapshot)),hash TEXT NOT NULL,PRIMARY KEY(requirement_id,revision));
CREATE TRIGGER requirement_revisions_immutable_update BEFORE UPDATE ON requirement_revisions BEGIN SELECT RAISE(ABORT,'requirement revision is immutable');END;
CREATE TRIGGER requirement_revisions_immutable_delete BEFORE DELETE ON requirement_revisions BEGIN SELECT RAISE(ABORT,'requirement revision is immutable');END;
PRAGMA user_version=8;`)
	return err
}

var planTaskFields = strings.Fields("id project_id parent_id sort_order wbs_code name plan_start plan_end assignee estimate_hours priority description version")
var planComparedFields = strings.Fields("parent_id sort_order wbs_code name plan_start plan_end assignee estimate_hours priority description")

func planSnapshot(q queryer, pid string) (map[string]any, error) {
	project, err := get(q, "projects", pid)
	if err != nil {
		return nil, err
	}
	rows, err := q.Query("SELECT * FROM tasks WHERE project_id=? AND deleted_at='' ORDER BY sort_order,id", pid)
	if err != nil {
		return nil, err
	}
	tasks, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	children := map[string][]map[string]any{}
	for _, t := range tasks {
		normalize("tasks", t)
		children[text(t["parent_id"])] = append(children[text(t["parent_id"])], t)
	}
	type item struct {
		task  map[string]any
		depth int
	}
	stack := []item{}
	for i := len(children[""]) - 1; i >= 0; i-- {
		stack = append(stack, item{children[""][i], 0})
	}
	ordered := []map[string]any{}
	seen := map[string]bool{}
	for len(stack) > 0 {
		v := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		id := text(v.task["id"])
		if seen[id] {
			return nil, invalid("计划任务树存在环")
		}
		seen[id] = true
		t := map[string]any{}
		for _, f := range planTaskFields {
			t[f] = v.task[f]
		}
		t["depth"] = v.depth
		t["order"] = len(ordered) + 1
		t["has_children"] = len(children[id]) > 0
		ordered = append(ordered, t)
		for i := len(children[id]) - 1; i >= 0; i-- {
			stack = append(stack, item{children[id][i], v.depth + 1})
		}
	}
	if len(ordered) != len(tasks) {
		return nil, invalid("计划任务树存在孤立或循环父项")
	}
	return map[string]any{"snapshot_format": 1, "project": project, "tasks": ordered}, nil
}
func frozenObject(raw, hash string) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil || m == nil {
		return nil, fmt.Errorf("版本快照损坏")
	}
	if shaText(raw) != hash {
		return nil, fmt.Errorf("版本快照哈希校验失败")
	}
	return m, nil
}
func planVersion(q queryer, id string) (map[string]any, error) {
	rows, err := q.Query("SELECT * FROM plan_versions WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fail("not_found", "计划版本不存在", 404)
	}
	m := items[0]
	snapshot, err := frozenObject(text(m["snapshot"]), text(m["hash"]))
	if err != nil {
		return nil, err
	}
	m["snapshot"] = snapshot
	return m, nil
}
func (s *Store) CreatePlanVersion(projectID string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		for k := range input {
			if !oneOf(k, "name", "description", "project_version") {
				return nil, invalid("未知计划版本字段: " + k)
			}
		}
		p, err := get(tx, "projects", projectID)
		if err != nil {
			return nil, err
		}
		if err = checkVersion(p, input["project_version"]); err != nil {
			return nil, err
		}
		if err = planningProjectWritable(tx, projectID); err != nil {
			return nil, err
		}
		name, err := inputText(input, "name", 200, false)
		if err != nil {
			return nil, err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, invalid("计划版本名称不能为空")
		}
		description, err := inputText(input, "description", 2000, true)
		if err != nil {
			return nil, err
		}
		snapshot, err := planSnapshot(tx, projectID)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return nil, err
		}
		var number int
		if err = tx.QueryRow("SELECT COALESCE(max(number),0)+1 FROM plan_versions WHERE project_id=?", projectID).Scan(&number); err != nil {
			return nil, err
		}
		id, at := uid(), now()
		if _, err = tx.Exec("INSERT INTO plan_versions(id,project_id,number,name,description,created_by,created_at,snapshot,hash,task_count)VALUES(?,?,?,?,?,?,?,?,?,?)", id, projectID, number, name, description, operator(tx), at, string(raw), shaText(string(raw)), len(snapshot["tasks"].([]map[string]any))); err != nil {
			return nil, err
		}
		m, err := planVersion(tx, id)
		if err != nil {
			return nil, err
		}
		if err = searchSync(tx, "plan_versions", m); err != nil {
			return nil, err
		}
		if err = audit(tx, "plan_versions", id, "create", nil, m); err != nil {
			return nil, err
		}
		return m, nil
	})
}
func listPlanVersions(q queryer, pid string, filters map[string]string) (map[string]any, error) {
	if pid == "" {
		pid = filters["project_id"]
	}
	if pid != "" {
		if _, err := get(q, "projects", pid); err != nil {
			return nil, err
		}
	}
	where := " WHERE 1=1"
	args := []any{}
	if pid != "" {
		where += " AND project_id=?"
		args = append(args, pid)
	}
	if query := strings.TrimSpace(filters["q"]); query != "" {
		if len([]rune(query)) > 200 {
			return nil, invalid("搜索内容不能超过200字符")
		}
		where += " AND (name LIKE ? ESCAPE '\\' OR description LIKE ? ESCAPE '\\')"
		args = append(args, like(query), like(query))
	}
	var total int
	if err := q.QueryRow("SELECT count(*) FROM plan_versions"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	args = append(args, pageLimit(filters, 100), offset)
	rows, err := q.Query("SELECT id,project_id,number,name,description,created_by,created_at,hash,task_count,version FROM plan_versions"+where+" ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	return map[string]any{"items": items, "total": total, "next_cursor": ""}, err
}
func (s *Store) ListPlanVersions(projectID string, filters map[string]string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return listPlanVersions(s.DB, projectID, filters)
}
func (s *Store) GetPlanVersion(id string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return planVersion(s.DB, id)
}
func planMeta(m map[string]any, current bool) map[string]any {
	out := map[string]any{"current": current}
	for _, k := range strings.Fields("id project_id number name created_by created_at hash task_count") {
		out[k] = m[k]
	}
	return out
}
func mapItems(v any) []map[string]any {
	out := []map[string]any{}
	switch a := v.(type) {
	case []map[string]any:
		return a
	case []any:
		for _, i := range a {
			if m, ok := i.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}
func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
func (s *Store) ComparePlanVersions(pid, fromID, toID string) (map[string]any, error) {
	return s.comparePlanVersions(pid, fromID, toID, false)
}

// CapturePlanComparison freezes both complete planning snapshots and their
// comparison within the same transaction, including when the target is current.
func (s *Store) CapturePlanComparison(pid, fromID, toID string) (map[string]any, error) {
	return s.comparePlanVersions(pid, fromID, toID, true)
}
func (s *Store) comparePlanVersions(pid, fromID, toID string, includeSnapshots bool) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = get(tx, "projects", pid); err != nil {
		return nil, err
	}
	from, err := planVersion(tx, fromID)
	if err != nil {
		return nil, err
	}
	if text(from["project_id"]) != pid {
		return nil, invalid("计划版本必须属于当前项目")
	}
	var to map[string]any
	if toID != "" {
		to, err = planVersion(tx, toID)
		if err != nil {
			return nil, err
		}
		if text(to["project_id"]) != pid {
			return nil, invalid("计划版本必须属于当前项目")
		}
	} else {
		snapshot, e := planSnapshot(tx, pid)
		if e != nil {
			return nil, e
		}
		raw, _ := json.Marshal(snapshot)
		to = map[string]any{"id": "", "project_id": pid, "number": 0, "name": "当前计划", "created_at": now(), "created_by": "", "hash": shaText(string(raw)), "task_count": len(mapItems(snapshot["tasks"])), "snapshot": snapshot}
	}
	before := mapItems(from["snapshot"].(map[string]any)["tasks"])
	after := mapItems(to["snapshot"].(map[string]any)["tasks"])
	old := map[string]map[string]any{}
	newer := map[string]map[string]any{}
	for _, t := range before {
		old[text(t["id"])] = t
	}
	for _, t := range after {
		newer[text(t["id"])] = t
	}
	added, removed, changed := []map[string]any{}, []map[string]any{}, []map[string]any{}
	moved, reordered := 0, 0
	for _, t := range after {
		b, ok := old[text(t["id"])]
		if !ok {
			added = append(added, t)
			continue
		}
		fields := []map[string]any{}
		for _, k := range planComparedFields {
			if !jsonEqual(b[k], t[k]) {
				fields = append(fields, map[string]any{"field": k, "before": b[k], "after": t[k]})
			}
		}
		if len(fields) > 0 {
			move := !jsonEqual(b["parent_id"], t["parent_id"])
			order := !jsonEqual(b["sort_order"], t["sort_order"])
			if move {
				moved++
			}
			if order {
				reordered++
			}
			changed = append(changed, map[string]any{"id": t["id"], "before": b, "after": t, "fields": fields, "moved": move, "reordered": order})
		}
	}
	for _, t := range before {
		if _, ok := newer[text(t["id"])]; !ok {
			removed = append(removed, t)
		}
	}
	result := map[string]any{"project_id": pid, "from": planMeta(from, false), "to": planMeta(to, toID == ""), "summary": map[string]any{"added": len(added), "removed": len(removed), "changed": len(changed), "moved": moved, "reordered": reordered, "total": len(added) + len(removed) + len(changed)}, "added": added, "removed": removed, "changed": changed}
	if includeSnapshots {
		result["from_snapshot"] = from["snapshot"]
		result["to_snapshot"] = to["snapshot"]
	}
	return result, nil
}

var requirementFields = strings.Fields("title description acceptance_criteria owner source status acceptance_note change_note")

func validateRequirement(tx *sql.Tx, m, before map[string]any, input map[string]any) error {
	if !nonempty(m["project_id"]) {
		return invalid("需求必须选择项目")
	}
	if err := planningProjectWritable(tx, text(m["project_id"])); err != nil {
		return err
	}
	if before != nil && text(before["project_id"]) != text(m["project_id"]) {
		return invalid("需求不能迁移项目")
	}
	for _, f := range []struct {
		k        string
		max      int
		required bool
	}{{"title", 500, true}, {"description", 200000, true}, {"acceptance_criteria", 20000, true}, {"owner", 100, false}, {"source", 2000, false}, {"acceptance_note", 20000, false}, {"change_note", 2000, before != nil}} {
		value, ok := m[f.k].(string)
		if !ok || f.required && strings.TrimSpace(value) == "" || len([]rune(value)) > f.max {
			return invalid("需求字段为空、类型错误或过长: " + f.k)
		}
	}
	m["title"] = strings.TrimSpace(text(m["title"]))
	if !oneOf(m["status"], "pending", "doing", "accepted", "void") {
		return invalid("需求状态无效")
	}
	if before != nil {
		if !nonempty(input["change_note"]) {
			return invalid("修改需求必须明确填写变更说明")
		}
		if text(before["status"]) == "accepted" && text(m["status"]) == "accepted" {
			return fail("read_only", "已验收需求须明确改为待确认、执行中或作废并说明原因后修订", 409)
		}
	}
	if text(m["status"]) == "accepted" && !nonempty(m["acceptance_note"]) {
		return invalid("验收需求必须填写验收说明")
	}
	return nil
}
func requirementTaskIDs(q queryer, id string) ([]any, error) {
	rows, err := q.Query("SELECT task_id FROM requirement_task_links WHERE requirement_id=? ORDER BY task_id", id)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	ids := []any{}
	for _, i := range items {
		ids = append(ids, i["task_id"])
	}
	return ids, err
}
func requirementLinkedTasks(q queryer, id string) ([]map[string]any, error) {
	rows, err := q.Query("SELECT t.id,t.project_id,t.parent_id,t.wbs_code,t.name,t.assignee,t.plan_start,t.plan_end,t.status,t.progress,t.version,t.deleted_at FROM requirement_task_links l JOIN tasks t ON t.id=l.task_id WHERE l.requirement_id=? ORDER BY t.sort_order,t.id", id)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	for _, m := range items {
		normalize("tasks", m)
	}
	return items, err
}
func syncRequirementLinks(tx *sql.Tx, m, before map[string]any, input map[string]any) error {
	raw, explicit := input["task_ids"]
	if !explicit {
		return nil
	}
	ids, err := stringIDs(raw)
	if err != nil {
		return err
	}
	existing := map[string]bool{}
	if before != nil {
		old, e := requirementTaskIDs(tx, text(m["id"]))
		if e != nil {
			return e
		}
		for _, v := range old {
			existing[text(v)] = true
		}
	}
	for _, value := range ids {
		t, e := getIncludingTrash(tx, "tasks", text(value), true)
		if e != nil {
			return invalid("关联任务不存在")
		}
		if text(t["project_id"]) != text(m["project_id"]) {
			return invalid("需求只能关联同项目任务")
		}
		if nonempty(t["deleted_at"]) && !existing[text(value)] {
			return invalid("不能新增关联已回收任务")
		}
	}
	if _, err = tx.Exec("DELETE FROM requirement_task_links WHERE requirement_id=?", m["id"]); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err = tx.Exec("INSERT INTO requirement_task_links(requirement_id,task_id)VALUES(?,?)", m["id"], id); err != nil {
			return err
		}
	}
	return nil
}
func appendRequirementRevision(tx *sql.Tx, m map[string]any) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO requirement_revisions(requirement_id,revision,created_by,created_at,change_note,snapshot,hash)VALUES(?,?,?,?,?,?,?)", m["id"], m["revision"], operator(tx), now(), m["change_note"], string(raw), shaText(string(raw)))
	return err
}
func requirementRevision(q queryer, id string, n int) (map[string]any, error) {
	rows, err := q.Query("SELECT * FROM requirement_revisions WHERE requirement_id=? AND revision=?", id, n)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fail("not_found", "需求版本不存在", 404)
	}
	m := items[0]
	snapshot, err := frozenObject(text(m["snapshot"]), text(m["hash"]))
	if err != nil {
		return nil, err
	}
	m["snapshot"] = snapshot
	return m, nil
}
func (s *Store) GetRequirementRevision(id string, n int) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	if _, err := getIncludingTrash(s.DB, "requirements", id, true); err != nil {
		return nil, err
	}
	return requirementRevision(s.DB, id, n)
}
func (s *Store) RequirementRevisions(id string, filters map[string]string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	if _, err := getIncludingTrash(s.DB, "requirements", id, true); err != nil {
		return nil, err
	}
	var total int
	if err := s.DB.QueryRow("SELECT count(*) FROM requirement_revisions WHERE requirement_id=?", id).Scan(&total); err != nil {
		return nil, err
	}
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	rows, err := s.DB.Query("SELECT * FROM requirement_revisions WHERE requirement_id=? ORDER BY revision DESC LIMIT ? OFFSET ?", id, pageLimit(filters, 100), offset)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	for _, m := range items {
		snapshot, e := frozenObject(text(m["snapshot"]), text(m["hash"]))
		if e != nil {
			return nil, e
		}
		m["snapshot"] = snapshot
	}
	return map[string]any{"items": items, "total": total, "next_cursor": ""}, nil
}
func (s *Store) CompareRequirementRevisions(id string, from, to int) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	a, err := requirementRevision(s.DB, id, from)
	if err != nil {
		return nil, err
	}
	b, err := requirementRevision(s.DB, id, to)
	if err != nil {
		return nil, err
	}
	x, y := a["snapshot"].(map[string]any), b["snapshot"].(map[string]any)
	fields := []map[string]any{}
	for _, key := range requirementFields {
		if !jsonEqual(x[key], y[key]) {
			fields = append(fields, map[string]any{"field": key, "before": x[key], "after": y[key]})
		}
	}
	ids := func(v any) []string {
		out := []string{}
		if list, ok := v.([]any); ok {
			for _, x := range list {
				out = append(out, text(x))
			}
		}
		return out
	}
	old, newer := ids(x["task_ids"]), ids(y["task_ids"])
	oldSet, newSet := map[string]bool{}, map[string]bool{}
	for _, id := range old {
		oldSet[id] = true
	}
	for _, id := range newer {
		newSet[id] = true
	}
	added, removed := []string{}, []string{}
	for _, id := range newer {
		if !oldSet[id] {
			added = append(added, id)
		}
	}
	for _, id := range old {
		if !newSet[id] {
			removed = append(removed, id)
		}
	}
	return map[string]any{"requirement_id": id, "from": a, "to": b, "fields": fields, "task_links": map[string]any{"added": added, "removed": removed}, "changed": len(fields) > 0 || len(added) > 0 || len(removed) > 0}, nil
}

func shaText(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:]) }
func immutablePlanError() error {
	return fail("read_only", "计划版本只读，请保存新的计划版本", 409)
}
func attachRequirementLinks(q queryer, m map[string]any) error {
	ids, err := requirementTaskIDs(q, text(m["id"]))
	if err != nil {
		return err
	}
	tasks, err := requirementLinkedTasks(q, text(m["id"]))
	if err != nil {
		return err
	}
	m["task_ids"] = ids
	m["linked_tasks"] = tasks
	return nil
}

func planningProjectWritable(q queryer, id string) error {
	if id == "" {
		return invalid("必须选择项目")
	}
	if err := projectWritable(q, id, ""); err != nil {
		return err
	}
	var status string
	if err := q.QueryRow("SELECT status FROM projects WHERE id=? AND deleted_at=''", id).Scan(&status); err != nil {
		return err
	}
	if status == "closed" {
		return fail("read_only", "已关闭项目的需求和计划版本只读", 409)
	}
	return nil
}
