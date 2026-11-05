package store

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"
)

func migrateV2(tx *sql.Tx) error {
	for _, kind := range []string{"projects", "tasks", "records", "issues", "worklogs"} {
		if _, err := tx.Exec("ALTER TABLE " + kind + " ADD COLUMN deleted_at TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
		if _, err := tx.Exec("CREATE INDEX " + kind + "_active ON " + kind + "(deleted_at)"); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("DROP INDEX worklogs_period; CREATE UNIQUE INDEX worklogs_period ON worklogs(COALESCE(project_id,''),period_type,log_date) WHERE deleted_at=''; PRAGMA user_version=2;"); err != nil {
		return err
	}
	defaults := map[string]string{"dataset_id": uid(), "gjb_level": "2", "setup_completed": "0", "backup_keep_count": "30", "backup_max_mb": "0", "backup_auto": "1"}
	if operator(tx) != "本人" {
		defaults["setup_completed"] = "1"
	}
	for k, v := range defaults {
		if _, err := tx.Exec("INSERT OR IGNORE INTO settings(key,value)VALUES(?,?)", k, v); err != nil {
			return err
		}
	}
	return nil
}
func settingValue(key string, v any) (string, error) {
	switch key {
	case "operator":
		str, ok := v.(string)
		if !ok || strings.TrimSpace(str) == "" || len([]rune(str)) > 80 {
			return "", invalid("操作者必须是 1 至 80 个字符")
		}
		return strings.TrimSpace(str), nil
	case "setup_completed", "backup_auto":
		if !oneOf(v, "0", "1") {
			return "", invalid("设置必须为 0 或 1")
		}
		return text(v), nil
	case "backup_keep_count", "backup_max_mb":
		n, err := strconv.ParseInt(text(v), 10, 64)
		if err != nil {
			return "", invalid("备份设置必须为整数")
		}
		if key == "backup_keep_count" && (n < 2 || n > 365) {
			return "", invalid("备份保留数量须为 2 至 365")
		}
		if key == "backup_max_mb" && n != 0 && (n < 100 || n > 1000000) {
			return "", invalid("备份容量须为 100 至 1000000 MB，0 为不限")
		}
		return strconv.FormatInt(n, 10), nil
	default:
		return "", invalid("不可修改或未知设置: " + key)
	}
}
func (s *Store) write(fn func(*sql.Tx) (map[string]any, error)) (map[string]any, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out, err := fn(tx)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, dbErr(err)
	}
	return out, nil
}
func checkVersion(m map[string]any, v any) error {
	if versionOf(v) == 0 || versionOf(m["version"]) != versionOf(v) {
		return fail("version_conflict", "内容已被修改，请刷新后重试", 409)
	}
	return nil
}
func copyMap(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}
func mapVersions(v any) (map[string]int, error) {
	out := map[string]int{}
	if v == nil {
		return out, nil
	}
	switch a := v.(type) {
	case map[string]any:
		for id, n := range a {
			if versionOf(n) == 0 {
				return nil, invalid("版本快照无效")
			}
			out[id] = versionOf(n)
		}
	case map[string]int:
		for id, n := range a {
			out[id] = n
		}
	default:
		return nil, invalid("expected_versions 必须是 ID 与版本的对象")
	}
	return out, nil
}
func subtree(tx *sql.Tx, id string) ([]map[string]any, error) {
	rows, err := tx.Query("WITH RECURSIVE tree(id,depth) AS (SELECT id,0 FROM tasks WHERE id=? AND deleted_at='' UNION ALL SELECT t.id,tree.depth+1 FROM tasks t JOIN tree ON t.parent_id=tree.id WHERE t.deleted_at='') SELECT t.*,tree.depth AS tree_depth FROM tasks t JOIN tree ON t.id=tree.id ORDER BY tree.depth,t.sort_order,t.id", id)
	if err != nil {
		return nil, err
	}
	all, err := rawRows(rows)
	for _, m := range all {
		normalize("tasks", m)
		delete(m, "tree_depth")
	}
	return all, err
}
func (s *Store) SoftDelete(kind, id string, input map[string]any) (map[string]any, error) {
	if err := kindOK(kind); err != nil {
		return nil, err
	}
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		m, err := get(tx, kind, id)
		if err != nil {
			return nil, err
		}
		if err = checkVersion(m, input["version"]); err != nil {
			return nil, err
		}
		reminderParents := map[string]map[string]any{}
		if kind == "reminders" {
			reminderParents, err = reminderRecords(tx, text(m["record_id"]))
			if err != nil {
				return nil, err
			}
		}
		pid := text(m["project_id"])
		if kind == "projects" {
			pid = id
		}
		if err = projectWritable(tx, pid, ""); err != nil {
			return nil, err
		}
		items := []map[string]any{m}
		if kind == "projects" {
			for _, child := range []string{"tasks", "records", "issues", "worklogs", "time_entries", "reminders"} {
				var n int
				if err = tx.QueryRow("SELECT count(*) FROM "+child+" WHERE project_id=?", id).Scan(&n); err != nil {
					return nil, err
				}
				if n > 0 {
					return nil, fail("project_not_empty", "项目仍有关联内容，请保留项目或先处理关联内容", 409)
				}
			}
		}
		if kind == "tasks" {
			items, err = subtree(tx, id)
			if err != nil {
				return nil, err
			}
			if len(items) > 1 {
				if cascade, ok := input["cascade"].(bool); !ok || !cascade {
					return nil, fail("cascade_required", "删除含子任务的任务须显式确认完整子树并携带各项版本", 409)
				}
				versions, e := mapVersions(input["expected_versions"])
				if e != nil {
					return nil, e
				}
				for _, item := range items {
					if text(item["id"]) == id {
						continue
					}
					if versions[text(item["id"])] != versionOf(item["version"]) {
						return nil, fail("version_conflict", "任务子树已变化，请刷新完整子树后重试", 409)
					}
				}
			}
		}
		for _, item := range items {
			if kind == "tasks" {
				var entries int
				if err = tx.QueryRow("SELECT count(*) FROM time_entries WHERE task_id=? AND deleted_at=''", item["id"]).Scan(&entries); err != nil {
					return nil, err
				}
				if entries > 0 {
					return nil, fail("has_time_entries", "任务存在有效工时，请先处理关联工时", 409)
				}
			}
		}
		for _, item := range items {
			if kind == "tasks" && text(item["status"]) == "done" || kind == "issues" && oneOf(item["status"], "closed", "rejected") {
				return nil, fail("read_only", "已结束内容不可回收", 409)
			}
		}
		at := now()
		if kind == "records" {
			if err = trashRecordReminder(tx, id, at); err != nil {
				return nil, err
			}
		}
		deleted := []map[string]any{}
		for _, item := range items {
			entityID := text(item["id"])
			if _, err = tx.Exec("UPDATE "+kind+" SET deleted_at=?,updated_at=?,version=version+1 WHERE id=? AND deleted_at=''", at, at, entityID); err != nil {
				return nil, err
			}
			if _, err = tx.Exec("DELETE FROM search_doc WHERE entity_type=? AND entity_id=?", kind, entityID); err != nil {
				return nil, err
			}
			after, e := getIncludingTrash(tx, kind, entityID, true)
			if e != nil {
				return nil, e
			}
			if err = audit(tx, kind, entityID, "trash", item, after); err != nil {
				return nil, err
			}
			deleted = append(deleted, after)
		}
		if kind == "tasks" {
			if err = recalculate(tx, pid, ""); err != nil {
				return nil, err
			}
		}
		if err = touchReminderRecords(tx, reminderParents); err != nil {
			return nil, err
		}
		return map[string]any{"deleted": true, "items": deleted, "total": len(deleted)}, nil
	})
}
func (s *Store) ListTrash(filters map[string]string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	kind := filters["kind"]
	if kind != "" {
		if err := kindOK(kind); err != nil {
			return nil, err
		}
	}
	all := []map[string]any{}
	for _, k := range []string{"projects", "tasks", "records", "issues", "worklogs", "time_entries", "reminders"} {
		if kind != "" && kind != k {
			continue
		}
		rows, err := s.DB.Query("SELECT * FROM " + k + " WHERE deleted_at!=''")
		if err != nil {
			return nil, err
		}
		items, err := rawRows(rows)
		if err != nil {
			return nil, err
		}
		for _, m := range items {
			normalize(k, m)
			m["entity_type"] = k
			all = append(all, m)
		}
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := text(all[i]["deleted_at"]), text(all[j]["deleted_at"])
		if a == b {
			return text(all[i]["id"]) > text(all[j]["id"])
		}
		return a > b
	})
	total := len(all)
	limit := pageLimit(filters, 100)
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return map[string]any{"items": all[offset:end], "total": total}, nil
}
func (s *Store) RestoreTrash(kind, id string, input map[string]any) (map[string]any, error) {
	if err := kindOK(kind); err != nil {
		return nil, err
	}
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		before, err := getIncludingTrash(tx, kind, id, true)
		if err != nil {
			return nil, err
		}
		if text(before["deleted_at"]) == "" {
			return nil, fail("conflict", "内容不在回收站", 409)
		}
		if err = checkVersion(before, input["version"]); err != nil {
			return nil, err
		}
		if kind == "projects" {
			if text(before["status"]) == "archived" {
				return nil, fail("read_only", "归档项目不可恢复修改", 409)
			}
		} else {
			if err = projectWritable(tx, text(before["project_id"]), ""); err != nil {
				return nil, err
			}
		}
		if kind == "tasks" && nonempty(before["parent_id"]) {
			parent, e := get(tx, "tasks", text(before["parent_id"]))
			if e != nil {
				return nil, fail("restore_parent_required", "请先恢复父任务", 409)
			}
			if text(parent["project_id"]) != text(before["project_id"]) {
				return nil, invalid("父子任务必须属于同一项目")
			}
		}
		if kind == "time_entries" {
			if err = validateTimeEntry(tx, before); err != nil {
				return nil, err
			}
		}
		reminderParents := map[string]map[string]any{}
		if kind == "reminders" {
			if err = validateReminder(tx, before, nil); err != nil {
				return nil, err
			}
		}
		if kind == "reminders" {
			reminderParents, err = reminderRecords(tx, text(before["record_id"]))
			if err != nil {
				return nil, err
			}
		}
		if kind == "records" {
			if err = restoreRecordReminder(tx, id, text(before["deleted_at"])); err != nil {
				return nil, err
			}
		}
		if kind == "tasks" {
			var order int
			if err = tx.QueryRow("SELECT COALESCE(max(sort_order),0)+1 FROM tasks WHERE project_id=? AND COALESCE(parent_id,'')=? AND deleted_at=''", before["project_id"], text(before["parent_id"])).Scan(&order); err != nil {
				return nil, err
			}
			if _, err = tx.Exec("UPDATE tasks SET sort_order=? WHERE id=?", order, id); err != nil {
				return nil, err
			}
		}
		if _, err = tx.Exec("UPDATE "+kind+" SET deleted_at='',updated_at=?,version=version+1 WHERE id=?", now(), id); err != nil {
			return nil, dbErr(err)
		}
		if kind == "tasks" {
			if err = recalculate(tx, text(before["project_id"]), id); err != nil {
				return nil, err
			}
		}
		after, err := get(tx, kind, id)
		if err != nil {
			return nil, err
		}
		if err = searchSync(tx, kind, after); err != nil {
			return nil, err
		}
		if err = audit(tx, kind, id, "restore", before, after); err != nil {
			return nil, err
		}
		if err = touchReminderRecords(tx, reminderParents); err != nil {
			return nil, err
		}
		return after, nil
	})
}
func checkExpected(tx *sql.Tx, kind string, versions map[string]int) error {
	for id, version := range versions {
		m, err := get(tx, kind, id)
		if err != nil {
			return fail("version_conflict", "版本快照中的内容不存在或已回收", 409)
		}
		if err = checkVersion(m, version); err != nil {
			return err
		}
	}
	return nil
}
func activeTasks(tx *sql.Tx, pid string) ([]map[string]any, error) {
	rows, err := tx.Query("SELECT * FROM tasks WHERE project_id=? AND deleted_at='' ORDER BY sort_order,id", pid)
	if err != nil {
		return nil, err
	}
	all, err := rawRows(rows)
	for _, m := range all {
		normalize("tasks", m)
	}
	return all, err
}
func (s *Store) MoveTask(id string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		root, err := get(tx, "tasks", id)
		if err != nil {
			return nil, err
		}
		if err = checkVersion(root, input["version"]); err != nil {
			return nil, err
		}
		pid := text(root["project_id"])
		if err = projectWritable(tx, pid, ""); err != nil {
			return nil, err
		}
		if text(root["status"]) == "done" {
			return nil, fail("read_only", "已结束任务不可移动", 409)
		}
		versions, err := mapVersions(input["expected_versions"])
		if err != nil {
			return nil, err
		}
		if err = checkExpected(tx, "tasks", versions); err != nil {
			return nil, err
		}
		parent, exists := input["parent_id"]
		if !exists {
			return nil, invalid("移动任务必须明确 parent_id，空值表示根任务")
		}
		if parent != nil {
			if _, ok := parent.(string); !ok {
				return nil, invalid("parent_id 必须是文本")
			}
		}
		targetParent := text(parent)
		candidate := copyMap(root)
		candidate["parent_id"] = targetParent
		if err = validate(tx, "tasks", candidate, root); err != nil {
			return nil, err
		}
		for _, target := range []string{targetParent, text(input["before_id"])} {
			if target != "" && target != id {
				if _, ok := versions[target]; !ok {
					return nil, fail("version_conflict", "移动须携带目标父任务和位置任务的版本快照", 409)
				}
			}
		}
		all, err := activeTasks(tx, pid)
		if err != nil {
			return nil, err
		}
		beforeByID := map[string]map[string]any{}
		groups := map[string][]string{}
		for _, m := range all {
			taskID := text(m["id"])
			beforeByID[taskID] = m
			if taskID != id {
				groups[text(m["parent_id"])] = append(groups[text(m["parent_id"])], taskID)
			}
		}
		beforeID := text(input["before_id"])
		position := len(groups[targetParent])
		if beforeID != "" {
			found := false
			for i, other := range groups[targetParent] {
				if other == beforeID {
					position = i
					found = true
					break
				}
			}
			if !found {
				return nil, invalid("before_id 必须是目标层级的其他任务")
			}
		}
		siblings := groups[targetParent]
		siblings = append(siblings, "")
		copy(siblings[position+1:], siblings[position:])
		siblings[position] = id
		groups[targetParent] = siblings
		for par, ids := range groups {
			for i, taskID := range ids {
				if _, err = tx.Exec("UPDATE tasks SET parent_id=?,sort_order=? WHERE id=?", dbValue("parent_id", par), i+1, taskID); err != nil {
					return nil, err
				}
			}
		}
		if err = recalculate(tx, pid, ""); err != nil {
			return nil, err
		}
		for taskID, before := range beforeByID {
			after, e := get(tx, "tasks", taskID)
			if e != nil {
				return nil, e
			}
			direct := text(before["parent_id"]) != text(after["parent_id"]) || num(before["sort_order"]) != num(after["sort_order"])
			if !direct {
				continue
			}
			if versionOf(before["version"]) == versionOf(after["version"]) {
				if _, err = tx.Exec("UPDATE tasks SET updated_at=?,version=version+1 WHERE id=?", now(), taskID); err != nil {
					return nil, err
				}
				after, e = get(tx, "tasks", taskID)
				if e != nil {
					return nil, e
				}
			}
			if err = audit(tx, "tasks", taskID, "move", before, after); err != nil {
				return nil, err
			}
		}
		return get(tx, "tasks", id)
	})
}
func (s *Store) ReopenTask(id string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		before, err := get(tx, "tasks", id)
		if err != nil {
			return nil, err
		}
		if err = checkVersion(before, input["version"]); err != nil {
			return nil, err
		}
		reason, reasonOK := input["reason"].(string)
		if !reasonOK || strings.TrimSpace(reason) == "" || len([]rune(reason)) > 2000 {
			return nil, invalid("重开任务必须填写原因")
		}
		if text(before["status"]) != "done" {
			return nil, invalid("只有已完成叶子任务可以重开")
		}
		var count int
		if err = tx.QueryRow("SELECT count(*) FROM tasks WHERE parent_id=? AND deleted_at=''", id).Scan(&count); err != nil {
			return nil, err
		}
		if count > 0 {
			return nil, invalid("汇总任务不能手动重开，请重开叶子任务")
		}
		m := copyMap(before)
		m["status"] = "doing"
		m["progress"] = 0
		if v, ok := input["progress"]; ok {
			m["progress"] = v
		}
		m["actual_end"] = ""
		oldForValidation := copyMap(before)
		oldForValidation["status"] = "doing"
		if err = validate(tx, "tasks", m, oldForValidation); err != nil {
			return nil, err
		}
		if _, err = tx.Exec("UPDATE tasks SET status='doing',progress=?,actual_start=?,actual_end='',updated_at=?,version=version+1 WHERE id=?", m["progress"], m["actual_start"], now(), id); err != nil {
			return nil, err
		}
		if err = recalculate(tx, text(m["project_id"]), id); err != nil {
			return nil, err
		}
		after, err := get(tx, "tasks", id)
		if err != nil {
			return nil, err
		}
		if err = searchSync(tx, "tasks", after); err != nil {
			return nil, err
		}
		auditAfter := copyMap(after)
		auditAfter["reopen_reason"] = strings.TrimSpace(text(input["reason"]))
		if err = audit(tx, "tasks", id, "reopen", before, auditAfter); err != nil {
			return nil, err
		}
		return after, nil
	})
}
func batchItems(input map[string]any) ([]map[string]any, error) {
	raw, ok := input["items"].([]any)
	if !ok {
		if maps, yes := input["items"].([]map[string]any); yes {
			for _, m := range maps {
				raw = append(raw, m)
			}
			ok = true
		}
	}
	if !ok || len(raw) == 0 || len(raw) > 100 {
		return nil, invalid("批量操作须为 1 至 100 项")
	}
	seen := map[string]bool{}
	items := []map[string]any{}
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok || !nonempty(m["id"]) || versionOf(m["version"]) == 0 {
			return nil, invalid("每项必须包含 ID 与版本")
		}
		id := text(m["id"])
		if seen[id] {
			return nil, invalid("批量项目不能重复")
		}
		seen[id] = true
		items = append(items, m)
	}
	return items, nil
}
func (s *Store) BatchTasks(input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		items, err := batchItems(input)
		if err != nil {
			return nil, err
		}
		changes, ok := input["changes"].(map[string]any)
		if !ok || len(changes) == 0 {
			return nil, invalid("请提供批量修改字段")
		}
		for k := range changes {
			if !oneOf(k, "status", "assignee", "plan_start", "plan_end") {
				return nil, invalid("不允许批量修改字段: " + k)
			}
		}
		for _, item := range items {
			m, e := get(tx, "tasks", text(item["id"]))
			if e != nil {
				return nil, e
			}
			if e = checkVersion(m, item["version"]); e != nil {
				return nil, e
			}
		}
		for _, item := range items {
			in := copyMap(changes)
			current, e := get(tx, "tasks", text(item["id"]))
			if e != nil {
				return nil, e
			}
			in["version"] = current["version"]
			if _, e = update(tx, "tasks", text(item["id"]), in); e != nil {
				return nil, e
			}
		}
		out := []map[string]any{}
		for _, item := range items {
			m, e := get(tx, "tasks", text(item["id"]))
			if e != nil {
				return nil, e
			}
			out = append(out, m)
		}
		return map[string]any{"items": out, "total": len(out)}, nil
	})
}
func (s *Store) ClassifyRecords(input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		items, err := batchItems(input)
		if err != nil {
			return nil, err
		}
		pid, vexists := input["project_id"]
		if !vexists {
			return nil, invalid("请明确目标项目，空值表示未归类")
		}
		if pid != nil {
			if _, ok := pid.(string); !ok {
				return nil, invalid("project_id 必须是文本")
			}
		}
		if err = projectWritable(tx, text(pid), ""); err != nil {
			return nil, err
		}
		out := []map[string]any{}
		for _, item := range items {
			m, e := update(tx, "records", text(item["id"]), map[string]any{"version": item["version"], "project_id": text(pid)})
			if e != nil {
				return nil, e
			}
			out = append(out, m)
		}
		return map[string]any{"items": out, "total": len(out)}, nil
	})
}
