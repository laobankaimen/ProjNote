package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTaskImportPreviewBindsExactRowsAndIsIdempotent(t *testing.T) {
	s := testServer(t)
	headers := map[string]string{"Content-Type": "application/json", "X-ProjNote-Request": "1"}
	p := decodeData(t, call(s, "POST", "/api/projects", `{"name":"导入项目"}`, headers))
	pid := p["id"].(string)
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	part, _ := mw.CreateFormFile("file", "导入.xlsx")
	_, _ = part.Write(taskImportTemplate)
	_ = mw.Close()
	preview := decodeData(t, call(s, "POST", "/api/projects/"+pid+"/import/preview", b.String(), map[string]string{"Content-Type": mw.FormDataContentType(), "X-ProjNote-Request": "1"}))
	if len(preview["errors"].([]any)) != 0 || preview["confirm_token"] == nil || preview["total"] != float64(3) {
		t.Fatalf("bad preview: %v", preview)
	}
	payload := map[string]any{"rows": preview["rows"], "source_hash": preview["source_hash"], "project_version": preview["project_version"], "confirm_token": preview["confirm_token"]}
	raw, _ := json.Marshal(payload)
	first := decodeData(t, call(s, "POST", "/api/projects/"+pid+"/import", string(raw), headers))
	if first["total"] != float64(3) {
		t.Fatal(first)
	}
	again := decodeData(t, call(s, "POST", "/api/projects/"+pid+"/import", string(raw), headers))
	if again["already_imported"] != true || again["import_id"] != first["import_id"] {
		t.Fatalf("retry duplicated: %v", again)
	}
	payload["rows"].([]any)[1].(map[string]any)["name"] = "偷偷更换"
	raw, _ = json.Marshal(payload)
	w := call(s, "POST", "/api/projects/"+pid+"/import", string(raw), headers)
	if w.Code != 409 {
		t.Fatalf("tampering accepted: %s", w.Body.String())
	}
	payload["confirm_token"] = s.importToken(pid, payload, time.Now().Add(-time.Minute).Unix())
	raw, _ = json.Marshal(payload)
	if w = call(s, "POST", "/api/projects/"+pid+"/import", string(raw), headers); w.Code != 409 {
		t.Fatal("expired preview accepted")
	}
	tree := decodeData(t, call(s, "GET", "/api/projects/"+pid+"/tree", "", nil))
	if tree["total"] != float64(3) {
		t.Fatal("failed confirmation mutated tasks")
	}
	audit := decodeData(t, call(s, "GET", "/api/audit/verify", "", nil))
	if audit["ok"] != true {
		t.Fatal(audit)
	}
}
func TestImportTemplateDownloadAndCompactHook(t *testing.T) {
	s := testServer(t)
	w := call(s, "GET", "/api/import-template.xlsx", "", nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), taskImportTemplate) {
		t.Fatal("template download changed bytes")
	}
	headers := map[string]string{"Content-Type": "application/json", "X-ProjNote-Request": "1"}
	w = call(s, "POST", "/api/desktop/compact", `{}`, headers)
	if w.Code != 501 {
		t.Fatal("missing compact hook should be explicit")
	}
	called := 0
	s.OpenCompact = func() error { called++; return nil }
	w = call(s, "POST", "/api/desktop/compact", `{}`, headers)
	if w.Code != 200 || called != 1 {
		t.Fatalf("compact hook %d %s", called, w.Body.String())
	}
	// A foreign website cannot trigger desktop processes through an absent write header.
	request := httptest.NewRequest("POST", "http://127.0.0.1:18600/api/desktop/compact", bytes.NewBufferString(`{}`))
	out := httptest.NewRecorder()
	s.Handler().ServeHTTP(out, request)
	if out.Code != 403 || called != 1 {
		t.Fatal("desktop hook bypassed same-origin write guard")
	}
}

func TestDesktopEntryHookDoesNotExpandPanel(t *testing.T) {
	s := testServer(t)
	headers := map[string]string{"Content-Type": "application/json", "X-ProjNote-Request": "1"}
	shown, opened := 0, 0
	s.OpenCompact = func() error { opened++; return nil }
	if w := call(s, "POST", "/api/desktop/entry", `{}`, headers); w.Code != 501 {
		t.Fatal("missing entry hook must not call panel", w.Code)
	}
	s.OpenCompactEntry = func() error { shown++; return nil }
	w := call(s, "POST", "/api/desktop/entry", `{}`, headers)
	if w.Code != 200 || shown != 1 || opened != 0 {
		t.Fatal("entry route expanded panel", w.Code, shown, opened)
	}
	w = call(s, "POST", "/api/desktop/entry", `{}`, map[string]string{"Content-Type": "application/json"})
	if w.Code != 403 || shown != 1 {
		t.Fatal("entry write guard bypassed")
	}
	w = call(s, "POST", "/api/desktop/compact", `{}`, headers)
	if w.Code != 200 || opened != 1 || shown != 1 {
		t.Fatal("panel action changed entry-only contract")
	}
}
