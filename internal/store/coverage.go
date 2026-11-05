package store

import (
	"math"
	"strconv"
	"strings"
)

const coverageBasis = "direct_links_current_state"

var coverageTaskFields = strings.Fields("id project_id parent_id wbs_code name assignee status progress version")
var coverageRequirementFields = strings.Fields("id project_id code title owner status revision version")

func coverageTask(m map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range coverageTaskFields {
		out[key] = m[key]
	}
	out["has_children"] = m["has_children"]
	return out
}
func coverageRequirement(m map[string]any) map[string]any {
	out := map[string]any{}
	for _, key := range coverageRequirementFields {
		out[key] = m[key]
	}
	return out
}
func coveragePage(items []map[string]any, filters map[string]string) map[string]any {
	total := len(items)
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := offset + pageLimit(filters, 100)
	if end > total {
		end = total
	}
	return map[string]any{"items": items[offset:end], "total": total, "next_cursor": ""}
}
func coverageQuery(filters map[string]string) (string, error) {
	query := strings.TrimSpace(filters["q"])
	if len([]rune(query)) > 200 {
		return "", invalid("搜索内容不能超过200字符")
	}
	return query, nil
}
func coverageStatus(filters map[string]string, task bool) error {
	status := filters["status"]
	if status == "" {
		return nil
	}
	if task {
		if !oneOf(status, "todo", "doing", "done", "canceled") {
			return invalid("任务状态无效")
		}
	} else if !oneOf(status, "pending", "doing", "accepted", "void") {
		return invalid("需求状态无效")
	}
	return nil
}
func requirementMatches(m map[string]any, filters map[string]string) bool {
	status := filters["status"]
	if status == "" && m["status"] == "void" || status != "" && text(m["status"]) != status {
		return false
	}
	if owner := filters["owner"]; owner != "" && text(m["owner"]) != owner {
		return false
	}
	return num(m["query_match"]) > 0
}

// RequirementCoverage reads one consistent project snapshot. Counts always use
// the full project; filters and pagination select rows only. Links are explicit
// task IDs: a parent link does not silently link all descendants to a requirement.
func (s *Store) RequirementCoverage(projectID string, filters map[string]string) (map[string]any, error) {
	query, err := coverageQuery(filters)
	if err != nil {
		return nil, err
	}
	view := filters["view"]
	if view == "" {
		view = "requirements"
	}
	if !oneOf(view, "requirements", "unlinked_tasks") {
		return nil, invalid("覆盖检查视图无效")
	}
	if err = coverageStatus(filters, view == "unlinked_tasks"); err != nil {
		return nil, err
	}
	check := filters["coverage"]
	if check == "" {
		check = "all"
	}
	if !oneOf(check, "all", "no_tasks", "ready_for_acceptance", "accepted_with_incomplete_tasks") {
		return nil, invalid("覆盖检查筛选无效")
	}
	if view == "unlinked_tasks" && check != "all" {
		return nil, invalid("未关联任务视图不支持需求覆盖筛选")
	}
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	project, err := get(tx, "projects", projectID)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query("SELECT id,project_id,parent_id,wbs_code,name,assignee,status,progress,version FROM tasks WHERE project_id=? AND deleted_at='' ORDER BY sort_order,id", projectID)
	if err != nil {
		return nil, err
	}
	tasks, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	byTask := map[string]map[string]any{}
	hasChildren := map[string]bool{}
	for _, task := range tasks {
		normalize("tasks", task)
		byTask[text(task["id"])] = task
		hasChildren[text(task["parent_id"])] = true
	}
	for _, task := range tasks {
		task["has_children"] = hasChildren[text(task["id"])]
	}
	// Compute text matching in SQLite without loading large description bodies into
	// the overview. Search escaping matches the ordinary requirement list.
	rows, err = tx.Query("SELECT r.id,r.project_id,r.code,r.title,r.owner,r.status,r.revision,r.version,CASE WHEN ?='' OR r.id IN (SELECT entity_id FROM search_doc WHERE entity_type='requirements' AND (title LIKE ? ESCAPE '\\' OR body LIKE ? ESCAPE '\\')) THEN 1 ELSE 0 END AS query_match FROM requirements r WHERE r.project_id=? AND r.deleted_at='' ORDER BY r.updated_at DESC,r.id DESC", query, like(query), like(query), projectID)
	if err != nil {
		return nil, err
	}
	requirements, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	rows, err = tx.Query("SELECT l.requirement_id,l.task_id FROM requirement_task_links l JOIN requirements r ON r.id=l.requirement_id JOIN tasks t ON t.id=l.task_id WHERE r.project_id=? AND r.deleted_at='' AND t.project_id=r.project_id AND t.deleted_at='' ORDER BY t.sort_order,t.id", projectID)
	if err != nil {
		return nil, err
	}
	links, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	linked := map[string][]map[string]any{}
	for _, link := range links {
		if task := byTask[text(link["task_id"])]; task != nil {
			rid := text(link["requirement_id"])
			linked[rid] = append(linked[rid], coverageTask(task))
		}
	}
	summary := map[string]any{"requirements_total": len(requirements), "active_requirements": 0, "status_counts": map[string]int{"pending": 0, "doing": 0, "accepted": 0, "void": 0}, "requirements_without_tasks": 0, "ready_for_acceptance": 0, "accepted_with_incomplete_tasks": 0, "tasks_total": len(tasks), "leaf_tasks": 0, "open_leaf_tasks": 0, "linked_leaf_tasks": 0, "unlinked_leaf_tasks": 0}
	increment := func(key string) { summary[key] = summary[key].(int) + 1 }
	items := []map[string]any{}
	covered := map[string]bool{}
	for _, requirement := range requirements {
		status := text(requirement["status"])
		summary["status_counts"].(map[string]int)[status]++
		taskItems := linked[text(requirement["id"])]
		if taskItems == nil {
			taskItems = []map[string]any{}
		}
		done := 0
		progress := float64(0)
		for _, task := range taskItems {
			if task["status"] == "done" {
				done++
			}
			progress += num(task["progress"])
			if status != "void" {
				covered[text(task["id"])] = true
			}
		}
		count := len(taskItems)
		incomplete := count - done
		noTasks := count == 0
		ready := status != "void" && status != "accepted" && count > 0 && incomplete == 0
		acceptedIncomplete := status == "accepted" && incomplete > 0
		if status != "void" {
			increment("active_requirements")
			if noTasks {
				increment("requirements_without_tasks")
			}
			if ready {
				increment("ready_for_acceptance")
			}
			if acceptedIncomplete {
				increment("accepted_with_incomplete_tasks")
			}
		}
		if view != "requirements" || !requirementMatches(requirement, filters) {
			continue
		}
		if check == "no_tasks" && !noTasks || check == "ready_for_acceptance" && !ready || check == "accepted_with_incomplete_tasks" && !acceptedIncomplete {
			continue
		}
		item := coverageRequirement(requirement)
		item["linked_tasks"] = taskItems
		item["task_count"] = count
		item["done_task_count"] = done
		item["incomplete_task_count"] = incomplete
		if count > 0 {
			progress = math.Round(progress/float64(count)*100) / 100
		}
		item["task_progress"] = progress
		item["no_tasks"] = noTasks
		item["ready_for_acceptance"] = ready
		item["accepted_with_incomplete_tasks"] = acceptedIncomplete
		items = append(items, item)
	}
	for _, task := range tasks {
		if task["has_children"] == true {
			continue
		}
		increment("leaf_tasks")
		if oneOf(task["status"], "todo", "doing") {
			increment("open_leaf_tasks")
		}
		if covered[text(task["id"])] {
			increment("linked_leaf_tasks")
			continue
		}
		increment("unlinked_leaf_tasks")
		if view != "unlinked_tasks" {
			continue
		}
		if status := filters["status"]; status != "" && text(task["status"]) != status {
			continue
		}
		if owner := filters["owner"]; owner != "" && text(task["assignee"]) != owner {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(text(task["wbs_code"])+"\n"+text(task["name"])+"\n"+text(task["assignee"])), strings.ToLower(query)) {
			continue
		}
		items = append(items, coverageTask(task))
	}
	result := coveragePage(items, filters)
	result["project_id"] = projectID
	result["project_name"] = project["name"]
	result["summary"] = summary
	result["basis"] = coverageBasis
	result["view"] = view
	return result, nil
}

