package store

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

func parseQuick(tx *sql.Tx, m map[string]any, allowProject bool) error {
	content := text(m["content"])
	words := strings.Fields(content)
	tags := []any{}
	seen := map[string]bool{}
	switch a := m["tags"].(type) {
	case []any:
		tags = a
	case []string:
		for _, t := range a {
			tags = append(tags, t)
		}
	default:
		return invalid("标签必须是数组")
	}
	for _, t := range tags {
		seen[text(t)] = true
	}
	for _, word := range words {
		if strings.HasPrefix(word, "#") && len(word) > 1 {
			tag := strings.Trim(strings.TrimPrefix(word, "#"), "，。；,;!！")
			if tag != "" && !seen[tag] {
				tags = append(tags, tag)
				seen[tag] = true
			}
		}
	}
	if allowProject && !nonempty(m["project_id"]) {
		for _, word := range words {
			if !strings.HasPrefix(word, "@") || len(word) < 2 {
				continue
			}
			name := strings.Trim(strings.TrimPrefix(word, "@"), "，。；,;!！")
			rows, err := tx.Query("SELECT id FROM projects WHERE deleted_at='' AND (name=? OR code=?)", name, name)
			if err != nil {
				return err
			}
			matches, err := rawRows(rows)
			if err != nil {
				return err
			}
			if len(matches) == 1 {
				m["project_id"] = matches[0]["id"]
				break
			}
		}
	}
	m["tags"] = tags
	return nil
}

type taskNode struct {
	m                 map[string]any
	children          []*taskNode
	order             int
	visiting          bool
	visited           bool
	effectiveWeight   float64
	effectiveProgress float64
}

