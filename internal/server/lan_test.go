package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func remoteCall(t *testing.T, remote *httptest.Server, method, path string, input any, cookie *http.Cookie, status int) (map[string]any, *http.Response) {
	t.Helper()
	var body io.Reader
	if input != nil {
		raw, _ := json.Marshal(input)
		body = bytes.NewReader(raw)
	}
	r, err := http.NewRequest(method, remote.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-ProjNote-Request", "1")
	r.Header.Set("Origin", remote.URL)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	response, err := remote.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != status {
		t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, raw)
	}
	var decoded map[string]any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err, string(raw))
	}
	if data, ok := decoded["data"].(map[string]any); ok {
		return data, response
	}
	return decoded, response
}
func pairRemote(t *testing.T, s *Server, remote *httptest.Server, member map[string]any) *http.Cookie {
	t.Helper()
	pair := exportJSON(t, s, "POST", "/api/lan/members/"+member["id"].(string)+"/pairing", map[string]any{"version": member["version"]}, 201)
	result, response := remoteCall(t, remote, "POST", "/api/lan/pair", map[string]any{"code": pair["code"], "device_name": "验收浏览器"}, nil, 200)
	if _, exists := result["token"]; exists {
		t.Fatal("plaintext session token returned in body")
	}
	cookies := response.Cookies()
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("session cookie lacks protections", cookies)
	}
	remoteCall(t, remote, "POST", "/api/lan/pair", map[string]any{"code": pair["code"]}, nil, 401)
	return cookies[0]
}
func TestLANMemberWhitelistIsolationTaskProgressDailyAndRevocation(t *testing.T) {
	s := testServer(t)
	p := exportJSON(t, s, "POST", "/api/projects", map[string]any{"name": "允许项目"}, 201)
	other := exportJSON(t, s, "POST", "/api/projects", map[string]any{"name": "未授权项目"}, 201)
	pid := p["id"].(string)
	task := exportJSON(t, s, "POST", "/api/tasks", map[string]any{"project_id": pid, "name": "成员任务", "description": "成员不应看见的任务私密备注"}, 201)
	secretTask := exportJSON(t, s, "POST", "/api/tasks", map[string]any{"project_id": other["id"], "name": "其他项目任务"}, 201)
	member := exportJSON(t, s, "POST", "/api/lan/members", map[string]any{"name": "同名成员", "project_ids": []any{pid}, "task_ids": []any{task["id"]}}, 201)
	remote := httptest.NewTLSServer(s.LANHandler())
	defer remote.Close()
	remoteCall(t, remote, "GET", "/api/lan/workspace", nil, nil, 401)
	cookie := pairRemote(t, s, remote, member)
	workspace, _ := remoteCall(t, remote, "GET", "/api/lan/workspace", nil, cookie, 200)
	raw, _ := json.Marshal(workspace)
	if strings.Contains(string(raw), "私密备注") || strings.Contains(string(raw), secretTask["id"].(string)) || len(workspace["projects"].([]any)) != 1 {
		t.Fatal("member projection leaked data", string(raw))
	}
	for _, path := range []string{"/api/settings", "/api/projects", "/api/tasks", "/api/records", "/api/search?q=任务", "/api/worklogs", "/api/time-entries", "/api/meeting-minutes", "/api/issues", "/api/backups", "/api/export-jobs", "/api/lan/status", "/api/lan/members", "/api/lan/daily-entries", "/api/lan/certificate", "/api/system/info", "/api/trash"} {
		remoteCall(t, remote, "GET", path, nil, cookie, 403)
	}
	for _, path := range []string{"/api/lan/control", "/api/lan/certificate/renew", "/api/lan/members/x/revoke"} {
		remoteCall(t, remote, "POST", path, map[string]any{}, cookie, 403)
	}
	remoteCall(t, remote, "PATCH", "/api/lan/tasks/"+secretTask["id"].(string), map[string]any{"version": secretTask["version"], "progress": 50}, cookie, 403)
	// Refresh after the owner's stable-ID assignment updated the task version.
	current := exportJSON(t, s, "GET", "/api/tasks/"+task["id"].(string), nil, 200)
	remoteCall(t, remote, "PATCH", "/api/lan/tasks/"+task["id"].(string), map[string]any{"version": current["version"], "assignee": "夹带改负责人"}, cookie, 400)
	updated, _ := remoteCall(t, remote, "PATCH", "/api/lan/tasks/"+task["id"].(string), map[string]any{"version": current["version"], "progress": 40, "status": "doing"}, cookie, 200)
	if updated["progress"] != float64(40) {
		t.Fatal(updated)
	}
	remoteCall(t, remote, "PATCH", "/api/lan/tasks/"+task["id"].(string), map[string]any{"version": current["version"], "progress": 50}, cookie, 409)
	date := time.Now().Format("2006-01-02")
	entry, _ := remoteCall(t, remote, "POST", "/api/lan/my-daily", map[string]any{"project_id": pid, "work_date": date, "content": "成员当天工作", "next_plan": "核对接口", "blockers": "", "minutes": 120}, cookie, 200)
	remoteCall(t, remote, "POST", "/api/lan/my-daily", map[string]any{"project_id": other["id"], "work_date": date, "content": "不允许跨项目", "minutes": 20}, cookie, 403)
	remoteCall(t, remote, "POST", "/api/lan/my-daily", map[string]any{"id": entry["id"], "version": entry["version"], "project_id": pid, "work_date": date, "content": "伪造成员", "member_id": "别的成员", "minutes": 120}, cookie, 400)
	own, _ := remoteCall(t, remote, "GET", "/api/lan/my-daily?member_id=other", nil, cookie, 200)
	if own["total"] != float64(1) {
		t.Fatal(own)
	}
	leader := exportJSON(t, s, "GET", "/api/lan/daily-entries?project_id="+pid, nil, 200)
	if leader["total"] != float64(1) {
		t.Fatal("owner did not receive member daily", leader)
	}
	draft := exportJSON(t, s, "POST", "/api/project-daily-logs/generate", map[string]any{"project_id": pid, "log_date": date}, 200)
	if !strings.Contains(draft["content"].(string), "成员当天工作") || draft["source_snapshot"].(map[string]any)["total_minutes"] != float64(120) {
		t.Fatal("team summary missing or doubled", draft)
	}
	remoteCall(t, remote, "POST", "/api/lan/my-daily", map[string]any{"id": entry["id"], "version": entry["version"], "project_id": pid, "work_date": date, "content": "成员更新后的工作", "next_plan": "继续验证", "blockers": "", "minutes": 180}, cookie, 200)
	exportJSON(t, s, "POST", "/api/project-daily-logs", draft, 409)
	exportJSON(t, s, "POST", "/api/lan/members/"+member["id"].(string)+"/revoke", map[string]any{"version": member["version"]}, 200)
	remoteCall(t, remote, "GET", "/api/lan/workspace", nil, cookie, 401)
	if verify := exportJSON(t, s, "GET", "/api/audit/verify", nil, 200); verify["ok"] != true {
		t.Fatal(verify)
	}
}
func TestLANRejectsForeignOriginAndPlainHTTP(t *testing.T) {
	s := testServer(t)
	request := httptest.NewRequest("GET", "http://127.0.0.1:18680/api/lan/session", nil)
	response := httptest.NewRecorder()
	s.LANHandler().ServeHTTP(response, request)
	if response.Code != 403 {
		t.Fatal("plain LAN HTTP accepted", response.Code)
	}
	remote := httptest.NewTLSServer(s.LANHandler())
	defer remote.Close()
	for _, method := range []string{"GET", "POST"} {
		r, _ := http.NewRequest(method, remote.URL+"/api/lan/pair", strings.NewReader(`{}`))
		r.Header.Set("Origin", "https://foreign.invalid")
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-ProjNote-Request", "1")
		response, err := remote.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 403 {
			t.Fatal(fmt.Sprint("cross origin accepted ", response.StatusCode))
		}
	}
}
