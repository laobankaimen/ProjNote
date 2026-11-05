package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"projnote/internal/exporter"
)

func storageJSON(t *testing.T, s *Server, method, path string, input any, status int) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(input)
	w := call(s, method, path, string(raw), map[string]string{"Content-Type": "application/json", "X-ProjNote-Request": "1"})
	if w.Code != status {
		t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
	}
	if status >= 400 {
		return nil
	}
	return decodeData(t, w)
}
func storageExport(t *testing.T, s *Server, format string) map[string]any {
	t.Helper()
	page := exportJSON(t, s, "GET", "/api/worklogs", nil, 200)
	var log map[string]any
	if page["total"] == float64(0) {
		log = exportJSON(t, s, "POST", "/api/worklogs", map[string]any{"content": "清理后保留来源正文"}, 201)
	} else {
		log = page["items"].([]any)[0].(map[string]any)
	}
	job := exportJSON(t, s, "POST", "/api/export-jobs", map[string]any{"source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"], "format": format}, 201)
	job = exportWait(t, s, fmt.Sprint(job["id"]))
	if job["state"] != "succeeded" {
		t.Fatal(job)
	}
	return job
}
func cleanupPayload(jobs ...map[string]any) map[string]any {
	items := []map[string]any{}
	for _, job := range jobs {
		items = append(items, map[string]any{"id": job["id"], "version": job["version"]})
	}
	return map[string]any{"items": items}
}
func storageItems(t *testing.T, page map[string]any) []any {
	t.Helper()
	items, ok := page["items"].([]any)
	if !ok {
		t.Fatal(page)
	}
	return items
}

