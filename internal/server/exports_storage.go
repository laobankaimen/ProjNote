package server

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"projnote/internal/exporter"
	"projnote/internal/store"
)

const exportCleanupMessage = "用户已清理导出文件，来源快照保留"

func managedExportName(name string) bool {
	if len(name) < 35 || name[32] != '.' || !exportID.MatchString(name[:32]) {
		return false
	}
	switch name[33:] {
	case "docx", "md", "json", "xlsx", "html":
		return true
	}
	return false
}
func exportArtifactName(job map[string]any) (string, error) {
	id, _ := job["id"].(string)
	format, _ := job["format"].(string)
	name, _ := job["output_name"].(string)
	if !exportID.MatchString(id) || !managedExportName(name) || name != id+"."+format {
		return "", errors.New("导出结果名称无效")
	}
	return name, nil
}
func (s *Server) exportArtifactDir(create bool) (string, error) {
	dir := filepath.Join(s.DataDir, "exports")
	if create {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return "", err
		}
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("导出结果目录不是普通目录")
	}
	return dir, nil
}
func exportInt(value any) (int64, bool) {
	switch n := value.(type) {
	case int:
		return int64(n), true
	case int64:
		return n, true
	case float64:
		if !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0 && n < float64(math.MaxInt64) && math.Trunc(n) == n {
			return int64(n), true
		}
	}
	return 0, false
}
func readExportArtifact(path string, job map[string]any) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return nil, errors.New("导出文件不存在或无效")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, errors.New("导出文件已发生变化")
	}
	data, err := io.ReadAll(io.LimitReader(file, (64<<20)+1))
	if err != nil {
		return nil, err
	}
	size, ok := exportInt(job["size"])
	if !ok || int64(len(data)) != size || len(data) > 64<<20 || exporter.Digest(data) != job["output_sha256"] {
		return nil, errors.New("导出文件大小或哈希不一致")
	}
	return data, nil
}
func (s *Server) exportsStorageAPI(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 1 && r.Method == "GET" {
		result, err := s.exportStorageStats()
		if err != nil {
			handleErr(w, err)
		} else {
			jsonResult(w, 200, result)
		}
		return
	}
	if len(p) == 2 && p[1] == "cleanup-preview" && r.Method == "POST" {
		input, ok := body(w, r)
		if !ok {
			return
		}
		days, kind, err := cleanupPreviewInput(input)
		if err != nil {
			writeError(w, 400, "invalid_input", err.Error())
			return
		}
		result, err := s.previewExportCleanup(days, kind, time.Now().UTC())
		if err != nil {
			handleErr(w, err)
		} else {
			jsonResult(w, 200, result)
		}
		return
	}
	if len(p) == 2 && p[1] == "cleanup" && r.Method == "POST" {
		input, ok := body(w, r)
		if !ok {
			return
		}
		items, err := cleanupItems(input)
		if err != nil {
			writeError(w, 400, "invalid_input", err.Error())
			return
		}
		jsonResult(w, 200, s.cleanupExportArtifacts(items))
		return
	}
	writeError(w, 404, "not_found", "接口不存在或请求方法不正确")
}
func (s *Server) exportStorageStats() (map[string]any, error) {
	s.exports.artifacts.Lock()
	defer s.exports.artifacts.Unlock()
	files, bytes := 0, int64(0)
	dir, err := s.exportArtifactDir(false)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if !managedExportName(entry.Name()) || entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			info, err := entry.Info()
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if info.Mode().IsRegular() {
				files++
				bytes += info.Size()
			}
		}
	}
	counts := map[string]int{"queued": 0, "running": 0, "succeeded": 0, "failed": 0}
	s.Store.Mu.RLock()
	defer s.Store.Mu.RUnlock()
	rows, err := s.Store.DB.Query("SELECT state,count(*) FROM export_jobs GROUP BY state")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		var state string
		var count int
		if err = rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		counts[state] = count
		total += count
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return map[string]any{"files": files, "bytes": bytes, "jobs": counts, "jobs_total": total}, nil
}
func cleanupPreviewInput(input map[string]any) (int, string, error) {
	for key := range input {
		if key != "days" && key != "source_kind" {
			return 0, "", fmt.Errorf("未知清理预览字段：%s", key)
		}
	}
	days, ok := exportInt(input["days"])
	if !ok || days < 0 || days > 365000 {
		return 0, "", errors.New("days 必须是 0 至 365000 的整数，0 表示全部成功结果")
	}
	kind := ""
	if raw, exists := input["source_kind"]; exists {
		var ok bool
		kind, ok = raw.(string)
		if !ok {
			return 0, "", errors.New("source_kind 必须是文本")
		}
		kind = strings.ReplaceAll(kind, "-", "_")
		if kind != "" {
			switch kind {
			case "worklogs", "project_daily_logs", "issues", "meeting_minutes", "requirements", "plan_versions", "requirement_revisions", "plan_comparisons":
			default:
				return 0, "", errors.New("来源类型无效")
			}
		}
	}
	return int(days), kind, nil
}
func (s *Server) previewExportCleanup(days int, kind string, at time.Time) (map[string]any, error) {
	s.exports.artifacts.Lock()
	defer s.exports.artifacts.Unlock()
	before := at.AddDate(0, 0, -days)
	items := []map[string]any{}
	bytes, skipped := int64(0), 0
	for offset := 0; ; offset += 500 {
		page, err := s.Store.ListExportJobs(map[string]string{"state": "succeeded", "source_kind": kind, "limit": "500", "offset": fmt.Sprint(offset)})
		if err != nil {
			return nil, err
		}
		jobs, _ := page["items"].([]map[string]any)
		for _, job := range jobs {
			updated, err := time.Parse(time.RFC3339Nano, fmt.Sprint(job["updated_at"]))
			if err != nil {
				skipped++
				continue
			}
			if days > 0 && !updated.Before(before) {
				continue
			}
			data, err := s.artifactLocked(job)
			if err != nil {
				skipped++
				continue
			}
			item := map[string]any{}
			for _, key := range []string{"id", "version", "output_name", "output_sha256", "size", "source_kind", "source_id", "format", "created_at", "updated_at"} {
				item[key] = job[key]
			}
			items = append(items, item)
			bytes += int64(len(data))
		}
		if len(jobs) < 500 {
			break
		}
	}
	return map[string]any{"days": days, "before": before.UTC().Format(time.RFC3339Nano), "source_kind": kind, "items": items, "total": len(items), "bytes": bytes, "skipped": skipped}, nil
}

