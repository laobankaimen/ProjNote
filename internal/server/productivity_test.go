package server

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestTimeEntryHTTPRoundTripAndHistory(t *testing.T) {
	s := testServer(t)
	headers := map[string]string{"Content-Type": "application/json", "X-ProjNote-Request": "1"}
	request := func(method, path string, data map[string]any) map[string]any {
		raw:=[]byte{}
        if data!=nil{raw,_=json.Marshal(data)}
		return decodeData(t, call(s, method, path, string(raw), headers))
	}
	project := request("POST", "/api/projects", map[string]any{"name": "工时项目"})
	pid := project["id"].(string)
	task := request("POST", "/api/tasks", map[string]any{"project_id": pid, "name": "实际工作"})
	tree := request("GET", "/api/projects/"+pid+"/tree", nil)
	if tree["total"] != float64(1) {
		t.Fatalf("incomplete task tree: %v", tree)
	}
	date := time.Now().Format("2006-01-02")
	entry := request("POST", "/api/time-entries", map[string]any{"project_id": pid, "task_id": task["id"], "work_date": date, "minutes": 90, "note": "联调"})
	id := entry["id"].(string)
	summary := request("GET", "/api/time-entries/summary?date_from="+date+"&date_to="+date, nil)
	if summary["total_minutes"] != float64(90) {
		t.Fatalf("summary mismatch %v", summary)
	}
	entry = request("PATCH", "/api/time-entries/"+id, map[string]any{"version": entry["version"], "minutes": 120})
	if w := call(s, "PATCH", "/api/time-entries/"+id, `{"version":1,"minutes":130}`, headers); w.Code != 409 {
		t.Fatalf("old version accepted: %d %s", w.Code, w.Body.String())
	}
	history := request("GET", "/api/time-entries/"+id+"/history", nil)
	if history["total"] != float64(2) {
		t.Fatalf("failed request mutated history: %v", history)
	}
	items := history["items"].([]any)
	latest := items[0].(map[string]any)
	if latest["action"] != "update" || latest["operator"] != "本人" {
		t.Fatalf("wrong audit response %v", latest)
	}
	request("DELETE", fmt.Sprintf("/api/time-entries/%s?version=%.0f", id, entry["version"]), nil)
	if w := call(s, "GET", "/api/time-entries/"+id, "", nil); w.Code != 404 {
		t.Fatal("trashed time entry remains readable")
	}
	summary = request("GET", "/api/time-entries/summary", nil)
	if summary["total_minutes"] != float64(0) {
		t.Fatal("trashed time included in sum")
	}
	trash := request("GET", "/api/trash?kind=time_entries", nil)
	trashed := trash["items"].([]any)[0].(map[string]any)
	request("POST", "/api/trash/time_entries/"+id+"/restore", map[string]any{"version": trashed["version"]})
	summary = request("GET", "/api/time-entries/summary", nil)
	if summary["total_minutes"] != float64(120) {
		t.Fatal("restored sum mismatch")
	}
	if w := call(s, "POST", "/api/time-entries/"+id, `{}`, headers); w.Code != 404 {
		t.Fatal("unsupported item POST accepted")
	}
}
func TestRecordReminderHTTPIsAtomicAndCompletionIsVersioned(t *testing.T) {
	s := testServer(t)
	headers := map[string]string{"Content-Type": "application/json", "X-ProjNote-Request": "1"}
	if w := call(s, "POST", "/api/records", `{"content":"不应留下半条记录","reminder_due_at":"2026-02-31"}`, headers); w.Code != 400 {
		t.Fatalf("invalid time accepted: %s", w.Body.String())
	}
	records := decodeData(t, call(s, "GET", "/api/records", "", nil))
	if records["total"] != float64(0) {
		t.Fatal("invalid reminder left a record")
	}
	record := decodeData(t, call(s, "POST", "/api/records", `{"content":"明天检查 !明天"}`, headers))
	id := record["id"].(string)
	reminder, ok := record["reminder"].(map[string]any)
	if !ok {
		t.Fatalf("quick reminder absent: %v", record)
	}
	reminderID := reminder["id"].(string)
	raw, _ := json.Marshal(map[string]any{"version": record["version"], "content": "修改说明，提醒不重复"})
	record = decodeData(t, call(s, "PATCH", "/api/records/"+id, string(raw), headers))
	listed := decodeData(t, call(s, "GET", "/api/reminders?status=all&record_id="+id, "", nil))
	if listed["total"] != float64(1) {
		t.Fatal("record edit duplicated reminder")
	}
	raw, _ = json.Marshal(map[string]any{"version": reminder["version"]})
	done := decodeData(t, call(s, "POST", "/api/reminders/"+reminderID+"/done", string(raw), headers))
	if done["done_at"] == "" {
		t.Fatal("missing completion date")
	}
	raw, _ = json.Marshal(map[string]any{"version": done["version"]})
	if w := call(s, "POST", "/api/reminders/"+reminderID+"/done", string(raw), headers); w.Code != 409 {
		t.Fatalf("duplicate completion accepted: %d", w.Code)
	}
	audit := decodeData(t, call(s, "GET", "/api/audit/verify", "", nil))
	if audit["ok"] != true {
		t.Fatal("reminder broke audit chain")
	}
}
