package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestHistoricalRevisionAndComparisonExportsRemainFrozen(t *testing.T) {
	s := exportTestServer(t)
	p := exportJSON(t, s, "POST", "/api/projects", map[string]any{"name": "比较导出项目"}, 201)
	pid := p["id"].(string)
	task := exportJSON(t, s, "POST", "/api/tasks", map[string]any{"project_id": pid, "name": "原始任务", "plan_end": "2026-11-06"}, 201)
	req := exportJSON(t, s, "POST", "/api/requirements", map[string]any{"project_id": pid, "title": "原始需求", "description": "原始正文", "acceptance_criteria": "旧版准则", "task_ids": []any{task["id"]}}, 201)
	first := exportJSON(t, s, "POST", "/api/projects/"+pid+"/plan-versions", map[string]any{"name": "第一版", "project_version": p["version"]}, 201)
	task = exportJSON(t, s, "PATCH", "/api/tasks/"+task["id"].(string), map[string]any{"version": task["version"], "name": "第二版任务", "plan_end": "2026-11-10"}, 200)
	req = exportJSON(t, s, "PATCH", "/api/requirements/"+req["id"].(string), map[string]any{"version": req["version"], "description": "后来的正文", "acceptance_criteria": "新准则", "change_note": "改为第二版"}, 200)
	for _, format := range []string{"docx", "md", "json", "xlsx", "html"} {
		job := exportJSON(t, s, "POST", "/api/export-jobs", map[string]any{"format": format, "source_kind": "requirement_revisions", "source_id": req["id"].(string) + ":1", "source_version": 1}, 201)
		done := exportWait(t, s, job["id"].(string))
		content := done["source_snapshot"].(map[string]any)["content"].(string)
		if done["state"] != "succeeded" || !strings.Contains(content, "原始正文") || !strings.Contains(content, "旧版准则") || strings.Contains(content, "后来的正文") {
			t.Fatal("historical revision changed", done)
		}
		comparison := exportJSON(t, s, "GET", "/api/projects/"+pid+"/plan-versions/compare?from="+first["id"].(string), nil, 200)
		payload := map[string]any{"from": first["id"], "to": "current", "format": format, "from_hash": comparison["from"].(map[string]any)["hash"], "to_hash": comparison["to"].(map[string]any)["hash"]}
		job = exportJSON(t, s, "POST", "/api/projects/"+pid+"/plan-versions/compare-export", payload, 201)
		done = exportWait(t, s, job["id"].(string))
		if done["state"] != "succeeded" {
			t.Fatal(done)
		}
		snapshot := done["source_snapshot"].(map[string]any)
		if !strings.Contains(snapshot["content"].(string), "2026-11-06") || !strings.Contains(snapshot["content"].(string), "2026-11-10") {
			t.Fatal("missing comparison details", snapshot)
		}
		sources := snapshot["source_snapshot"].(map[string]any)
		for _, end := range []string{"from", "to"} {
			if len(sources[end].(map[string]any)["tasks"].([]any)) != 1 {
				t.Fatal("lost full comparison endpoint", sources)
			}
		}
		payload["to_hash"] = strings.Repeat("0", 64)
		exportJSON(t, s, "POST", "/api/projects/"+pid+"/plan-versions/compare-export", payload, 409)
	}
}

func TestRegenerateUsesOldSnapshotAfterSourceChangesAndRejectsWrongVersion(t *testing.T) {
	s := exportTestServer(t)
	log := exportJSON(t, s, "POST", "/api/worklogs", map[string]any{"content": "原来已保存的版本"}, 201)
	job := exportJSON(t, s, "POST", "/api/export-jobs", map[string]any{"format": "md", "source_kind": "worklogs", "source_id": log["id"], "source_version": log["version"]}, 201)
	done := exportWait(t, s, job["id"].(string))
	exportJSON(t, s, "PATCH", "/api/worklogs/"+log["id"].(string), map[string]any{"version": log["version"], "content": "后来修改的版本"}, 200)
	path := "/api/export-jobs/" + job["id"].(string) + "/regenerate"
	exportJSON(t, s, "POST", path, map[string]any{"version": 1}, 409)
	fresh := exportJSON(t, s, "POST", path, map[string]any{"version": done["version"]}, 201)
	if fresh["id"] == job["id"] {
		t.Fatal("regeneration reused old job")
	}
	fresh = exportWait(t, s, fresh["id"].(string))
	oldBytes, _ := json.Marshal(done["source_snapshot"])
	newBytes, _ := json.Marshal(fresh["source_snapshot"])
	if string(oldBytes) != string(newBytes) {
		t.Fatal("regeneration reread live source")
	}
	w := call(s, "GET", fmt.Sprintf("/api/export-jobs/%v/download", fresh["id"]), "", nil)
	if w.Code != 200 || w.Body.String() != "原来已保存的版本" {
		t.Fatal("wrong regenerated artifact", w.Body.String())
	}
}
