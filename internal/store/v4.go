package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func migrateV4(tx *sql.Tx) error {
	if _, err := tx.Exec(`ALTER TABLE time_entries ADD COLUMN assignee TEXT NOT NULL DEFAULT '';
 CREATE INDEX time_entries_person_day ON time_entries(assignee,work_date,deleted_at);
 CREATE TABLE team_daily_entries(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,log_date TEXT NOT NULL,person_name TEXT NOT NULL,content TEXT NOT NULL DEFAULT '',next_plan TEXT NOT NULL DEFAULT '',blockers TEXT NOT NULL DEFAULT '',task_ids TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(task_ids)),created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,deleted_at TEXT NOT NULL DEFAULT '');
 CREATE UNIQUE INDEX team_daily_entry_active ON team_daily_entries(project_id,log_date,person_name) WHERE deleted_at='';
 CREATE TABLE project_daily_logs(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,log_date TEXT NOT NULL,content TEXT NOT NULL,source_snapshot TEXT NOT NULL CHECK(json_valid(source_snapshot)),created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,deleted_at TEXT NOT NULL DEFAULT '');
 CREATE UNIQUE INDEX project_daily_log_active ON project_daily_logs(project_id,log_date) WHERE deleted_at='';
 CREATE TABLE task_imports(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,source_hash TEXT NOT NULL,rows_hash TEXT NOT NULL,task_ids TEXT NOT NULL CHECK(json_valid(task_ids)),created_at TEXT NOT NULL,UNIQUE(project_id,source_hash));
 PRAGMA user_version=4;`); err != nil {
		return err
	}
	_, err := tx.Exec("UPDATE time_entries SET assignee=? WHERE assignee=''", operator(tx))
	return err
}
func dailyProjectWritable(tx *sql.Tx, id string) error {
	if strings.TrimSpace(id) == "" {
		return invalid("请选择项目")
	}
	if err := projectWritable(tx, id, ""); err != nil {
		return err
	}
	var status string
	if err := tx.QueryRow("SELECT status FROM projects WHERE id=? AND deleted_at=''", id).Scan(&status); err != nil {
		return err
	}
	if status == "closed" {
		return fail("read_only", "已关闭项目的团队与项目日报只读", 409)
	}
	return nil
}
func stringIDs(v any) ([]any, error) {
	out := []any{}
	switch list := v.(type) {
	case []any:
		out = list
	case []string:
		for _, id := range list {
			out = append(out, id)
		}
	default:
		return nil, invalid("task_ids 必须是任务 ID 数组")
	}
	if len(out) > 5000 {
		return nil, invalid("关联任务不能超过 5000 项")
	}
	seen := map[string]bool{}
	for _, value := range out {
		id, ok := value.(string)
		if !ok || id == "" || seen[id] {
			return nil, invalid("关联任务 ID 不能为空或重复")
		}
		seen[id] = true
	}
	return out, nil
}
func validateTeamDailyEntry(tx *sql.Tx, m map[string]any) error {
	if err := dailyProjectWritable(tx, text(m["project_id"])); err != nil {
		return err
	}
	if !nonempty(m["log_date"]) || !validDate(m["log_date"]) {
		return invalid("日报日期须为真实 YYYY-MM-DD 日期")
	}
	name := strings.TrimSpace(text(m["person_name"]))
	if name == "" || len([]rune(name)) > 100 {
		return invalid("日报人员须为 1 至 100 个字符")
	}
	m["person_name"] = name
	if !nonempty(m["content"]) && !nonempty(m["next_plan"]) && !nonempty(m["blockers"]) {
		return invalid("完成工作、后续计划或阻碍至少填写一项")
	}
	ids, err := stringIDs(m["task_ids"])
	if err != nil {
		return err
	}
	m["task_ids"] = ids
	for _, id := range ids {
		var pid string
		if err = tx.QueryRow("SELECT project_id FROM tasks WHERE id=? AND deleted_at=''", id).Scan(&pid); err != nil {
			return invalid("关联任务不存在或已回收")
		}
		if pid != text(m["project_id"]) {
			return invalid("关联任务必须属于日报项目")
		}
	}
	return nil
}
func (s *Store) CreateTeamDailyEntry(input map[string]any) (map[string]any, error) {
	return s.Create("team_daily_entries", input)
}
func (s *Store) UpdateTeamDailyEntry(id string, input map[string]any) (map[string]any, error) {
	return s.Update("team_daily_entries", id, input)
}
func (s *Store) DeleteTeamDailyEntry(id string, input map[string]any) (map[string]any, error) {
	return s.SoftDelete("team_daily_entries", id, input)
}
func (s *Store) GetTeamDailyEntry(id string) (map[string]any, error) {
	return s.Get("team_daily_entries", id)
}
func dailyWhere(filters map[string]string, team bool) (string, []any, error) {
	filters = canonicalFilters(filters)
	where := " WHERE deleted_at=''"
	args := []any{}
	keys := []string{"project_id", "log_date"}
	if team {
		keys = append(keys, "person_name")
	}
	for _, key := range keys {
		if value, ok := filters[key]; ok {
			if key == "log_date" && (!validDate(value) || value == "") {
				return "", nil, invalid("日报日期无效")
			}
			where += " AND " + key + "=?"
			args = append(args, value)
		}
	}
	for _, key := range []string{"date_from", "date_to"} {
		if value := filters[key]; value != "" {
			if !validDate(value) {
				return "", nil, invalid("日报日期无效")
			}
			op := ">="
			if key == "date_to" {
				op = "<="
			}
			where += " AND log_date" + op + "?"
			args = append(args, value)
		}
	}
	if filters["date_from"] != "" && filters["date_to"] != "" && filters["date_from"] > filters["date_to"] {
		return "", nil, invalid("开始日期不能晚于结束日期")
	}
	if q := strings.TrimSpace(filters["q"]); q != "" {
		if len([]rune(q)) > 200 {
			return "", nil, invalid("搜索内容不能超过 200 个字符")
		}
		where += " AND content LIKE ? ESCAPE '\\'"
		args = append(args, like(q))
	}
	return where, args, nil
}
func dailyList(q queryer, kind string, filters map[string]string) (map[string]any, error) {
	where, args, err := dailyWhere(filters, kind == "team_daily_entries")
	if err != nil {
		return nil, err
	}
	var total int
	if err = q.QueryRow("SELECT count(*) FROM "+kind+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	limit := pageLimit(filters, 100)
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	rows, err := q.Query("SELECT * FROM "+kind+where+" ORDER BY log_date DESC,created_at DESC,id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	for _, m := range items {
		normalize(kind, m)
	}
	return map[string]any{"items": items, "total": total, "next_cursor": ""}, err
}
func (s *Store) ListTeamDailyEntries(filters map[string]string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return dailyList(s.DB, "team_daily_entries", filters)
}
func (s *Store) ListProjectDailyLogs(filters map[string]string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return dailyList(s.DB, "project_daily_logs", filters)
}
func (s *Store) GetProjectDailyLog(id string) (map[string]any, error) {
	return s.Get("project_daily_logs", id)
}

// Source snapshots contain the exact IDs, versions and content used to generate a
// draft; they remain historical evidence when saved prose is subsequently edited.
func projectDailyDraft(q queryer, projectID, date string) (map[string]any, error) {
	project, err := get(q, "projects", projectID)
	if err != nil {
		return nil, err
	}
	if date == "" {
		date = today()
	}
	if !validDate(date) {
		return nil, invalid("日报日期须为真实 YYYY-MM-DD 日期")
	}
	rows, err := q.Query("SELECT * FROM team_daily_entries WHERE project_id=? AND log_date=? AND deleted_at='' ORDER BY person_name,id", projectID, date)
	if err != nil {
		return nil, err
	}
	entries, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	for _, m := range entries {
		normalize("team_daily_entries", m)
	}
	rows, err = q.Query("SELECT * FROM time_entries WHERE project_id=? AND work_date=? AND deleted_at='' ORDER BY assignee,id", projectID, date)
	if err != nil {
		return nil, err
	}
	times, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	for _, m := range times {
		normalize("time_entries", m)
	}
	taskSQL := "SELECT id,project_id,parent_id,wbs_code,name,assignee,status,progress,version,updated_at,sort_order FROM tasks WHERE project_id=? AND deleted_at='' ORDER BY sort_order,id"
	var lanTable int
	if err = q.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='lan_member_tasks'").Scan(&lanTable); err != nil {
		return nil, err
	}
	if lanTable > 0 {
		var assigned int
		if err = q.QueryRow("SELECT count(*) FROM lan_member_tasks a JOIN tasks t ON t.id=a.task_id WHERE t.project_id=?", projectID).Scan(&assigned); err != nil {
			return nil, err
		}
		if assigned > 0 {
			taskSQL = "SELECT t.id,t.project_id,t.parent_id,t.wbs_code,t.name,t.assignee,t.status,t.progress,t.version,t.updated_at,t.sort_order,COALESCE(a.member_id,'') AS lan_member_id FROM tasks t LEFT JOIN lan_member_tasks a ON a.task_id=t.id WHERE t.project_id=? AND t.deleted_at='' ORDER BY t.sort_order,t.id"
		}
	}
	rows, err = q.Query(taskSQL, projectID)
	if err != nil {
		return nil, err
	}
	tasks, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	people := map[string]bool{}
	submitted := map[string]bool{}
	byEntry := map[string][]map[string]any{}
	byTime := map[string]int{}
	byTask := map[string][]map[string]any{}
	expected := map[string]bool{}
	totalMinutes := 0
	for _, m := range entries {
		name := text(m["person_name"])
		people[name] = true
		submitted[name] = true
		byEntry[name] = append(byEntry[name], m)
	}
	for _, m := range times {
		name := text(m["assignee"])
		people[name] = true
		byTime[name] += int(num(m["minutes"]))
		totalMinutes += int(num(m["minutes"]))
	}
	for _, m := range tasks {
		if nonempty(m["lan_member_id"]) {
			continue
		}
		name := strings.TrimSpace(text(m["assignee"]))
		if name == "" {
			continue
		}
		people[name] = true
		expected[name] = true
		byTask[name] = append(byTask[name], m)
	}
	names := []string{}
	for name := range people {
		names = append(names, name)
	}
	sort.Strings(names)
	missing := []string{}
	expectedNames := []string{}
	for name := range expected {
		expectedNames = append(expectedNames, name)
		if !submitted[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	sort.Strings(expectedNames)
	groups := []map[string]any{}
	var content strings.Builder
	fmt.Fprintf(&content, "# %s · 项目日报 · %s\n\n", text(project["name"]), date)
	fmt.Fprintf(&content, "## 工时汇总\n\n- 合计 %d 分钟（%.2f 小时）\n\n", totalMinutes, float64(totalMinutes)/60)
	for _, name := range names {
		personEntries := byEntry[name]
		personTasks := byTask[name]
		if personEntries == nil {
			personEntries = []map[string]any{}
		}
		if personTasks == nil {
			personTasks = []map[string]any{}
		}
		groups = append(groups, map[string]any{"person_name": name, "minutes": byTime[name], "submitted": submitted[name]})
		fmt.Fprintf(&content, "## %s\n\n- 当日工时：%d 分钟\n", name, byTime[name])
		if !submitted[name] {
			content.WriteString("- 当日工作记录：未填写\n")
		}
		for _, entry := range personEntries {
			if nonempty(entry["content"]) {
				fmt.Fprintf(&content, "- 完成工作：%s\n", text(entry["content"]))
			}
			if nonempty(entry["next_plan"]) {
				fmt.Fprintf(&content, "- 后续计划：%s\n", text(entry["next_plan"]))
			}
			if nonempty(entry["blockers"]) {
				fmt.Fprintf(&content, "- 阻碍/需协调：%s\n", text(entry["blockers"]))
			}
		}
		for _, task := range personTasks {
			fmt.Fprintf(&content, "- 任务 [%s] %s：%s，进度 %.0f%%\n", text(task["wbs_code"]), text(task["name"]), map[string]string{"todo": "未开始", "doing": "进行中", "done": "已完成", "canceled": "已取消"}[text(task["status"])], num(task["progress"]))
		}
		content.WriteString("\n")
	}
	content.WriteString("## 未填写人员\n\n")
	if len(missing) == 0 {
		content.WriteString("- 当前任务负责人均已填写\n")
	} else {
		for _, name := range missing {
			fmt.Fprintf(&content, "- %s\n", name)
		}
	}
	snapshot := map[string]any{"format": 1, "project": map[string]any{"id": project["id"], "version": project["version"], "name": project["name"]}, "log_date": date, "generated_at": now(), "team_entries": entries, "time_entries": times, "tasks": tasks, "task_snapshot_semantics": "生成时的当前任务状态，并非日志日期当天的历史状态", "people": groups, "expected_people": expectedNames, "missing_people": missing, "total_minutes": totalMinutes}
	if err = projectDailyLAN(q, projectID, date, snapshot, &content); err != nil {
		return nil, err
	}
	if combined := int(num(snapshot["total_minutes"])); combined != totalMinutes {
		old := fmt.Sprintf("- 合计 %d 分钟（%.2f 小时）", totalMinutes, float64(totalMinutes)/60)
		new := fmt.Sprintf("- 合计 %d 分钟（%.2f 小时，单机工时条目与成员日报工时各计一次）", combined, float64(combined)/60)
		body := strings.Replace(content.String(), old, new, 1)
		content.Reset()
		content.WriteString(body)
	}
	return map[string]any{"project_id": projectID, "log_date": date, "content": content.String(), "source_snapshot": snapshot}, nil
}
func (s *Store) ProjectDailyLog(projectID, date string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return projectDailyDraft(s.DB, projectID, date)
}
func snapshotMap(v any) (map[string]any, error) {
	if v == nil {
		return nil, invalid("项目日报须携带生成时的 source_snapshot")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, invalid("日报来源快照无效")
	}
	if len(b) > 16<<20 {
		return nil, invalid("日报来源快照超过 16 MB")
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil || m == nil {
		return nil, invalid("日报来源快照必须是对象")
	}
	return m, nil
}
func equivalentSnapshots(a, b map[string]any) bool {
	ac, bc := copyMap(a), copyMap(b)
	delete(ac, "generated_at")
	delete(bc, "generated_at")
	aj, _ := json.Marshal(ac)
	bj, _ := json.Marshal(bc)
	return string(aj) == string(bj)
}
func validateProjectDailyLog(tx *sql.Tx, m, before map[string]any) error {
	if err := dailyProjectWritable(tx, text(m["project_id"])); err != nil {
		return err
	}
	if !nonempty(m["log_date"]) || !validDate(m["log_date"]) {
		return invalid("项目日报日期须为真实 YYYY-MM-DD 日期")
	}
	if !nonempty(m["content"]) {
		return invalid("项目日报正文不能为空")
	}
	snapshot, err := snapshotMap(m["source_snapshot"])
	if err != nil {
		return err
	}
	m["source_snapshot"] = snapshot
	if before != nil && m["_source_snapshot_explicit"] != true && text(before["project_id"]) == text(m["project_id"]) && text(before["log_date"]) == text(m["log_date"]) {
		old, e := snapshotMap(before["source_snapshot"])
		if e == nil && equivalentSnapshots(old, snapshot) {
			return nil
		}
	}
	current, err := projectDailyDraft(tx, text(m["project_id"]), text(m["log_date"]))
	if err != nil {
		return err
	}
	expected, err := snapshotMap(current["source_snapshot"])
	if err != nil {
		return err
	}
	if !equivalentSnapshots(expected, snapshot) {
		return fail("source_conflict", "日报来源已变化或快照无效，请显式重新生成并核对后保存", 409)
	}
	return nil
}
func (s *Store) SaveProjectDailyLog(input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		projectID, date := text(input["project_id"]), text(input["log_date"])
		if projectID == "" || date == "" {
			return nil, invalid("请选择项目与日报日期")
		}
		if saveAsNew, ok := input["save_as_new"].(bool); ok && saveAsNew {
			return create(tx, "project_daily_logs", input)
		}
		var id string
		err := tx.QueryRow("SELECT id FROM project_daily_logs WHERE project_id=? AND log_date=? AND variant='' AND deleted_at=''", projectID, date).Scan(&id)
		in := copyMap(input)
		delete(in, "id")
		if err == sql.ErrNoRows {
			if _, provided := in["version"]; provided {
				return nil, fail("version_conflict", "日报已被回收或版本无效，请重新加载", 409)
			}
			delete(in, "version")
			return create(tx, "project_daily_logs", in)
		}
		if err != nil {
			return nil, err
		}
		if !nonempty(in["version"]) {
			return nil, fail("version_conflict", "该项目当天日报已存在，请加载当前版本后显式保存", 409)
		}
		return update(tx, "project_daily_logs", id, in)
	})
}

func (s *Store) UpdateProjectDailyLog(id string, input map[string]any) (map[string]any, error) {
	return s.Update("project_daily_logs", id, input)
}
func (s *Store) DeleteProjectDailyLog(id string, input map[string]any) (map[string]any, error) {
	return s.SoftDelete("project_daily_logs", id, input)
}
