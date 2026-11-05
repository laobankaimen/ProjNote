package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func coverageRequirementCreate(t *testing.T, s *Store, pid any, title string, status string, taskIDs ...any) map[string]any {
	t.Helper()
	input := requirementInput(pid)
	input["title"] = title
	input["status"] = status
	input["task_ids"] = taskIDs
	if len(taskIDs) == 0 {
		input["task_ids"] = []any{}
	}
	if status == "accepted" {
		input["acceptance_note"] = "人工按准则核对通过"
	}
	return mustCreate(t, s, "requirements", input)
}
func coverageFind(t *testing.T, items any, id any) map[string]any {
	t.Helper()
	for _, item := range mapItems(items) {
		if item["id"] == id {
			return item
		}
	}
	t.Fatal("missing coverage item", id, items)
	return nil
}
func coverageSummary(t *testing.T, s *Store, pid any, filters map[string]string) map[string]any {
	t.Helper()
	result, e := s.RequirementCoverage(text(pid), filters)
	if e != nil {
		t.Fatal(e)
	}
	return result
}

func TestRequirementCoverageCurrentDirectLinksHintsAndNoWrites(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "覆盖检查中文项目"})
	pid := p["id"]
	parent := mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "name": "需求关联父项"})
	done := mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "parent_id": parent["id"], "name": "执行已完成", "status": "done", "estimate_hours": 2})
	doing := mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "parent_id": parent["id"], "name": "仍在执行", "status": "doing", "progress": 20, "estimate_hours": 2})
	child := mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "parent_id": parent["id"], "name": "仅父关联不继承", "estimate_hours": 2})
	free := mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "name": "只有作废与回收需求关联"})
	canceled := mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "name": "取消并非完成", "status": "canceled"})
	recycled := mustCreate(t, s, "tasks", map[string]any{"project_id": pid, "name": "关联任务已回收"})
	ready := coverageRequirementCreate(t, s, pid, "任务完成待人工验收", "doing", done["id"])
	accepted := coverageRequirementCreate(t, s, pid, "已验收但任务尚未完成", "accepted", doing["id"])
	noTask := coverageRequirementCreate(t, s, pid, "没有关联任务", "pending")
	coverageRequirementCreate(t, s, pid, "作废不覆盖", "void", free["id"])
	trashedTask := coverageRequirementCreate(t, s, pid, "任务已回收须补关联", "pending", recycled["id"])
	canceledReq := coverageRequirementCreate(t, s, pid, "取消任务不提示验收", "pending", canceled["id"])
	parentReq := coverageRequirementCreate(t, s, pid, "只关联父任务", "doing", parent["id"])
	removed := coverageRequirementCreate(t, s, pid, "回收需求不覆盖", "pending", free["id"])
	if e := s.Delete("requirements", text(removed["id"]), versionOf(removed["version"])); e != nil {
		t.Fatal(e)
	}
	if e := s.Delete("tasks", text(recycled["id"]), versionOf(recycled["version"])); e != nil {
		t.Fatal(e)
	}
	auditBefore := sqlCount(t, s, "audit")
	revisionsBefore := sqlCount(t, s, "requirement_revisions")
	result := coverageSummary(t, s, pid, nil)
	summary := result["summary"].(map[string]any)
	want := map[string]any{"requirements_total": 7, "active_requirements": 6, "status_counts": map[string]int{"pending": 3, "doing": 2, "accepted": 1, "void": 1}, "requirements_without_tasks": 2, "ready_for_acceptance": 1, "accepted_with_incomplete_tasks": 1, "tasks_total": 6, "leaf_tasks": 5, "open_leaf_tasks": 3, "linked_leaf_tasks": 3, "unlinked_leaf_tasks": 2}
	if !reflect.DeepEqual(summary, want) || result["total"] != 6 || result["basis"] != coverageBasis || result["project_name"] != p["name"] {
		t.Fatal(result, summary, want)
	}
	item := coverageFind(t, result["items"], ready["id"])
	if item["ready_for_acceptance"] != true || item["task_count"] != 1 || item["done_task_count"] != 1 || item["task_progress"] != float64(100) {
		t.Fatal(item)
	}
	item = coverageFind(t, result["items"], accepted["id"])
	if item["accepted_with_incomplete_tasks"] != true || item["incomplete_task_count"] != 1 || item["task_progress"] != float64(20) {
		t.Fatal(item)
	}
	for _, id := range []any{noTask["id"], trashedTask["id"]} {
		item = coverageFind(t, result["items"], id)
		if item["no_tasks"] != true || item["task_count"] != 0 || len(mapItems(item["linked_tasks"])) != 0 {
			t.Fatal(item)
		}
	}
	item = coverageFind(t, result["items"], canceledReq["id"])
	if item["ready_for_acceptance"] != false || item["done_task_count"] != 0 || item["incomplete_task_count"] != 1 {
		t.Fatal(item)
	}
	item = coverageFind(t, result["items"], parentReq["id"])
	if item["task_count"] != 1 || mapItems(item["linked_tasks"])[0]["has_children"] != true || item["task_progress"] != float64(40) {
		t.Fatal("parent link expanded or lost current rollup", item)
	}
	orphan := coverageSummary(t, s, pid, map[string]string{"view": "unlinked_tasks"})
	if orphan["total"] != 2 {
		t.Fatal(orphan)
	}
	coverageFind(t, orphan["items"], child["id"])
	coverageFind(t, orphan["items"], free["id"])
	inverse, e := s.TaskRequirements(text(child["id"]), nil)
	if e != nil || inverse["total"] != 0 {
		t.Fatal("parent requirement link inherited by child", inverse, e)
	}
	for name, total := range map[string]int{"no_tasks": 2, "ready_for_acceptance": 1, "accepted_with_incomplete_tasks": 1} {
		filtered := coverageSummary(t, s, pid, map[string]string{"coverage": name, "limit": "1"})
		if filtered["total"] != total || !reflect.DeepEqual(filtered["summary"], want) {
			t.Fatal("filtered summary changed", filtered)
		}
	}
	if getNow(t, s, "requirements", text(ready["id"]))["status"] != "doing" || getNow(t, s, "requirements", text(accepted["id"]))["status"] != "accepted" || sqlCount(t, s, "audit") != auditBefore || sqlCount(t, s, "requirement_revisions") != revisionsBefore {
		t.Fatal("coverage mutated state/history")
	}
	assertChain(t, s)
}
func TestCoverageTaskRequirementsFiltersStableIDsRecyclingAndReadOnlyProject(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "反向需求查询"})
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "任务_%", "assignee": "张三"})
	a := coverageRequirementCreate(t, s, p["id"], "字面_%需求", "pending", task["id"])
	a = mustUpdate(t, s, "requirements", a, map[string]any{"owner": "张三", "change_note": "明确负责人"})
	b := coverageRequirementCreate(t, s, p["id"], "第二当前需求", "doing", task["id"])
	void := coverageRequirementCreate(t, s, p["id"], "作废也可显式查", "void", task["id"])
	deleted := coverageRequirementCreate(t, s, p["id"], "回收不展示", "pending", task["id"])
	if e := s.Delete("requirements", text(deleted["id"]), versionOf(deleted["version"])); e != nil {
		t.Fatal(e)
	}
	result, e := s.TaskRequirements(text(task["id"]), map[string]string{"limit": "1", "offset": "1"})
	if e != nil || result["total"] != 2 || len(mapItems(result["items"])) != 1 || result["task_id"] != task["id"] || result["task"].(map[string]any)["has_children"] != false {
		t.Fatal(result, e)
	}
	result, e = s.TaskRequirements(text(task["id"]), map[string]string{"q": "_%", "owner": "张三", "status": "pending"})
	if e != nil || result["total"] != 1 {
		t.Fatal(result, e)
	}
	item := coverageFind(t, result["items"], a["id"])
	if item["revision"] != a["revision"] || item["version"] != a["version"] {
		t.Fatal("old revision in reverse link", item)
	}
	result, e = s.TaskRequirements(text(task["id"]), map[string]string{"status": "void"})
	if e != nil || result["total"] != 1 || mapItems(result["items"])[0]["id"] != void["id"] {
		t.Fatal(result, e)
	}
	a = mustUpdate(t, s, "requirements", a, map[string]any{"task_ids": []any{}, "change_note": "明确解除当前关联"})
	result, e = s.TaskRequirements(text(task["id"]), nil)
	if e != nil || result["total"] != 1 || mapItems(result["items"])[0]["id"] != b["id"] {
		t.Fatal("old frozen link used as current", result, e)
	}
	p = mustUpdate(t, s, "projects", p, map[string]any{"status": "closed"})
	if _, e = s.RequirementCoverage(text(p["id"]), nil); e != nil {
		t.Fatal("closed project read rejected", e)
	}
	if _, e = s.TaskRequirements(text(task["id"]), nil); e != nil {
		t.Fatal(e)
	}
	p = mustUpdate(t, s, "projects", p, map[string]any{"status": "archived"})
	if _, e = s.RequirementCoverage(text(p["id"]), nil); e != nil {
		t.Fatal("archived project read rejected", e)
	}
	if _, e = s.TaskRequirements(text(task["id"]), nil); e != nil {
		t.Fatal(e)
	}
	// Use a separate active project to exercise current-task recycle/restore.
	other := mustCreate(t, s, "projects", map[string]any{"name": "可回收项目"})
	leaf := mustCreate(t, s, "tasks", map[string]any{"project_id": other["id"], "name": "可回收任务"})
	r := coverageRequirementCreate(t, s, other["id"], "反向链接保留", "pending", leaf["id"])
	if e = s.Delete("tasks", text(leaf["id"]), versionOf(leaf["version"])); e != nil {
		t.Fatal(e)
	}
	_, e = s.TaskRequirements(text(leaf["id"]), nil)
	assertStatus(t, e, 404)
	recycled, _ := getIncludingTrash(s.DB, "tasks", text(leaf["id"]), true)
	_, e = s.RestoreTrash("tasks", text(leaf["id"]), map[string]any{"version": recycled["version"]})
	if e != nil {
		t.Fatal(e)
	}
	result, e = s.TaskRequirements(text(leaf["id"]), nil)
	if e != nil || result["total"] != 1 || mapItems(result["items"])[0]["id"] != r["id"] {
		t.Fatal(result, e)
	}
	assertChain(t, s)
}
func TestRequirementCoverageCompleteProjectAboveFiveHundredAndGlobalSummary(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "大于500不截断"})
	other := mustCreate(t, s, "projects", map[string]any{"name": "异项目不混入"})
	coverageRequirementCreate(t, s, other["id"], "异项目需求", "pending")
	_, e := s.write(func(tx *sql.Tx) (map[string]any, error) {
		for i := 0; i < 507; i++ {
			task, err := create(tx, "tasks", map[string]any{"project_id": p["id"], "name": fmt.Sprintf("任务%03d", i), "assignee": "张三"})
			if err != nil {
				return nil, err
			}
			in := requirementInput(p["id"])
			in["title"] = fmt.Sprintf("需求%03d", i)
			in["owner"] = "张三"
			in["task_ids"] = []any{task["id"]}
			if _, err = create(tx, "requirements", in); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if e != nil {
		t.Fatal(e)
	}
	result := coverageSummary(t, s, p["id"], map[string]string{"limit": "2", "offset": "505", "owner": "张三"})
	summary := result["summary"].(map[string]any)
	if result["total"] != 507 || len(mapItems(result["items"])) != 2 || summary["active_requirements"] != 507 || summary["tasks_total"] != 507 || summary["linked_leaf_tasks"] != 507 || summary["unlinked_leaf_tasks"] != 0 {
		t.Fatal(result)
	}
	filtered := coverageSummary(t, s, p["id"], map[string]string{"q": "需求506", "limit": "1"})
	if filtered["total"] != 1 || !reflect.DeepEqual(filtered["summary"], summary) {
		t.Fatal("overview summarized the page/filter", filtered)
	}
	none := coverageSummary(t, s, p["id"], map[string]string{"owner": "不存在", "offset": "999999"})
	if none["total"] != 0 || len(mapItems(none["items"])) != 0 || !reflect.DeepEqual(none["summary"], summary) {
		t.Fatal(none)
	}
	// The summary and paging are read-only and do not add any business revisions.
	if sqlCount(t, s, "requirement_revisions") != 508 {
		t.Fatal("overview wrote revisions")
	}
	assertChain(t, s)
}
func TestCoverageFilteringCurrentTasksAndInvalidInputs(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "筛选边界"})
	task := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "未关联_%任务", "assignee": "张三"})
	r := coverageRequirementCreate(t, s, p["id"], "孤立需求", "pending")
	current := coverageSummary(t, s, p["id"], map[string]string{"view": "unlinked_tasks", "q": "_%", "owner": "张三", "status": "todo", "limit": "1"})
	if current["total"] != 1 || mapItems(current["items"])[0]["id"] != task["id"] {
		t.Fatal(current)
	}
	literal := coverageSummary(t, s, p["id"], map[string]string{"view": "unlinked_tasks", "q": "_%X"})
	if literal["total"] != 0 {
		t.Fatal("query interpreted wildcard", literal)
	}
	r = mustUpdate(t, s, "requirements", r, map[string]any{"task_ids": []any{task["id"]}, "change_note": "新增关联"})
	current = coverageSummary(t, s, p["id"], map[string]string{"view": "unlinked_tasks"})
	if current["total"] != 0 {
		t.Fatal(current)
	}
	task = mustUpdate(t, s, "tasks", task, map[string]any{"status": "done"})
	current = coverageSummary(t, s, p["id"], map[string]string{"coverage": "ready_for_acceptance"})
	if current["total"] != 1 || coverageFind(t, current["items"], r["id"])["ready_for_acceptance"] != true {
		t.Fatal("current task state not reflected", current)
	}
	for _, filters := range []map[string]string{{"q": strings.Repeat("中", 201)}, {"status": "done"}, {"view": "bad"}, {"coverage": "bad"}, {"view": "unlinked_tasks", "status": "accepted"}, {"view": "unlinked_tasks", "coverage": "no_tasks"}} {
		_, e := s.RequirementCoverage(text(p["id"]), filters)
		assertStatus(t, e, 400)
	}
	_, e := s.RequirementCoverage("missing", nil)
	assertStatus(t, e, 404)
	_, e = s.TaskRequirements("missing", nil)
	assertStatus(t, e, 404)
	_, e = s.TaskRequirements(text(task["id"]), map[string]string{"status": "done"})
	assertStatus(t, e, 400)
	_, e = s.TaskRequirements(text(task["id"]), map[string]string{"q": strings.Repeat("a", 201)})
	assertStatus(t, e, 400)
	empty := mustCreate(t, s, "projects", map[string]any{"name": "空项目"})
	if e = s.Delete("projects", text(empty["id"]), versionOf(empty["version"])); e != nil {
		t.Fatal(e)
	}
	_, e = s.RequirementCoverage(text(empty["id"]), nil)
	assertStatus(t, e, 404)
	assertChain(t, s)
}

