package store

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

func exportKind(kind string) (string, error) {
	kind = strings.ReplaceAll(kind, "-", "_")
	if !oneOf(kind, "worklogs", "project_daily_logs", "issues", "meeting_minutes", "requirements", "plan_versions") {
		return "", invalid("请选择已保存的日志、会议纪要、问题、需求或计划版本")
	}
	return kind, nil
}
func exportSnapshot(q queryer, kind, id string) (map[string]any, error) {
	kind, err := exportKind(kind)
	if err != nil {
		return nil, err
	}
	var entity map[string]any
	if kind == "plan_versions" {
		entity, err = planVersion(q, id)
	} else {
		entity, err = get(q, kind, id)
	}
	if err != nil {
		return nil, err
	}
	project := map[string]any{}
	projectName := "跨项目"
	if kind == "plan_versions" {
		if snapshot, ok := entity["snapshot"].(map[string]any); ok {
			project, _ = snapshot["project"].(map[string]any)
			projectName = text(project["name"])
		}
	} else if pid := text(entity["project_id"]); pid != "" {
		project, err = get(q, "projects", pid)
		if err != nil {
			return nil, err
		}
		projectName = text(project["name"])
	}
	var enteredBy string
	err = q.QueryRow("SELECT operator FROM audit WHERE entity_type=? AND entity_id=? AND action='create' ORDER BY id LIMIT 1", kind, id).Scan(&enteredBy)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	date := text(entity["log_date"])
	title, content := date+" 工作日志", text(entity["content"])
	period := text(entity["period_type"])
	person := enteredBy
	scope := "个人工作日志"
	if kind == "project_daily_logs" {
		title = projectName + " · " + text(entity["log_date"]) + " 项目日报"
		period = "day"
		scope = "项目团队日报"
	}
	if kind == "issues" {
		scope = "问题记录"
		title = text(entity["code"]) + " · " + text(entity["title"])
		person = text(entity["owner"])
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n", title)
		for _, field := range []struct{ key, label string }{{"description", "问题描述"}, {"root_cause", "根因分析"}, {"solution", "解决方案"}, {"verify_result", "验证结果"}, {"rejection_reason", "驳回原因"}} {
			fmt.Fprintf(&b, "## %s\n\n%s\n\n", field.label, text(entity[field.key]))
		}
		content = b.String()
	}
	if kind == "meeting_minutes" {
		scope = "会议纪要"
		title = text(entity["title"])
		date = text(entity["meeting_date"])
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n会议编号：%s\n会议日期：%s\n项目：%s\n", title, text(entity["code"]), date, projectName)
		for _, field := range []struct{ key, label string }{{"location", "会议地点"}, {"attendees", "参会人员"}} {
			if value := text(entity[field.key]); strings.TrimSpace(value) != "" {
				fmt.Fprintf(&b, "%s：%s\n", field.label, value)
			}
		}
		fmt.Fprintf(&b, "\n## 纪要正文\n\n%s", content)
		content = b.String()
	}
	if kind == "requirements" {
		scope = "项目需求"
		title = text(entity["code"]) + " · " + text(entity["title"])
		person = text(entity["owner"])
		date = strings.SplitN(text(entity["updated_at"]), "T", 2)[0]
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n项目：%s\n需求版本：%s\n负责人：%s\n状态：%s\n", title, projectName, text(entity["revision"]), person, map[string]string{"pending": "待确认", "doing": "执行中", "accepted": "已验收", "void": "已作废"}[text(entity["status"])])
		for _, field := range []struct{ key, label string }{{"description", "需求描述"}, {"acceptance_criteria", "验收准则"}, {"source", "需求来源"}, {"acceptance_note", "验收说明"}, {"change_note", "本次变更说明"}} {
			fmt.Fprintf(&b, "\n## %s\n\n%s\n", field.label, text(entity[field.key]))
		}
		b.WriteString("\n## 关联任务\n\n")
		for _, task := range exportObjects(entity["linked_tasks"]) {
			recycled := ""
			if nonempty(task["deleted_at"]) {
				recycled = "（已回收）"
			}
			fmt.Fprintf(&b, "- %s %s%s（任务 ID：%s）\n", text(task["wbs_code"]), text(task["name"]), recycled, text(task["id"]))
		}
		content = b.String()
	}
	if kind == "plan_versions" {
		scope = "项目计划版本"
		title = projectName + " · " + text(entity["name"])
		person = text(entity["created_by"])
		date = strings.SplitN(text(entity["created_at"]), "T", 2)[0]
		var b strings.Builder
		fmt.Fprintf(&b, "# %s\n\n计划版本：%s\n保存人：%s\n保存时间：%s\n计划任务数：%s\n内容 SHA-256：%s\n\n## 版本说明\n\n%s\n\n## 完整计划\n\n", title, text(entity["number"]), person, text(entity["created_at"]), text(entity["task_count"]), text(entity["hash"]), text(entity["description"]))
		if source, ok := entity["snapshot"].(map[string]any); ok {
			for _, task := range exportObjects(source["tasks"]) {
				fmt.Fprintf(&b, "### %s %s\n\n任务 ID：%s\n负责人：%s\n计划开始：%s\n计划结束：%s\n预计工时：%s 小时\n父任务 ID：%s\n\n%s\n\n", text(task["wbs_code"]), text(task["name"]), text(task["id"]), text(task["assignee"]), text(task["plan_start"]), text(task["plan_end"]), text(task["estimate_hours"]), text(task["parent_id"]), text(task["description"]))
			}
		}
		content = b.String()
	}
	rows := []map[string]any{}
	add := func(key, label string, value any) {
		rows = append(rows, map[string]any{"key": key, "label": label, "value": value})
	}
	add("source_kind", "来源类型", kind)
	add("source_id", "来源 ID", id)
	add("source_version", "来源版本", entity["version"])
	add("project_name", "项目", projectName)
	add("person_name", "负责人/录入人", person)
	if period != "" {
		add("period_type", "周期", period)
	}
	if date != "" {
		add("log_date", "日期", date)
	}
	if kind == "meeting_minutes" {
		for _, field := range []struct{ key, label string }{{"code", "会议编号"}, {"meeting_date", "会议日期"}, {"location", "会议地点"}, {"attendees", "参会人员"}} {
			add(field.key, field.label, entity[field.key])
		}
	}
	if kind == "issues" {
		for _, field := range []struct{ key, label string }{{"code", "问题编号"}, {"status", "状态"}, {"severity", "等级"}, {"owner", "负责人"}, {"due_at", "截止日期"}, {"closed_at", "关闭时间"}} {
			add(field.key, field.label, entity[field.key])
		}
	}
	if kind == "requirements" {
		for _, field := range []struct{ key, label string }{{"code", "需求编号"}, {"revision", "需求版本"}, {"status", "状态"}, {"owner", "负责人"}, {"source", "需求来源"}, {"acceptance_criteria", "验收准则"}, {"acceptance_note", "验收说明"}, {"change_note", "变更说明"}} {
			add(field.key, field.label, entity[field.key])
		}
	}
	if kind == "plan_versions" {
		for _, field := range []struct{ key, label string }{{"number", "计划版本"}, {"name", "版本名称"}, {"description", "版本说明"}, {"created_by", "保存人"}, {"task_count", "任务数量"}, {"hash", "计划内容 SHA-256"}} {
			add(field.key, field.label, entity[field.key])
		}
	}
	add("created_at", "创建时间", entity["created_at"])
	add("updated_at", "更新时间", entity["updated_at"])
	snapshot := map[string]any{"snapshot_format": 1, "source_kind": kind, "source_id": id, "source_version": entity["version"], "title": strings.TrimSpace(title), "content": content, "project_id": text(entity["project_id"]), "project_name": projectName, "project": project, "person_name": person, "entered_by": enteredBy, "export_operator": operator(q), "period_type": period, "log_date": date, "scope": scope, "fields": entity, "rows": rows, "captured_at": now()}
	if kind == "project_daily_logs" {
		snapshot["source_snapshot"] = entity["source_snapshot"]
	}
	if kind == "plan_versions" {
		snapshot["source_snapshot"] = entity["snapshot"]
	}
	return snapshot, nil
}
func (s *Store) ExportSnapshot(kind, id string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return exportSnapshot(s.DB, kind, id)
}
func shaValid(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == 32
}
func job(q queryer, id string) (map[string]any, error) {
	rows, err := q.Query("SELECT * FROM export_jobs WHERE id=?", id)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, fail("not_found", "导出任务不存在", 404)
	}
	m := items[0]
	var snapshot map[string]any
	if err = json.Unmarshal([]byte(text(m["source_snapshot"])), &snapshot); err != nil || snapshot == nil {
		return nil, fmt.Errorf("导出任务来源快照损坏")
	}
	m["source_snapshot"] = snapshot
	return m, nil
}
func (s *Store) CreateExportJob(input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		for key := range input {
			if !oneOf(key, "format", "source_kind", "source_id", "source_version", "template_id", "template_hash") {
				return nil, invalid("未知导出字段: " + key)
			}
		}
		format, err := inputText(input, "format", 10, false)
		if err != nil {
			return nil, err
		}
		if !oneOf(format, "docx", "md", "json", "xlsx", "html") {
			return nil, invalid("导出格式须为 docx、md、json、xlsx 或 html")
		}
		kind, err := inputText(input, "source_kind", 50, false)
		if err != nil {
			return nil, err
		}
		id, err := inputText(input, "source_id", 200, false)
		if err != nil || id == "" {
			return nil, invalid("请选择已保存的导出来源")
		}
		snapshot, err := exportSnapshot(tx, kind, id)
		if err != nil {
			return nil, err
		}
		if err = checkVersion(map[string]any{"version": snapshot["source_version"]}, input["source_version"]); err != nil {
			return nil, err
		}
		templateID, err := inputText(input, "template_id", 200, true)
		if err != nil {
			return nil, err
		}
		templateHash, err := inputText(input, "template_hash", 64, true)
		if err != nil {
			return nil, err
		}
		if templateHash != "" && !shaValid(templateHash) {
			return nil, invalid("模板哈希须为 SHA256")
		}
		if strings.ContainsFunc(templateID, unicode.IsControl) {
			return nil, invalid("模板标识含无效字符")
		}
		raw, err := json.Marshal(snapshot)
		if err != nil {
			return nil, err
		}
		jobID, at := uid(), now()
		_, err = tx.Exec("INSERT INTO export_jobs(id,state,format,source_kind,source_id,source_version,template_id,template_hash,source_snapshot,created_at,updated_at)VALUES(?,'queued',?,?,?,?,?,?,?,?,?)", jobID, format, snapshot["source_kind"], id, snapshot["source_version"], templateID, strings.ToLower(templateHash), string(raw), at, at)
		if err != nil {
			return nil, err
		}
		m, err := job(tx, jobID)
		if err != nil {
			return nil, err
		}
		if err = audit(tx, "export_jobs", jobID, "create", nil, m); err != nil {
			return nil, err
		}
		return m, nil
	})
}
func (s *Store) GetExportJob(id string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	return job(s.DB, id)
}
func (s *Store) ListExportJobs(filters map[string]string) (map[string]any, error) {
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	where := " WHERE 1=1"
	args := []any{}
	for _, key := range []string{"state", "format", "source_kind", "source_id"} {
		if value := filters[key]; value != "" {
			if key == "state" && !oneOf(value, "queued", "running", "succeeded", "failed") {
				return nil, invalid("导出任务状态无效")
			}
			if key == "format" && !oneOf(value, "docx", "md", "json", "xlsx", "html") {
				return nil, invalid("导出格式无效")
			}
			if key == "source_kind" {
				var err error
				value, err = exportKind(value)
				if err != nil {
					return nil, err
				}
			}
			where += " AND " + key + "=?"
			args = append(args, value)
		}
	}
	var total int
	if err := s.DB.QueryRow("SELECT count(*) FROM export_jobs"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	limit := pageLimit(filters, 30)
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	args = append(args, limit, offset)
	rows, err := s.DB.Query("SELECT id,state,format,source_kind,source_id,source_version,template_id,template_hash,output_name,output_sha256,size,error,created_at,updated_at,version FROM export_jobs"+where+" ORDER BY created_at DESC,id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	return map[string]any{"items": items, "total": total, "next_cursor": ""}, err
}
func (s *Store) ClaimExportJob(id string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		for key := range input {
			if key != "version" {
				return nil, invalid("未知任务认领字段: " + key)
			}
		}
		before, err := job(tx, id)
		if err != nil {
			return nil, err
		}
		if err = checkVersion(before, input["version"]); err != nil {
			return nil, err
		}
		if text(before["state"]) != "queued" {
			return nil, fail("export_state_conflict", "仅待处理导出可以开始", 409)
		}
		if _, err = tx.Exec("UPDATE export_jobs SET state='running',updated_at=?,version=version+1 WHERE id=? AND state='queued' AND version=?", now(), id, before["version"]); err != nil {
			return nil, err
		}
		after, err := job(tx, id)
		if err != nil {
			return nil, err
		}
		if err = audit(tx, "export_jobs", id, "start", before, after); err != nil {
			return nil, err
		}
		return after, nil
	})
}
func safeOutputName(name, format string) bool {
	return name != "" && len([]byte(name)) <= 240 && name == filepath.Base(name) && !strings.ContainsAny(name, "/\\:") && !strings.ContainsFunc(name, unicode.IsControl) && strings.EqualFold(filepath.Ext(name), "."+format)
}
func (s *Store) CompleteExportJob(id string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		for key := range input {
			if !oneOf(key, "version", "output_name", "output_sha256", "size") {
				return nil, invalid("未知导出结果字段: " + key)
			}
		}
		before, err := job(tx, id)
		if err != nil {
			return nil, err
		}
		if err = checkVersion(before, input["version"]); err != nil {
			return nil, err
		}
		if text(before["state"]) != "running" {
			return nil, fail("export_state_conflict", "仅进行中的导出可以完成", 409)
		}
		name, err := inputText(input, "output_name", 240, false)
		if err != nil || !safeOutputName(name, text(before["format"])) {
			return nil, invalid("导出文件名必须是匹配格式的安全文件名")
		}
		hash, err := inputText(input, "output_sha256", 64, false)
		if err != nil || !shaValid(hash) {
			return nil, invalid("导出文件哈希必须是 SHA256")
		}
		size, ok := numeric(input["size"])
		if !ok || size < 1 || size > float64(1<<53) || size != math.Trunc(size) {
			return nil, invalid("导出文件大小必须是正整数")
		}
		if _, err = tx.Exec("UPDATE export_jobs SET state='succeeded',output_name=?,output_sha256=?,size=?,error='',updated_at=?,version=version+1 WHERE id=?", name, strings.ToLower(hash), int64(size), now(), id); err != nil {
			return nil, err
		}
		after, err := job(tx, id)
		if err != nil {
			return nil, err
		}
		if err = audit(tx, "export_jobs", id, "complete", before, after); err != nil {
			return nil, err
		}
		return after, nil
	})
}
func (s *Store) FailExportJob(id string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		for key := range input {
			if !oneOf(key, "version", "error") {
				return nil, invalid("未知导出失败字段: " + key)
			}
		}
		before, err := job(tx, id)
		if err != nil {
			return nil, err
		}
		if err = checkVersion(before, input["version"]); err != nil {
			return nil, err
		}
		if !oneOf(before["state"], "queued", "running") {
			return nil, fail("export_state_conflict", "已结束的导出不能改写结果", 409)
		}
		message, err := inputText(input, "error", 4000, false)
		if err != nil || strings.TrimSpace(message) == "" {
			return nil, invalid("导出失败必须提供原因")
		}
		if _, err = tx.Exec("UPDATE export_jobs SET state='failed',error=?,updated_at=?,version=version+1 WHERE id=?", strings.TrimSpace(message), now(), id); err != nil {
			return nil, err
		}
		after, err := job(tx, id)
		if err != nil {
			return nil, err
		}
		if err = audit(tx, "export_jobs", id, "fail", before, after); err != nil {
			return nil, err
		}
		return after, nil
	})
}
func (s *Store) InterruptExportJobs() error {
	_, err := s.write(func(tx *sql.Tx) (map[string]any, error) {
		rows, err := tx.Query("SELECT id FROM export_jobs WHERE state IN('queued','running') ORDER BY id")
		if err != nil {
			return nil, err
		}
		items, err := rawRows(rows)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			id := text(item["id"])
			before, e := job(tx, id)
			if e != nil {
				return nil, e
			}
			if _, err = tx.Exec("UPDATE export_jobs SET state='failed',error='服务中断，请重新导出',updated_at=?,version=version+1 WHERE id=?", now(), id); err != nil {
				return nil, err
			}
			after, e := job(tx, id)
			if e != nil {
				return nil, e
			}
			if err = audit(tx, "export_jobs", id, "interrupt", before, after); err != nil {
				return nil, err
			}
		}
		return map[string]any{"total": len(items)}, nil
	})
	return err
}

