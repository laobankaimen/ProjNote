package server

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"projnote/internal/exporter"
)

var exportID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type exportRuntime struct {
	mu      sync.Mutex
	wg      sync.WaitGroup
	closing bool
}

func (s *Server) initExports() error {
	if err := s.Store.InterruptExportJobs(); err != nil {
		return err
	}
	offset := 0
	for {
		page, err := s.Store.ListExportJobs(map[string]string{"state": "succeeded", "limit": "100", "offset": fmt.Sprint(offset)})
		if err != nil {
			return err
		}
		items, _ := page["items"].([]map[string]any)
		if len(items) == 0 {
			break
		}
		invalidated := 0
		for _, job := range items {
			if _, err = s.artifact(job); err != nil {
				if _, e := s.Store.InvalidateExportJob(job["id"].(string), map[string]any{"version": job["version"], "error": "导出结果文件缺失或校验失败，请重新导出；完整备份保留数据来源和模板，不包含生成结果"}); e != nil {
					return e
				}
				invalidated++
			}
		}
		offset += len(items) - invalidated
	}
	return nil
}

// InitializeExports reconciles persisted jobs before the HTTP listener serves.
func (s *Server) InitializeExports() error { return s.initExports() }
func (s *Server) CloseExports() {
	s.exports.mu.Lock()
	s.exports.closing = true
	s.exports.mu.Unlock()
	s.exports.wg.Wait()
}
func (s *Server) exportsAPI(w http.ResponseWriter, r *http.Request, p []string) {
	if p[0] == "export-templates" {
		s.templatesAPI(w, r, p)
		return
	}
	if len(p) == 1 && r.Method == "GET" {
		page, e := s.Store.ListExportJobs(filtersOf(r))
		if e != nil {
			handleErr(w, e)
		} else {
			jsonResult(w, 200, page)
		}
		return
	}
	if len(p) == 1 && r.Method == "POST" {
		input, ok := body(w, r)
		if !ok {
			return
		}
		format, _ := input["format"].(string)
		if format != "docx" && format != "md" && format != "json" && format != "xlsx" && format != "html" {
			writeError(w, 400, "invalid_format", "导出格式必须为docx、md、json、xlsx或html")
			return
		}
		for key := range input {
			switch key {
			case "format", "source_kind", "source_id", "source_version", "template_id", "template_hash":
			default:
				writeError(w, 400, "invalid_input", "未知导出字段："+key)
				return
			}
		}
		id, _ := input["template_id"].(string)
		var template []byte
		var meta exporter.Template
		var e error
		if format == "docx" {
			if id == "" {
				if input["source_kind"] == "issues" {
					id = "builtin-issue"
				} else {
					id = "builtin-journal"
				}
			}
			s.Store.Mu.RLock()
			meta, template, e = exporter.LoadTemplate(s.DataDir, id)
			s.Store.Mu.RUnlock()
			if e != nil {
				writeError(w, 400, "invalid_template", e.Error())
				return
			}
		} else {
			meta.ID = "builtin-" + map[string]string{"md": "markdown", "json": "json", "xlsx": "excel", "html": "print-html"}[format]
			meta.SHA256 = exporter.Digest([]byte("ProjNote " + meta.ID + " source snapshot renderer v1"))
		}
		if hash, ok := input["template_hash"]; ok && hash != "" && hash != meta.SHA256 {
			writeError(w, 409, "template_changed", "模板已发生变化，请重新选择模板后创建任务")
			return
		}

		payload := map[string]any{"format": format, "source_kind": input["source_kind"], "source_id": input["source_id"], "source_version": input["source_version"], "template_id": meta.ID, "template_hash": meta.SHA256}
		s.exports.mu.Lock()
		if s.exports.closing {
			s.exports.mu.Unlock()
			writeError(w, 503, "shutting_down", "服务正在退出，请稍后重试")
			return
		}
		job, e := s.Store.CreateExportJob(payload)
		if e == nil {
			s.exports.wg.Add(1)
			go func() { defer s.exports.wg.Done(); s.runExport(job, template) }()
		}
		s.exports.mu.Unlock()
		if e != nil {
			handleErr(w, e)
		} else {
			jsonResult(w, 201, job)
		}
		return
	}
	if len(p) >= 2 && r.Method == "GET" {
		job, e := s.Store.GetExportJob(p[1])
		if e != nil {
			handleErr(w, e)
			return
		}
		if len(p) == 2 {
			jsonResult(w, 200, job)
			return
		}
		if len(p) == 3 && (p[2] == "download" || p[2] == "print") {
			if p[2] == "print" && job["format"] != "html" {
				writeError(w, 400, "not_print_report", "请先生成打印稿后再打开")
				return
			}
			if job["state"] != "succeeded" {
				writeError(w, 409, "export_not_ready", "导出尚未完成或已失败，请查看任务状态")
				return
			}
			data, e := s.artifact(job)
			if e != nil {
				_, _ = s.Store.InvalidateExportJob(p[1], map[string]any{"version": job["version"], "error": "导出结果文件缺失或哈希不符，请重新导出"})
				writeError(w, 410, "artifact_unavailable", "结果文件缺失或校验失败，请重新导出")
				return
			}
			format := fmt.Sprint(job["format"])
			contentType := map[string]string{"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "md": "text/markdown; charset=utf-8", "json": "application/json; charset=utf-8", "xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "html": "text/html; charset=utf-8"}[format]
			w.Header().Set("Content-Type", contentType)
			disposition := "attachment"
			if p[2] == "print" {
				disposition = "inline"
			}
			w.Header().Set("Content-Disposition", mime.FormatMediaType(disposition, map[string]string{"filename": "ProjNote-" + p[1] + "." + format}))
			_, _ = w.Write(data)
			return
		}
	}
	writeError(w, 404, "not_found", "接口不存在或请求方法不正确")
}
func (s *Server) artifact(job map[string]any) ([]byte, error) {
	id, _ := job["id"].(string)
	format, _ := job["format"].(string)
	name, _ := job["output_name"].(string)
	if !exportID.MatchString(id) || name != id+"."+format {
		return nil, errors.New("导出结果名称无效")
	}
	path := filepath.Join(s.DataDir, "exports", name)
	info, e := os.Lstat(path)
	if e != nil || !info.Mode().IsRegular() || info.Size() > 64<<20 {
		return nil, errors.New("导出文件不存在或无效")
	}
	b, e := os.ReadFile(path)
	if e != nil {
		return nil, e
	}
	if exporter.Digest(b) != job["output_sha256"] {
		return nil, errors.New("导出文件哈希不一致")
	}
	return b, nil
}
func (s *Server) runExport(job map[string]any, template []byte) {
	s.exportWorker.Lock()
	defer s.exportWorker.Unlock()
	id := job["id"].(string)
	running, e := s.Store.ClaimExportJob(id, map[string]any{"version": job["version"]})
	if e != nil {
		return
	}
	fail := func(e error) {
		_, _ = s.Store.FailExportJob(id, map[string]any{"version": running["version"], "error": e.Error()})
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			fail(errors.New("导出处理异常，请检查模板或重新导出"))
		}
	}()
	snapshot, ok := running["source_snapshot"].(map[string]any)
	if !ok {
		fail(errors.New("导出来源快照无效"))
		return
	}
	format := fmt.Sprint(running["format"])
	var data []byte
	switch format {
	case "docx":
		data, e = exporter.Render(template, snapshot)
	case "xlsx":
		data, e = exporter.RenderXLSX(snapshot)
	case "html":
		data, e = exporter.RenderHTML(snapshot)
	case "md":
		data = []byte(fmt.Sprint(snapshot["content"]))
	case "json":
		data, e = json.MarshalIndent(snapshot, "", "  ")
	}
	if e != nil {
		fail(e)
		return
	}
	if len(data) > 64<<20 {
		fail(errors.New("导出结果超过64MB"))
		return
	}
	dir := filepath.Join(s.DataDir, "exports")
	if e = os.MkdirAll(dir, 0700); e != nil {
		fail(e)
		return
	}
	temp, e := os.CreateTemp(dir, ".export-*")
	if e != nil {
		fail(e)
		return
	}
	tmp := temp.Name()
	defer os.Remove(tmp)
	if _, e = temp.Write(data); e == nil {
		e = temp.Sync()
	}
	closeErr := temp.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		fail(e)
		return
	}
	name := id + "." + format
	target := filepath.Join(dir, name)
	if e = os.Rename(tmp, target); e != nil {
		fail(e)
		return
	}
	_, e = s.Store.CompleteExportJob(id, map[string]any{"version": running["version"], "output_name": name, "output_sha256": exporter.Digest(data), "size": len(data)})
	if e != nil {
		_ = os.Remove(target)
		fail(e)
	}
}
func (s *Server) templatesAPI(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 1 && r.Method == "GET" {
		s.Store.Mu.RLock()
		items, e := exporter.ListTemplates(s.DataDir)
		s.Store.Mu.RUnlock()
		if e != nil {
			writeError(w, 500, "template_unavailable", e.Error())
		} else {
			jsonResult(w, 200, map[string]any{"items": items, "total": len(items)})
		}
		return
	}
	if len(p) == 1 && r.Method == "POST" {
		r.Body = http.MaxBytesReader(w, r.Body, exporter.MaxTemplate+(1<<20))
		if e := r.ParseMultipartForm(exporter.MaxTemplate); e != nil {
			writeError(w, 400, "invalid_upload", "请上传不超过8MB的.docx模板")
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, header, e := r.FormFile("file")
		if e != nil {
			writeError(w, 400, "file_required", "请选择Word模板")
			return
		}
		defer file.Close()
		if !strings.EqualFold(filepath.Ext(header.Filename), ".docx") {
			writeError(w, 400, "invalid_template", "模板须为.docx格式")
			return
		}
		data, e := io.ReadAll(io.LimitReader(file, exporter.MaxTemplate+1))
		if e != nil || len(data) > exporter.MaxTemplate {
			writeError(w, 400, "invalid_template", "模板读取失败或超过8MB")
			return
		}
		random := make([]byte, 16)
		if _, e = rand.Read(random); e != nil {
			handleErr(w, e)
			return
		}
		id := hex.EncodeToString(random)
		name := strings.TrimSpace(r.FormValue("name"))
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename))
		}
		if len([]rune(name)) > 120 || strings.ContainsAny(name, "\r\n\x00") {
			writeError(w, 400, "invalid_template", "模板名称过长或含无效字符")
			return
		}
		s.Store.Mu.Lock()
		meta, e := exporter.SaveTemplate(s.DataDir, id, name, data)
		s.Store.Mu.Unlock()
		if e != nil {
			writeError(w, 400, "invalid_template", e.Error())
			return
		}
		if e = s.Store.RecordEvent("template_upload", "export_templates", id, map[string]any{"name": name, "sha256": meta.SHA256, "fields": meta.Fields}); e != nil {
			_ = os.Remove(filepath.Join(s.DataDir, "templates", "docx", id+".docx"))
			_ = os.Remove(filepath.Join(s.DataDir, "templates", "docx", id+".json"))
			handleErr(w, e)
			return
		}
		jsonResult(w, 201, meta)
		return
	}
	if len(p) == 3 && p[2] == "download" && r.Method == "GET" {
		s.Store.Mu.RLock()
		meta, data, e := exporter.LoadTemplate(s.DataDir, p[1])
		s.Store.Mu.RUnlock()
		if e != nil {
			writeError(w, 404, "template_unavailable", e.Error())
			return
		}
		w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": meta.Name + ".docx"}))
		_, _ = io.Copy(w, bytes.NewReader(data))
		return
	}
	writeError(w, 404, "not_found", "接口不存在或请求方法不正确")
}
func (s *Server) desktopDraft(w http.ResponseWriter, r *http.Request) {
	var result map[string]any
	var e error
	switch r.Method {
	case "GET":
		result, e = s.Store.DesktopDraft()
	case "PUT":
		input, ok := body(w, r)
		if !ok {
			return
		}
		result, e = s.Store.SaveDesktopDraft(input)
	default:
		writeError(w, 404, "not_found", "接口不存在或请求方法不正确")
		return
	}
	if e != nil {
		handleErr(w, e)
	} else {
		jsonResult(w, 200, result)
	}
}
