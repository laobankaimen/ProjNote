package server

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestPlanVersionAPIPreservesCompleteTreeAndComparesCurrent(t *testing.T) {
	s := exportTestServer(t)
	project := exportJSON(t, s, "POST", "/api/projects", map[string]any{"name": "完整计划项目"}, 201)
	pid := project["id"].(string)
	task := exportJSON(t, s, "POST", "/api/tasks", map[string]any{"project_id": pid, "name": "现场联调", "plan_start": "2026-11-05", "plan_end": "2026-11-06", "assignee": "张三"}, 201)
	for i := 0; i < 51; i++ {
		exportJSON(t, s, "POST", "/api/tasks", map[string]any{"project_id": pid, "name": fmt.Sprintf("计划任务%02d", i)}, 201)
	}
	path := "/api/projects/" + pid + "/plan-versions"
	first := exportJSON(t, s, "POST", path, map[string]any{"project_version": project["version"], "name": "首次计划", "description": "完整WBS快照"}, 201)
	if first["task_count"] != float64(52) || len(first["snapshot"].(map[string]any)["tasks"].([]any)) != 52 {
		t.Fatal("saved only a page of the project", first)
	}
	frozen, _ := json.Marshal(first)
	id := first["id"].(string)
	download := call(s, "GET", "/api/plan-versions/"+id+"/download", "", nil)
	if download.Code != 200 || !strings.HasPrefix(download.Header().Get("Content-Disposition"), "attachment") {
		t.Fatal("missing JSON snapshot download", download.Code)
	}
	var saved map[string]any
	if err := json.Unmarshal(download.Body.Bytes(), &saved); err != nil || saved["hash"] != first["hash"] {
		t.Fatal("download changed plan snapshot", saved, err)
	}
	exportJSON(t, s, "PATCH", "/api/plan-versions/"+id, map[string]any{"version": 1, "name": "覆盖旧版"}, 409)
	exportJSON(t, s, "DELETE", "/api/plan-versions/"+id, map[string]any{"version": 1}, 409)
	exportJSON(t, s, "PATCH", "/api/tasks/"+task["id"].(string), map[string]any{"version": task["version"], "name": "现场联调改期", "plan_end": "2026-11-09", "assignee": "李四"}, 200)
	comparison := exportJSON(t, s, "GET", path+"/compare?from="+id+"&to=current", nil, 200)
	if comparison["summary"].(map[string]any)["changed"] != float64(1) {
		t.Fatal("current plan changes not detected", comparison)
	}
	second := exportJSON(t, s, "POST", path, map[string]any{"project_version": project["version"], "name": "第二版计划"}, 201)
	exportJSON(t, s, "GET", path+"/compare?from="+id+"&to="+second["id"].(string), nil, 200)
	exportJSON(t, s, "GET", path+"/compare?to=current", nil, 400)
	other := exportJSON(t, s, "POST", "/api/projects", map[string]any{"name": "其他项目"}, 201)
	exportJSON(t, s, "GET", "/api/projects/"+other["id"].(string)+"/plan-versions/compare?from="+id, nil, 400)
	got := exportJSON(t, s, "GET", "/api/plan-versions/"+id, nil, 200)
	after, _ := json.Marshal(got)
	if string(frozen) != string(after) {
		t.Fatal("later task edits changed prior plan")
	}
	list := exportJSON(t, s, "GET", "/api/plan-versions?limit=1", nil, 200)
	if list["total"] != float64(2) || len(list["items"].([]any)) != 1 {
		t.Fatal("incorrect plan pagination", list)
	}
	if _, exists := list["items"].([]any)[0].(map[string]any)["snapshot"]; exists {
		t.Fatal("list should not embed every full snapshot")
	}
	for _, format := range []string{"docx", "md", "json", "xlsx", "html"} {
		job := exportJSON(t, s, "POST", "/api/export-jobs", map[string]any{"format": format, "source_kind": "plan_versions", "source_id": id, "source_version": 1}, 201)
		done := exportWait(t, s, job["id"].(string))
		if done["state"] != "succeeded" {
			t.Fatal("plan cannot be exported", format, done)
		}
		snapshot := done["source_snapshot"].(map[string]any)
		if !strings.Contains(snapshot["content"].(string), "现场联调") || strings.Contains(snapshot["content"].(string), "现场联调改期") || snapshot["fields"].(map[string]any)["hash"] != first["hash"] {
			t.Fatal("plan export used live tasks", snapshot)
		}
	}
	project = exportJSON(t, s, "PATCH", "/api/projects/"+pid, map[string]any{"version": project["version"], "name": "后来改名的项目"}, 200)
	job := exportJSON(t, s, "POST", "/api/export-jobs", map[string]any{"format": "json", "source_kind": "plan_versions", "source_id": id, "source_version": 1}, 201)
	if job["source_snapshot"].(map[string]any)["project_name"] != "完整计划项目" {
		t.Fatal("old plan name replaced by live project name", job)
	}
	exportJSON(t, s, "POST", path, map[string]any{"project_version": 1, "name": "旧项目页面应冲突"}, 409)
	exportJSON(t, s, "GET", "/api/plan-versions/"+id+"/history", nil, 200)
}

