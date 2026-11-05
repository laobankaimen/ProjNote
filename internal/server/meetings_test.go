package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestMeetingMinutesAPIHistorySearchAndRecycle(t *testing.T) {
	s := testServer(t)
	path := "/api/meeting-minutes"
	input := map[string]any{"title": "迭代会议", "meeting_date": "2026-11-05", "location": "三楼会议室", "attendees": "张三、李四", "content": "## 决议\n周五完成接口联调"}
	made := exportJSON(t, s, "POST", path, input, 201)
	id := made["id"].(string)
	if !strings.HasPrefix(made["code"].(string), "MTG-") || made["meeting_date"] != "2026-11-05" {
		t.Fatal("invalid persisted meeting", made)
	}
	for _, query := range []string{"?q=接口联调", "?date_from=2026-11-05&date_to=2026-11-05", "?unclassified=1"} {
		page := exportJSON(t, s, "GET", path+query, nil, 200)
		if page["total"] != float64(1) {
			t.Fatal("meeting missing from filtered list", query, page)
		}
	}
	page := exportJSON(t, s, "GET", path+"?date_to=2026-11-04", nil, 200)
	if page["total"] != float64(0) {
		t.Fatal("meeting outside date filter", page)
	}
	search := exportJSON(t, s, "GET", "/api/search?q=三楼会议室", nil, 200)
	if search["total"] != float64(1) || search["items"].([]any)[0].(map[string]any)["entity_type"] != "meeting_minutes" {
		t.Fatal("meeting absent from global search", search)
	}
	exportJSON(t, s, "PATCH", path+"/"+id, map[string]any{"version": 0, "content": "旧内容覆盖"}, 409)
	edited := exportJSON(t, s, "PATCH", path+"/"+id, map[string]any{"version": made["version"], "content": "## 决议\n下周一进行验收"}, 200)
	history := exportJSON(t, s, "GET", path+"/"+id+"/history", nil, 200)
	if history["total"].(float64) < 2 {
		t.Fatal("missing create/update history", history)
	}
	exportJSON(t, s, "DELETE", path+"/"+id, map[string]any{"version": edited["version"]}, 200)
	exportJSON(t, s, "GET", path+"/"+id, nil, 404)
	search = exportJSON(t, s, "GET", "/api/search?q=三楼会议室", nil, 200)
	if search["total"] != float64(0) {
		t.Fatal("recycled meeting searchable", search)
	}
	trash := exportJSON(t, s, "GET", "/api/trash?kind=meeting_minutes", nil, 200)
	items := trash["items"].([]any)
	if trash["total"] != float64(1) {
		t.Fatal("missing meeting in recycle bin", trash)
	}
	recycled := items[0].(map[string]any)
	restored := exportJSON(t, s, "POST", "/api/trash/meeting_minutes/"+id+"/restore", map[string]any{"version": recycled["version"]}, 200)
	if restored["content"] != edited["content"] || restored["code"] != made["code"] {
		t.Fatal("restore changed meeting identity/content", restored)
	}
	verify := exportJSON(t, s, "GET", "/api/audit/verify", nil, 200)
	if verify["ok"] != true {
		t.Fatal("meeting damaged audit chain", verify)
	}
}

func TestMeetingExportsFreezeMetadataAndRenderAllFormats(t *testing.T) {
	s := exportTestServer(t)
	made := exportJSON(t, s, "POST", "/api/meeting-minutes", map[string]any{"title": "接口协调会议", "meeting_date": "2026-11-04", "location": "三楼会议室", "attendees": "张三、李四", "content": "## 决议\n周五完成接口联调\n<script>不得执行</script>"}, 201)
	for _, format := range []string{"docx", "md", "json", "xlsx", "html"} {
		job := exportJSON(t, s, "POST", "/api/export-jobs", map[string]any{"format": format, "source_kind": "meeting_minutes", "source_id": made["id"], "source_version": made["version"]}, 201)
		done := exportWait(t, s, job["id"].(string))
		if done["state"] != "succeeded" {
			t.Fatal("meeting export failed", format, done)
		}
		snapshot := done["source_snapshot"].(map[string]any)
		if snapshot["title"] != made["title"] || snapshot["log_date"] != made["meeting_date"] || snapshot["scope"] != "会议纪要" {
			t.Fatal("incorrect meeting metadata", snapshot)
		}
		if snapshot["fields"].(map[string]any)["content"] != made["content"] {
			t.Fatal("export changed raw saved meeting", snapshot)
		}
		w := call(s, "GET", "/api/export-jobs/"+job["id"].(string)+"/download", "", nil)
		if w.Code != 200 {
			t.Fatal("cannot download meeting", format, w.Code)
		}
		text := w.Body.String()
		if format == "docx" || format == "xlsx" {
			archive, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
			if err != nil {
				t.Fatal(err)
			}
			var all strings.Builder
			for _, part := range archive.File {
				if strings.HasSuffix(part.Name, ".xml") {
					r, err := part.Open()
					if err != nil {
						t.Fatal(err)
					}
					_, err = io.Copy(&all, r)
					r.Close()
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			text = all.String()
		}
		if format == "json" {
			var decoded map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &decoded); err != nil {
				t.Fatal(err)
			}
			text = decoded["content"].(string)
		}
		for _, required := range []string{"接口协调会议", "2026-11-04", "三楼会议室", "张三、李四", "周五完成接口联调"} {
			if !strings.Contains(text, required) {
				t.Fatal("export lost meeting information", format, required)
			}
		}
		if format == "html" && strings.Contains(text, "<script>不得执行") {
			t.Fatal("meeting content executed as HTML")
		}
		// A later meeting edit must not rewrite the prior export or its source.
		if _, err := s.Store.Update("meeting_minutes", made["id"].(string), map[string]any{"version": made["version"], "location": "新的会议室"}); err != nil {
			t.Fatal(err)
		}
		old := exportJSON(t, s, "GET", "/api/export-jobs/"+job["id"].(string), nil, 200)
		if old["source_snapshot"].(map[string]any)["fields"].(map[string]any)["location"] != "三楼会议室" {
			t.Fatal("existing export snapshot changed after edit")
		}
		made = exportJSON(t, s, "PATCH", "/api/meeting-minutes/"+made["id"].(string), map[string]any{"version": made["version"].(float64) + 1, "location": "三楼会议室"}, 200)
	}
	exportJSON(t, s, "POST", "/api/export-jobs", map[string]any{"format": "docx", "source_kind": "meeting_minutes", "source_id": made["id"], "source_version": 1}, 409)
}