// InvalidateExportJob is used after the runtime verifies that a previously
// successful generated artifact is absent or corrupt. The source remains frozen;
// the invalid output is no longer advertised as downloadable success.
func (s *Store) InvalidateExportJob(id string, input map[string]any) (map[string]any, error) {
	return s.write(func(tx *sql.Tx) (map[string]any, error) {
		for key := range input {
			if !oneOf(key, "version", "error") {
				return nil, invalid("未知导出文件失效字段: " + key)
			}
		}
		before, err := job(tx, id)
		if err != nil {
			return nil, err
		}
		if err = checkVersion(before, input["version"]); err != nil {
			return nil, err
		}
		if text(before["state"]) != "succeeded" {
			return nil, fail("export_state_conflict", "仅成功导出的缺失或损坏文件可以标记失效", 409)
		}
		message, err := inputText(input, "error", 4000, false)
		if err != nil || strings.TrimSpace(message) == "" {
			return nil, invalid("导出文件失效必须提供原因")
		}
		if _, err = tx.Exec("UPDATE export_jobs SET state='failed',output_name='',output_sha256='',size=0,error=?,updated_at=?,version=version+1 WHERE id=?", strings.TrimSpace(message), now(), id); err != nil {
			return nil, err
		}
		after, err := job(tx, id)
		if err != nil {
			return nil, err
		}
		if err = audit(tx, "export_jobs", id, "artifact_unavailable", before, after); err != nil {
			return nil, err
		}
		return after, nil
	})
}

func exportObjects(value any) []map[string]any {
	switch items := value.(type) {
	case []map[string]any:
		return items
	case []any:
		out := make([]map[string]any, 0, len(items))
		for _, item := range items {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}
