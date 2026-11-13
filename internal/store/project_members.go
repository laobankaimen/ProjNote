package store

import (
	"database/sql"
	"strconv"
	"strings"
)

// Project membership uses the same stable identities as LAN sessions. A name
// never implicitly creates a task grant, including when two members share it.
func migrateProjectMembersV10(tx *sql.Tx) error {
	_, err := tx.Exec(`ALTER TABLE tasks ADD COLUMN assignee_member_id TEXT REFERENCES lan_members(id);
CREATE INDEX tasks_assignee_member ON tasks(assignee_member_id,project_id);
UPDATE tasks SET assignee_member_id=(SELECT member_id FROM lan_member_tasks WHERE task_id=tasks.id) WHERE EXISTS(SELECT 1 FROM lan_member_tasks WHERE task_id=tasks.id);`)
	return err
}

func (s *Store) ProjectMembers(projectID string, filters map[string]string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	if _, err := get(s.DB, "projects", projectID); err != nil {
		return nil, err
	}
	where := " WHERE p.project_id=?"
	args := []any{projectID}
	if q := filters["q"]; q != "" {
		if len([]rune(q)) > 200 {
			return nil, invalid("查询内容过长")
		}
		where += " AND m.name LIKE ? ESCAPE '\\'"
		args = append(args, like(q))
	}
	var total int
	if err := s.DB.QueryRow("SELECT count(*) FROM lan_member_projects p JOIN lan_members m ON m.id=p.member_id"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	args = append(args, pageLimit(filters, 100), offset)
	rows, err := s.DB.Query("SELECT m.id FROM lan_member_projects p JOIN lan_members m ON m.id=p.member_id"+where+" ORDER BY m.name,m.id LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	ids, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	items := []map[string]any{}
	for _, value := range ids {
		m, err := lanMember(s.DB, text(value["id"]), false)
		if err != nil {
			return nil, err
		}
		items = append(items, m)
	}
	return map[string]any{"items": items, "total": total, "next_cursor": ""}, nil
}

func (s *Store) AddProjectMember(projectID string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		if err := planningProjectWritable(tx, projectID); err != nil {
			return nil, err
		}
		for key := range input {
			if !oneOf(key, "name", "member_id", "version") {
				return nil, invalid("未知项目成员字段: " + key)
			}
		}
		if nonempty(input["member_id"]) {
			if _, exists := input["name"]; exists {
				return nil, invalid("加入已有成员时不接受姓名")
			}
			before, err := lanMember(tx, text(input["member_id"]), false)
			if err != nil {
				return nil, err
			}
			if err = checkVersion(before, input["version"]); err != nil {
				return nil, err
			}
			if before["enabled"] != true {
				return nil, invalid("请先启用此成员")
			}
			var exists int
			if err = tx.QueryRow("SELECT count(*) FROM lan_member_projects WHERE member_id=? AND project_id=?", before["id"], projectID).Scan(&exists); err != nil {
				return nil, err
			}
			if exists != 0 {
				return nil, fail("conflict", "此成员已在项目名单中", 409)
			}
			if _, err = tx.Exec("INSERT INTO lan_member_projects(member_id,project_id)VALUES(?,?)", before["id"], projectID); err != nil {
				return nil, err
			}
			if err = touchLANMemberScope(tx, text(before["id"]), before, "project_join"); err != nil {
				return nil, err
			}
			return lanMember(tx, text(before["id"]), false)
		}
		if _, exists := input["version"]; exists {
			return nil, invalid("新成员不接受版本")
		}
		name, err := inputText(input, "name", 100, false)
		if err != nil {
			return nil, err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, invalid("成员姓名不能为空")
		}
		id, at := uid(), now()
		if _, err = tx.Exec("INSERT INTO lan_members(id,name,enabled,created_at,updated_at)VALUES(?,?,1,?,?)", id, name, at, at); err != nil {
			return nil, err
		}
		if _, err = tx.Exec("INSERT INTO lan_member_projects(member_id,project_id)VALUES(?,?)", id, projectID); err != nil {
			return nil, err
		}
		after, err := lanMember(tx, id, false)
		if err != nil {
			return nil, err
		}
		return after, audit(tx, "lan_members", id, "create", nil, after)
	})
}