func recalculate(tx *sql.Tx, projectID, changedID string) error {
	return recalculateAs(tx, projectID, changedID, "")
}
func recalculateAs(tx *sql.Tx, projectID, changedID, actor string) error {
	rows, err := tx.Query("SELECT * FROM tasks WHERE project_id=? AND deleted_at='' ORDER BY sort_order,id", projectID)
	if err != nil {
		return err
	}
	all, err := rawRows(rows)
	if err != nil {
		return err
	}
	nodes := map[string]*taskNode{}
	for _, m := range all {
		nodes[text(m["id"])] = &taskNode{m: m, order: versionOf(m["sort_order"])}
	}
	roots := []*taskNode{}
	for _, n := range nodes {
		p := text(n.m["parent_id"])
		if p == "" {
			roots = append(roots, n)
		} else if parent, ok := nodes[p]; ok {
			parent.children = append(parent.children, n)
		} else {
			return invalid("父子任务必须属于同一项目")
		}
	}
	less := func(a, b *taskNode) bool {
		if a.order == b.order {
			return text(a.m["id"]) < text(b.m["id"])
		}
		return a.order < b.order
	}
	sort.Slice(roots, func(i, j int) bool { return less(roots[i], roots[j]) })
	var walk func(*taskNode, string) error
	walk = func(n *taskNode, prefix string) error {
		if n.visiting {
			return invalid("任务层级不能形成环")
		}
		if n.visited {
			return nil
		}
		n.visiting = true
		code := fmt.Sprint(n.order)
		if prefix != "" {
			code = prefix + "." + code
		}
		sort.Slice(n.children, func(i, j int) bool { return less(n.children[i], n.children[j]) })
		for _, child := range n.children {
			if err := walk(child, code); err != nil {
				return err
			}
		}
		progress := num(n.m["progress"])
		hours := num(n.m["estimate_hours"])
		status := text(n.m["status"])
		actualStart, actualEnd := text(n.m["actual_start"]), text(n.m["actual_end"])
		n.effectiveProgress = progress
		n.effectiveWeight = hours
		if n.effectiveWeight <= 0 {
			n.effectiveWeight = 1
		}
		if status == "canceled" {
			n.effectiveWeight = 0
		}
		if len(n.children) > 0 {
			var weighted, weight, totalHours float64
			allDone, allCanceled := true, true
			anyStarted := false
			actualStart, actualEnd = "", ""
			for _, c := range n.children {
				cs := text(c.m["status"])
				ch := num(c.m["estimate_hours"])
				if cs != "canceled" {
					w := c.effectiveWeight
					weighted += c.effectiveProgress * w
					weight += w
					totalHours += ch
				}
				if cs != "done" && cs != "canceled" {
					allDone = false
				}
				if cs != "canceled" {
					allCanceled = false
				}
				if cs == "doing" || cs == "done" || num(c.m["progress"]) > 0 {
					anyStarted = true
				}
				start, end := text(c.m["actual_start"]), text(c.m["actual_end"])
				if start != "" && (actualStart == "" || start < actualStart) {
					actualStart = start
				}
				if end > actualEnd {
					actualEnd = end
				}
			}
			progress = 0
			if weight > 0 {
				n.effectiveProgress = weighted / weight
				progress = math.Round(n.effectiveProgress)
			}
			hours = totalHours
			n.effectiveWeight = weight
			switch {
			case allCanceled:
				status = "canceled"
			case allDone:
				status = "done"
				progress = 100
			case anyStarted:
				status = "doing"
				actualEnd = ""
			default:
				status = "todo"
				actualEnd = ""
			}
		}
		changed := text(n.m["wbs_code"]) != code || num(n.m["progress"]) != progress || num(n.m["estimate_hours"]) != hours || text(n.m["status"]) != status || text(n.m["actual_start"]) != actualStart || text(n.m["actual_end"]) != actualEnd
		if changed {
			var before map[string]any
			if text(n.m["id"]) != changedID {
				before, err = get(tx, "tasks", text(n.m["id"]))
				if err != nil {
					return err
				}
			}
			bump := 0
			if text(n.m["id"]) != changedID {
				bump = 1
			}
			_, err = tx.Exec("UPDATE tasks SET wbs_code=?,progress=?,estimate_hours=?,status=?,actual_start=?,actual_end=?,updated_at=?,version=version+? WHERE id=?", code, int(progress), hours, status, actualStart, actualEnd, now(), bump, n.m["id"])
			if err != nil {
				return err
			}
			n.m["wbs_code"] = code
			n.m["progress"] = int(progress)
			n.m["estimate_hours"] = hours
			n.m["status"] = status
			n.m["actual_start"] = actualStart
			n.m["actual_end"] = actualEnd
			if before != nil {
				after, err := get(tx, "tasks", text(n.m["id"]))
				if err != nil {
					return err
				}
				if err = searchSync(tx, "tasks", after); err != nil {
					return err
				}
				if err = auditAs(tx, actor, "tasks", text(n.m["id"]), "rollup", before, after); err != nil {
					return err
				}
			}
		}
		n.visiting = false
		n.visited = true
		return nil
	}
	for _, n := range roots {
		if err = walk(n, ""); err != nil {
			return err
		}
	}
	for _, n := range nodes {
		if !n.visited {
			return invalid("任务层级不能形成环")
		}
	}
	return endIneligibleTaskReminders(tx, projectID, actor)
}

