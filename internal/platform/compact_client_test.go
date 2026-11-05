package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type compactMockService struct {
	mu                                                    sync.Mutex
	draft                                                 compactDraft
	records                                               []map[string]any
	taskInputs                                            []map[string]any
	createdTasks                                          []compactTask
	projectsOverride                                      []compactProject
	draftLoads, draftWrites, recordAttempts, projectPages int
	failRecord, failClear, failTask                       bool
	badHeader                                             bool
}

func newCompactMockService(t *testing.T) (*compactMockService, *httptest.Server) {
	t.Helper()
	state := &compactMockService{draft: compactDraft{Content: "服务端已有正文草稿", Mode: "record", Version: 1}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		write := func(status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": value})
		}
		fail := func(status int, message string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": message}})
		}
		if r.Method != "GET" && (r.Header.Get("X-ProjNote-Request") != "1" || r.Header.Get("Content-Type") != "application/json") {
			state.badHeader = true
			fail(403, "缺少同源写保护")
			return
		}
		switch r.URL.Path {
		case "/api/health":
			write(200, map[string]any{"version": "0.3.5-dev", "instance": "never-forward-this"})
		case "/api/desktop/draft":
			if r.Method == "GET" {
				state.draftLoads++
				write(200, state.draft)
				return
			}
			var d compactDraft
			if json.NewDecoder(r.Body).Decode(&d) != nil {
				fail(400, "invalid")
				return
			}
			if state.failClear && d.Content == "" {
				fail(503, "模拟草稿清除失败")
				return
			}
			if d.Version != state.draft.Version {
				fail(409, "草稿已被其他入口修改")
				return
			}
			if d.Mode == "" {
				d.Mode = state.draft.Mode
			}
			d.Version++
			d.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
			state.draft = d
			state.draftWrites++
			write(200, d)
		case "/api/projects":
			state.projectPages++
			offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
			items := []compactProject{}
			if offset == 0 {
				for i := 0; i < 100; i++ {
					items = append(items, compactProject{ID: strconv.Itoa(i), Name: "项目" + strconv.Itoa(i), Status: "active"})
				}
			} else if offset == 100 {
				items = append(items, compactProject{ID: "last", Name: "最后项目", Status: "active"}, compactProject{ID: "archive", Name: "归档项目", Status: "archived"})
			}
			total := 102
			if state.projectsOverride != nil {
				total = len(state.projectsOverride)
				items = nil
				if offset < total {
					end := offset + 100
					if end > total {
						end = total
					}
					items = state.projectsOverride[offset:end]
				}
			}
			write(200, map[string]any{"items": items, "total": total})
		case "/api/today":
			write(200, map[string]any{"tasks": append([]compactTask{{ID: "task", Name: "原生面板今日任务", Status: "doing"}}, state.createdTasks...)})
		case "/api/settings":
			write(200, map[string]any{"operator": "测试操作人"})
		case "/api/tasks":
			if state.failTask {
				fail(503, "模拟任务添加失败")
				return
			}
			var input map[string]any
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				fail(400, "invalid")
				return
			}
			state.taskInputs = append(state.taskInputs, input)
			task := compactTask{ID: "created-" + strconv.Itoa(len(state.taskInputs)), Name: input["name"].(string), ProjectID: input["project_id"].(string), PlanEnd: input["plan_end"].(string), Status: "todo"}
			state.createdTasks = append(state.createdTasks, task)
			write(201, map[string]any{"id": task.ID, "name": task.Name})
		case "/api/records":
			state.recordAttempts++
			if state.failRecord {
				fail(503, "模拟保存失败")
				return
			}
			var input map[string]any
			if json.NewDecoder(r.Body).Decode(&input) != nil {
				fail(400, "invalid")
				return
			}
			state.records = append(state.records, input)
			write(201, map[string]any{"id": "saved-record", "content": input["content"]})
		default:
			fail(404, "unknown")
		}
	}))
	t.Cleanup(server.Close)
	return state, server
}