func TestCapturePlanComparisonFreezesBothCompleteSidesAndMatchingHashes(t *testing.T) {
	s := testStore(t)
	p := mustCreate(t, s, "projects", map[string]any{"name": "比较导出冻结"})
	unchanged := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "未改任务也要保留", "estimate_hours": 1.25})
	changed := mustCreate(t, s, "tasks", map[string]any{"project_id": p["id"], "name": "原计划任务", "plan_end": "2026-11-06"})
	first := savePlan(t, s, p, "比较起点")
	changed = mustUpdate(t, s, "tasks", changed, map[string]any{"name": "已调整任务", "plan_end": "2026-11-08", "estimate_hours": 2.75})
	p = mustUpdate(t, s, "projects", p, map[string]any{"name": "当前项目名称"})
	second := savePlan(t, s, p, "比较终点")
	auditBefore := sqlCount(t, s, "audit")
	for _, toID := range []string{text(second["id"]), ""} {
		captured, e := s.CapturePlanComparison(text(p["id"]), text(first["id"]), toID)
		if e != nil {
			t.Fatal(e)
		}
		for _, side := range []string{"from", "to"} {
			snapshot, ok := captured[side+"_snapshot"].(map[string]any)
			if !ok {
				t.Fatal("missing complete comparison side", captured)
			}
			if len(mapItems(snapshot["tasks"])) != 2 {
				t.Fatal("comparison side only retained changed tasks", snapshot)
			}
			coverageFind(t, snapshot["tasks"], unchanged["id"])
			raw, e := json.Marshal(snapshot)
			if e != nil || shaText(string(raw)) != captured[side].(map[string]any)["hash"] {
				t.Fatal("comparison metadata hash did not match complete snapshot", side, string(raw), captured[side], e)
			}
		}
		if captured["from_snapshot"].(map[string]any)["project"].(map[string]any)["name"] != "比较导出冻结" || captured["to_snapshot"].(map[string]any)["project"].(map[string]any)["name"] != "当前项目名称" {
			t.Fatal("comparison project names replaced by live project", captured)
		}
		ordinary, e := s.ComparePlanVersions(text(p["id"]), text(first["id"]), toID)
		if e != nil {
			t.Fatal(e)
		}
		if _, ok := ordinary["from_snapshot"]; ok {
			t.Fatal("ordinary comparison unexpectedly includes full snapshots")
		}
		if _, ok := ordinary["to_snapshot"]; ok {
			t.Fatal("ordinary comparison unexpectedly includes full snapshots")
		}
		if !reflect.DeepEqual(captured["summary"], ordinary["summary"]) || !reflect.DeepEqual(captured["changed"], ordinary["changed"]) {
			t.Fatal("capture and ordinary comparison differ")
		}
		raw, e := json.Marshal(captured)
		if e != nil {
			t.Fatal(e)
		}
		changed = mustUpdate(t, s, "tasks", changed, map[string]any{"description": "后续编辑不会改写已经返回的比较快照 " + toID})
		after, _ := json.Marshal(captured)
		if string(after) != string(raw) {
			t.Fatal("captured comparison changed after task edit")
		}
	}
	if sqlCount(t, s, "audit") != auditBefore+2 {
		t.Fatal("read-only capture appended audit")
	}
	assertChain(t, s)
}
