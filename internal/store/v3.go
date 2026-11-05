package store

import (
	"database/sql"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"
)

func migrateV3(tx *sql.Tx) error {
	_, err := tx.Exec(`CREATE TABLE time_entries(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,task_id TEXT REFERENCES tasks(id) ON DELETE RESTRICT,work_date TEXT NOT NULL,minutes INTEGER NOT NULL CHECK(minutes BETWEEN 1 AND 1440 AND typeof(minutes)='integer'),note TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,deleted_at TEXT NOT NULL DEFAULT '');
 CREATE INDEX time_entries_date ON time_entries(deleted_at,work_date,project_id,task_id);
 CREATE TABLE reminders(id TEXT PRIMARY KEY,title TEXT NOT NULL,project_id TEXT REFERENCES projects(id) ON DELETE RESTRICT,record_id TEXT REFERENCES records(id) ON DELETE RESTRICT,due_at TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,done_at TEXT NOT NULL DEFAULT '',deleted_at TEXT NOT NULL DEFAULT '');
 CREATE INDEX reminders_due ON reminders(deleted_at,done_at,due_at);
 CREATE UNIQUE INDEX reminders_record_active ON reminders(record_id) WHERE record_id IS NOT NULL AND deleted_at='';
 CREATE INDEX tasks_parent_active ON tasks(parent_id,deleted_at);
 PRAGMA user_version=3;`)
	return err
}
func timeProjectWritable(tx *sql.Tx, id string) error {
	if !nonempty(id) {
		return invalid("工时必须选择项目")
	}
	if err := projectWritable(tx, id, ""); err != nil {
		return err
	}
	var status string
	if err := tx.QueryRow("SELECT status FROM projects WHERE id=? AND deleted_at=''", id).Scan(&status); err != nil {
		return err
	}
	if status == "closed" {
		return fail("read_only", "已关闭项目不能登记、修改或恢复工时", 409)
	}
	return nil
}
func validateTimeEntry(tx *sql.Tx, m map[string]any) error {
	if err := timeProjectWritable(tx, text(m["project_id"])); err != nil {
		return err
	}
	assignee, ok := m["assignee"].(string)
	if !ok || strings.TrimSpace(assignee) == "" || len([]rune(strings.TrimSpace(assignee))) > 100 {
		return invalid("工时登记人必须是 1 至 100 个字符")
	}
	m["assignee"] = strings.TrimSpace(assignee)
	if !nonempty(m["work_date"]) || !validDate(m["work_date"]) {
		return invalid("工时日期格式应为 YYYY-MM-DD")
	}
	minutes, ok := numeric(m["minutes"])
	if !ok || minutes < 1 || minutes > 1440 || minutes != math.Trunc(minutes) {
		return invalid("工时必须是 1 至 1440 的整数分钟")
	}
	if id := text(m["task_id"]); id != "" {
		task, err := get(tx, "tasks", id)
		if err != nil {
			return invalid("关联任务不存在或已回收")
		}
		if text(task["project_id"]) != text(m["project_id"]) {
			return invalid("工时任务必须与项目一致")
		}
		var count int
		if err = tx.QueryRow("SELECT count(*) FROM tasks WHERE parent_id=? AND deleted_at=''", id).Scan(&count); err != nil {
			return err
		}
		if count > 0 {
			return invalid("工时只能登记到叶子任务")
		}
	}
	var total int
	if err := tx.QueryRow("SELECT COALESCE(sum(minutes),0) FROM time_entries WHERE work_date=? AND assignee=? AND deleted_at='' AND id!=?", m["work_date"], m["assignee"], m["id"]).Scan(&total); err != nil {
		return err
	}
	if total+int(minutes) > 1440 {
		return fail("daily_time_limit", "该登记人当日所有项目工时合计不能超过 1440 分钟", 409)
	}
	return nil
}
func (s *Store) CreateTimeEntry(input map[string]any) (map[string]any, error) {
	return s.Create("time_entries", input)
}
func (s *Store) UpdateTimeEntry(id string, input map[string]any) (map[string]any, error) {
	return s.Update("time_entries", id, input)
}
func (s *Store) DeleteTimeEntry(id string, input map[string]any) (map[string]any, error) {
	return s.SoftDelete("time_entries", id, input)
}
func (s *Store) ListTimeEntries(filters map[string]string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	where, args, err := timeWhere(filters)
	if err != nil {
		return nil, err
	}
	var total int
	if err = s.DB.QueryRow("SELECT count(*) FROM time_entries e"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	limit := pageLimit(filters, 100)
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	rows, err := s.DB.Query("SELECT e.*,p.name AS project_name,COALESCE(t.name,'') AS task_name FROM time_entries e JOIN projects p ON p.id=e.project_id LEFT JOIN tasks t ON t.id=e.task_id"+where+" ORDER BY e.work_date DESC,e.created_at DESC,e.id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	for _, m := range items {
		normalize("time_entries", m)
	}
	return map[string]any{"items": items, "total": total, "next_cursor": ""}, err
}
func timeWhere(filters map[string]string) (string, []any, error) {
	filters = canonicalFilters(filters)
	where := " WHERE e.deleted_at=''"
	args := []any{}
	for _, key := range []string{"project_id", "task_id", "work_date", "assignee"} {
		if value, ok := filters[key]; ok {
			if key == "work_date" && !validDate(value) {
				return "", nil, invalid("工时日期无效")
			}
			if value == "" && key == "task_id" {
				where += " AND e.task_id IS NULL"
			} else {
				where += " AND e." + key + "=?"
				args = append(args, value)
			}
		}
	}
	for _, key := range []string{"date_from", "date_to"} {
		if value := filters[key]; value != "" {
			if !validDate(value) {
				return "", nil, invalid("工时日期格式应为 YYYY-MM-DD")
			}
			op := ">="
			if key == "date_to" {
				op = "<="
			}
			where += " AND e.work_date" + op + "?"
			args = append(args, value)
		}
	}
	if filters["date_from"] != "" && filters["date_to"] != "" && filters["date_from"] > filters["date_to"] {
		return "", nil, invalid("开始日期不能晚于结束日期")
	}
	if query := strings.TrimSpace(filters["q"]); query != "" {
		if len([]rune(query)) > 200 {
			return "", nil, invalid("搜索内容不能超过 200 字")
		}
		where += " AND e.note LIKE ? ESCAPE '\\'"
		args = append(args, like(query))
	}
	return where, args, nil
}
func (s *Store) TimeSummary(filters map[string]string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return timeSummary(s.DB, filters)
}
func timeSummary(q queryer, filters map[string]string) (map[string]any, error) {
	where, args, err := timeWhere(filters)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	var total int
	if err = q.QueryRow("SELECT COALESCE(sum(e.minutes),0) FROM time_entries e"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	out["total_minutes"] = total
	for _, group := range []struct{ key, query string }{{"by_day", "SELECT e.work_date AS date,sum(e.minutes) AS minutes FROM time_entries e" + where + " GROUP BY e.work_date ORDER BY e.work_date"}, {"by_project", "SELECT e.project_id,p.name AS project_name,sum(e.minutes) AS minutes FROM time_entries e JOIN projects p ON p.id=e.project_id" + where + " GROUP BY e.project_id ORDER BY minutes DESC,e.project_id"}, {"by_assignee", "SELECT e.assignee,sum(e.minutes) AS minutes FROM time_entries e" + where + " GROUP BY e.assignee ORDER BY minutes DESC,e.assignee"}, {"by_person", "SELECT e.assignee,sum(e.minutes) AS minutes FROM time_entries e" + where + " GROUP BY e.assignee ORDER BY minutes DESC,e.assignee"}, {"by_person_day", "SELECT e.assignee,e.work_date AS date,sum(e.minutes) AS minutes FROM time_entries e" + where + " GROUP BY e.assignee,e.work_date ORDER BY e.work_date,e.assignee"}, {"by_task", "SELECT COALESCE(e.task_id,'') AS task_id,e.project_id,COALESCE(t.name,'未关联任务') AS task_name,sum(e.minutes) AS minutes FROM time_entries e LEFT JOIN tasks t ON t.id=e.task_id" + where + " GROUP BY e.project_id,COALESCE(e.task_id,'') ORDER BY minutes DESC,e.project_id,task_id"}} {
		rows, e := q.Query(group.query, args...)
		if e != nil {
			return nil, e
		}
		items, e := rawRows(rows)
		if e != nil {
			return nil, e
		}
		if group.key == "by_person_day" || group.key == "by_day" {
			for _, m := range items {
				m["over_eight_hours"] = num(m["minutes"]) > 480
			}
		}
		out[group.key] = items
	}
	return out, nil
}

func validateReminder(tx *sql.Tx, m, before map[string]any) error {
	if !nonempty(m["title"]) {
		return invalid("提醒标题不能为空")
	}
	at, err := time.Parse(time.RFC3339Nano, text(m["due_at"]))
	if err != nil {
		return invalid("提醒时间必须包含时区，格式为 RFC3339")
	}
	m["due_at"] = at.UTC().Format(time.RFC3339Nano)
	if err = projectWritable(tx, text(m["project_id"]), ""); err != nil {
		return err
	}
	if id := text(m["project_id"]); id != "" {
		var status string
		if err = tx.QueryRow("SELECT status FROM projects WHERE id=? AND deleted_at=''", id).Scan(&status); err != nil {
			return err
		}
		if status == "closed" {
			return fail("read_only", "已关闭项目不能新增、修改或恢复提醒，可完成现有提醒收尾", 409)
		}
	}
	if rid := text(m["record_id"]); rid != "" {
		r, e := get(tx, "records", rid)
		if e != nil {
			return invalid("关联记录不存在或已回收")
		}
		if text(r["project_id"]) != text(m["project_id"]) {
			return invalid("提醒项目必须与关联记录一致")
		}
	}
	if before != nil && nonempty(before["done_at"]) {
		return fail("read_only", "已完成提醒不可修改，请保留历史并创建新提醒", 409)
	}
	return nil
}
func (s *Store) CreateReminder(input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		records, err := reminderRecords(tx, text(input["record_id"]))
		if err != nil {
			return nil, err
		}
		m, err := create(tx, "reminders", input)
		if err != nil {
			return nil, err
		}
		if err = touchReminderRecords(tx, records); err != nil {
			return nil, err
		}
		return m, nil
	})
}
func (s *Store) UpdateReminder(id string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		before, err := get(tx, "reminders", id)
		if err != nil {
			return nil, err
		}
		if err = validateReminder(tx, before, nil); err != nil {
			return nil, err
		}
		records, err := reminderRecords(tx, text(before["record_id"]), text(input["record_id"]))
		if err != nil {
			return nil, err
		}
		m, err := update(tx, "reminders", id, input)
		if err != nil {
			return nil, err
		}
		if err = touchReminderRecords(tx, records); err != nil {
			return nil, err
		}
		return m, nil
	})
}
func (s *Store) CompleteReminder(id string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		before, err := get(tx, "reminders", id)
		if err != nil {
			return nil, err
		}
		records, err := reminderRecords(tx, text(before["record_id"]))
		if err != nil {
			return nil, err
		}
		if err = checkVersion(before, input["version"]); err != nil {
			return nil, err
		}
		if err = projectWritable(tx, text(before["project_id"]), ""); err != nil {
			return nil, err
		}
		if nonempty(before["done_at"]) {
			return nil, fail("conflict", "提醒已经完成", 409)
		}
		if _, err = tx.Exec("UPDATE reminders SET done_at=?,updated_at=?,version=version+1 WHERE id=?", now(), now(), id); err != nil {
			return nil, err
		}
		after, err := get(tx, "reminders", id)
		if err != nil {
			return nil, err
		}
		if err = searchSync(tx, "reminders", after); err != nil {
			return nil, err
		}
		if err = audit(tx, "reminders", id, "complete", before, after); err != nil {
			return nil, err
		}
		if err = touchReminderRecords(tx, records); err != nil {
			return nil, err
		}
		return after, nil
	})
}
func (s *Store) ListReminders(filters map[string]string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return reminderList(s.DB, filters)
}
func reminderList(q queryer, filters map[string]string) (map[string]any, error) {
	filters = canonicalFilters(filters)
	for _, key := range []string{"date_from", "date_to"} {
		if value := filters[key]; value != "" {
			if !validDate(value) {
				return nil, invalid("提醒筛选日期无效")
			}
			local, _ := time.ParseInLocation("2006-01-02", value, time.Local)
			target := "due_from"
			if key == "date_to" {
				local = local.AddDate(0, 0, 1)
				target = "due_to"
			}
			if filters[target] == "" {
				filters[target] = local.UTC().Format(time.RFC3339Nano)
			}
		}
	}
	if filters["date_from"] != "" && filters["date_to"] != "" && filters["date_from"] > filters["date_to"] {
		return nil, invalid("开始日期不能晚于结束日期")
	}
	where := " WHERE deleted_at=''"
	if filters["actionable"] == "1" {
		where += " AND (project_id IS NULL OR project_id IN(SELECT id FROM projects WHERE deleted_at='' AND status IN('active','paused')))"
	}
	args := []any{}
	if pid, ok := filters["project_id"]; ok {
		if pid == "" {
			where += " AND project_id IS NULL"
		} else {
			where += " AND project_id=?"
			args = append(args, pid)
		}
	}
	status := filters["status"]
	if status == "done" {
		where += " AND done_at!=''"
	} else if status == "pending" || status == "" {
		where += " AND done_at=''"
	} else if status != "all" {
		return nil, invalid("提醒状态必须是 pending/done/all")
	}
	for _, key := range []string{"due_from", "due_to"} {
		if value := filters[key]; value != "" {
			at, err := time.Parse(time.RFC3339Nano, value)
			if err != nil {
				return nil, invalid("提醒筛选时间须为 RFC3339")
			}
			op := ">="
			if key == "due_to" {
				op = "<"
			}
			where += " AND julianday(due_at)" + op + "julianday(?)"
			args = append(args, at.UTC().Format(time.RFC3339Nano))
		}
	}
	if rid := filters["record_id"]; rid != "" {
		where += " AND record_id=?"
		args = append(args, rid)
	}
	var total int
	if err := q.QueryRow("SELECT count(*) FROM reminders"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	limit := pageLimit(filters, 100)
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	rows, err := q.Query("SELECT * FROM reminders"+where+" ORDER BY julianday(due_at),id LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	for _, m := range items {
		normalize("reminders", m)
	}
	return map[string]any{"items": items, "total": total, "next_cursor": ""}, err
}

// parseReminder deliberately recognizes only documented standalone tokens.
func parseReminder(content string, clock time.Time) (string, string) {
	words := strings.Fields(content)
	for i, word := range words {
		if !strings.HasPrefix(word, "!") {
			continue
		}
		token := strings.TrimRight(strings.TrimPrefix(word, "!"), "，。；,;！")
		local := clock.In(time.Local)
		date := time.Date(local.Year(), local.Month(), local.Day(), 9, 0, 0, 0, time.Local)
		recognized := true
		switch token {
		case "今天":
		case "明天":
			date = date.AddDate(0, 0, 1)
		default:
			if strings.HasPrefix(token, "周") && len([]rune(token)) == 2 {
				weekday := map[string]time.Weekday{"周一": time.Monday, "周二": time.Tuesday, "周三": time.Wednesday, "周四": time.Thursday, "周五": time.Friday, "周六": time.Saturday, "周日": time.Sunday, "周天": time.Sunday}
				day, ok := weekday[token]
				if !ok {
					recognized = false
				} else {
					delta := (int(day) - int(local.Weekday()) + 7) % 7
					date = date.AddDate(0, 0, delta)
				}
			} else {
				parsed, e := time.ParseInLocation("2006-01-02", token, time.Local)
				if e != nil {
					recognized = false
				} else {
					date = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 9, 0, 0, 0, time.Local)
				}
			}
		}
		if !recognized {
			continue
		}
		explicitTime := false
		if i+1 < len(words) && strings.Contains(words[i+1], ":") {
			tm, e := time.Parse("15:04", strings.TrimRight(words[i+1], "，。；,;"))
			if e != nil {
				return "", "提醒时间无效，已保留原文，未创建提醒"
			}
			date = time.Date(date.Year(), date.Month(), date.Day(), tm.Hour(), tm.Minute(), 0, 0, time.Local)
			explicitTime = true
		}
		hint := "已按本地时区创建一次提醒"
		if !explicitTime {
			hint = "已按本地时区默认 09:00 创建一次提醒"
		}
		if token == "今天" && !date.After(local) {
			date = local.Add(time.Minute)
			hint = "今天的提醒时间已过，已调整为当前时间后 1 分钟"
		} else if strings.HasPrefix(token, "周") && !date.After(local) {
			date = date.AddDate(0, 0, 7)
			hint = "已安排到下一个对应星期的提醒时间"
		}
		return date.UTC().Format(time.RFC3339Nano), hint
	}
	return "", "未识别提醒指令，已保留原文"
}
func syncRecordReminder(tx *sql.Tx, record, input map[string]any, creating bool) (string, error) {
	explicit, provided := input["reminder_due_at"]
	due := ""
	hint := ""
	if provided {
		hint = "已使用指定时间保存一次提醒"
		if !nonempty(explicit) {
			hint = "已清除关联提醒"
		}
		if explicit != nil {
			if _, ok := explicit.(string); !ok {
				return "", invalid("提醒时间必须是 RFC3339 文本或空值")
			}
		}
		due = text(explicit)
		if due != "" {
			at, err := time.Parse(time.RFC3339Nano, due)
			if err != nil {
				return "", invalid("提醒时间必须包含时区，格式为 RFC3339")
			}
			due = at.UTC().Format(time.RFC3339Nano)
		}
	} else if creating && strings.Contains(text(record["content"]), "!") {
		due, hint = parseReminder(text(record["content"]), time.Now())
	} else if !creating && strings.Contains(text(input["content"]), "!") {
		hint = "正文已保存；编辑不会重复创建提醒，可使用提醒时间字段调整"
	}
	rows, err := tx.Query("SELECT * FROM reminders WHERE record_id=? AND deleted_at=''", record["id"])
	if err != nil {
		return hint, err
	}
	items, err := rawRows(rows)
	if err != nil {
		return hint, err
	}
	var existing map[string]any
	if len(items) > 0 {
		existing = normalize("reminders", items[0])
	}
	if existing != nil {
		if !provided {
			if text(existing["project_id"]) != text(record["project_id"]) {
				before := copyMap(existing)
				existing["project_id"] = record["project_id"]
				if _, err = tx.Exec("UPDATE reminders SET project_id=?,updated_at=?,version=version+1 WHERE id=?", dbValue("project_id", record["project_id"]), now(), existing["id"]); err != nil {
					return hint, err
				}
				after, e := get(tx, "reminders", text(existing["id"]))
				if e != nil {
					return hint, e
				}
				if err = searchSync(tx, "reminders", after); err != nil {
					return hint, err
				}
				if err = audit(tx, "reminders", text(existing["id"]), "classify", before, after); err != nil {
					return hint, err
				}
			}
			return hint, nil
		}
		if due == "" {
			return hint, trashRecordReminder(tx, text(record["id"]), now())
		}
		if _, err = update(tx, "reminders", text(existing["id"]), map[string]any{"version": existing["version"], "due_at": due, "project_id": record["project_id"]}); err != nil {
			return hint, err
		}
		return hint, nil
	}
	if due == "" {
		return hint, nil
	}
	title := text(record["title"])
	if title == "" {
		title = text(record["content"])
		runes := []rune(title)
		if len(runes) > 100 {
			title = string(runes[:100])
		}
	}
	_, err = create(tx, "reminders", map[string]any{"title": title, "project_id": record["project_id"], "record_id": record["id"], "due_at": due})
	return hint, err
}
func trashRecordReminder(tx *sql.Tx, recordID, at string) error {
	rows, err := tx.Query("SELECT * FROM reminders WHERE record_id=? AND deleted_at=''", recordID)
	if err != nil {
		return err
	}
	items, err := rawRows(rows)
	if err != nil {
		return err
	}
	for _, before := range items {
		normalize("reminders", before)
		if _, err = tx.Exec("UPDATE reminders SET deleted_at=?,updated_at=?,version=version+1 WHERE id=?", at, at, before["id"]); err != nil {
			return err
		}
		if _, err = tx.Exec("DELETE FROM search_doc WHERE entity_type='reminders' AND entity_id=?", before["id"]); err != nil {
			return err
		}
		after, e := getIncludingTrash(tx, "reminders", text(before["id"]), true)
		if e != nil {
			return e
		}
		if err = audit(tx, "reminders", text(before["id"]), "trash_with_record", before, after); err != nil {
			return err
		}
	}
	return nil
}
func restoreRecordReminder(tx *sql.Tx, recordID, deletedAt string) error {
	rows, err := tx.Query("SELECT * FROM reminders WHERE record_id=? AND deleted_at=?", recordID, deletedAt)
	if err != nil {
		return err
	}
	items, err := rawRows(rows)
	if err != nil {
		return err
	}
	for _, before := range items {
		normalize("reminders", before)
		if _, err = tx.Exec("UPDATE reminders SET deleted_at='',updated_at=?,version=version+1 WHERE id=?", now(), before["id"]); err != nil {
			return dbErr(err)
		}
		after, e := get(tx, "reminders", text(before["id"]))
		if e != nil {
			return e
		}
		if err = searchSync(tx, "reminders", after); err != nil {
			return err
		}
		if err = audit(tx, "reminders", text(before["id"]), "restore_with_record", before, after); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) EntityHistory(kind, id string, filters map[string]string) (map[string]any, error) {
	if err := kindOK(kind); err != nil {
		return nil, err
	}
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	if _, err := getIncludingTrash(s.DB, kind, id, true); err != nil {
		return nil, err
	}
	var total int
	if err := s.DB.QueryRow("SELECT count(*) FROM audit WHERE entity_type=? AND entity_id=?", kind, id).Scan(&total); err != nil {
		return nil, err
	}
	args := []any{kind, id}
	where := " WHERE entity_type=? AND entity_id=?"
	scope := filterScope("history:"+kind+":"+id, filters)
	if raw := filters["cursor"]; raw != "" {
		c, e := decodeCursor(raw, scope)
		if e != nil || c.Number < 1 {
			return nil, invalid("历史游标无效")
		}
		where += " AND id<?"
		args = append(args, c.Number)
	}
	limit := pageLimit(filters, 30)
	offset := 0
	if filters["cursor"] == "" {
		offset, _ = strconv.Atoi(filters["offset"])
		if offset < 0 {
			offset = 0
		}
	}
	args = append(args, limit+1, offset)
	rows, err := s.DB.Query("SELECT id,created_at,operator,entity_type,entity_id,action,before_json,after_json,prev_hash,hash FROM audit"+where+" ORDER BY id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = encodeCursor(pageCursor{Scope: scope, Number: int64(num(last["id"]))})
	}
	for _, m := range items {
		for _, pair := range [][2]string{{"before_json", "before"}, {"after_json", "after"}} {
			var v any
			if json.Unmarshal([]byte(text(m[pair[0]])), &v) == nil {
				m[pair[1]] = v
			}
		}
	}
	return map[string]any{"items": items, "total": total, "next_cursor": next}, nil
}
func (s *Store) TaskTree(projectID string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	if _, err := get(s.DB, "projects", projectID); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query("SELECT * FROM tasks WHERE project_id=? AND deleted_at='' ORDER BY sort_order,id", projectID)
	if err != nil {
		return nil, err
	}
	all, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	byID := map[string]map[string]any{}
	children := map[string][]map[string]any{}
	for _, m := range all {
		normalize("tasks", m)
		byID[text(m["id"])] = m
		children[text(m["parent_id"])] = append(children[text(m["parent_id"])], m)
	}
	items := []map[string]any{}
	type queued struct {
		m     map[string]any
		depth int
	}
	stack := []queued{}
	roots := children[""]
	for i := len(roots) - 1; i >= 0; i-- {
		stack = append(stack, queued{roots[i], 0})
	}
	seen := map[string]bool{}
	for len(stack) > 0 {
		last := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		id := text(last.m["id"])
		if seen[id] {
			return nil, invalid("任务树存在环")
		}
		seen[id] = true
		last.m["depth"] = last.depth
		last.m["has_children"] = len(children[id]) > 0
		last.m["order"] = len(items) + 1
		items = append(items, last.m)
		for i := len(children[id]) - 1; i >= 0; i-- {
			stack = append(stack, queued{children[id][i], last.depth + 1})
		}
	}
	if len(items) != len(byID) {
		return nil, invalid("任务树存在孤立父节点或环")
	}
	if err = attachTaskActuals(s.DB, items, true); err != nil {
		return nil, err
	}
	return map[string]any{"items": items, "total": len(items)}, nil
}

// A record version also represents its linked reminder snapshot. Standalone
// reminder operations advance it so a stale record editor cannot overwrite them.
func reminderRecords(tx *sql.Tx, ids ...string) (map[string]map[string]any, error) {
	records := map[string]map[string]any{}
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, seen := records[id]; seen {
			continue
		}
		r, err := get(tx, "records", id)
		if err != nil {
			return nil, err
		}
		records[id] = r
	}
	return records, nil
}
func touchReminderRecords(tx *sql.Tx, records map[string]map[string]any) error {
	for id, before := range records {
		if _, err := tx.Exec("UPDATE records SET updated_at=?,version=version+1 WHERE id=? AND deleted_at=''", now(), id); err != nil {
			return err
		}
		after, err := get(tx, "records", id)
		if err != nil {
			return err
		}
		if err = audit(tx, "records", id, "reminder_change", before, after); err != nil {
			return err
		}
	}
	return nil
}
