package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type AppError struct {
	Code    string
	Message string
	Status  int
}

func (e *AppError) Error() string             { return e.Message }
func fail(code, msg string, status int) error { return &AppError{code, msg, status} }
func invalid(msg string) error                { return fail("validation", msg, 400) }

type Store struct {
	DB *sql.DB
	Mu sync.RWMutex
}
type queryer interface {
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
	Exec(string, ...any) (sql.Result, error)
}

var columns = map[string][]string{
	"projects": strings.Fields("id code name customer phase manager start_date end_date status description created_at updated_at version"),
	"tasks":    strings.Fields("id project_id parent_id wbs_code name assignee plan_start plan_end progress status priority estimate_hours description actual_start actual_end created_at updated_at version sort_order"),
	"records":  strings.Fields("id code project_id type title content location occurred_at tags attachments created_at updated_at version"),
	"issues":   strings.Fields("id code project_id title description severity source status owner due_at root_cause solution verify_result rejection_reason closed_at created_at updated_at version"),
	"worklogs": strings.Fields("id project_id period_type log_date content created_at updated_at version"),
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "app.db"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	for _, pragma := range []string{"PRAGMA foreign_keys=ON", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA busy_timeout=5000"} {
		if _, err = db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	s := &Store{DB: db}
	if err = s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.DB.Close() }
func (s *Store) migrate() error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err = tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return fmt.Errorf("数据库版本 %d 高于当前程序支持的版本", version)
	}
	if version == 1 {
		return tx.Commit()
	}
	schema := `
CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT NOT NULL);
INSERT INTO settings VALUES('operator','本人');
CREATE TABLE projects(id TEXT PRIMARY KEY,code TEXT NOT NULL UNIQUE,name TEXT NOT NULL,customer TEXT NOT NULL DEFAULT '',phase TEXT NOT NULL DEFAULT '',manager TEXT NOT NULL DEFAULT '',start_date TEXT NOT NULL DEFAULT '',end_date TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'active' CHECK(status IN('active','paused','closed','archived')),description TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1);
CREATE TABLE tasks(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,parent_id TEXT REFERENCES tasks(id) ON DELETE RESTRICT,wbs_code TEXT NOT NULL DEFAULT '',name TEXT NOT NULL,assignee TEXT NOT NULL DEFAULT '',plan_start TEXT NOT NULL DEFAULT '',plan_end TEXT NOT NULL DEFAULT '',progress INTEGER NOT NULL DEFAULT 0 CHECK(progress BETWEEN 0 AND 100),status TEXT NOT NULL DEFAULT 'todo' CHECK(status IN('todo','doing','done','canceled')),priority INTEGER NOT NULL DEFAULT 2 CHECK(priority BETWEEN 1 AND 3),estimate_hours REAL NOT NULL DEFAULT 0 CHECK(estimate_hours>=0),description TEXT NOT NULL DEFAULT '',actual_start TEXT NOT NULL DEFAULT '',actual_end TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1,sort_order INTEGER NOT NULL DEFAULT 0);
CREATE INDEX tasks_project ON tasks(project_id,parent_id,sort_order);
CREATE TABLE records(id TEXT PRIMARY KEY,code TEXT NOT NULL UNIQUE,project_id TEXT REFERENCES projects(id) ON DELETE RESTRICT,type TEXT NOT NULL DEFAULT 'note' CHECK(type IN('note','inspection','abnormal','photo','decision','other')),title TEXT NOT NULL DEFAULT '',content TEXT NOT NULL DEFAULT '',location TEXT NOT NULL DEFAULT '',occurred_at TEXT NOT NULL,tags TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(tags)),attachments TEXT NOT NULL DEFAULT '[]' CHECK(json_valid(attachments)),created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1);
CREATE INDEX records_project_time ON records(project_id,occurred_at);
CREATE TABLE issues(id TEXT PRIMARY KEY,code TEXT NOT NULL UNIQUE,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE RESTRICT,title TEXT NOT NULL,description TEXT NOT NULL DEFAULT '',severity TEXT NOT NULL DEFAULT 'minor' CHECK(severity IN('critical','major','minor','suggestion')),source TEXT NOT NULL DEFAULT '',status TEXT NOT NULL DEFAULT 'open' CHECK(status IN('open','analyzing','fixing','verifying','closed','rejected')),owner TEXT NOT NULL DEFAULT '',due_at TEXT NOT NULL DEFAULT '',root_cause TEXT NOT NULL DEFAULT '',solution TEXT NOT NULL DEFAULT '',verify_result TEXT NOT NULL DEFAULT '',rejection_reason TEXT NOT NULL DEFAULT '',closed_at TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1);
CREATE TABLE issue_history(id INTEGER PRIMARY KEY AUTOINCREMENT,issue_id TEXT NOT NULL REFERENCES issues(id) ON DELETE CASCADE,from_status TEXT NOT NULL,to_status TEXT NOT NULL,operator TEXT NOT NULL,created_at TEXT NOT NULL,detail TEXT NOT NULL);
CREATE TABLE worklogs(id TEXT PRIMARY KEY,project_id TEXT REFERENCES projects(id) ON DELETE RESTRICT,period_type TEXT NOT NULL CHECK(period_type IN('day','week')),log_date TEXT NOT NULL,content TEXT NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,version INTEGER NOT NULL DEFAULT 1);
CREATE UNIQUE INDEX worklogs_period ON worklogs(COALESCE(project_id,''),period_type,log_date);
CREATE TABLE audit(id INTEGER PRIMARY KEY AUTOINCREMENT,created_at TEXT NOT NULL,operator TEXT NOT NULL,entity_type TEXT NOT NULL,entity_id TEXT NOT NULL,action TEXT NOT NULL,before_json TEXT NOT NULL,after_json TEXT NOT NULL,prev_hash TEXT NOT NULL,hash TEXT NOT NULL);
CREATE TABLE search_doc(id INTEGER PRIMARY KEY AUTOINCREMENT,entity_type TEXT NOT NULL,entity_id TEXT NOT NULL,project_id TEXT NOT NULL DEFAULT '',title TEXT NOT NULL,body TEXT NOT NULL,UNIQUE(entity_type,entity_id));
CREATE VIRTUAL TABLE search_fts USING fts5(title,body,content='search_doc',content_rowid='id',tokenize='trigram');
CREATE TRIGGER search_doc_ai AFTER INSERT ON search_doc BEGIN INSERT INTO search_fts(rowid,title,body)VALUES(new.id,new.title,new.body); END;
CREATE TRIGGER search_doc_ad AFTER DELETE ON search_doc BEGIN INSERT INTO search_fts(search_fts,rowid,title,body)VALUES('delete',old.id,old.title,old.body); END;
CREATE TRIGGER search_doc_au AFTER UPDATE ON search_doc BEGIN INSERT INTO search_fts(search_fts,rowid,title,body)VALUES('delete',old.id,old.title,old.body); INSERT INTO search_fts(rowid,title,body)VALUES(new.id,new.title,new.body); END;
PRAGMA user_version=1;`
	if _, err = tx.Exec(schema); err != nil {
		return fmt.Errorf("初始化数据库: %w", err)
	}
	return tx.Commit()
}
func kindOK(kind string) error {
	if _, ok := columns[kind]; !ok {
		return fail("not_found", "未知资源", 404)
	}
	return nil
}
func now() string   { return time.Now().UTC().Format(time.RFC3339Nano) }
func today() string { return time.Now().Format("2006-01-02") }
func uid() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func text(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(n, 64)
		return f
	}
	return 0
}
func versionOf(v any) int {
	n, ok := numeric(v)
	if !ok || n < 1 || n != math.Trunc(n) || n > float64(1<<53) {
		return 0
	}
	return int(n)
}
func numeric(v any) (float64, bool) {
	switch v.(type) {
	case int, int64, float64, float32, json.Number:
		n := num(v)
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	}
	return 0, false
}
func nonempty(v any) bool { return strings.TrimSpace(text(v)) != "" }
func oneOf(v any, options ...string) bool {
	for _, s := range options {
		if text(v) == s {
			return true
		}
	}
	return false
}
func normalize(kind string, m map[string]any) map[string]any {
	for k, v := range m {
		if b, ok := v.([]byte); ok {
			m[k] = string(b)
		}
		if m[k] == nil {
			m[k] = ""
		}
	}
	if kind == "records" {
		for _, k := range []string{"tags", "attachments"} {
			var a []any
			if json.Unmarshal([]byte(text(m[k])), &a) != nil || a == nil {
				a = []any{}
			}
			m[k] = a
		}
	}
	if kind == "tasks" {
		m["overdue"] = nonempty(m["plan_end"]) && text(m["plan_end"]) < today() && !oneOf(m["status"], "done", "canceled")
		delete(m, "sort_order")
	}
	return m
}
func rawRows(rows *sql.Rows) ([]map[string]any, error) {
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := map[string]any{}
		for i, c := range cols {
			m[c] = vals[i]
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func get(q queryer, kind, id string) (map[string]any, error) {
	rows, err := q.Query("SELECT * FROM "+kind+" WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	all, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, fail("not_found", "记录不存在", 404)
	}
	m := normalize(kind, all[0])
	if kind == "issues" {
		rows, err = q.Query("SELECT from_status,to_status,operator,created_at,detail FROM issue_history WHERE issue_id=? ORDER BY id", id)
		if err != nil {
			return nil, err
		}
		history, err := rawRows(rows)
		if err != nil {
			return nil, err
		}
		m["history"] = history
	}
	return m, nil
}
func (s *Store) Get(kind, id string) (map[string]any, error) {
	if err := kindOK(kind); err != nil {
		return nil, err
	}
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return get(s.DB, kind, id)
}
func like(q string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
}
func (s *Store) List(kind string, filters map[string]string) ([]map[string]any, int, error) {
	if err := kindOK(kind); err != nil {
		return nil, 0, err
	}
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	where := " WHERE 1=1"
	args := []any{}
	for _, key := range []string{"project_id", "status", "type"} {
		v, ok := filters[key]
		if !ok {
			continue
		}
		valid := false
		for _, c := range columns[kind] {
			if c == key {
				valid = true
			}
		}
		if !valid {
			continue
		}
		if key == "project_id" && v == "" {
			where += " AND project_id IS NULL"
		} else {
			where += " AND " + key + "=?"
			args = append(args, v)
		}
	}
	if q := strings.TrimSpace(filters["q"]); q != "" {
		where += " AND id IN (SELECT entity_id FROM search_doc WHERE entity_type=? AND (title LIKE ? ESCAPE '\\' OR body LIKE ? ESCAPE '\\'))"
		args = append(args, kind, like(q), like(q))
	}
	var total int
	if err := s.DB.QueryRow("SELECT count(*) FROM "+kind+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := 100
	if n, err := strconv.Atoi(filters["limit"]); err == nil && n > 0 {
		limit = n
	}
	if limit > 500 {
		limit = 500
	}
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	order := "created_at DESC,id"
	if kind == "tasks" {
		order = "project_id,sort_order,id"
	}
	args = append(args, limit, offset)
	rows, err := s.DB.Query("SELECT * FROM "+kind+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, 0, err
	}
	all, err := rawRows(rows)
	for _, m := range all {
		normalize(kind, m)
	}
	return all, total, err
}
func operator(q queryer) string {
	var v string
	if q.QueryRow("SELECT value FROM settings WHERE key='operator'").Scan(&v) != nil || strings.TrimSpace(v) == "" {
		return "本人"
	}
	return v
}

type auditPayload struct {
	CreatedAt  string `json:"created_at"`
	Operator   string `json:"operator"`
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Action     string `json:"action"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Previous   string `json:"previous"`
}

func auditHash(p auditPayload) string {
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func audit(tx *sql.Tx, kind, id, action string, before, after any) error {
	b, err := json.Marshal(before)
	if err != nil {
		return err
	}
	a, err := json.Marshal(after)
	if err != nil {
		return err
	}
	var prev string
	err = tx.QueryRow("SELECT hash FROM audit ORDER BY id DESC LIMIT 1").Scan(&prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	p := auditPayload{now(), operator(tx), kind, id, action, string(b), string(a), prev}
	_, err = tx.Exec("INSERT INTO audit(created_at,operator,entity_type,entity_id,action,before_json,after_json,prev_hash,hash)VALUES(?,?,?,?,?,?,?,?,?)", p.CreatedAt, p.Operator, kind, id, action, p.Before, p.After, prev, auditHash(p))
	return err
}
func searchSync(tx *sql.Tx, kind string, m map[string]any) error {
	title := text(m["name"])
	if title == "" {
		title = text(m["title"])
	}
	if kind == "worklogs" {
		title = text(m["log_date"]) + " " + text(m["period_type"])
	}
	body := []string{}
	for _, k := range []string{"code", "description", "content", "location", "root_cause", "solution", "verify_result", "owner", "assignee", "customer", "manager"} {
		if nonempty(m[k]) {
			body = append(body, text(m[k]))
		}
	}
	if kind == "records" {
		b, _ := json.Marshal(m["tags"])
		body = append(body, string(b))
	}
	_, err := tx.Exec("INSERT INTO search_doc(entity_type,entity_id,project_id,title,body)VALUES(?,?,?,?,?) ON CONFLICT(entity_type,entity_id) DO UPDATE SET project_id=excluded.project_id,title=excluded.title,body=excluded.body", kind, m["id"], text(m["project_id"]), title, strings.Join(body, "\n"))
	return err
}
func projectWritable(q queryer, id string, newKind string) error {
	if id == "" {
		return nil
	}
	var status string
	if err := q.QueryRow("SELECT status FROM projects WHERE id=?", id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return invalid("关联项目不存在")
		}
		return err
	}
	if status == "archived" {
		return fail("read_only", "归档项目及其内容只读", 409)
	}
	if status == "closed" && (newKind == "tasks" || newKind == "issues") {
		return fail("read_only", "已关闭项目不能新增任务或问题", 409)
	}
	return nil
}
func cleanInput(kind string, input map[string]any, creating bool) (map[string]any, error) {
	out := map[string]any{}
	allowed := map[string]bool{}
	for _, c := range columns[kind] {
		allowed[c] = true
	}
	for k, v := range input {
		if k == "version" && !creating {
			continue
		}
		if !allowed[k] {
			return nil, invalid("未知字段: " + k)
		}
		if oneOf(k, "id", "created_at", "updated_at", "wbs_code", "sort_order", "closed_at") || k == "version" {
			return nil, invalid("字段由服务器生成: " + k)
		}
		if v == nil && !oneOf(k, "project_id", "parent_id") {
			return nil, invalid("字段不能为空值: " + k)
		}
		out[k] = v
	}
	return out, nil
}
func defaults(kind string) map[string]any {
	m := map[string]any{}
	for _, c := range columns[kind] {
		m[c] = ""
	}
	m["id"] = uid()
	m["created_at"] = now()
	m["updated_at"] = m["created_at"]
	m["version"] = 1
	switch kind {
	case "projects":
		m["status"] = "active"
	case "tasks":
		m["parent_id"] = ""
		m["progress"] = 0
		m["status"] = "todo"
		m["priority"] = 2
		m["estimate_hours"] = 0
		m["sort_order"] = 0
	case "records":
		m["type"] = "note"
		m["occurred_at"] = now()
		m["tags"] = []any{}
		m["attachments"] = []any{}
	case "issues":
		m["status"] = "open"
		m["severity"] = "minor"
	case "worklogs":
		m["period_type"] = "day"
		m["log_date"] = today()
	}
	return m
}
func validDate(v any) bool {
	if text(v) == "" {
		return true
	}
	t, err := time.Parse("2006-01-02", text(v))
	return err == nil && t.Format("2006-01-02") == text(v)
}
func validate(tx *sql.Tx, kind string, m, before map[string]any) error {
	if kind != "projects" {
		if kind == "tasks" || kind == "issues" {
			if !nonempty(m["project_id"]) {
				return invalid("必须选择项目")
			}
		}
		if err := projectWritable(tx, text(m["project_id"]), ""); err != nil {
			return err
		}
	}
	for _, c := range columns[kind] {
		if !oneOf(c, "progress", "priority", "estimate_hours", "version", "sort_order", "tags", "attachments") && m[c] != nil {
			if _, ok := m[c].(string); !ok {
				return invalid("字段必须是文本: " + c)
			}
		}
	}
	switch kind {
	case "projects":
		if !nonempty(m["name"]) {
			return invalid("项目名称不能为空")
		}
		if !oneOf(m["status"], "active", "paused", "closed", "archived") {
			return invalid("项目状态无效")
		}
		if before != nil && text(before["status"]) == "archived" {
			return fail("read_only", "归档项目只读", 409)
		}
	case "tasks":
		if !nonempty(m["name"]) {
			return invalid("任务名称不能为空")
		}
		if !oneOf(m["status"], "todo", "doing", "done", "canceled") {
			return invalid("任务状态无效")
		}
		if _, ok := numeric(m["progress"]); !ok {
			return invalid("进度必须是数字")
		}
		if _, ok := numeric(m["priority"]); !ok {
			return invalid("优先级必须是数字")
		}
		if _, ok := numeric(m["estimate_hours"]); !ok {
			return invalid("预计工时必须是数字")
		}
		if num(m["progress"]) < 0 || num(m["progress"]) > 100 || num(m["progress"]) != math.Trunc(num(m["progress"])) {
			return invalid("进度必须是 0 至 100 的整数")
		}
		if num(m["priority"]) < 1 || num(m["priority"]) > 3 || num(m["priority"]) != math.Trunc(num(m["priority"])) {
			return invalid("优先级必须是 1 至 3 的整数")
		}
		if num(m["estimate_hours"]) < 0 {
			return invalid("预计工时不能为负数")
		}
		if before != nil && text(before["status"]) == "done" {
			return fail("read_only", "已完成任务不可直接修改", 409)
		}
		if parent := text(m["parent_id"]); parent != "" {
			if parent == text(m["id"]) {
				return invalid("任务不能以自身为父任务")
			}
			p, err := get(tx, "tasks", parent)
			if err != nil {
				return invalid("父任务不存在")
			}
			if text(p["project_id"]) != text(m["project_id"]) {
				return invalid("父子任务必须属于同一项目")
			}
			if oneOf(p["status"], "done", "canceled") {
				return invalid("已结束任务不能添加子任务")
			}
			seen := map[string]bool{text(m["id"]): true}
			for parent != "" {
				if seen[parent] {
					return invalid("任务层级不能形成环")
				}
				seen[parent] = true
				var next sql.NullString
				if err = tx.QueryRow("SELECT parent_id FROM tasks WHERE id=?", parent).Scan(&next); err != nil {
					return err
				}
				parent = next.String
			}
		}
		if before != nil && text(m["project_id"]) != text(before["project_id"]) {
			var n int
			if err := tx.QueryRow("SELECT count(*) FROM tasks WHERE parent_id=?", m["id"]).Scan(&n); err != nil {
				return err
			}
			if n > 0 {
				return invalid("含子任务的任务不能迁移项目")
			}
		}
		if text(m["status"]) == "done" {
			m["progress"] = 100
			if !nonempty(m["actual_end"]) {
				m["actual_end"] = today()
			}
		}
		if text(m["status"]) == "doing" && !nonempty(m["actual_start"]) {
			m["actual_start"] = today()
		}
	case "records":
		if !oneOf(m["type"], "note", "inspection", "abnormal", "photo", "decision", "other") {
			return invalid("记录类型无效")
		}
		attachments, _ := m["attachments"].([]any)
		if !nonempty(m["content"]) && !nonempty(m["title"]) && len(attachments) == 0 {
			return invalid("记录内容、标题或附件至少填写一项")
		}
		occurred, err := time.Parse(time.RFC3339Nano, text(m["occurred_at"]))
		if err != nil {
			return invalid("发生时间必须是 RFC3339 时间")
		}
		m["occurred_at"] = occurred.UTC().Format(time.RFC3339Nano)
		if _, ok := m["tags"].([]any); !ok {
			if tags, ok := m["tags"].([]string); ok {
				a := []any{}
				for _, v := range tags {
					a = append(a, v)
				}
				m["tags"] = a
			} else {
				return invalid("标签必须是数组")
			}
		}
		for _, tag := range m["tags"].([]any) {
			if _, ok := tag.(string); !ok {
				return invalid("标签必须是文本")
			}
		}
		if _, ok := m["attachments"].([]any); !ok {
			return invalid("附件必须是数组")
		}
	case "issues":
		if !nonempty(m["title"]) {
			return invalid("问题标题不能为空")
		}
		if !oneOf(m["severity"], "critical", "major", "minor", "suggestion") {
			return invalid("问题等级无效")
		}
		if !oneOf(m["status"], "open", "analyzing", "fixing", "verifying", "closed", "rejected") {
			return invalid("问题状态无效")
		}
		if before == nil && text(m["status"]) != "open" {
			return invalid("新问题必须从待处理状态开始")
		}
		if before != nil {
			old, next := text(before["status"]), text(m["status"])
			if oneOf(old, "closed", "rejected") {
				return fail("read_only", "已结束问题不可直接修改", 409)
			}
			transitions := map[string][]string{"open": {"analyzing", "rejected"}, "analyzing": {"fixing", "rejected"}, "fixing": {"verifying", "analyzing", "rejected"}, "verifying": {"closed", "fixing"}}
			if old != next && !oneOf(next, transitions[old]...) {
				return invalid("不允许的问题状态转换: " + old + " → " + next)
			}
		}
		if text(m["status"]) == "closed" {
			if !nonempty(m["root_cause"]) || !nonempty(m["solution"]) || !nonempty(m["verify_result"]) {
				return invalid("关闭问题必须填写根因、解决方案和验证结果")
			}
			m["closed_at"] = now()
		}
		if text(m["status"]) == "rejected" && !nonempty(m["rejection_reason"]) {
			return invalid("拒绝问题必须填写原因")
		}
	case "worklogs":
		if !oneOf(m["period_type"], "day", "week") {
			return invalid("日志周期无效")
		}
		if !nonempty(m["content"]) {
			return invalid("日志内容不能为空")
		}
		if !nonempty(m["log_date"]) {
			return invalid("日志日期不能为空")
		}
		if text(m["period_type"]) == "week" {
			t, _ := time.Parse("2006-01-02", text(m["log_date"]))
			if !t.IsZero() {
				delta := (int(t.Weekday()) + 6) % 7
				m["log_date"] = t.AddDate(0, 0, -delta).Format("2006-01-02")
			}
		}
	}
	for _, k := range []string{"start_date", "end_date", "plan_start", "plan_end", "actual_start", "actual_end", "log_date", "due_at"} {
		if v, ok := m[k]; ok && !validDate(v) {
			return invalid("日期格式应为 YYYY-MM-DD: " + k)
		}
	}
	for _, pair := range [][2]string{{"start_date", "end_date"}, {"plan_start", "plan_end"}, {"actual_start", "actual_end"}} {
		if nonempty(m[pair[0]]) && nonempty(m[pair[1]]) && text(m[pair[0]]) > text(m[pair[1]]) {
			return invalid("开始日期不能晚于结束日期")
		}
	}
	return nil
}
func dbValue(k string, v any) any {
	if oneOf(k, "tags", "attachments") {
		b, _ := json.Marshal(v)
		return string(b)
	}
	if oneOf(k, "project_id", "parent_id") && text(v) == "" {
		return nil
	}
	return v
}
func dbErr(err error) error {
	if err == nil {
		return nil
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "unique constraint") {
		return fail("conflict", "编号或同项目同周期的日志已存在", 409)
	}
	if strings.Contains(s, "foreign key constraint") {
		return fail("conflict", "存在关联内容，无法删除", 409)
	}
	return err
}
func insert(tx *sql.Tx, kind string, m map[string]any) error {
	cols := columns[kind]
	args := []any{}
	marks := []string{}
	for _, c := range cols {
		args = append(args, dbValue(c, m[c]))
		marks = append(marks, "?")
	}
	_, err := tx.Exec("INSERT INTO "+kind+"("+strings.Join(cols, ",")+")VALUES("+strings.Join(marks, ",")+")", args...)
	return dbErr(err)
}
func nextCode(tx *sql.Tx, kind string) string {
	prefix := map[string]string{"projects": "PRJ", "records": "REC", "issues": "ISS"}[kind]
	for i := 1; ; i++ {
		code := fmt.Sprintf("%s-%s-%03d", prefix, time.Now().Format("20060102"), i)
		var n int
		tx.QueryRow("SELECT count(*) FROM "+kind+" WHERE code=?", code).Scan(&n)
		if n == 0 {
			return code
		}
	}
}
func (s *Store) Create(kind string, input map[string]any) (map[string]any, error) {
	if err := kindOK(kind); err != nil {
		return nil, err
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	m, err := create(tx, kind, input)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return m, nil
}
func create(tx *sql.Tx, kind string, input map[string]any) (map[string]any, error) {
	in, err := cleanInput(kind, input, true)
	if err != nil {
		return nil, err
	}
	m := defaults(kind)
	for k, v := range in {
		m[k] = v
	}
	if kind == "records" {
		if err = parseQuick(tx, m); err != nil {
			return nil, err
		}
	}
	if err = projectWritable(tx, text(m["project_id"]), kind); err != nil {
		return nil, err
	}
	if err = validate(tx, kind, m, nil); err != nil {
		return nil, err
	}
	if kind == "projects" || kind == "records" || kind == "issues" {
		if !nonempty(m["code"]) {
			m["code"] = nextCode(tx, kind)
		}
	}
	if kind == "tasks" {
		var position int
		if err = tx.QueryRow("SELECT COALESCE(max(sort_order),0)+1 FROM tasks WHERE project_id=? AND COALESCE(parent_id,'')=?", m["project_id"], text(m["parent_id"])).Scan(&position); err != nil {
			return nil, err
		}
		m["sort_order"] = position
	}
	if err = insert(tx, kind, m); err != nil {
		return nil, err
	}
	if kind == "tasks" {
		if err = recalculate(tx, text(m["project_id"]), text(m["id"])); err != nil {
			return nil, err
		}
	}
	if kind == "issues" {
		if err = issueHistory(tx, m, "", "open"); err != nil {
			return nil, err
		}
	}
	m, err = get(tx, kind, text(m["id"]))
	if err != nil {
		return nil, err
	}
	if err = searchSync(tx, kind, m); err != nil {
		return nil, err
	}
	if err = audit(tx, kind, text(m["id"]), "create", nil, m); err != nil {
		return nil, err
	}
	return m, nil
}
func (s *Store) Update(kind, id string, input map[string]any) (map[string]any, error) {
	if err := kindOK(kind); err != nil {
		return nil, err
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	before, err := get(tx, kind, id)
	if err != nil {
		return nil, err
	}
	if versionOf(input["version"]) != versionOf(before["version"]) {
		return nil, fail("version_conflict", "内容已被修改，请刷新后重试", 409)
	}
	if kind != "projects" {
		if err = projectWritable(tx, text(before["project_id"]), ""); err != nil {
			return nil, err
		}
	}
	in, err := cleanInput(kind, input, false)
	if err != nil {
		return nil, err
	}
	if kind == "tasks" {
		var n int
		if err = tx.QueryRow("SELECT count(*) FROM tasks WHERE parent_id=?", id).Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			for _, key := range []string{"progress", "estimate_hours", "status"} {
				if _, ok := in[key]; ok && num(in[key]) != num(before[key]) || key == "status" && in[key] != nil && text(in[key]) != text(before[key]) {
					return nil, invalid("父任务的进度、工时和状态由子任务汇总")
				}
			}
		}
	}
	m := map[string]any{}
	for k, v := range before {
		m[k] = v
	}
	for k, v := range in {
		m[k] = v
	}
	if kind == "records" {
		if err = parseQuick(tx, m); err != nil {
			return nil, err
		}
	}
	if kind != "projects" && text(m["project_id"]) != text(before["project_id"]) {
		if err = projectWritable(tx, text(m["project_id"]), kind); err != nil {
			return nil, err
		}
	}
	if err = validate(tx, kind, m, before); err != nil {
		return nil, err
	}
	m["version"] = versionOf(before["version"]) + 1
	m["updated_at"] = now()
	if kind == "tasks" {
		var order int
		if text(m["parent_id"]) != text(before["parent_id"]) || text(m["project_id"]) != text(before["project_id"]) {
			if err = tx.QueryRow("SELECT COALESCE(max(sort_order),0)+1 FROM tasks WHERE project_id=? AND COALESCE(parent_id,'')=?", m["project_id"], text(m["parent_id"])).Scan(&order); err != nil {
				return nil, err
			}
		} else {
			if err = tx.QueryRow("SELECT sort_order FROM tasks WHERE id=?", id).Scan(&order); err != nil {
				return nil, err
			}
		}
		m["sort_order"] = order
	}
	sets := []string{}
	args := []any{}
	for _, c := range columns[kind] {
		if c == "id" || c == "created_at" {
			continue
		}
		sets = append(sets, c+"=?")
		args = append(args, dbValue(c, m[c]))
	}
	args = append(args, id, versionOf(before["version"]))
	res, err := tx.Exec("UPDATE "+kind+" SET "+strings.Join(sets, ",")+" WHERE id=? AND version=?", args...)
	if err != nil {
		return nil, dbErr(err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		return nil, fail("version_conflict", "内容已被修改，请刷新后重试", 409)
	}
	if kind == "tasks" {
		if err = recalculate(tx, text(m["project_id"]), id); err != nil {
			return nil, err
		}
		if text(m["project_id"]) != text(before["project_id"]) {
			if err = recalculate(tx, text(before["project_id"]), id); err != nil {
				return nil, err
			}
		}
	}
	if kind == "issues" {
		if err = issueHistory(tx, m, text(before["status"]), text(m["status"])); err != nil {
			return nil, err
		}
	}
	m, err = get(tx, kind, id)
	if err != nil {
		return nil, err
	}
	if err = searchSync(tx, kind, m); err != nil {
		return nil, err
	}
	if err = audit(tx, kind, id, "update", before, m); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return m, nil
}
func issueHistory(tx *sql.Tx, m map[string]any, from, to string) error {
	b, _ := json.Marshal(map[string]any{"root_cause": m["root_cause"], "solution": m["solution"], "verify_result": m["verify_result"], "rejection_reason": m["rejection_reason"]})
	_, err := tx.Exec("INSERT INTO issue_history(issue_id,from_status,to_status,operator,created_at,detail)VALUES(?,?,?,?,?,?)", m["id"], from, to, operator(tx), now(), string(b))
	return err
}
func (s *Store) Delete(kind, id string, version int) error {
	if err := kindOK(kind); err != nil {
		return err
	}
	s.Mu.Lock()
	defer s.Mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	m, err := get(tx, kind, id)
	if err != nil {
		return err
	}
	if version != versionOf(m["version"]) {
		return fail("version_conflict", "内容已被修改，请刷新后重试", 409)
	}
	pid := text(m["project_id"])
	if kind == "projects" {
		pid = id
	}
	if err = projectWritable(tx, pid, ""); err != nil {
		return err
	}
	if (kind == "tasks" && text(m["status"]) == "done") || (kind == "issues" && oneOf(m["status"], "closed", "rejected")) {
		return fail("read_only", "已结束内容不可删除", 409)
	}
	if _, err = tx.Exec("DELETE FROM "+kind+" WHERE id=? AND version=?", id, version); err != nil {
		return dbErr(err)
	}
	if _, err = tx.Exec("DELETE FROM search_doc WHERE entity_type=? AND entity_id=?", kind, id); err != nil {
		return err
	}
	if kind == "tasks" {
		if err = recalculate(tx, pid, id); err != nil {
			return err
		}
	}
	if err = audit(tx, kind, id, "delete", m, nil); err != nil {
		return err
	}
	return tx.Commit()
}