func TestExportStorageStatsOnlyManagedRegularFilesAndJobCounts(t *testing.T) {
	s := exportTestServer(t)
	empty := storageJSON(t, s, "GET", "/api/export-storage", nil, 200)
	if empty["files"] != float64(0) || empty["bytes"] != float64(0) || empty["jobs_total"] != float64(0) {
		t.Fatal(empty)
	}
	job := storageExport(t, s, "md")
	dir := filepath.Join(s.DataDir, "exports")
	managed := strings.Repeat("a", 32) + ".xlsx"
	if err := os.WriteFile(filepath.Join(dir, managed), []byte("orphan"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"unknown.md", strings.Repeat("b", 32) + ".exe", ".cleanup-leftover", strings.Repeat("C", 32) + ".json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("ignored"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, strings.Repeat("d", 32)+".json"), 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(outside, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, strings.Repeat("e", 32)+".md")); err != nil {
		t.Log("symlink unavailable", err)
	}
	pending, err := s.Store.CreateExportJob(map[string]any{"source_kind": "worklogs", "source_id": job["source_id"], "source_version": job["source_version"], "format": "json"})
	if err != nil {
		t.Fatal(err)
	}
	running, err := s.Store.CreateExportJob(map[string]any{"source_kind": "worklogs", "source_id": job["source_id"], "source_version": job["source_version"], "format": "json"})
	if err != nil {
		t.Fatal(err)
	}
	running, err = s.Store.ClaimExportJob(fmt.Sprint(running["id"]), map[string]any{"version": running["version"]})
	if err != nil {
		t.Fatal(err)
	}
	_ = pending
	stats := storageJSON(t, s, "GET", "/api/export-storage", nil, 200)
	if stats["files"] != float64(2) || stats["bytes"] != float64(job["size"].(int64)+6) || stats["jobs_total"] != float64(3) {
		t.Fatal(stats)
	}
	counts := stats["jobs"].(map[string]any)
	if counts["queued"] != float64(1) || counts["running"] != float64(1) || counts["succeeded"] != float64(1) || counts["failed"] != float64(0) {
		t.Fatal(counts)
	}
}
func TestExportCleanupPreviewAgeKindValidationAndNoMutation(t *testing.T) {
	s := exportTestServer(t)
	old := storageExport(t, s, "md")
	recent := storageExport(t, s, "json")
	at := time.Now().UTC()
	if _, err := s.Store.DB.Exec("UPDATE export_jobs SET updated_at=? WHERE id=?", at.AddDate(0, 0, -31).Format(time.RFC3339Nano), old["id"]); err != nil {
		t.Fatal(err)
	}
	for _, input := range []map[string]any{{}, {"days": -1}, {"days": 1.5}, {"days": "30"}, {"days": nil}, {"days": 365001}, {"days": 0, "before": "2026-11-05T00:00:00Z"}, {"days": 0, "source_kind": "files"}, {"days": 0, "source_kind": 4}} {
		storageJSON(t, s, "POST", "/api/export-storage/cleanup-preview", input, 400)
	}
	preview := storageJSON(t, s, "POST", "/api/export-storage/cleanup-preview", map[string]any{"days": 30}, 200)
	items := storageItems(t, preview)
	if preview["total"] != float64(1) || preview["bytes"] != float64(old["size"].(int64)) || items[0].(map[string]any)["id"] != old["id"] {
		t.Fatal(preview)
	}
	item := items[0].(map[string]any)
	if item["output_sha256"] != old["output_sha256"] || item["output_name"] != old["output_name"] || item["version"] != float64(old["version"].(int64)) {
		t.Fatal(item)
	}
	all := storageJSON(t, s, "POST", "/api/export-storage/cleanup-preview", map[string]any{"days": 0, "source_kind": "worklogs"}, 200)
	if all["total"] != float64(2) {
		t.Fatal(all)
	}
	other := storageJSON(t, s, "POST", "/api/export-storage/cleanup-preview", map[string]any{"days": 0, "source_kind": "project-daily-logs"}, 200)
	if other["total"] != float64(0) {
		t.Fatal(other)
	}
	for _, job := range []map[string]any{old, recent} {
		current, err := s.Store.GetExportJob(fmt.Sprint(job["id"]))
		if err != nil || current["state"] != "succeeded" || current["version"] != job["version"] {
			t.Fatal(current, err)
		}
		if _, err := s.artifact(current); err != nil {
			t.Fatal(err)
		}
	}
}
func TestExportCleanupKeepsFrozenSourcesTemplatesAndAudits(t *testing.T) {
	s := exportTestServer(t)
	job := storageExport(t, s, "docx")
	before, _ := json.Marshal(job["source_snapshot"])
	_, template, err := exporter.LoadTemplate(s.DataDir, "builtin-journal")
	if err != nil {
		t.Fatal(err)
	}
	result := storageJSON(t, s, "POST", "/api/export-storage/cleanup", cleanupPayload(job), 200)
	if result["cleaned"] != float64(1) || result["failed"] != float64(0) || result["bytes"] != float64(job["size"].(int64)) {
		t.Fatal(result)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir, "exports", fmt.Sprint(job["output_name"]))); !os.IsNotExist(err) {
		t.Fatal("output not removed", err)
	}
	after, err := s.Store.GetExportJob(fmt.Sprint(job["id"]))
	if err != nil {
		t.Fatal(err)
	}
	frozen, _ := json.Marshal(after["source_snapshot"])
	if after["state"] != "failed" || after["error"] != exportCleanupMessage || after["output_name"] != "" || after["output_sha256"] != "" || after["size"] != int64(0) || !bytes.Equal(before, frozen) {
		t.Fatal("job or frozen source not retained", after)
	}
	if _, err := s.Store.Get("worklogs", fmt.Sprint(job["source_id"])); err != nil {
		t.Fatal("source removed", err)
	}
	_, templateAfter, err := exporter.LoadTemplate(s.DataDir, "builtin-journal")
	if err != nil || !bytes.Equal(template, templateAfter) {
		t.Fatal("template changed", err)
	}
	entries, err := os.ReadDir(filepath.Join(s.DataDir, "exports"))
	if err != nil || len(entries) != 0 {
		t.Fatal("staging file leaked", entries, err)
	}
	stats := storageJSON(t, s, "GET", "/api/export-storage", nil, 200)
	if stats["files"] != float64(0) || stats["jobs"].(map[string]any)["failed"] != float64(1) {
		t.Fatal(stats)
	}
	verify := exportJSON(t, s, "GET", "/api/audit/verify", nil, 200)
	if verify["ok"] != true {
		t.Fatal(verify)
	}
}
func TestExportCleanupRejectsStaleActiveAndChangedFilesWithoutDeleting(t *testing.T) {
	s := exportTestServer(t)
	job := storageExport(t, s, "md")
	for _, input := range []map[string]any{{}, {"items": []any{}}, {"items": []any{map[string]any{"id": job["id"], "version": 1.5}}}, {"items": []any{map[string]any{"id": "../a", "version": 1}}}, {"items": []any{map[string]any{"id": job["id"], "version": job["version"], "output_name": "other"}}}, {"items": []any{map[string]any{"id": job["id"], "version": job["version"]}, map[string]any{"id": job["id"], "version": job["version"]}}}} {
		storageJSON(t, s, "POST", "/api/export-storage/cleanup", input, 400)
	}
	stale := map[string]any{"id": job["id"], "version": 1}
	result := storageJSON(t, s, "POST", "/api/export-storage/cleanup", cleanupPayload(stale), 200)
	if result["failed"] != float64(1) || storageItems(t, result)[0].(map[string]any)["status"] != float64(409) {
		t.Fatal(result)
	}
	pending, err := s.Store.CreateExportJob(map[string]any{"source_kind": "worklogs", "source_id": job["source_id"], "source_version": job["source_version"], "format": "json"})
	if err != nil {
		t.Fatal(err)
	}
	result = storageJSON(t, s, "POST", "/api/export-storage/cleanup", cleanupPayload(pending), 200)
	if storageItems(t, result)[0].(map[string]any)["code"] != "export_not_ready" {
		t.Fatal(result)
	}
	running, err := s.Store.ClaimExportJob(fmt.Sprint(pending["id"]), map[string]any{"version": pending["version"]})
	if err != nil {
		t.Fatal(err)
	}
	result = storageJSON(t, s, "POST", "/api/export-storage/cleanup", cleanupPayload(running), 200)
	if result["cleaned"] != float64(0) {
		t.Fatal(result)
	}
	path := filepath.Join(s.DataDir, "exports", fmt.Sprint(job["output_name"]))
	if err := os.WriteFile(path, []byte("外部修改保留"), 0600); err != nil {
		t.Fatal(err)
	}
	result = storageJSON(t, s, "POST", "/api/export-storage/cleanup", cleanupPayload(job), 200)
	if storageItems(t, result)[0].(map[string]any)["status"] != float64(410) {
		t.Fatal(result)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "外部修改保留" {
		t.Fatal("tampered file removed", err)
	}
	preview := storageJSON(t, s, "POST", "/api/export-storage/cleanup-preview", map[string]any{"days": 0}, 200)
	if preview["total"] != float64(0) || preview["skipped"] != float64(1) {
		t.Fatal(preview)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	result = storageJSON(t, s, "POST", "/api/export-storage/cleanup", cleanupPayload(job), 200)
	if storageItems(t, result)[0].(map[string]any)["status"] != float64(410) {
		t.Fatal(result)
	}
	current, err := s.Store.GetExportJob(fmt.Sprint(job["id"]))
	if err != nil || current["state"] != "succeeded" || current["version"] != job["version"] {
		t.Fatal("rejected cleanup mutated job", current, err)
	}
}
func TestExportCleanupDatabaseAuditFailureRestoresExactFile(t *testing.T) {
	s := exportTestServer(t)
	job := storageExport(t, s, "json")
	path := filepath.Join(s.DataDir, "exports", fmt.Sprint(job["output_name"]))
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Store.DB.Exec("CREATE TRIGGER reject_cleanup_audit BEFORE INSERT ON audit WHEN NEW.entity_type='export_jobs' AND NEW.action='artifact_unavailable' BEGIN SELECT RAISE(ABORT,'simulate cleanup audit failure');END"); err != nil {
		t.Fatal(err)
	}
	result := storageJSON(t, s, "POST", "/api/export-storage/cleanup", cleanupPayload(job), 200)
	if result["cleaned"] != float64(0) || result["failed"] != float64(1) {
		t.Fatal("audit failure reported success", result)
	}
	restored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(restored, original) {
		t.Fatal("rollback lost finished output", err)
	}
	current, err := s.Store.GetExportJob(fmt.Sprint(job["id"]))
	if err != nil || current["state"] != "succeeded" || current["version"] != job["version"] || current["output_sha256"] != job["output_sha256"] {
		t.Fatal("failed state change leaked", current, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatal("rollback left temporary files", entries)
	}
	if _, err := s.Store.DB.Exec("DROP TRIGGER reject_cleanup_audit"); err != nil {
		t.Fatal(err)
	}
	retry := storageJSON(t, s, "POST", "/api/export-storage/cleanup", cleanupPayload(job), 200)
	if retry["cleaned"] != float64(1) {
		t.Fatal(retry)
	}
}
func TestExportCleanupSerializesDownloadsAndPreservesMissingExplanation(t *testing.T) {
	s := exportTestServer(t)
	job := storageExport(t, s, "html")
	var wg sync.WaitGroup
	codes := make(chan int, 40)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := call(s, "GET", "/api/export-jobs/"+fmt.Sprint(job["id"])+"/download", "", nil)
			if w.Code == 200 && exporter.Digest(w.Body.Bytes()) != job["output_sha256"] {
				codes <- 0
				return
			}
			codes <- w.Code
		}()
	}
	result := s.cleanupExportArtifacts([]exportCleanupItem{{fmt.Sprint(job["id"]), job["version"].(int64)}})
	wg.Wait()
	close(codes)
	if result["cleaned"] != 1 {
		t.Fatal(result)
	}
	for code := range codes {
		if code != 200 && code != 409 {
			t.Fatalf("download raced staging file or invalidated cleanup explanation: %d", code)
		}
	}
	current, err := s.Store.GetExportJob(fmt.Sprint(job["id"]))
	if err != nil || current["error"] != exportCleanupMessage {
		t.Fatal(current, err)
	}
}
func TestExportCleanupRejectsSymlinkArtifactAndSymlinkDirectory(t *testing.T) {
	s := exportTestServer(t)
	job := storageExport(t, s, "md")
	dir := filepath.Join(s.DataDir, "exports")
	path := filepath.Join(dir, fmt.Sprint(job["output_name"]))
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "external.md")
	if err := os.WriteFile(outside, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skip("symlink unavailable", err)
	}
	result := storageJSON(t, s, "POST", "/api/export-storage/cleanup", cleanupPayload(job), 200)
	if storageItems(t, result)[0].(map[string]any)["status"] != float64(410) {
		t.Fatal(result)
	}
	if got, err := os.ReadFile(outside); err != nil || !bytes.Equal(got, raw) {
		t.Fatal("outside symlink target changed", err)
	}
	stats := storageJSON(t, s, "GET", "/api/export-storage", nil, 200)
	if stats["files"] != float64(0) {
		t.Fatal(stats)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(outside), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := s.artifact(job); err == nil {
		t.Fatal("symlink exports directory accepted")
	}
	storageJSON(t, s, "POST", "/api/export-storage/cleanup", cleanupPayload(job), 200)
}

func TestExportCleanupBatchReportsPartialResultsAndRespectsRequestProtection(t *testing.T) {
	s := exportTestServer(t)
	first := storageExport(t, s, "md")
	second := storageExport(t, s, "json")
	foreign := filepath.Join(s.DataDir, "exports", strings.Repeat("a", 32)+".md")
	if err := os.WriteFile(foreign, []byte("没有对应任务的文件"), 0600); err != nil {
		t.Fatal(err)
	}
	payload := cleanupPayload(first, map[string]any{"id": second["id"], "version": 1})
	raw, _ := json.Marshal(payload)
	blocked := call(s, "POST", "/api/export-storage/cleanup", string(raw), map[string]string{"Content-Type": "application/json"})
	if blocked.Code != 403 {
		t.Fatal("cleanup bypassed request protection", blocked.Code)
	}
	result := storageJSON(t, s, "POST", "/api/export-storage/cleanup", payload, 200)
	items := storageItems(t, result)
	if result["cleaned"] != float64(1) || result["failed"] != float64(1) || items[0].(map[string]any)["ok"] != true || items[1].(map[string]any)["code"] != "version_conflict" {
		t.Fatal(result)
	}
	if _, err := s.artifact(second); err != nil {
		t.Fatal("other batch failure removed finished output", err)
	}
	if raw, err := os.ReadFile(foreign); err != nil || string(raw) != "没有对应任务的文件" {
		t.Fatal("unrequested managed file was removed", err)
	}
}
