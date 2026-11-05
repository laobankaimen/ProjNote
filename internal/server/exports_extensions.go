package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"projnote/internal/exporter"
	"projnote/internal/store"
)

func exportVersion(value any) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case float64:
		if v > 0 && v < math.MaxInt64 && math.Trunc(v) == v {
			return int64(v)
		}
	}
	return 0
}
func exportTemplate(root, format, id, expected string) (exporter.Template, []byte, error) {
	if format == "docx" {
		if id == "" {
			id = "builtin-journal"
		}
		meta, data, err := exporter.LoadTemplate(root, id)
		if err != nil {
			return meta, nil, err
		}
		if expected != "" && meta.SHA256 != expected {
			return meta, nil, &store.AppError{Code: "template_changed", Message: "原模板已变化，请明确选择模板后重新创建导出", Status: 409}
		}
		return meta, data, nil
	}
	name, ok := map[string]string{"md": "markdown", "json": "json", "xlsx": "excel", "html": "print-html"}[format]
	if !ok {
		return exporter.Template{}, nil, &store.AppError{Code: "invalid_format", Message: "请选择有效的导出格式", Status: 400}
	}
	meta := exporter.Template{ID: "builtin-" + name}
	meta.SHA256 = exporter.Digest([]byte("ProjNote " + meta.ID + " source snapshot renderer v1"))
	if expected != "" && expected != meta.SHA256 {
		return meta, nil, &store.AppError{Code: "renderer_changed", Message: "原导出布局已变化，请重新选择导出格式", Status: 409}
	}
	return meta, nil, nil
}
func (s *Server) queueFrozenExport(snapshot map[string]any, format, templateID, templateHash string) (map[string]any, error) {
	s.Store.Mu.RLock()
	meta, data, err := exportTemplate(s.DataDir, format, templateID, templateHash)
	s.Store.Mu.RUnlock()
	if err != nil {
		return nil, err
	}
	s.exports.mu.Lock()
	defer s.exports.mu.Unlock()
	if s.exports.closing {
		return nil, &store.AppError{Code: "shutting_down", Message: "服务正在退出，请稍后再试", Status: 503}
	}
	job, err := s.Store.CreateFrozenExportJob(map[string]any{"format": format, "template_id": meta.ID, "template_hash": meta.SHA256}, snapshot)
	if err == nil {
		s.exports.wg.Add(1)
		go func() { defer s.exports.wg.Done(); s.runExport(job, data) }()
	}
	return job, err
}
func (s *Server) regenerateExport(w http.ResponseWriter, r *http.Request, id string) {
	input, ok := body(w, r)
	if !ok {
		return
	}
	for key := range input {
		if key != "version" {
			writeError(w, 400, "invalid_input", "仅接受原任务版本")
			return
		}
	}
	prior, err := s.Store.GetExportJob(id)
	if err != nil {
		handleErr(w, err)
		return
	}
	if exportVersion(input["version"]) == 0 || exportVersion(prior["version"]) != exportVersion(input["version"]) {
		writeError(w, 409, "version_conflict", "导出任务已变化，请刷新后重试")
		return
	}
	if prior["state"] != "failed" && prior["state"] != "succeeded" {
		writeError(w, 409, "export_state_conflict", "进行中的任务无需重新生成")
		return
	}
	// Roundtrip copies the persisted snapshot; source entities may have since
	// changed or been recycled. Neither lookup nor regeneration edits them.
	raw, _ := json.Marshal(prior["source_snapshot"])
	var snapshot map[string]any
	if json.Unmarshal(raw, &snapshot) != nil || snapshot == nil {
		writeError(w, 409, "invalid_snapshot", "原来源快照无法读取")
		return
	}
	job, err := s.queueFrozenExport(snapshot, fmt.Sprint(prior["format"]), fmt.Sprint(prior["template_id"]), fmt.Sprint(prior["template_hash"]))
	resourceResult(w, job, err, 201)
}
func (s *Server) exportPlanComparison(w http.ResponseWriter, r *http.Request, pid string) {
	input, ok := body(w, r)
	if !ok {
		return
	}
	for key := range input {
		switch key {
		case "from", "to", "format", "template_id", "template_hash", "from_hash", "to_hash":
		default:
			writeError(w, 400, "invalid_input", "未知计划比较导出字段："+key)
			return
		}
	}
	from, _ := input["from"].(string)
	to, _ := input["to"].(string)
	if from == "" || from == "current" {
		writeError(w, 400, "version_required", "请选择已保存的起点计划版本")
		return
	}
	if to == "current" {
		to = ""
	}
	comparison, err := s.Store.CapturePlanComparison(pid, from, to)
	if err != nil {
		handleErr(w, err)
		return
	}
	left, right := comparison["from"].(map[string]any), comparison["to"].(map[string]any)
	for key, side := range map[string]map[string]any{"from_hash": left, "to_hash": right} {
		if expected, exists := input[key]; exists && fmt.Sprint(expected) != fmt.Sprint(side["hash"]) {
			writeError(w, 409, "comparison_changed", "计划比较来源已变化，请重新比较后再导出")
			return
		}
	}
	leftSnapshot := comparison["from_snapshot"].(map[string]any)
	rightSnapshot := comparison["to_snapshot"].(map[string]any)
	project := rightSnapshot["project"].(map[string]any)
	name := fmt.Sprint(project["name"])
	label := func(side map[string]any) string {
		if side["current"] == true {
			return "当前计划"
		}
		return fmt.Sprint(side["number"]) + " · " + fmt.Sprint(side["name"])
	}
	title := name + " · 计划比较"
	var content strings.Builder
	fmt.Fprintf(&content, "# %s\n\n原计划：%s\n新计划：%s\n\n", title, label(left), label(right))
	summary := comparison["summary"].(map[string]any)
	fmt.Fprintf(&content, "新增 %v 项，移除 %v 项，修改 %v 项，移动 %v 项，调序 %v 项。\n", summary["added"], summary["removed"], summary["changed"], summary["moved"], summary["reordered"])
	for _, section := range []struct{ key, label string }{{"added", "新增任务"}, {"removed", "移除任务"}} {
		fmt.Fprintf(&content, "\n## %s\n\n", section.label)
		for _, task := range objectList(comparison[section.key]) {
			fmt.Fprintf(&content, "- %v %v（任务 ID：%v）\n", task["wbs_code"], task["name"], task["id"])
		}
	}
	content.WriteString("\n## 修改明细\n\n")
	labels := map[string]string{"parent_id": "上级任务", "sort_order": "同级顺序", "wbs_code": "WBS编码", "name": "任务名称", "plan_start": "计划开始", "plan_end": "计划结束", "assignee": "负责人", "estimate_hours": "预计工时", "priority": "优先级", "description": "任务说明"}
	for _, change := range objectList(comparison["changed"]) {
		after, _ := change["after"].(map[string]any)
		fmt.Fprintf(&content, "### %v %v\n\n", after["wbs_code"], after["name"])
		for _, field := range objectList(change["fields"]) {
			key := fmt.Sprint(field["field"])
			label := labels[key]
			if label == "" {
				label = key
			}
			fmt.Fprintf(&content, "- %s：%v → %v\n", label, field["before"], field["after"])
		}
	}
	var b [16]byte
	if _, err = rand.Read(b[:]); err != nil {
		handleErr(w, err)
		return
	}
	rows := []map[string]any{{"key": "from", "label": "原计划", "value": label(left)}, {"key": "to", "label": "新计划", "value": label(right)}, {"key": "from_hash", "label": "原计划 SHA-256", "value": left["hash"]}, {"key": "to_hash", "label": "新计划 SHA-256", "value": right["hash"]}}
	for _, key := range []string{"added", "removed", "changed", "moved", "reordered"} {
		rows = append(rows, map[string]any{"key": key, "label": key, "value": summary[key]})
	}
	settings, err := s.Store.Settings()
	if err != nil {
		handleErr(w, err)
		return
	}
	snapshot := map[string]any{"snapshot_format": 1, "source_kind": "plan_comparisons", "source_id": hex.EncodeToString(b[:]), "source_version": 1, "title": title, "content": content.String(), "project_id": pid, "project_name": name, "project": project, "person_name": settings["operator"], "export_operator": settings["operator"], "log_date": time.Now().Format("2006-01-02"), "scope": "项目计划比较", "fields": comparison, "source_snapshot": map[string]any{"from": leftSnapshot, "to": rightSnapshot}, "rows": rows, "captured_at": time.Now().UTC().Format(time.RFC3339Nano)}
	format, _ := input["format"].(string)
	template, _ := input["template_id"].(string)
	hash, _ := input["template_hash"].(string)
	job, err := s.queueFrozenExport(snapshot, format, template, hash)
	resourceResult(w, job, err, 201)
}
func objectList(value any) []map[string]any {
	if items, ok := value.([]map[string]any); ok {
		return items
	}
	out := []map[string]any{}
	if items, ok := value.([]any); ok {
		for _, item := range items {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
	}
	return out
}