type exportCleanupItem struct {
	id      string
	version int64
}

func cleanupItems(input map[string]any) ([]exportCleanupItem, error) {
	for key := range input {
		if key != "items" {
			return nil, fmt.Errorf("未知清理字段：%s", key)
		}
	}
	list, ok := input["items"].([]any)
	if !ok || len(list) == 0 || len(list) > 5000 {
		return nil, errors.New("items 必须是 1 至 5000 项的任务 ID 和版本数组")
	}
	items := make([]exportCleanupItem, 0, len(list))
	seen := map[string]bool{}
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, errors.New("清理项必须是对象")
		}
		for key := range m {
			if key != "id" && key != "version" {
				return nil, fmt.Errorf("未知清理项字段：%s", key)
			}
		}
		id, ok := m["id"].(string)
		if !ok || !exportID.MatchString(id) || seen[id] {
			return nil, errors.New("清理任务 ID 无效或重复")
		}
		version, ok := exportInt(m["version"])
		if !ok || version < 1 {
			return nil, errors.New("清理任务必须提供正整数版本")
		}
		items = append(items, exportCleanupItem{id, version})
		seen[id] = true
	}
	return items, nil
}
func (s *Server) cleanupExportArtifacts(items []exportCleanupItem) map[string]any {
	s.exportWorker.Lock()
	defer s.exportWorker.Unlock()
	results := []map[string]any{}
	cleaned, bytes := 0, int64(0)
	for _, item := range items {
		s.exports.artifacts.Lock()
		result := s.cleanupExportArtifactLocked(item)
		s.exports.artifacts.Unlock()
		results = append(results, result)
		if result["ok"] == true {
			cleaned++
			size, _ := exportInt(result["bytes"])
			bytes += size
		}
	}
	return map[string]any{"items": results, "total": len(items), "cleaned": cleaned, "bytes": bytes, "failed": len(items) - cleaned}
}
func cleanupError(id string, status int, code, message string) map[string]any {
	return map[string]any{"id": id, "ok": false, "status": status, "code": code, "error": message}
}
func cleanupStoreError(id string, err error) map[string]any {
	var app *store.AppError
	if errors.As(err, &app) {
		return cleanupError(id, app.Status, app.Code, app.Message)
	}
	slog.Error("export cleanup state update failed", "id", id, "error", err)
	return cleanupError(id, 500, "cleanup_failed", "清理状态未能保存，结果文件已保留，请查看运行日志")
}
func restoreStagedExport(stage, target string) error {
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return errors.New("原结果路径已有文件或无法检查，未覆盖该路径")
	}
	return os.Rename(stage, target)
}
func (s *Server) cleanupExportArtifactLocked(item exportCleanupItem) map[string]any {
	job, err := s.Store.GetExportJob(item.id)
	if err != nil {
		return cleanupStoreError(item.id, err)
	}
	version, _ := exportInt(job["version"])
	if version != item.version {
		return cleanupError(item.id, 409, "version_conflict", "导出任务已变化，请重新预览后清理")
	}
	if job["state"] != "succeeded" {
		return cleanupError(item.id, 409, "export_not_ready", "只能清理成功的导出结果，待处理、运行中和失败任务均保留")
	}
	data, err := s.artifactLocked(job)
	if err != nil {
		return cleanupError(item.id, 410, "artifact_unavailable", "导出结果缺失、名称无效或校验失败，未删除任何文件；请重新生成")
	}
	name, _ := exportArtifactName(job)
	dir, err := s.exportArtifactDir(false)
	if err != nil {
		return cleanupError(item.id, 500, "cleanup_failed", "导出目录无法访问，未清理结果")
	}
	target := filepath.Join(dir, name)
	original, err := os.Lstat(target)
	if err != nil {
		return cleanupError(item.id, 410, "artifact_unavailable", "导出结果已变化，未删除任何文件")
	}
	temp, err := os.CreateTemp(dir, ".cleanup-"+item.id+"-*")
	if err != nil {
		return cleanupError(item.id, 500, "cleanup_failed", "无法准备清理暂存文件，结果保留")
	}
	stage := temp.Name()
	if err = temp.Close(); err != nil {
		_ = os.Remove(stage)
		return cleanupError(item.id, 500, "cleanup_failed", "无法关闭清理暂存文件，结果保留")
	}
	if err = os.Rename(target, stage); err != nil {
		_ = os.Remove(stage)
		return cleanupError(item.id, 500, "cleanup_failed", "无法暂存导出结果，未清理文件")
	}
	// Recheck the moved object: an external filesystem change between the read
	// and rename must never cause an unrelated object to be removed.
	staged, statErr := os.Lstat(stage)
	_, readErr := readExportArtifact(stage, job)
	if statErr != nil || !os.SameFile(original, staged) || readErr != nil {
		if err = restoreStagedExport(stage, target); err != nil {
			return cleanupRollbackError(item.id, stage, err)
		}
		return cleanupError(item.id, 410, "artifact_changed", "导出结果在清理时发生变化，已退回原路径，未删除文件")
	}
	after, err := s.Store.InvalidateExportJob(item.id, map[string]any{"version": job["version"], "error": exportCleanupMessage})
	if err != nil {
		if rollbackErr := restoreStagedExport(stage, target); rollbackErr != nil {
			return cleanupRollbackError(item.id, stage, rollbackErr)
		}
		return cleanupStoreError(item.id, err)
	}
	if err = os.Remove(stage); err != nil {
		result := cleanupError(item.id, 500, "cleanup_incomplete", "任务已标记文件清理，但暂存文件未能删除；来源快照保留，请查看运行日志")
		result["invalidated"], result["recovery_name"] = true, filepath.Base(stage)
		slog.Error("export cleanup temporary removal failed", "id", item.id, "error", err)
		return result
	}
	return map[string]any{"id": item.id, "ok": true, "state": after["state"], "version": after["version"], "bytes": len(data), "error": after["error"]}
}
func cleanupRollbackError(id, stage string, err error) map[string]any {
	slog.Error("export cleanup rollback failed", "id", id, "staging_name", filepath.Base(stage), "error", err)
	result := cleanupError(id, 500, "cleanup_rollback_failed", "清理未完成，文件保留在受控暂存位置；请查看运行日志后恢复文件")
	result["recovery_name"] = filepath.Base(stage)
	return result
}