func (s *Store) UpdateProjectMember(projectID, memberID string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		if err := planningProjectWritable(tx, projectID); err != nil {
			return nil, err
		}
		for key := range input {
			if !oneOf(key, "version", "name", "remove") {
				return nil, invalid("未知项目成员字段: " + key)
			}
		}
		before, err := lanMember(tx, memberID, false)
		if err != nil {
			return nil, err
		}
		if err = checkVersion(before, input["version"]); err != nil {
			return nil, err
		}
		var exists int
		if err = tx.QueryRow("SELECT count(*) FROM lan_member_projects WHERE member_id=? AND project_id=?", memberID, projectID).Scan(&exists); err != nil {
			return nil, err
		}
		if exists == 0 {
			return nil, fail("not_found", "此成员不在项目名单中", 404)
		}
		if value, hasRemove := input["remove"]; hasRemove {
			if value != true || len(input) != 2 {
				return nil, invalid("移出项目须明确提交remove:true和版本")
			}
			var count int
			if err = tx.QueryRow("SELECT count(*) FROM lan_member_tasks a JOIN tasks t ON t.id=a.task_id WHERE a.member_id=? AND t.project_id=? AND t.deleted_at='' AND t.status IN('todo','doing')", memberID, projectID).Scan(&count); err != nil {
				return nil, err
			}
			if count > 0 {
				return nil, fail("assignment_conflict", "成员仍有未完成任务，请先改派或清空负责人", 409)
			}
			if _, err = tx.Exec("DELETE FROM lan_member_tasks WHERE member_id=? AND task_id IN(SELECT id FROM tasks WHERE project_id=?)", memberID, projectID); err != nil {
				return nil, err
			}
			if _, err = tx.Exec("DELETE FROM lan_member_projects WHERE member_id=? AND project_id=?", memberID, projectID); err != nil {
				return nil, err
			}
			if err = touchLANMemberScope(tx, memberID, before, "project_leave"); err != nil {
				return nil, err
			}
			return lanMember(tx, memberID, false)
		}
		name, err := inputText(input, "name", 100, false)
		if err != nil {
			return nil, err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, invalid("成员姓名不能为空")
		}
		m := copyMap(before)
		m["name"] = name
		if _, err = tx.Exec("UPDATE lan_members SET name=?,auth_version=auth_version+1,version=version+1,updated_at=? WHERE id=?", name, now(), memberID); err != nil {
			return nil, err
		}
		if err = lanApplyScope(tx, m, before, map[string]any{"name": name}); err != nil {
			return nil, err
		}
		if err = lanRevoke(tx, memberID); err != nil {
			return nil, err
		}
		after, err := lanMember(tx, memberID, false)
		if err != nil {
			return nil, err
		}
		return after, audit(tx, "lan_members", memberID, "update", before, after)
	})
}

func touchLANMemberScope(tx *sql.Tx, memberID string, before map[string]any, action string) error {
	if _, err := tx.Exec("UPDATE lan_members SET auth_version=auth_version+1,version=version+1,updated_at=? WHERE id=?", now(), memberID); err != nil {
		return err
	}
	if err := lanRevoke(tx, memberID); err != nil {
		return err
	}
	after, err := lanMember(tx, memberID, false)
	if err != nil {
		return err
	}
	return audit(tx, "lan_members", memberID, action, before, after)
}

func prepareTaskAssignee(tx *sql.Tx, m, before, input map[string]any) error {
	value, explicit := input["assignee_member_id"]
	if !explicit {
		if name, changed := input["assignee"]; changed && before != nil && text(name) != text(before["assignee"]) {
			if oneOf(before["status"], "done", "canceled") {
				return fail("read_only", "已结束任务不能重新指派负责人", 409)
			}
			m["assignee_member_id"] = ""
		}
		return nil
	}
	if value != nil {
		if _, ok := value.(string); !ok {
			return invalid("assignee_member_id必须是成员ID文本")
		}
	}
	id := text(value)
	if before != nil && oneOf(before["status"], "done", "canceled") && id != text(before["assignee_member_id"]) {
		return fail("read_only", "已结束任务不能重新指派负责人", 409)
	}
	m["assignee_member_id"] = id
	if id == "" {
		m["assignee"] = ""
		return nil
	}
	retained := before != nil && id == text(before["assignee_member_id"])
	if !retained {
		if oneOf(m["status"], "done", "canceled") {
			return fail("read_only", "已结束任务不能新指派负责人", 409)
		}
		if err := planningProjectWritable(tx, text(m["project_id"])); err != nil {
			return err
		}
		var children int
		if err := tx.QueryRow("SELECT count(*) FROM tasks WHERE parent_id=? AND deleted_at=''", m["id"]).Scan(&children); err != nil {
			return err
		}
		if children > 0 {
			return invalid("只能向成员分配叶子任务")
		}
	}
	var member map[string]any
	var err error
	if retained {
		// Keeping an existing person is not a new grant. Disabled members and
		// completed history remain readable while the owner edits other fields.
		member, err = lanMember(tx, id, false)
	} else {
		member, err = lanScope(tx, id, text(m["project_id"]), false)
	}
	if err != nil {
		return invalid("负责人必须是此项目名单中的有效成员")
	}
	if !retained || !oneOf(m["status"], "done", "canceled") {
		m["assignee"] = member["name"]
	}
	return nil
}

func syncTaskAssignee(tx *sql.Tx, m, before, input map[string]any) error {
	_, explicit := input["assignee_member_id"]
	_, textChanged := input["assignee"]
	if !explicit && !(textChanged && before != nil && text(m["assignee"]) != text(before["assignee"])) {
		return nil
	}
	var oldID string
	err := tx.QueryRow("SELECT member_id FROM lan_member_tasks WHERE task_id=?", m["id"]).Scan(&oldID)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	newID := text(m["assignee_member_id"])
	if oldID == newID {
		return nil
	}
	if before != nil && oldID == "" && newID == text(before["assignee_member_id"]) {
		return nil // A retained historical owner never recreates a withdrawn grant.
	}
	beforeMembers := map[string]map[string]any{}
	for _, id := range []string{oldID, newID} {
		if id != "" {
			beforeMembers[id], err = lanMember(tx, id, false)
			if err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec("DELETE FROM lan_member_tasks WHERE task_id=?", m["id"]); err != nil {
		return err
	}
	if newID != "" {
		if _, err = tx.Exec("INSERT INTO lan_member_tasks(task_id,member_id)VALUES(?,?)", m["id"], newID); err != nil {
			return err
		}
	}
	for _, id := range []string{oldID, newID} {
		if id != "" {
			if err = touchLANMemberScope(tx, id, beforeMembers[id], "task_scope"); err != nil {
				return err
			}
		}
	}
	return nil
}