// TaskRequirements exposes current direct associations. Recycled tasks return
// not_found, and void requirements appear only when explicitly requested.
func (s *Store) TaskRequirements(taskID string, filters map[string]string) (map[string]any, error) {
	query, err := coverageQuery(filters)
	if err != nil {
		return nil, err
	}
	if err = coverageStatus(filters, false); err != nil {
		return nil, err
	}
	s.Mu.RLock()
	defer s.Mu.RUnlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	task, err := get(tx, "tasks", taskID)
	if err != nil {
		return nil, err
	}
	if _, err = get(tx, "projects", text(task["project_id"])); err != nil {
		return nil, err
	}
	where := " WHERE l.task_id=? AND r.project_id=? AND r.deleted_at=''"
	args := []any{taskID, task["project_id"]}
	if status := filters["status"]; status != "" {
		where += " AND r.status=?"
		args = append(args, status)
	} else {
		where += " AND r.status!='void'"
	}
	if owner := filters["owner"]; owner != "" {
		where += " AND r.owner=?"
		args = append(args, owner)
	}
	if query != "" {
		where += " AND r.id IN (SELECT entity_id FROM search_doc WHERE entity_type='requirements' AND (title LIKE ? ESCAPE '\\' OR body LIKE ? ESCAPE '\\'))"
		args = append(args, like(query), like(query))
	}
	var total int
	if err = tx.QueryRow("SELECT count(*) FROM requirement_task_links l JOIN requirements r ON r.id=l.requirement_id"+where, args...).Scan(&total); err != nil {
		return nil, err
	}
	offset, _ := strconv.Atoi(filters["offset"])
	if offset < 0 {
		offset = 0
	}
	args = append(args, pageLimit(filters, 100), offset)
	rows, err := tx.Query("SELECT r.id,r.project_id,r.code,r.title,r.owner,r.status,r.revision,r.version FROM requirement_task_links l JOIN requirements r ON r.id=l.requirement_id"+where+" ORDER BY r.updated_at DESC,r.id DESC LIMIT ? OFFSET ?", args...)
	if err != nil {
		return nil, err
	}
	items, err := rawRows(rows)
	if err != nil {
		return nil, err
	}
	result := map[string]any{"items": items, "total": total, "next_cursor": ""}
	result["task_id"] = taskID
	result["project_id"] = task["project_id"]
	result["task"] = coverageTask(task)
	result["basis"] = coverageBasis
	return result, nil
}
