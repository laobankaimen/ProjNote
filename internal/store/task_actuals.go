package store

import "strings"

// attachTaskActuals sums each active entry once at its task and then rolls the
// value up the current task tree. Project-level entries have no task allocation.
// The caller already holds the store lock. Tree responses can reuse their full
// graph; paged lists and details load just the graph for the involved projects.
func attachTaskActuals(q queryer, items []map[string]any, fullTree bool) error {
	if len(items) == 0 {
		return nil
	}
	projects := map[string]bool{}
	args := []any{}
	marks := []string{}
	for _, item := range items {
		pid := text(item["project_id"])
		if pid != "" && !projects[pid] {
			projects[pid] = true
			args = append(args, pid)
			marks = append(marks, "?")
		}
	}
	if len(args) == 0 {
		for _, item := range items {
			item["actual_minutes"] = int64(0)
			item["actual_hours"] = float64(0)
		}
		return nil
	}
	graph := items
	if !fullTree {
		rows, err := q.Query("SELECT id,parent_id FROM tasks WHERE deleted_at='' AND project_id IN ("+strings.Join(marks, ",")+")", args...)
		if err != nil {
			return err
		}
		graph, err = rawRows(rows)
		if err != nil {
			return err
		}
	}
	parents := map[string]string{}
	pending := map[string]int{}
	totals := map[string]int64{}
	for _, node := range graph {
		id := text(node["id"])
		parents[id] = text(node["parent_id"])
		pending[id] = 0
		totals[id] = 0
	}
	for _, parent := range parents {
		if _, exists := pending[parent]; exists {
			pending[parent]++
		}
	}
	rows, err := q.Query("SELECT task_id,sum(minutes) AS minutes FROM time_entries WHERE deleted_at='' AND task_id IS NOT NULL AND project_id IN ("+strings.Join(marks, ",")+") GROUP BY task_id", args...)
	if err != nil {
		return err
	}
	entries, err := rawRows(rows)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		id := text(entry["task_id"])
		if _, exists := totals[id]; exists {
			totals[id] = int64(num(entry["minutes"]))
		}
	}
	queue := []string{}
	for id, children := range pending {
		if children == 0 {
			queue = append(queue, id)
		}
	}
	processed := 0
	for cursor := 0; cursor < len(queue); cursor++ {
		id := queue[cursor]
		processed++
		parent := parents[id]
		if _, exists := pending[parent]; exists {
			totals[parent] += totals[id]
			pending[parent]--
			if pending[parent] == 0 {
				queue = append(queue, parent)
			}
		}
	}
	if processed != len(parents) {
		return invalid("任务树存在环，无法汇总实际工时")
	}
	for _, item := range items {
		minutes := totals[text(item["id"])]
		item["actual_minutes"] = minutes
		item["actual_hours"] = float64(minutes) / 60
	}
	return nil
}
