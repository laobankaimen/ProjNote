package store

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func pageLimit(filters map[string]string, def int) int {
	n, e := strconv.Atoi(filters["limit"])
	if e != nil || n < 1 {
		n = def
	}
	if n > 500 {
		n = 500
	}
	return n
}
func canonicalFilters(filters map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range filters {
		out[k] = v
	}
	for alias, key := range map[string]string{"from": "date_from", "to": "date_to"} {
		if out[key] == "" {
			out[key] = out[alias]
		}
		delete(out, alias)
	}
	return out
}
func filterWhere(kind string, filters map[string]string) (string, []any, error) {
	filters = canonicalFilters(filters)
	where := " WHERE deleted_at=''"
	args := []any{}
	for _, key := range []string{"project_id", "status", "type"} {
		value, ok := filters[key]
		if !ok {
			continue
		}
		valid := false
		for _, col := range columns[kind] {
			if key == col {
				valid = true
			}
		}
		if !valid {
			continue
		}
		if key == "project_id" && value == "" {
			where += " AND project_id IS NULL"
		} else {
			where += " AND " + key + "=?"
			args = append(args, value)
		}
	}
	if filters["unclassified"] == "true" || filters["unclassified"] == "1" {
		if kind != "projects" {
			where += " AND project_id IS NULL"
		}
	}
	if q := strings.TrimSpace(filters["q"]); q != "" {
		if utf8.RuneCountInString(q) > 200 {
			return "", nil, invalid("搜索内容不能超过 200 个字符")
		}
		where += " AND id IN (SELECT entity_id FROM search_doc WHERE entity_type=? AND (title LIKE ? ESCAPE '\\' OR body LIKE ? ESCAPE '\\'))"
		args = append(args, kind, like(q), like(q))
	}
	if kind == "records" {
		if tag := filters["tag"]; tag != "" {
			where += " AND EXISTS(SELECT 1 FROM json_each(tags) WHERE json_each.value=?)"
			args = append(args, tag)
		}
		for _, key := range []string{"date_from", "date_to"} {
			if value := filters[key]; value != "" {
				if !validDate(value) {
					return "", nil, invalid("日期格式应为 YYYY-MM-DD")
				}
				d, _ := time.ParseInLocation("2006-01-02", value, time.Local)
				op := ">="
				if key == "date_to" {
					d = d.AddDate(0, 0, 1)
					op = "<"
				}
				where += " AND julianday(occurred_at)" + op + "julianday(?)"
				args = append(args, d.UTC().Format(time.RFC3339Nano))
			}
		}
		if filters["date_from"] != "" && filters["date_to"] != "" && filters["date_from"] > filters["date_to"] {
			return "", nil, invalid("开始日期不能晚于结束日期")
		}
	}
	return where, args, nil
}
func listRows(q queryer, kind string, filters map[string]string) ([]map[string]any, int, error) {
	where, args, err := filterWhere(kind, filters)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err = q.QueryRow("SELECT count(*) FROM "+kind+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := pageLimit(filters, 100)
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	order := "created_at DESC,id DESC"
	if kind == "tasks" {
		order = "project_id,sort_order,id"
	}
	if kind == "records" {
		order = recordTimeKey + " DESC,id DESC"
	}
	args = append(args, limit, offset)
	selectColumns := "*"
	if kind == "tasks" {
		selectColumns += ",EXISTS(SELECT 1 FROM tasks child WHERE child.parent_id=tasks.id AND child.deleted_at='') AS has_children"
	}
	rows, err := q.Query("SELECT "+selectColumns+" FROM "+kind+where+" ORDER BY "+order+" LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, 0, err
	}
	items, err := rawRows(rows)
	for _, m := range items {
		normalize(kind, m)
	}
	if err == nil && kind == "tasks" {
		err = attachTaskActuals(q, items, false)
	}
	return items, total, err
}

// Canonical UTC fractional seconds preserve subsecond order for v1 timestamps.
const recordTimeKey = "(substr(occurred_at,1,19)||'.'||substr(rtrim(substr(occurred_at,21),'Z')||'000000000',1,9)||'Z')"

type pageCursor struct {
	Scope  string `json:"scope"`
	ID     string `json:"id"`
	Time   string `json:"time,omitempty"`
	Number int64  `json:"number,omitempty"`
}

func filterScope(kind string, filters map[string]string) string {
	m := map[string]string{"kind": kind}
	for k, v := range filters {
		if !oneOf(k, "cursor", "limit", "offset") {
			m[k] = v
		}
	}
	b, _ := json.Marshal(m)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func encodeCursor(c pageCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}
func decodeCursor(raw, scope string) (pageCursor, error) {
	var c pageCursor
	if len(raw) > 4096 {
		return c, invalid("分页游标无效")
	}
	b, e := base64.RawURLEncoding.DecodeString(raw)
	if e != nil {
		return c, invalid("分页游标无效")
	}
	if json.Unmarshal(b, &c) != nil || c.Scope != scope {
		return c, invalid("分页游标无效或筛选条件已变化")
	}
	return c, nil
}
func (s *Store) ListPage(kind string, filters map[string]string) (map[string]any, error) {
	filters = canonicalFilters(filters)
	if err := kindOK(kind); err != nil {
		return nil, err
	}
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	if kind != "records" {
		items, total, err := listRows(s.DB, kind, filters)
		return map[string]any{"items": items, "total": total, "next_cursor": ""}, err
	}
	where, args, err := filterWhere(kind, filters)
	if err != nil {
		return nil, err
	}
	var total int
	if err = s.DB.QueryRow("SELECT count(*) FROM records"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	scope := filterScope(kind, filters)
	if raw := filters["cursor"]; raw != "" {
		cursor, e := decodeCursor(raw, scope)
		if e != nil {
			return nil, e
		}
		if cursor.ID == "" || cursor.Time == "" {
			return nil, invalid("分页游标无效")
		}
		where += " AND (" + recordTimeKey + "<? OR (" + recordTimeKey + "=? AND id<?))"
		args = append(args, cursor.Time, cursor.Time, cursor.ID)
	}
	limit := pageLimit(filters, 100)
	offset := 0
	if filters["cursor"] == "" {
		offset, _ = strconv.Atoi(filters["offset"])
		if offset < 0 {
			offset = 0
		}
	}
	args = append(args, limit+1, offset)
	rows, err := s.DB.Query("SELECT *,"+recordTimeKey+" AS cursor_time FROM records"+where+" ORDER BY "+recordTimeKey+" DESC,id DESC LIMIT ? OFFSET ?", args...)
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
		next = encodeCursor(pageCursor{Scope: scope, ID: text(last["id"]), Time: text(last["cursor_time"])})
	}
	for _, m := range items {
		delete(m, "cursor_time")
		normalize("records", m)
	}
	return map[string]any{"items": items, "total": total, "next_cursor": next}, nil
}
func (s *Store) SearchPage(filters map[string]string) (map[string]any, error) {
	filters = canonicalFilters(filters)
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	q := strings.TrimSpace(filters["q"])
	if utf8.RuneCountInString(q) > 200 {
		return nil, invalid("搜索内容不能超过 200 个字符")
	}
	where := " WHERE 1=1"
	args := []any{}
	if q != "" {
		if utf8.RuneCountInString(q) >= 3 {
			where += " AND d.id IN(SELECT rowid FROM search_fts WHERE search_fts MATCH ?)"
			args = append(args, "\""+strings.ReplaceAll(q, "\"", "\"\"")+"\"")
		} else {
			where += " AND (d.title LIKE ? ESCAPE '\\' OR d.body LIKE ? ESCAPE '\\')"
			args = append(args, like(q), like(q))
		}
	}
	kind := filters["entity_type"]
	if kind == "" {
		kind = filters["kind"]
	}
	if kind != "" {
		if err := kindOK(kind); err != nil {
			return nil, err
		}
		where += " AND d.entity_type=?"
		args = append(args, kind)
	}
	if pid, ok := filters["project_id"]; ok {
		where += " AND d.project_id=?"
		args = append(args, pid)
	}
	if filters["unclassified"] == "true" || filters["unclassified"] == "1" {
		where += " AND d.entity_type='records' AND d.project_id=''"
	}
	recordFilters := map[string]string{}
	for _, key := range []string{"date_from", "date_to", "tag", "type"} {
		if value := filters[key]; value != "" {
			recordFilters[key] = value
		}
	}
	if len(recordFilters) > 0 {
		rw, ra, err := filterWhere("records", recordFilters)
		if err != nil {
			return nil, err
		}
		where += " AND d.entity_type='records' AND d.entity_id IN(SELECT id FROM records" + rw + ")"
		args = append(args, ra...)
	}
	var total int
	if err := s.DB.QueryRow("SELECT count(*) FROM search_doc d"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	scope := filterScope("search", filters)
	if raw := filters["cursor"]; raw != "" {
		c, err := decodeCursor(raw, scope)
		if err != nil {
			return nil, err
		}
		if c.Number < 1 {
			return nil, invalid("分页游标无效")
		}
		where += " AND d.id<?"
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
	rows, err := s.DB.Query("SELECT d.id AS cursor_number,d.entity_type,d.entity_id,d.project_id,d.title,d.body FROM search_doc d"+where+" ORDER BY d.id DESC LIMIT ? OFFSET ?", args...)
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
		next = encodeCursor(pageCursor{Scope: scope, Number: int64(num(last["cursor_number"]))})
	}
	for _, m := range items {
		delete(m, "cursor_number")
	}
	return map[string]any{"items": items, "total": total, "next_cursor": next}, nil
}

// Tags returns only tags in active records; a query can narrow a large catalogue.
func (s *Store) Tags(query string) ([]string, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) > 100 {
		return nil, invalid("标签查询不能超过 100 个字符")
	}
	rows, err := s.DB.Query("SELECT DISTINCT j.value FROM records r,json_each(r.tags) j WHERE r.deleted_at='' AND j.type='text' AND j.value!='' AND j.value LIKE ? ESCAPE '\\' ORDER BY j.value LIMIT 1000", like(query))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []string{}
	for rows.Next() {
		var tag string
		if err = rows.Scan(&tag); err != nil {
			return nil, err
		}
		items = append(items, tag)
	}
	return items, rows.Err()
}
