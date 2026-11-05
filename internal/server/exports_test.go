package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"projnote/internal/exporter"
)

func exportTestServer(t *testing.T) *Server {
	t.Helper()
	s := testServer(t)
	if e := s.InitializeExports(); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(s.CloseExports)
	return s
}
func exportJSON(t *testing.T, s *Server, method, path string, input any, status int) map[string]any {
	t.Helper()
	b, _ := json.Marshal(input)
	w := call(s, method, path, string(b), map[string]string{"Content-Type": "application/json", "X-ProjNote-Request": "1"})
	if w.Code != status {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	if status >= 400 {
		return nil
	}
	return decodeData(t, w)
}
func exportWait(t *testing.T, s *Server, id string) map[string]any {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		j, e := s.Store.GetExportJob(id)
		if e != nil {
			t.Fatal(e)
		}
		if j["state"] == "succeeded" || j["state"] == "failed" {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("export worker timed out")
	return nil
}
func TestExportAPIUsesFrozenSavedSourcesAndChecksArtifacts(t *testing.T) {
	s := exportTestServer(t)
	log := exportJSON(t, s, "POST", "/api/worklogs", map[string]any{"content": "## 完成工作\n已保存的中文正文\n## 明日计划\n核对材料"}, 201)
	for _, format := range []string{"docx", "md", "json", "xlsx", "html"} {
		payload := map[string]any{"source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"], "format": format}
		exportJSON(t, s, "POST", "/api/export-jobs", map[string]any{"source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"], "format": format, "source_snapshot": map[string]any{"content": "伪造内容"}}, 400)
		made := exportJSON(t, s, "POST", "/api/export-jobs", payload, 201)
		j := exportWait(t, s, made["id"].(string))
		if j["state"] != "succeeded" {
			t.Fatal(j)
		}
		if j["source_snapshot"].(map[string]any)["content"] != log["content"] || j["template_hash"] == "" {
			t.Fatal("wrong frozen source/template", j)
		}
		w := call(s, "GET", "/api/export-jobs/"+made["id"].(string)+"/download", "", nil)
		if w.Code != 200 || exporter.Digest(w.Body.Bytes()) != j["output_sha256"] {
			t.Fatal("wrong artifact", w.Code, w.Body.String())
		}
		if format == "docx" {
			if _, e := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len())); e != nil {
				t.Fatal(e)
			}
		}
		if format == "md" && w.Body.String() != log["content"] {
			t.Fatal("markdown changed saved content")
		}
		printPage := call(s, "GET", "/api/export-jobs/"+made["id"].(string)+"/print", "", nil)
		if format == "html" {
			if printPage.Code != 200 || !strings.Contains(printPage.Header().Get("Content-Type"), "text/html") || !strings.HasPrefix(printPage.Header().Get("Content-Disposition"), "inline") || !bytes.Equal(printPage.Body.Bytes(), w.Body.Bytes()) {
				t.Fatal("saved print artifact cannot be opened", printPage.Code, printPage.Body.String())
			}
			if !strings.Contains(printPage.Body.String(), "已保存的中文正文") {
				t.Fatal("print source content lost")
			}
		} else if printPage.Code != 400 {
			t.Fatal("non-print artifact accepted as printable", format, printPage.Code)
		}
		if format == "xlsx" {
			if _, e := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len())); e != nil {
				t.Fatal(e)
			}
		}

		if e := os.WriteFile(filepath.Join(s.DataDir, "exports", j["output_name"].(string)), []byte("被外部修改"), 0600); e != nil {
			t.Fatal(e)
		}
		w = call(s, "GET", "/api/export-jobs/"+made["id"].(string)+"/download", "", nil)
		if w.Code != 410 {
			t.Fatal("tampered artifact downloadable", w.Code)
		}
		j, _ = s.Store.GetExportJob(made["id"].(string))
		if j["state"] != "failed" || j["source_snapshot"].(map[string]any)["content"] != log["content"] {
			t.Fatal(j)
		}
	}
	log = exportJSON(t, s, "PATCH", "/api/worklogs/"+log["id"].(string), map[string]any{"version": log["version"], "content": "下一版本"}, 200)
	exportJSON(t, s, "POST", "/api/export-jobs", map[string]any{"source_kind": "worklogs", "source_id": log["id"], "source_version": 1, "format": "docx"}, 409)
	exportJSON(t, s, "POST", "/api/export-jobs", map[string]any{"source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"], "format": "docx", "template_hash": strings.Repeat("0", 64)}, 409)
	audit := exportJSON(t, s, "GET", "/api/audit/verify", nil, 200)
	if audit["ok"] != true {
		t.Fatal(audit)
	}
}
func TestExportStartupExplainsInterruptedAndMissingResults(t *testing.T) {
	s := exportTestServer(t)
	log, e := s.Store.Create("worklogs", map[string]any{"content": "还原后仍保留来源"})
	if e != nil {
		t.Fatal(e)
	}
	input := map[string]any{"format": "md", "source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"]}
	pending, e := s.Store.CreateExportJob(input)
	if e != nil {
		t.Fatal(e)
	}
	complete, e := s.Store.CreateExportJob(input)
	if e != nil {
		t.Fatal(e)
	}
	complete, e = s.Store.ClaimExportJob(complete["id"].(string), map[string]any{"version": complete["version"]})
	if e != nil {
		t.Fatal(e)
	}
	complete, e = s.Store.CompleteExportJob(complete["id"].(string), map[string]any{"version": complete["version"], "output_name": fmt.Sprint(complete["id"]) + ".md", "output_sha256": exporter.Digest([]byte("历史结果")), "size": 12})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.InitializeExports(); e != nil {
		t.Fatal(e)
	}
	for _, before := range []map[string]any{pending, complete} {
		after, e := s.Store.GetExportJob(before["id"].(string))
		if e != nil || after["state"] != "failed" || after["error"] == "" || after["source_snapshot"].(map[string]any)["content"] != log["content"] {
			t.Fatal("lost explanation or source", after, e)
		}
	}
}
func TestWordTemplateUploadValidatedAndAudited(t *testing.T) {
	s := exportTestServer(t)
	_, data, e := exporter.LoadTemplate(s.DataDir, "builtin-journal")
	if e != nil {
		t.Fatal(e)
	}
	for i, b := range [][]byte{[]byte("not a docx"), data} {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("name", "我的日志模板")
		part, _ := mw.CreateFormFile("file", "日志.docx")
		_, _ = part.Write(b)
		_ = mw.Close()
		w := call(s, "POST", "/api/export-templates", buf.String(), map[string]string{"X-ProjNote-Request": "1", "Content-Type": mw.FormDataContentType()})
		if i == 0 {
			if w.Code != 400 {
				t.Fatal("invalid template accepted")
			}
			continue
		}
		if w.Code != 201 {
			t.Fatal(w.Code, w.Body.String())
		}
		meta := decodeData(t, w)
		if meta["sha256"] != exporter.Digest(data) || meta["name"] != "我的日志模板" {
			t.Fatal(meta)
		}
		got := call(s, "GET", "/api/export-templates/"+meta["id"].(string)+"/download", "", nil)
		if got.Code != 200 || !bytes.Equal(got.Body.Bytes(), data) {
			t.Fatal("template bytes changed")
		}
	}
	page := exportJSON(t, s, "GET", "/api/export-templates", nil, 200)
	if page["total"] != float64(3) {
		t.Fatal(page)
	}
	audit := exportJSON(t, s, "GET", "/api/audit/verify", nil, 200)
	if audit["ok"] != true {
		t.Fatal(audit)
	}
}
func TestDesktopDraftAndNextPlanRoutesKeepWritesExplicit(t *testing.T) {
	s := exportTestServer(t)
	draft := exportJSON(t, s, "GET", "/api/desktop/draft", nil, 200)
	payload := map[string]any{"content": "尚未提交的便签", "project_id": "", "version": draft["version"]}
	saved := exportJSON(t, s, "PUT", "/api/desktop/draft", payload, 200)
	exportJSON(t, s, "PUT", "/api/desktop/draft", payload, 409)
	if saved["content"] != payload["content"] {
		t.Fatal(saved)
	}
	records := exportJSON(t, s, "GET", "/api/records", nil, 200)
	if records["total"] != float64(0) {
		t.Fatal("draft became business record")
	}
	candidates := exportJSON(t, s, "POST", "/api/logs/next-plan-candidates", map[string]any{"date": "2026-11-05", "limit": 10}, 200)
	if candidates["total"] != float64(0) {
		t.Fatal(candidates)
	}
}

func TestPrintHandlerIsFixedAndUnknownExportsFail(t *testing.T) {
	s := exportTestServer(t)
	script := call(s, "GET", "/api/export-print.js", "", nil)
	if script.Code != 200 || script.Header().Get("Content-Type") != "text/javascript; charset=utf-8" || !strings.Contains(script.Body.String(), "window.print()") || strings.Contains(script.Body.String(), "fetch(") {
		t.Fatal("invalid print handler")
	}
	exportJSON(t, s, "POST", "/api/export-jobs", map[string]any{"format": "pdf"}, 400)
}
