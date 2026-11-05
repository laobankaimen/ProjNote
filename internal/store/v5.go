package store

import (
	"database/sql"
	"math"
	"strconv"
	"strings"
	"time"
)

func migrateV5(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE desktop_drafts(key TEXT PRIMARY KEY CHECK(key='quick_note'),content TEXT NOT NULL DEFAULT '',project_id TEXT NOT NULL DEFAULT '',version INTEGER NOT NULL DEFAULT 1,updated_at TEXT NOT NULL);
 CREATE TABLE export_jobs(id TEXT PRIMARY KEY,state TEXT NOT NULL CHECK(state IN('queued','running','succeeded','failed')),format TEXT NOT NULL CHECK(format IN('docx','md','json')),source_kind TEXT NOT NULL,source_id TEXT NOT NULL,source_version INTEGER NOT NULL CHECK(source_version>=1),template_id TEXT NOT NULL DEFAULT '',template_hash TEXT NOT NULL DEFAULT '',source_snapshot TEXT NOT NULL CHECK(json_valid(source_snapshot)),output_name TEXT NOT NULL DEFAULT '',output_sha256 TEXT NOT NULL DEFAULT '',size INTEGER NOT NULL DEFAULT 0 CHECK(size>=0),error TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1);
 CREATE INDEX export_jobs_created ON export_jobs(created_at,id);
 CREATE INDEX export_jobs_state ON export_jobs(state);
 PRAGMA user_version=5;`)
	return err
}
func inputText(input map[string]any, key string, maximum int, optional bool) (string, error) {
	v, exists := input[key]
	if !exists || v == nil {
		if optional {
			return "", nil
		}
		return "", invalid("缺少文本字段: " + key)
	}
	str, ok := v.(string)
	if !ok {
		return "", invalid("字段必须是文本: " + key)
	}
	if maximum > 0 && len([]rune(str)) > maximum {
		return "", invalid("字段过长: " + key)
	}
	return str, nil
}
func bodyPage(input map[string]any) (int, int, error) {
	limit, offset := 100, 0
	for _, key := range []string{"limit", "offset"} {
		value, exists := input[key]
		if !exists {
			continue
		}
		n, ok := numeric(value)
		if str, yes := value.(string); yes {
			parsed, err := strconv.ParseFloat(str, 64)
			if err == nil {
				n, ok = parsed, true
			}
		}
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) || n < 0 || n > float64(1<<31) {
			return 0, 0, invalid("分页参数必须是非负整数")
		}
		if key == "limit" {
			if n < 1 {
				return 0, 0, invalid("limit 必须大于 0")
			}
			limit = int(n)
			if limit > 500 {
				limit = 500
			}
		} else {
			offset = int(n)
		}
	}
	return limit, offset, nil
}

// NextPlan only suggests sources. It never alters a task, note, or saved log.
func (s *Store) NextPlan(input map[string]any) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	date, err := inputText(input, "date", 10, true)
	if err != nil {
		return nil, err
	}
	if date == "" {
		date = today()
	}
	if !validDate(date) {
		return nil, invalid("候选日期须为真实 YYYY-MM-DD 日期")
	}
	local, _ := time.ParseInLocation("2006-01-02", date, time.Local)
	target, err := inputText(input, "target_date", 10, true)
	if err != nil {
		return nil, err
	}
	if target == "" {
		target = local.AddDate(0, 0, 1).Format("2006-01-02")
	}
	if !validDate(target) {
		return nil, invalid("计划日期须为真实 YYYY-MM-DD 日期")
	}
	assignee, err := inputText(input, "assignee", 100, true)
	if err != nil {
		return nil, err
	}
	assignee = strings.TrimSpace(assignee)
	if assignee == "" {
		assignee = operator(s.DB)
	}
	pid, err := inputText(input, "project_id", 200, true)
	if err != nil {
		return nil, err
	}
	if pid != "" {
		if _, err = get(s.DB, "projects", pid); err != nil {
			return nil, err
		}
	}
	query, err := inputText(input, "q", 200, true)
	if err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	limit, offset, err := bodyPage(input)
	if err != nil {
		return nil, err
	}
	taskWhere := " WHERE t.deleted_at='' AND p.deleted_at='' AND p.status IN('active','paused') AND t.status IN('todo','doing') AND t.assignee=? AND (t.plan_start='' OR t.plan_start<=?) AND NOT EXISTS(SELECT 1 FROM tasks c WHERE c.parent_id=t.id AND c.deleted_at='')"
	taskArgs := []any{assignee, target}
	recordWhere := " WHERE r.deleted_at='' AND (r.project_id IS NULL OR (p.deleted_at='' AND p.status!='archived')) AND julianday(r.occurred_at)>=julianday(?) AND julianday(r.occurred_at)<julianday(?)"
	recordArgs := []any{local.UTC().Format(time.RFC3339Nano), local.AddDate(0, 0, 1).UTC().Format(time.RFC3339Nano)}
	if pid != "" {
		taskWhere += " AND t.project_id=?"
		taskArgs = append(taskArgs, pid)
		recordWhere += " AND r.project_id=?"
		recordArgs = append(recordArgs, pid)
	}
	if query != "" {
		taskWhere += " AND (t.name LIKE ? ESCAPE '\\' OR t.description LIKE ? ESCAPE '\\' OR t.wbs_code LIKE ? ESCAPE '\\')"
		taskArgs = append(taskArgs, like(query), like(query), like(query))
		recordWhere += " AND (r.title LIKE ? ESCAPE '\\' OR r.content LIKE ? ESCAPE '\\')"
		recordArgs = append(recordArgs, like(query), like(query))
	}
	// Stable ordering leaves total independent of pagination, including >500 sources.
	union := "SELECT 'tasks:'||t.id AS key,'tasks' AS kind,t.id,t.version,t.name AS title,t.project_id,t.assignee,t.description AS content,t.plan_start,t.plan_end,'' AS occurred_at,0 AS source_order FROM tasks t JOIN projects p ON p.id=t.project_id" + taskWhere + " UNION ALL SELECT 'records:'||r.id AS key,'records' AS kind,r.id,r.version,CASE WHEN r.title='' THEN substr(r.content,1,100) ELSE r.title END AS title,COALESCE(r.project_id,'') AS project_id,'' AS assignee,r.content,'' AS plan_start,'' AS plan_end,r.occurred_at,1 AS source_order FROM records r LEFT JOIN projects p ON p.id=r.project_id" + recordWhere
	args := append(taskArgs, recordArgs...)
	var total int
	if err = s.DB.QueryRow("SELECT count(*) FROM ("+union+")", args...).Scan(&total); err != nil {
		return nil, err
	}
	args = append(args, limit, offset)
	rows, err := s.DB.Query("SELECT * FROM ("+union+") ORDER BY source_order,CASE WHEN plan_end='' THEN 1 ELSE 0 END,plan_end,occurred_at DESC,id LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	for _, m := range items {
		delete(m, "source_order")
		if text(m["kind"]) == "records" {
			m["reason"] = "当天本机现场记录，供手动选取"
			continue
		}
		start, end := text(m["plan_start"]), text(m["plan_end"])
		switch {
		case end != "" && end < target:
			m["reason"] = "计划结束时间早于目标日，逾期未完成"
		case start == target:
			m["reason"] = "计划在目标日开始"
		case start == "" || end == "":
			m["reason"] = "日期未完整安排，待排程"
		default:
			m["reason"] = "计划跨目标日，任务尚未完成"
		}
	}
	return map[string]any{"date": date, "target_date": target, "assignee": assignee, "project_id": pid, "candidates": items, "items": items, "total": total, "limit": limit, "offset": offset, "has_more": offset+len(items) < total, "note": "候选仅供手动选取，尚未写入计划或日志；记录为当日本机录入，不推断人员归属"}, nil
}
func desktopDraft(q queryer) (map[string]any, error) {
	var content, pid, updated, mode string
	var version int
	err := q.QueryRow("SELECT content,project_id,version,updated_at,mode FROM desktop_drafts WHERE key='quick_note'").Scan(&content, &pid, &version, &updated, &mode)
	if err == sql.ErrNoRows {
		return map[string]any{"key": "quick_note", "content": "", "project_id": "", "version": 1, "updated_at": "", "mode": "task", "project_available": true, "notice": "桌面便签草稿，尚未保存为任务"}, nil
	}
	if err != nil {
		return nil, err
	}
	available := true
	if pid != "" {
		var count int
		if err = q.QueryRow("SELECT count(*) FROM projects WHERE id=? AND deleted_at=''", pid).Scan(&count); err != nil {
			return nil, err
		}
		available = count == 1
	}
	notice := "桌面便签草稿，尚未保存为现场记录"
	if mode == "task" {
		notice = "桌面便签草稿，尚未保存为任务"
	}
	return map[string]any{"key": "quick_note", "content": content, "project_id": pid, "version": version, "updated_at": updated, "mode": mode, "project_available": available, "notice": notice}, nil
}
func (s *Store) DesktopDraft() (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return desktopDraft(s.DB)
}
func (s *Store) SaveDesktopDraft(input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		for key := range input {
			if !oneOf(key, "version", "content", "project_id", "mode") {
				return nil, invalid("未知便签字段: " + key)
			}
		}
		before, err := desktopDraft(tx)
		if err != nil {
			return nil, err
		}
		if err = checkVersion(before, input["version"]); err != nil {
			return nil, err
		}
		mode := text(before["mode"])
		if value, provided := input["mode"]; provided {
			var ok bool
			mode, ok = value.(string)
			if !ok || !oneOf(mode, "task", "record") {
				return nil, invalid("便签模式须为 task 或 record")
			}
		}
		content, err := inputText(input, "content", 200000, false)
		if err != nil {
			return nil, err
		}
		pid, err := inputText(input, "project_id", 200, false)
		if err != nil {
			return nil, err
		}
		if pid != "" && pid != text(before["project_id"]) {
			if _, err = get(tx, "projects", pid); err != nil {
				return nil, invalid("便签关联项目不存在或已回收，请选择有效项目；正文未被覆盖")
			}
		}
		_, err = tx.Exec("INSERT INTO desktop_drafts(key,content,project_id,version,updated_at,mode)VALUES('quick_note',?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET content=excluded.content,project_id=excluded.project_id,version=excluded.version,updated_at=excluded.updated_at,mode=excluded.mode", content, pid, versionOf(before["version"])+1, now(), mode)
		if err != nil {
			return nil, err
		}
		return desktopDraft(tx)
	})
}
