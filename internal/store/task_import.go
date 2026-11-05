package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type importRow struct {
	key, parent string
	position    int
	task        map[string]any
}
type importPlan struct {
	project                        map[string]any
	rows                           []*importRow
	warnings                       []string
	sourceHash, rowsHash, importID string
}

func importFingerprint(input map[string]any) (string, error) {
	b, err := json.Marshal(input["rows"])
	if err != nil {
		return "", invalid("导入数据格式无效")
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}
func prepareTaskImport(tx *sql.Tx, projectID string, input map[string]any) (*importPlan, error) {
	project, err := get(tx, "projects", projectID)
	if err != nil {
		return nil, err
	}
	if err = projectWritable(tx, projectID, "tasks"); err != nil {
		return nil, err
	}
	if err = checkVersion(project, input["project_version"]); err != nil {
		return nil, err
	}
	hash := strings.ToLower(text(input["source_hash"]))
	decoded, err := hex.DecodeString(hash)
	if err != nil || len(decoded) != 32 {
		return nil, invalid("source_hash 必须是文件内容的 SHA256")
	}
	fingerprint, err := importFingerprint(input)
	if err != nil {
		return nil, err
	}
	importID := text(input["import_id"])
	if importID == "" {
		importID = uid()
	}
	if len(importID) > 128 {
		return nil, invalid("import_id 过长")
	}
	var raw []any
	switch rows := input["rows"].(type) {
	case []any:
		raw = rows
	case []map[string]any:
		for _, row := range rows {
			raw = append(raw, row)
		}
	default:
		return nil, invalid("rows 必须是任务行数组")
	}
	if len(raw) == 0 || len(raw) > 5000 {
		return nil, invalid("每次导入必须包含 1 至 5000 行任务")
	}
	plan := &importPlan{project: project, sourceHash: hash, rowsHash: fingerprint, importID: importID, warnings: []string{}}
	byKey := map[string]*importRow{}
	children := map[string][]*importRow{}
	for i, value := range raw {
		row, ok := value.(map[string]any)
		if !ok {
			return nil, invalid(fmt.Sprintf("第 %d 行不是任务对象", i+1))
		}
		key, ok := row["key"].(string)
		if !ok || strings.TrimSpace(key) == "" || len([]rune(key)) > 100 {
			return nil, invalid(fmt.Sprintf("第 %d 行需要有效唯一 key", i+1))
		}
		key = strings.TrimSpace(key)
		if _, exists := byKey[key]; exists {
			return nil, invalid("导入key重复: " + key)
		}
		parent := ""
		if v, exists := row["parent_key"]; exists && v != nil {
			var valid bool
			parent, valid = v.(string)
			if !valid {
				return nil, invalid("parent_key 必须是文本")
			}
			parent = strings.TrimSpace(parent)
		}
		allowed := map[string]bool{"key": true, "parent_key": true, "name": true, "assignee": true, "plan_start": true, "plan_end": true, "estimate_hours": true, "priority": true, "progress": true, "status": true, "description": true, "row": true}
		task := defaults("tasks")
		task["project_id"] = projectID
		for k, v := range row {
			if !allowed[k] {
				return nil, invalid("导入包含不支持的列: " + k)
			}
			if k == "key" || k == "parent_key" || k == "row" {
				continue
			}
			task[k] = v
		}
		name, isString := task["name"].(string)
		if !isString {
			return nil, invalid(fmt.Sprintf("第 %d 行任务名称须为文本", i+1))
		}
		task["name"] = strings.TrimSpace(name)
		node := &importRow{key: key, parent: parent, position: i, task: task}
		plan.rows = append(plan.rows, node)
		byKey[key] = node
		children[parent] = append(children[parent], node)
	}
	for _, row := range plan.rows {
		if row.parent != "" {
			if _, ok := byKey[row.parent]; !ok {
				return nil, invalid("父key不存在: " + row.parent)
			}
			if row.parent == row.key {
				return nil, invalid("任务不能以自身为父key")
			}
		}
		if len(children[row.key]) > 0 {
			for _, field := range []string{"progress", "estimate_hours", "status"} {
				if (field == "status" && text(row.task[field]) != "todo") || (field != "status" && num(row.task[field]) != 0) {
					plan.warnings = append(plan.warnings, fmt.Sprintf("%s：父任务 %s 已忽略，保存后按叶子任务汇总", row.key, field))
				}
			}
			row.task["progress"] = 0
			row.task["estimate_hours"] = 0
			row.task["status"] = "todo"
		}
		if err = validate(tx, "tasks", row.task, nil); err != nil {
			return nil, invalid(fmt.Sprintf("第 %d 行 (%s): %s", row.position+1, row.key, err.Error()))
		}
	}
	// Stable Kahn traversal validates cycles and ensures parents are inserted first.
	ready := append([]*importRow{}, children[""]...)
	ordered := []*importRow{}
	for pos := 0; pos < len(ready); pos++ {
		row := ready[pos]
		ordered = append(ordered, row)
		ready = append(ready, children[row.key]...)
	}
	if len(ordered) != len(plan.rows) {
		return nil, invalid("导入层级存在环")
	}
	plan.rows = ordered
	return plan, nil
}
func importResult(tx *sql.Tx, projectID string, input map[string]any) (map[string]any, bool, error) {
	hash := strings.ToLower(text(input["source_hash"]))
	if hash == "" {
		return nil, false, nil
	}
	var id, rowsHash, idsJSON string
	err := tx.QueryRow("SELECT id,rows_hash,task_ids FROM task_imports WHERE project_id=? AND source_hash=?", projectID, hash).Scan(&id, &rowsHash, &idsJSON)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	fingerprint, err := importFingerprint(input)
	if err != nil {
		return nil, false, err
	}
	if fingerprint != rowsHash {
		return nil, false, fail("import_source_conflict", "同一文件哈希已导入，但本次行内容不同；请核对源文件与映射", 409)
	}
	var ids []string
	if json.Unmarshal([]byte(idsJSON), &ids) != nil {
		return nil, false, fmt.Errorf("导入历史损坏")
	}
	items := []map[string]any{}
	for _, taskID := range ids {
		m, e := getIncludingTrash(tx, "tasks", taskID, true)
		if e != nil {
			return nil, false, e
		}
		items = append(items, m)
	}
	return map[string]any{"items": items, "total": len(items), "import_id": id, "source_hash": hash, "already_imported": true, "warnings": []string{}}, true, nil
}
func previewImport(plan *importPlan) map[string]any {
	rows := []map[string]any{}
	for _, row := range plan.rows {
		m := copyMap(row.task)
		for _, key := range []string{"id", "created_at", "updated_at", "version", "deleted_at", "sort_order", "wbs_code", "parent_id"} {
			delete(m, key)
		}
		m["key"] = row.key
		m["parent_key"] = row.parent
		rows = append(rows, m)
	}
	return map[string]any{"items": rows, "rows": rows, "total": len(rows), "project_id": plan.project["id"], "project_version": plan.project["version"], "source_hash": plan.sourceHash, "import_id": plan.importID, "warnings": plan.warnings, "already_imported": false}
}
func (s *Store) ImportTasksPreview(projectID string, input map[string]any) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if result, exists, e := importResult(tx, projectID, input); e != nil || exists {
		return result, e
	}
	plan, err := prepareTaskImport(tx, projectID, input)
	if err != nil {
		return nil, err
	}
	return previewImport(plan), nil
}
func (s *Store) ImportTasks(projectID string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		if result, exists, e := importResult(tx, projectID, input); e != nil || exists {
			return result, e
		}
		plan, err := prepareTaskImport(tx, projectID, input)
		if err != nil {
			return nil, err
		}
		byKey := map[string]*importRow{}
		for _, row := range plan.rows {
			byKey[row.key] = row
		}
		var maxOrder int
		if err = tx.QueryRow("SELECT COALESCE(max(sort_order),0) FROM tasks WHERE project_id=? AND parent_id IS NULL AND deleted_at=''", projectID).Scan(&maxOrder); err != nil {
			return nil, err
		}
		orders := map[string]int{"": maxOrder}
		ids := []string{}
		for _, row := range plan.rows {
			if row.parent != "" {
				row.task["parent_id"] = byKey[row.parent].task["id"]
			}
			orders[row.parent]++
			row.task["sort_order"] = orders[row.parent]
			if err = insert(tx, "tasks", row.task); err != nil {
				return nil, err
			}
			ids = append(ids, text(row.task["id"]))
		}
		if err = recalculate(tx, projectID, ""); err != nil {
			return nil, err
		}
		items := []map[string]any{}
		for _, row := range plan.rows {
			m, e := get(tx, "tasks", text(row.task["id"]))
			if e != nil {
				return nil, e
			}
			if err = searchSync(tx, "tasks", m); err != nil {
				return nil, err
			}
			if err = audit(tx, "tasks", text(m["id"]), "import", nil, m); err != nil {
				return nil, err
			}
			items = append(items, m)
		}
		sort.SliceStable(items, func(i, j int) bool { return num(items[i]["sort_order"]) < num(items[j]["sort_order"]) })
		idsBytes, _ := json.Marshal(ids)
		if _, err = tx.Exec("INSERT INTO task_imports(id,project_id,source_hash,rows_hash,task_ids,created_at)VALUES(?,?,?,?,?,?)", plan.importID, projectID, plan.sourceHash, plan.rowsHash, string(idsBytes), now()); err != nil {
			return nil, dbErr(err)
		}
		if _, err = tx.Exec("UPDATE projects SET version=version+1,updated_at=? WHERE id=? AND version=?", now(), projectID, plan.project["version"]); err != nil {
			return nil, err
		}
		after, err := get(tx, "projects", projectID)
		if err != nil {
			return nil, err
		}
		if err = audit(tx, "projects", projectID, "import_tasks", plan.project, map[string]any{"project": after, "import_id": plan.importID, "source_hash": plan.sourceHash, "task_ids": ids}); err != nil {
			return nil, err
		}
		return map[string]any{"items": items, "total": len(items), "import_id": plan.importID, "source_hash": plan.sourceHash, "project_version": after["version"], "warnings": plan.warnings, "already_imported": false}, nil
	})
}