func TestRequirementAPIRevisionsLinksExplicitAcceptanceAndRestore(t *testing.T) {
	s := exportTestServer(t)
	project := exportJSON(t, s, "POST", "/api/projects", map[string]any{"name": "需求项目"}, 201)
	pid := project["id"].(string)
	task := exportJSON(t, s, "POST", "/api/tasks", map[string]any{"project_id": pid, "name": "实现接口"}, 201)
	made := exportJSON(t, s, "POST", "/api/requirements", map[string]any{"project_id": pid, "title": "接口断线重连", "description": "断线后保留输入并允许重新连接", "acceptance_criteria": "恢复连接后数据完整，且无重复提交", "owner": "张三", "source": "会议确认", "task_ids": []any{task["id"]}}, 201)
	id := made["id"].(string)
	path := "/api/requirements/" + id
	if made["revision"] != float64(1) || !strings.HasPrefix(made["code"].(string), "REQ-") {
		t.Fatal("missing stable requirement identity", made)
	}
	original := exportJSON(t, s, "GET", path+"/revisions/1", nil, 200)
	exportJSON(t, s, "PATCH", path, map[string]any{"version": made["version"], "description": "省略变更说明不得保存"}, 400)
	exportJSON(t, s, "PATCH", path, map[string]any{"version": made["version"], "status": "accepted", "change_note": "验证通过但缺少验收说明"}, 400)
	exportJSON(t, s, "PATCH", path, map[string]any{"version": 0, "change_note": "旧版不得覆盖", "description": "旧稿"}, 409)
	task = exportJSON(t, s, "PATCH", "/api/tasks/"+task["id"].(string), map[string]any{"version": task["version"], "status": "done"}, 200)
	if exportJSON(t, s, "GET", path, nil, 200)["status"] == "accepted" {
		t.Fatal("task completion implicitly accepted requirement")
	}
	made = exportJSON(t, s, "PATCH", path, map[string]any{"version": made["version"], "status": "accepted", "acceptance_note": "按验收准则复测通过，数据与提交次数均核对", "change_note": "完成本轮验收"}, 200)
	if made["revision"] != float64(2) {
		t.Fatal("business revision not incremented", made)
	}
	exportJSON(t, s, "PATCH", path, map[string]any{"version": made["version"], "description": "已验收内容偷偷修改", "change_note": "仍保持验收"}, 409)
	made = exportJSON(t, s, "PATCH", path, map[string]any{"version": made["version"], "status": "doing", "description": "断线保留输入，增加重复提交保护", "change_note": "范围调整，明确重开需求"}, 200)
	revisions := exportJSON(t, s, "GET", path+"/revisions", nil, 200)
	if revisions["total"] != float64(3) {
		t.Fatal("missing frozen requirement revisions", revisions)
	}
	comparison := exportJSON(t, s, "GET", path+"/revisions/compare?from=1&to=3", nil, 200)
	if comparison["changed"] != true || len(comparison["fields"].([]any)) == 0 {
		t.Fatal("requirement comparison missing", comparison)
	}
	exportJSON(t, s, "GET", path+"/revisions/compare?from=invalid&to=3", nil, 400)
	exportJSON(t, s, "PATCH", path+"/revisions/1", map[string]any{"content": "篡改旧版"}, 409)
	got := exportJSON(t, s, "GET", path+"/revisions/1", nil, 200)
	a, _ := json.Marshal(original)
	b, _ := json.Marshal(got)
	if string(a) != string(b) {
		t.Fatal("old requirement revision changed")
	}
	for _, format := range []string{"docx", "md", "json", "xlsx", "html"} {
		job := exportJSON(t, s, "POST", "/api/export-jobs", map[string]any{"format": format, "source_kind": "requirements", "source_id": id, "source_version": made["version"]}, 201)
		done := exportWait(t, s, job["id"].(string))
		if done["state"] != "succeeded" || !strings.Contains(done["source_snapshot"].(map[string]any)["content"].(string), "恢复连接后数据完整") {
			t.Fatal("requirement export lost acceptance criteria", format, done)
		}
	}
	exportJSON(t, s, "DELETE", path, map[string]any{"version": made["version"]}, 200)
	exportJSON(t, s, "GET", path, nil, 404)
	trash := exportJSON(t, s, "GET", "/api/trash?kind=requirements", nil, 200)
	recycled := trash["items"].([]any)[0].(map[string]any)
	restored := exportJSON(t, s, "POST", "/api/trash/requirements/"+id+"/restore", map[string]any{"version": recycled["version"]}, 200)
	if restored["revision"] != made["revision"] || restored["code"] != made["code"] {
		t.Fatal("restore created false business version", restored)
	}
	if audit := exportJSON(t, s, "GET", "/api/audit/verify", nil, 200); audit["ok"] != true {
		t.Fatal("audit broken", audit)
	}
}