func TestCompactNativeClientWritesAndConflicts(t *testing.T) {
	state, server := newCompactMockService(t)
	client, err := newCompactClient(server.URL + "/ignored/path#/compact")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if version, err := client.version(ctx); err != nil || version != "v0.3.5-dev" {
		t.Fatalf("version: %q %v", version, err)
	}
	draft, err := client.draft(ctx)
	if err != nil || draft.Content != "服务端已有正文草稿" {
		t.Fatalf("draft: %#v %v", draft, err)
	}
	draft.Content = "新正文"
	saved, err := client.putDraft(ctx, draft)
	if err != nil || saved.Version != 2 {
		t.Fatalf("write draft: %#v %v", saved, err)
	}
	if _, err = client.putDraft(ctx, draft); err == nil {
		t.Fatal("stale draft write succeeded")
	}
	projects, err := client.projects(ctx)
	if err != nil || len(projects) != 101 {
		t.Fatalf("projects pagination/filter: %d %v", len(projects), err)
	}
	tasks, err := client.today(ctx)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("today: %#v %v", tasks, err)
	}
	result, err := client.saveRecord(ctx, map[string]any{"content": "实际提交正文", "project_id": "last", "path": "http://external.invalid", "version": 99})
	if err != nil || result["id"] != "saved-record" {
		t.Fatalf("save record: %#v %v", result, err)
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.badHeader || len(state.records) != 1 || len(state.records[0]) != 2 || state.records[0]["content"] != "实际提交正文" {
		t.Fatalf("unexpected POST: %#v", state.records)
	}
	if state.projectPages != 2 {
		t.Fatalf("projects truncated: %d", state.projectPages)
	}
	if OpenCompactWindow(server.URL) == nil {
		t.Fatal("deprecated compact browser function unexpectedly succeeded")
	}
}

func TestCompactNativeClientRejectsRedirectsAndUnknownActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "http://example.invalid/", 302) }))
	defer server.Close()
	client, err := newCompactClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.draft(context.Background()); err == nil {
		t.Fatal("redirect accepted")
	}
	reply := client.dispatch(context.Background(), compactRequest{Seq: 9, Action: "http://example.invalid"})
	if reply.Error == "" || reply.Seq != 9 {
		t.Fatal(reply)
	}
}

func TestCompactTaskModeUsesTaskAPIAndKeepsSourceText(t *testing.T) {
	mock, server := newCompactMockService(t)
	client, err := newCompactClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	content := "\n  校对设备配置  \n完整正文与后续说明"
	draft := compactDraft{Content: content, Mode: "task", ProjectID: "last", Version: 1}
	reply := client.dispatch(context.Background(), compactRequest{Seq: 7, Action: "save", Payload: map[string]any{"content": content, "mode": "task", "project_id": "last", "path": "http://example.invalid", "name": "伪造标题"}})
	if reply.Error != "" || reply.Data.(map[string]any)["id"] != "created-1" {
		t.Fatalf("task save: %#v", reply)
	}
	items, err := client.today(context.Background())
	if err != nil || len(items) != 2 || items[1].ID != "created-1" {
		t.Fatalf("saved task absent from refreshed API: %#v %v", items, err)
	}
	mock.mu.Lock()
	input := mock.taskInputs[0]
	if len(mock.records) != 0 || input["name"] != "校对设备配置" || input["description"] != content || input["assignee"] != "测试操作人" || input["status"] != "todo" || len(input) != 7 || input["plan_start"] != time.Now().Format("2006-01-02") || input["plan_end"] != input["plan_start"] {
		t.Fatalf("task source incorrectly mapped: %#v", input)
	}
	mock.mu.Unlock()
	if _, err = client.saveTask(context.Background(), compactDraft{Content: content, Mode: "task"}); err == nil || !strings.Contains(err.Error(), "请选择项目") {
		t.Fatalf("missing project: %v", err)
	}
	if _, err = compactTaskInput(compactDraft{Content: strings.Repeat("字", 501), ProjectID: "last"}, "本人"); err == nil {
		t.Fatal("oversized first line accepted")
	}
	if _, err = compactTaskInput(compactDraft{Content: strings.Repeat("字", 500), ProjectID: "last"}, "本人"); err != nil {
		t.Fatal(err)
	}
	if _, err = client.saveDraftContent(context.Background(), compactDraft{Content: content, Mode: "wrong"}); err == nil {
		t.Fatal("invalid mode accepted")
	}
	draft.Mode = "record"
	written, err := client.putDraft(context.Background(), draft)
	if err != nil || written.Mode != "record" {
		t.Fatalf("record mode not persisted: %#v %v", written, err)
	}
	written.Mode = "task"
	written, err = client.putDraft(context.Background(), written)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := client.draft(context.Background())
	if err != nil || restored.Mode != "task" || restored.Content != content {
		t.Fatalf("mode roundtrip: %#v %v", restored, err)
	}
	mock.mu.Lock()
	mock.failTask = true
	mock.mu.Unlock()
	if _, err = client.saveDraftContent(context.Background(), restored); err == nil {
		t.Fatal("task API error was hidden")
	}
	if restored.Content != content || restored.Mode != "task" {
		t.Fatal("failed task mutated local draft")
	}
}