func (s *Store) Settings() (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return settings(s.DB)
}
func settings(q queryer) (map[string]any, error) {
	rows, err := q.Query("SELECT key,value FROM settings ORDER BY key")
	if err != nil {
		return nil, err
	}
	all, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, r := range all {
		out[text(r["key"])] = r["value"]
	}
	return out, nil
}
func (s *Store) SetSettings(input map[string]any) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	before, err := settings(tx)
	if err != nil {
		return err
	}
	for k, v := range input {
		str, e := settingValue(k, v)
		if e != nil {
			return e
		}
		if _, err = tx.Exec("INSERT INTO settings(key,value)VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, str); err != nil {
			return err
		}
	}
	after, err := settings(tx)
	if err != nil {
		return err
	}
	if err = audit(tx, "settings", "settings", "update", before, after); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) Search(query string) ([]map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	q := strings.TrimSpace(query)
	if q == "" {
		return []map[string]any{}, nil
	}
	if utf8.RuneCountInString(q) > 200 {
		return nil, invalid("搜索内容不能超过 200 个字符")
	}
	var rows *sql.Rows
	var err error
	if utf8.RuneCountInString(q) >= 3 {
		phrase := "\"" + strings.ReplaceAll(q, "\"", "\"\"") + "\""
		rows, err = s.DB.Query("SELECT d.entity_type,d.entity_id,d.project_id,d.title,d.body FROM search_fts JOIN search_doc d ON d.id=search_fts.rowid WHERE search_fts MATCH ? ORDER BY rank LIMIT 500", phrase)
	} else {
		rows, err = s.DB.Query("SELECT entity_type,entity_id,project_id,title,body FROM search_doc WHERE title LIKE ? ESCAPE '\\' OR body LIKE ? ESCAPE '\\' ORDER BY id DESC LIMIT 500", like(q), like(q))
	}
	if err != nil {
		return nil, err
	}
	return rawRows(rows)
}
func (s *Store) Today() (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	d := today()
	start, _ := time.ParseInLocation("2006-01-02", d, time.Local)
	end := start.AddDate(0, 0, 1)
	boundaryStart, boundaryEnd := start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano)
	stats := map[string]any{}
	queries := map[string]string{"projects": "SELECT count(*) FROM projects WHERE deleted_at='' AND status IN('active','paused')", "tasks_open": "SELECT count(*) FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.deleted_at='' AND p.deleted_at='' AND t.status IN('todo','doing') AND p.status!='archived'", "tasks_done": "SELECT count(*) FROM tasks WHERE deleted_at='' AND status='done'", "issues_open": "SELECT count(*) FROM issues i JOIN projects p ON p.id=i.project_id WHERE i.deleted_at='' AND p.deleted_at='' AND i.status NOT IN('closed','rejected') AND p.status!='archived'", "records_today": "SELECT count(*) FROM records WHERE deleted_at='' AND julianday(occurred_at)>=julianday(?) AND julianday(occurred_at)<julianday(?)", "unclassified": "SELECT count(*) FROM records WHERE deleted_at='' AND project_id IS NULL"}
	for k, q := range queries {
		var count int
		var err error
		if k == "records_today" {
			err = s.DB.QueryRow(q, boundaryStart, boundaryEnd).Scan(&count)
		} else {
			err = s.DB.QueryRow(q).Scan(&count)
		}
		if err != nil {
			return nil, err
		}
		stats[k] = count
	}
	rows, err := s.DB.Query("SELECT t.*,EXISTS(SELECT 1 FROM tasks child WHERE child.parent_id=t.id AND child.deleted_at='') AS has_children FROM tasks t JOIN projects p ON p.id=t.project_id WHERE t.deleted_at='' AND p.deleted_at='' AND t.status IN('todo','doing') AND p.status!='archived' AND (t.plan_start='' OR t.plan_start<=?) ORDER BY CASE WHEN t.plan_end='' THEN 1 ELSE 0 END,t.plan_end,t.priority,t.created_at,t.id", d)
	if err != nil {
		return nil, err
	}
	tasks, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	for _, t := range tasks {
		normalize("tasks", t)
	}
	if err = attachTaskActuals(s.DB, tasks, false); err != nil {
		return nil, err
	}
	rows, err = s.DB.Query("SELECT * FROM records WHERE deleted_at='' AND julianday(occurred_at)>=julianday(?) AND julianday(occurred_at)<julianday(?) ORDER BY occurred_at DESC LIMIT 100", boundaryStart, boundaryEnd)
	if err != nil {
		return nil, err
	}
	records, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		normalize("records", r)
	}
	reminders, err := reminderList(s.DB, map[string]string{"status": "pending", "due_to": boundaryEnd, "limit": "500", "actionable": "1"})
	if err != nil {
		return nil, err
	}
	var minutes int
	if err = s.DB.QueryRow("SELECT COALESCE(sum(minutes),0) FROM time_entries WHERE deleted_at='' AND work_date=?", d).Scan(&minutes); err != nil {
		return nil, err
	}
	stats["time_minutes_today"] = minutes
	taskReminders, err := taskReminderList(s.DB, map[string]string{"status": "pending", "due_to": boundaryEnd, "limit": "100"})
	if err != nil {
		return nil, err
	}
	stats["task_reminders_due"] = taskReminders["total"]
	stats["reminders_due"] = reminders["total"]
	return map[string]any{"date": d, "tasks": tasks, "records": records, "stats": stats, "reminders": reminders["items"], "task_reminders": taskReminders["items"], "time_minutes_today": minutes}, nil
}
func (s *Store) GenerateLog(input map[string]any) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	date := text(input["date"])
	if date == "" {
		date = today()
	}
	if !validDate(date) {
		return nil, invalid("日期格式应为 YYYY-MM-DD")
	}
	period := text(input["period_type"])
	if period == "" {
		period = "day"
	}
	start, end, err := logPeriodBounds(date, period)
	if err != nil {
		return nil, err
	}
	label := map[string]string{"day": "工作日报", "week": "工作周报", "month": "工作月报"}[period]
	date = start.Format("2006-01-02")
	assignee := ""
	if value, provided := input["assignee"]; provided && value != nil {
		var valid bool
		assignee, valid = value.(string)
		if !valid {
			return nil, invalid("日志人员须为文本")
		}
		assignee = strings.TrimSpace(assignee)
		if len([]rune(assignee)) > 100 {
			return nil, invalid("日志人员不能超过 100 个字符")
		}
	}
	pid := text(input["project_id"])
	if pid != "" {
		var n int
		if err := s.DB.QueryRow("SELECT count(*) FROM projects WHERE id=? AND deleted_at=''", pid).Scan(&n); err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, invalid("关联项目不存在")
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s · %s\n\n", label, date)
	if assignee != "" {
		fmt.Fprintf(&b, "人员：%s（个人日志）\n\n", assignee)
	}
	for _, section := range []struct{ kind, title, col string }{{"tasks", "任务进展", "updated_at"}, {"records", "现场记录", "occurred_at"}, {"issues", "问题跟踪", "updated_at"}} {
		fmt.Fprintf(&b, "## %s\n\n", section.title)
		where := " WHERE deleted_at='' AND julianday(" + section.col + ")>=julianday(?) AND julianday(" + section.col + ")<julianday(?)"
		args := []any{start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano)}
		if pid != "" {
			where += " AND project_id=?"
			args = append(args, pid)
		}
		if assignee != "" {
			if section.kind == "tasks" {
				where += " AND assignee=?"
				args = append(args, assignee)
			} else if section.kind == "issues" {
				where += " AND owner=?"
				args = append(args, assignee)
			}
		}
		query := "SELECT * FROM " + section.kind + where + " ORDER BY " + section.col + ",id"
		if period != "month" {
			query += " LIMIT 500"
		}
		rows, err := s.DB.Query(query, args...)
		if err != nil {
			return nil, err
		}
		items, err := rawRows(rows)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			b.WriteString("- 暂无\n")
		}
		for _, item := range items {
			switch section.kind {
			case "tasks":
				fmt.Fprintf(&b, "- [%s] %s（%s，进度 %.0f%%）\n", text(item["wbs_code"]), text(item["name"]), text(item["status"]), num(item["progress"]))
			case "records":
				title := text(item["title"])
				if title == "" {
					title = text(item["content"])
				}
				fmt.Fprintf(&b, "- %s\n", strings.ReplaceAll(title, "\n", " "))
			case "issues":
				fmt.Fprintf(&b, "- %s（%s / %s）\n", text(item["title"]), text(item["severity"]), text(item["status"]))
			}
		}
		b.WriteString("\n")
	}
	filters := map[string]string{"date_from": date, "date_to": end.AddDate(0, 0, -1).Format("2006-01-02")}
	if pid != "" {
		filters["project_id"] = pid
	}
	if assignee != "" {
		filters["assignee"] = assignee
	}
	summary, err := timeSummary(s.DB, filters)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(&b, "## 工时记录\n\n- 合计 %d 分钟（%.2f 小时）\n", int(num(summary["total_minutes"])), num(summary["total_minutes"])/60)
	for _, day := range summary["by_day"].([]map[string]any) {
		warning := ""
		if day["over_eight_hours"] == true {
			warning = "，超过 8 小时，请核对"
		}
		fmt.Fprintf(&b, "- %s：%d 分钟%s\n", text(day["date"]), int(num(day["minutes"])), warning)
	}
	b.WriteString("\n")
	if period == "day" {
		b.WriteString("## 明日计划\n\n")
	} else {
		b.WriteString("## 后续计划\n\n")
	}
	return map[string]any{"content": b.String(), "log_date": date, "period_type": period, "project_id": pid}, nil
}
func (s *Store) VerifyAudit() (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	rows, err := s.DB.Query("SELECT id,created_at,operator,entity_type,entity_id,action,before_json,after_json,prev_hash,hash FROM audit ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	previous := ""
	total := 0
	brokenAt := 0
	for rows.Next() {
		var id int
		var p auditPayload
		var hash string
		if err = rows.Scan(&id, &p.CreatedAt, &p.Operator, &p.EntityType, &p.EntityID, &p.Action, &p.Before, &p.After, &p.Previous, &hash); err != nil {
			return nil, err
		}
		total++
		if brokenAt == 0 && (p.Previous != previous || auditHash(p) != hash) {
			brokenAt = id
		}
		previous = hash
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if brokenAt != 0 {
		return map[string]any{"ok": false, "total": total, "broken_at": brokenAt}, nil
	}
	return map[string]any{"ok": true, "total": total}, nil
}
func (s *Store) Demo() error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err = tx.QueryRow("SELECT (SELECT count(*) FROM projects)+(SELECT count(*) FROM tasks)+(SELECT count(*) FROM records)+(SELECT count(*) FROM issues)+(SELECT count(*) FROM worklogs)+(SELECT count(*) FROM meeting_minutes)+(SELECT count(*) FROM requirements)+(SELECT count(*) FROM plan_versions)").Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return fail("conflict", "仅允许在空数据库中创建示例数据", 409)
	}
	p, err := create(tx, "projects", map[string]any{"name": "示例 · 产线改造项目", "code": "DEMO-001", "customer": "示例客户", "manager": "本人", "phase": "实施阶段", "description": "此数据为手动创建的示例，可用于熟悉项目、任务与现场记录。", "start_date": today()})
	if err != nil {
		return err
	}
	pid := text(p["id"])
	parent, err := create(tx, "tasks", map[string]any{"project_id": pid, "name": "现场实施", "assignee": "本人"})
	if err != nil {
		return err
	}
	if _, err = create(tx, "tasks", map[string]any{"project_id": pid, "parent_id": parent["id"], "name": "确认设备安装条件", "status": "doing", "progress": 40, "estimate_hours": 8, "plan_end": today(), "assignee": "本人"}); err != nil {
		return err
	}
	if _, err = create(tx, "tasks", map[string]any{"project_id": pid, "parent_id": parent["id"], "name": "完成联调检查", "estimate_hours": 16, "assignee": "本人"}); err != nil {
		return err
	}
	if _, err = create(tx, "records", map[string]any{"project_id": pid, "type": "inspection", "title": "现场巡检记录", "content": "已核对设备到货情况，待确认联调窗口。 #现场 #待跟进", "location": "一号车间"}); err != nil {
		return err
	}
	if _, err = create(tx, "issues", map[string]any{"project_id": pid, "title": "联调窗口尚未确认", "description": "需与现场负责人确认可用停机时间。", "severity": "major", "owner": "本人", "due_at": today()}); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordEvent appends a server event to the same hash chain as entity changes.
func (s *Store) RecordEvent(action, entityType, entityID string, detail map[string]any) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = audit(tx, entityType, entityID, action, nil, detail); err != nil {
		return err
	}
	return tx.Commit()
}
