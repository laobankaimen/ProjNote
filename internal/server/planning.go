package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"projnote/internal/planimport"
	"projnote/internal/store"
)

func (s *Server) importTasks(w http.ResponseWriter, r *http.Request, p []string) {
	if r.Method != "POST" {
		writeError(w, 404, "not_found", "接口不存在或请求方法不正确")
		return
	}
	if len(p) == 4 && p[3] == "preview" {
		r.Body = http.MaxBytesReader(w, r.Body, planimport.MaxFileBytes+(1<<20))
		if err := r.ParseMultipartForm(planimport.MaxFileBytes); err != nil {
			writeError(w, 400, "invalid_upload", "请上传不超过10MB的.xlsx文件")
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, 400, "file_required", "请选择Excel文件")
			return
		}
		defer file.Close()
		if !strings.EqualFold(filepath.Ext(header.Filename), ".xlsx") {
			writeError(w, 400, "unsupported_file", "当前支持.xlsx；旧版.xls请先另存为.xlsx")
			return
		}
		data, err := io.ReadAll(io.LimitReader(file, planimport.MaxFileBytes+1))
		if err != nil || len(data) > planimport.MaxFileBytes {
			writeError(w, 400, "invalid_upload", "Excel文件读取失败或超过10MB")
			return
		}
		parsed, err := planimport.Read(data)
		if err != nil {
			writeError(w, 400, "invalid_xlsx", err.Error())
			return
		}
		project, err := s.Store.Get("projects", p[1])
		if err != nil {
			handleErr(w, err)
			return
		}
		hash := sha256.Sum256(data)
		input := map[string]any{"rows": parsed.Rows, "source_hash": hex.EncodeToString(hash[:]), "project_version": project["version"]}
		result, err := s.Store.ImportTasksPreview(p[1], input)
		if err != nil {
			var ae *store.AppError
			if errors.As(err, &ae) && ae.Status == 400 {
				result = map[string]any{"errors": []map[string]any{{"row": 0, "field": "rows", "message": ae.Message}}}
			} else {
				handleErr(w, err)
				return
			}
		}
		// Parser errors are kept alongside domain errors; no row is silently dropped.
		errors := []any{}
		warnings := []any{}
		for _, e := range parsed.Errors {
			errors = append(errors, e)
		}
		for _, e := range parsed.Warnings {
			warnings = append(warnings, e)
		}
		appendValues := func(dst []any, v any) []any {
			switch list := v.(type) {
			case []map[string]any:
				for _, item := range list {
					dst = append(dst, item)
				}
			case []any:
				dst = append(dst, list...)
			case []string:
				for _, item := range list {
					dst = append(dst, item)
				}
			}
			return dst
		}
		errors = appendValues(errors, result["errors"])
		warnings = appendValues(warnings, result["warnings"])
		result["errors"] = errors
		result["warnings"] = warnings
		result["filename"] = filepath.Base(header.Filename)
		result["sheet"] = parsed.Sheet
		result["source_hash"] = input["source_hash"]
		result["project_version"] = project["version"]
		// Confirm the exact rows returned in the preview, including normalization.
		result["rows"] = parsed.Rows
		if result["total"] == nil {
			result["total"] = len(parsed.Rows)
		}
		if len(errors) == 0 {
			result["confirm_token"] = s.importToken(p[1], result, time.Now().Add(15*time.Minute).Unix())
		}
		jsonResult(w, 200, result)
		return
	}
	if len(p) == 3 {
		input, ok := body(w, r)
		if !ok {
			return
		}
		if !s.validImportToken(p[1], input) {
			writeError(w, 409, "import_preview_expired", "导入预览已过期或内容已变化，请重新上传预览")
			return
		}
		result, err := s.Store.ImportTasks(p[1], input)
		if err != nil {
			handleErr(w, err)
		} else {
			jsonResult(w, 201, result)
		}
		return
	}
	writeError(w, 404, "not_found", "接口不存在或请求方法不正确")
}
func (s *Server) importSignature(pid string, input map[string]any, expires int64) string {
	s.importOnce.Do(func() {
		s.importSecret = make([]byte, 32)
		if _, err := rand.Read(s.importSecret); err != nil {
			panic(err)
		}
	})
	payload, _ := json.Marshal(map[string]any{"project_id": pid, "rows": input["rows"], "source_hash": input["source_hash"], "project_version": input["project_version"], "expires": expires})
	h := hmac.New(sha256.New, s.importSecret)
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))
}
func (s *Server) importToken(pid string, input map[string]any, expires int64) string {
	return fmt.Sprint(expires) + "." + s.importSignature(pid, input, expires)
}
func (s *Server) validImportToken(pid string, input map[string]any) bool {
	token, _ := input["confirm_token"].(string)
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	expiry, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().Unix() > expiry || expiry > time.Now().Add(16*time.Minute).Unix() {
		return false
	}
	expected := s.importSignature(pid, input, expiry)
	return hmac.Equal([]byte(parts[1]), []byte(expected))
}

func (s *Server) journals(w http.ResponseWriter, r *http.Request, p []string) {
	team := p[0] == "team-daily-entries"
	if len(p) == 2 && p[1] == "generate" && !team && r.Method == "POST" {
		input, ok := body(w, r)
		if !ok {
			return
		}
		pid, _ := input["project_id"].(string)
		date, _ := input["log_date"].(string)
		result, err := s.Store.ProjectDailyLog(pid, date)
		if err != nil {
			handleErr(w, err)
		} else {
			jsonResult(w, 200, result)
		}
		return
	}
	if len(p) == 1 && r.Method == "GET" {
		var result map[string]any
		var err error
		if team {
			result, err = s.Store.ListTeamDailyEntries(filtersOf(r))
		} else {
			result, err = s.Store.ListProjectDailyLogs(filtersOf(r))
		}
		if err != nil {
			handleErr(w, err)
		} else {
			jsonResult(w, 200, result)
		}
		return
	}
	if len(p) == 2 && r.Method == "GET" {
		var result map[string]any
		var err error
		if team {
			result, err = s.Store.GetTeamDailyEntry(p[1])
		} else {
			result, err = s.Store.GetProjectDailyLog(p[1])
		}
		if err != nil {
			handleErr(w, err)
		} else {
			jsonResult(w, 200, result)
		}
		return
	}
	if (len(p) == 1 && r.Method == "POST") || (len(p) == 2 && (r.Method == "PATCH" || r.Method == "DELETE")) {
		input := map[string]any{}
		if r.Method == "DELETE" && r.ContentLength == 0 {
			n, _ := strconv.Atoi(r.URL.Query().Get("version"))
			input["version"] = n
		} else {
			var ok bool
			input, ok = bodyLimit(w, r, 16<<20)
			if !ok {
				return
			}
		}
		var result map[string]any
		var err error
		status := 200
		switch {
		case len(p) == 1 && team:
			result, err = s.Store.CreateTeamDailyEntry(input)
			status = 201
		case len(p) == 1:
			result, err = s.Store.SaveProjectDailyLog(input)
			status = 201
		case r.Method == "DELETE" && team:
			result, err = s.Store.DeleteTeamDailyEntry(p[1], input)
		case r.Method == "DELETE":
			result, err = s.Store.DeleteProjectDailyLog(p[1], input)
		case team:
			result, err = s.Store.UpdateTeamDailyEntry(p[1], input)
		default:
			result, err = s.Store.UpdateProjectDailyLog(p[1], input)
		}
		if err != nil {
			handleErr(w, err)
		} else {
			jsonResult(w, status, result)
		}
		return
	}
	writeError(w, 404, "not_found", "接口不存在或请求方法不正确")
}
