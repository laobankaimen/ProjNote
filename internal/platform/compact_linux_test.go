//go:build linux

package platform

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// Opt-in only: creates actual Gtk widgets against an isolated httptest service.
// No browser, user dataset, startup entry or desktop preference is touched.
func TestCompactNativeLifecycle(t *testing.T) {
	if os.Getenv("PROJNOTE_TEST_COMPACT_NATIVE") != "1" {
		t.Skip("native desktop check requires explicit opt-in")
	}
	mock, server := newCompactMockService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entry, err := startCompact(ctx, server.URL+"/#/compact", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer entry.Close()
	if state := CompactEntryState(entry); state.Backend != "gtk3-x11-native" {
		t.Fatalf("not native: %#v", state)
	}
	native := entry.(*compactEntry).Closer.(*compactLinuxCloser)
	command := func(action string, extra map[string]any) {
		t.Helper()
		payload := map[string]any{"test_action": action}
		for k, v := range extra {
			payload[k] = v
		}
		if err := native.send(payload); err != nil {
			t.Fatal(err)
		}
	}
	inspect := func() map[string]any {
		t.Helper()
		command("inspect", nil)
		select {
		case state := <-native.states:
			return state
		case <-time.After(3 * time.Second):
			t.Fatal("native widget inspect timed out")
			return nil
		}
	}
	wait := func(check func(map[string]any) bool) map[string]any {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		var state map[string]any
		for time.Now().Before(deadline) {
			state = inspect()
			if check(state) {
				return state
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("native UI condition not reached: %#v", state)
		return nil
	}
	wait(func(s map[string]any) bool {
		return s["loading"] == false && s["content"] == "服务端已有正文草稿"
	})
	if err := ShowCompact(entry); err != nil {
		t.Fatal(err)
	}
	wait(func(s map[string]any) bool { return s["visible"] == true && s["task_count"] == float64(1) })
	mock.mu.Lock()
	loadsBefore, writesBefore := mock.draftLoads, mock.draftWrites
	mock.mu.Unlock()
	command("fill", map[string]any{"content": "尚未触发一秒持久化的正文"})
	if err := ShowCompact(entry); err != nil {
		t.Fatal(err)
	}
	if state := inspect(); state["content"] != "尚未触发一秒持久化的正文" || state["visible"] != true {
		t.Fatalf("show replaced unsaved native input: %#v", state)
	}
	mock.mu.Lock()
	loadsAfter, writesAfter := mock.draftLoads, mock.draftWrites
	mock.mu.Unlock()
	if loadsAfter != loadsBefore || writesAfter != writesBefore {
		t.Fatalf("repeated show reloaded or persisted the draft: loads %d/%d writes %d/%d", loadsBefore, loadsAfter, writesBefore, writesAfter)
	}
	var output []byte
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		output, err = exec.Command("xwininfo", "-name", "ProjNote 便签").CombinedOutput()
		if err == nil && strings.Contains(string(output), "IsViewable") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil || !strings.Contains(string(output), "IsViewable") {
		t.Fatalf("native note is not visible: %v: %s", err, output)
	}
	t.Logf("visible native note:\n%s", output)
	windowID := regexp.MustCompile(`Window id: (0x[0-9a-f]+)`).FindStringSubmatch(string(output))
	if len(windowID) != 2 {
		t.Fatal("missing window ID")
	}
	properties, err := exec.Command("xprop", "-id", windowID[1], "_NET_WM_STATE", "_NET_WM_WINDOW_TYPE").CombinedOutput()
	if err != nil || !strings.Contains(string(properties), "_NET_WM_STATE_ABOVE") {
		t.Fatalf("not topmost: %v %s", err, properties)
	}
	t.Logf("native properties:\n%s", properties)
	command("fill", map[string]any{"content": "原生控件输入，收起仍保留"})
	wait(func(s map[string]any) bool {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		return mock.draft.Content == "原生控件输入，收起仍保留" && s["draft_busy"] == false
	})
	command("close_button", nil)
	state := wait(func(s map[string]any) bool { return s["visible"] == false && s["entry_visible"] == true })
	if state["content"] != "原生控件输入，收起仍保留" {
		t.Fatal(state)
	}
	if err := ShowCompact(entry); err != nil {
		t.Fatal(err)
	}
	wait(func(s map[string]any) bool {
		return s["visible"] == true && s["content"] == "原生控件输入，收起仍保留"
	})
	mock.mu.Lock()
	mock.failRecord = true
	mock.mu.Unlock()
	command("save", nil)
	wait(func(s map[string]any) bool {
		return s["saving"] == false && strings.Contains(fmt.Sprint(s["status"]), "保存失败")
	})
	if state = inspect(); state["content"] != "原生控件输入，收起仍保留" {
		t.Fatalf("failed save cleared input: %#v", state)
	}
	mock.mu.Lock()
	mock.failRecord = false
	mock.mu.Unlock()
	command("save", nil)
	wait(func(s map[string]any) bool {
		return s["saving"] == false && s["content"] == "" && strings.Contains(fmt.Sprint(s["status"]), "速记已保存")
	})
	mock.mu.Lock()
	if len(mock.records) != 1 || mock.records[0]["content"] != "原生控件输入，收起仍保留" || mock.draft.Content != "" || mock.badHeader {
		t.Fatalf("native save did not reach real HTTP handler: %#v %#v", mock.records, mock.draft)
	}
	mock.mu.Unlock()
	// A confirmed record must not be resubmitted when clearing its draft fails.
	mock.mu.Lock()
	mock.failClear = true
	mock.mu.Unlock()
	command("fill", map[string]any{"content": "已保存但清草稿失败"})
	wait(func(s map[string]any) bool {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		return mock.draft.Content == "已保存但清草稿失败" && s["draft_busy"] == false
	})
	command("save", nil)
	wait(func(s map[string]any) bool {
		return s["saving"] == false && s["content"] == "" && strings.Contains(fmt.Sprint(s["status"]), "请勿重复提交")
	})
	command("save", nil)
	wait(func(s map[string]any) bool { return strings.Contains(fmt.Sprint(s["status"]), "请填写速记正文") })
	mock.mu.Lock()
	if len(mock.records) != 2 || mock.draft.Content != "已保存但清草稿失败" {
		t.Fatalf("clear failure repeated or lost note: %#v %#v", mock.records, mock.draft)
	}
	mock.failClear = false
	mock.mu.Unlock()
	command("restore", nil)
	wait(func(s map[string]any) bool {
		return s["conflict"] == false && s["content"] == "已保存但清草稿失败"
	})
	// An independent writer changes the version; autosave must preserve local text.
	mock.mu.Lock()
	mock.draft.Version++
	mock.draft.Content = "另一个入口的新草稿"
	mock.mu.Unlock()
	command("fill", map[string]any{"content": "409时本地正文保留"})
	state = wait(func(s map[string]any) bool { return s["conflict"] == true })
	if state["content"] != "409时本地正文保留" {
		t.Fatal(state)
	}
	mock.mu.Lock()
	if mock.draft.Content != "另一个入口的新草稿" {
		t.Fatal("409 overwrote remote draft")
	}
	mock.mu.Unlock()
	cancel()
	done := make(chan error, 1)
	go func() { done <- entry.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("native panel ignored cancellation")
	}
	if err = entry.Close(); err != nil {
		t.Fatal(err)
	}
	if err = ShowCompact(entry); err != errCompactClosed {
		t.Fatalf("closed native entry accepted show: %v", err)
	}
	t.Log("PASS native GTK: actual project combo/text/list/button, save HTTP, failed-save retention, 1s draft persistence, collapse/reopen, 409 retention and close; no compact browser command exists")
}

func TestCompactWithoutDisplayDoesNotFallback(t *testing.T) {
	t.Setenv("DISPLAY", "")
	if _, err := StartCompact(context.Background(), "http://127.0.0.1:18600/#/compact", "http://127.0.0.1:18600/"); err == nil || !strings.Contains(err.Error(), "DISPLAY") {
		t.Fatalf("missing native display must be explicit: %v", err)
	}
}

func TestCompactNativeReadyDoesNotWaitForHTTP(t *testing.T) {
	if os.Getenv("PROJNOTE_TEST_COMPACT_NATIVE") != "1" {
		t.Skip("native desktop check requires explicit opt-in")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := time.Now()
	entry, err := StartCompact(ctx, server.URL+"/#/compact", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer entry.Close()
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("native READY waited for HTTP reply: %v", elapsed)
	}
	cancel()
	if err = entry.Close(); err != nil {
		t.Fatal(err)
	}
	t.Log("native READY succeeds while business HTTP has not responded")
}

func TestCompactLinuxShowRejectsExitedAndClosingProcess(t *testing.T) {
	done := make(chan struct{})
	calls := 0
	native := &compactLinuxCloser{compactProcessCloser: &compactProcessCloser{closeFn: func() error { return nil }}, done: done, send: func(v any) error {
		calls++
		if v.(map[string]any)["action"] != "show" {
			t.Fatalf("unexpected action: %#v", v)
		}
		return nil
	}}
	if err := native.Show(); err != nil || calls != 1 {
		t.Fatalf("show: %d %v", calls, err)
	}
	close(done)
	if err := native.Show(); err != errCompactClosed || calls != 1 {
		t.Fatalf("exited process: %d %v", calls, err)
	}
	active := &compactLinuxCloser{compactProcessCloser: &compactProcessCloser{closeFn: func() error { return nil }}, done: make(chan struct{}), send: native.send}
	if err := active.Close(); err != nil {
		t.Fatal(err)
	}
	if err := active.Show(); err != errCompactClosed || calls != 1 {
		t.Fatalf("closing process: %d %v", calls, err)
	}
}

func TestCompactNativeTaskAndRecordModes(t *testing.T) {
	if os.Getenv("PROJNOTE_TEST_COMPACT_NATIVE") != "1" {
		t.Skip("native desktop check requires explicit opt-in")
	}
	mock, server := newCompactMockService(t)
	mock.mu.Lock()
	mock.draft = compactDraft{Mode: "task", Version: 1}
	mock.projectsOverride = []compactProject{{ID: "only", Name: "唯一项目", Status: "active"}}
	mock.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entry, err := StartCompact(ctx, server.URL, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer entry.Close()
	native := entry.(*compactEntry).Closer.(*compactLinuxCloser)
	command := func(action string, extra map[string]any) {
		t.Helper()
		payload := map[string]any{"test_action": action}
		for k, v := range extra {
			payload[k] = v
		}
		if err := native.send(payload); err != nil {
			t.Fatal(err)
		}
	}
	inspect := func() map[string]any {
		t.Helper()
		command("inspect", nil)
		select {
		case s := <-native.states:
			return s
		case <-time.After(3 * time.Second):
			t.Fatal("inspect timed out")
			return nil
		}
	}
	wait := func(check func(map[string]any) bool) map[string]any {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		var state map[string]any
		for time.Now().Before(deadline) {
			state = inspect()
			if check(state) {
				return state
			}
			time.Sleep(40 * time.Millisecond)
		}
		t.Fatalf("native mode condition: %#v", state)
		return nil
	}
	if err := ShowCompact(entry); err != nil {
		t.Fatal(err)
	}
	wait(func(s map[string]any) bool {
		return s["loading"] == false && s["mode"] == "task" && s["project_id"] == "only" && s["save_label"] == "添加任务"
	})
	command("fill", map[string]any{"content": "  原生新增任务  \n保留全部说明"})
	command("mode", map[string]any{"mode": "record"})
	command("mode", map[string]any{"mode": "task"})
	if err := ShowCompact(entry); err != nil {
		t.Fatal(err)
	}
	state := inspect()
	if state["content"] != "  原生新增任务  \n保留全部说明" || state["mode"] != "task" {
		t.Fatalf("mode switch/show lost content: %#v", state)
	}
	wait(func(s map[string]any) bool {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		return s["draft_busy"] == false && mock.draft.Mode == "task" && mock.draft.Content == "  原生新增任务  \n保留全部说明"
	})
	command("project", map[string]any{"project_id": ""})
	command("save", nil)
	wait(func(s map[string]any) bool { return strings.Contains(fmt.Sprint(s["status"]), "请选择项目") })
	mock.mu.Lock()
	if len(mock.taskInputs) != 0 {
		t.Fatal("unclassified task sent to API")
	}
	mock.failTask = true
	mock.mu.Unlock()
	command("project", map[string]any{"project_id": "only"})
	command("save", nil)
	state = wait(func(s map[string]any) bool {
		return s["saving"] == false && strings.Contains(fmt.Sprint(s["status"]), "模拟任务添加失败")
	})
	if state["content"] != "  原生新增任务  \n保留全部说明" || state["mode"] != "task" {
		t.Fatal("task failure lost draft", state)
	}
	mock.mu.Lock()
	mock.failTask = false
	mock.mu.Unlock()
	command("save", nil)
	wait(func(s map[string]any) bool {
		return s["saving"] == false && s["content"] == "" && strings.Contains(fmt.Sprint(s["status"]), "已添加到今日任务") && len(s["task_items"].([]any)) == 2
	})
	mock.mu.Lock()
	if len(mock.taskInputs) != 1 || len(mock.records) != 0 || mock.taskInputs[0]["name"] != "原生新增任务" || mock.taskInputs[0]["description"] != "  原生新增任务  \n保留全部说明" || mock.taskInputs[0]["assignee"] != "测试操作人" || mock.draft.Mode != "task" {
		t.Fatalf("wrong native task destination/source: %#v %#v", mock.taskInputs, mock.draft)
	}
	mock.mu.Unlock()
	command("mode", map[string]any{"mode": "record"})
	command("fill", map[string]any{"content": "模式草稿里的速记正文"})
	wait(func(s map[string]any) bool {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		return s["draft_busy"] == false && mock.draft.Mode == "record" && mock.draft.Content == "模式草稿里的速记正文"
	})
	command("close_button", nil)
	if err := ShowCompact(entry); err != nil {
		t.Fatal(err)
	}
	state = wait(func(s map[string]any) bool {
		return s["visible"] == true && s["mode"] == "record" && s["content"] == "模式草稿里的速记正文"
	})
	command("save", nil)
	wait(func(s map[string]any) bool {
		return s["saving"] == false && s["content"] == "" && strings.Contains(fmt.Sprint(s["status"]), "速记已保存")
	})
	mock.mu.Lock()
	if len(mock.records) != 1 || len(mock.taskInputs) != 1 {
		t.Fatal("record mode wrote wrong API")
	}
	mock.draft.Version++
	mock.draft.Mode = "record"
	mock.draft.Content = "其他入口的速记"
	mock.mu.Unlock()
	command("mode", map[string]any{"mode": "task"})
	command("fill", map[string]any{"content": "冲突时任务正文保留"})
	state = wait(func(s map[string]any) bool { return s["conflict"] == true })
	if state["mode"] != "task" || state["content"] != "冲突时任务正文保留" {
		t.Fatal("409 lost local mode/content", state)
	}
	command("restore", nil)
	wait(func(s map[string]any) bool {
		return s["conflict"] == false && s["mode"] == "record" && s["content"] == "其他入口的速记" && s["save_label"] == "保存速记"
	})
	t.Log("PASS native GTK mode controls: first blank task/sole project, mode switch retention, missing-project guard, task failure retention, real task POST and refreshed Today, record POST, mode draft restore, collapse/Show and 409 retention")
}

func TestCompactNativeSingleArrowCloseReopenNoDrag(t *testing.T) {
	if os.Getenv("PROJNOTE_TEST_COMPACT_NATIVE") != "1" {
		t.Skip("native desktop check requires explicit opt-in")
	}
	browserDir := t.TempDir()
	browserLog := filepath.Join(browserDir, "browser.log")
	if err := os.WriteFile(filepath.Join(browserDir, "xdg-open"), []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$PROJNOTE_TEST_BROWSER_LOG\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", browserDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PROJNOTE_TEST_BROWSER_LOG", browserLog)
	mock, server := newCompactMockService(t)
	mock.mu.Lock()
	for i := 0; i < 4; i++ {
		mock.createdTasks = append(mock.createdTasks, compactTask{ID: fmt.Sprint(i), Name: "行高核对任务", Status: "todo"})
	}
	mock.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entry, err := StartCompact(ctx, server.URL, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer entry.Close()
	native := entry.(*compactEntry).Closer.(*compactLinuxCloser)
	command := func(action string, extra map[string]any) {
		t.Helper()
		payload := map[string]any{"test_action": action}
		for k, v := range extra {
			payload[k] = v
		}
		if err := native.send(payload); err != nil {
			t.Fatal(err)
		}
	}
	read := func() map[string]any {
		t.Helper()
		select {
		case state := <-native.states:
			return state
		case <-time.After(3 * time.Second):
			t.Fatal("native entry state timed out")
			return nil
		}
	}
	inspect := func() map[string]any { command("inspect", nil); return read() }
	wait := func(check func(map[string]any) bool) map[string]any {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		var state map[string]any
		for time.Now().Before(deadline) {
			state = inspect()
			if check(state) {
				return state
			}
			time.Sleep(35 * time.Millisecond)
		}
		t.Fatalf("native entry state not reached: %#v", state)
		return nil
	}
	bounds := func(state map[string]any, key string) (x, y, w, h int) {
		m := state[key].(map[string]any)
		return int(m["x"].(float64)), int(m["y"].(float64)), int(m["width"].(float64)), int(m["height"].(float64))
	}
	arrow := func(s map[string]any) bool {
		x, y, w, h := bounds(s, "entry_geometry")
		ax, ay, aw, ah := bounds(s, "workarea")
		return s["entry_visible"] == true && s["visible"] == false && s["entry_text"] == "‹" && w == 26 && h == 52 && x+w == ax+aw && y+h == ay+ah-8
	}
	opened := func(s map[string]any) bool { return s["visible"] == true && s["entry_visible"] == false }
	state := wait(func(s map[string]any) bool {
		return s["loading"] == false && s["version_text"] == "v0.3.5-dev" && arrow(s)
	})
	if fmt.Sprint(state["menu_items"]) != "[open_full close]" {
		t.Fatal("obsolete entry menu actions", state)
	}
	command("attempt_drag", map[string]any{"target": "entry"})
	wait(arrow)
	command("entry_click", nil)
	state = wait(func(s map[string]any) bool { return opened(s) && s["task_count"] == float64(5) })
	if state["panel_decorated"] != false || state["panel_resizable"] != false || state["close_text"] != "×" {
		t.Fatal("panel still has native drag/title or duplicate controls", state)
	}
	var rowHeight float64
	for _, h := range state["task_row_heights"].([]any) {
		rowHeight = max(rowHeight, h.(float64))
	}
	if state["task_view_height"].(float64) < 4*rowHeight+2 {
		t.Fatal("four task rows do not fit", state)
	}
	command("fill", map[string]any{"content": "关闭箭头循环不丢正文"})
	wait(func(s map[string]any) bool {
		mock.mu.Lock()
		defer mock.mu.Unlock()
		return s["draft_busy"] == false && mock.draft.Content == "关闭箭头循环不丢正文"
	})
	mock.mu.Lock()
	loadsBefore, versionBefore := mock.draftLoads, mock.draft.Version
	mock.mu.Unlock()
	if _, err := os.Stat(browserLog); !os.IsNotExist(err) {
		t.Fatal("browser opened without an explicit workbench click", err)
	}
	if state["full_text"] != "打开完整工作台" {
		t.Fatal("direct workbench button missing", state)
	}
	command("full_button", nil)
	state = wait(func(s map[string]any) bool {
		output, err := os.ReadFile(browserLog)
		return err == nil && strings.TrimSpace(string(output)) == server.URL && opened(s)
	})
	if state["content"] != "关闭箭头循环不丢正文" {
		t.Fatal("workbench click lost draft", state)
	}

	for cycle := 0; cycle < 3; cycle++ {
		// The close and open paths invoke real owned Gtk.Button controls; the
		// pointer path delivers GDK press/release events only to our own button.
		command("close_button", nil)
		wait(arrow)
		command("entry_pointer_click", nil)
		state = wait(opened)
		if err := native.ShowEntry(); err != nil {
			t.Fatal(err)
		}
		state = wait(opened)
		if err := native.Show(); err != nil {
			t.Fatal(err)
		}
		state = wait(opened)
		px, py, pw, ph := bounds(state, "panel_geometry")
		command("attempt_drag", map[string]any{"target": "panel"})
		state = wait(func(s map[string]any) bool {
			x, y, w, h := bounds(s, "panel_geometry")
			return opened(s) && x == px && y == py && w == pw && h == ph
		})
		ax, ay, aw, ah := bounds(state, "workarea")
		if px < ax || py < ay || px+pw > ax+aw || py+ph > ay+ah {
			t.Fatal("actual panel outside workarea", state)
		}
		if state["content"] != "关闭箭头循环不丢正文" {
			t.Fatal("open/show cycle lost draft", state)
		}
		command("alt_f4", nil)
		wait(arrow)
		if err := native.ShowEntry(); err != nil {
			t.Fatal(err)
		}
		wait(arrow)
		command("entry_click", nil)
		wait(opened)
		command("close_panel", nil)
		wait(arrow)
		command("entry_click", nil)
		wait(opened)
	}
	mock.mu.Lock()
	loadsAfter, versionAfter := mock.draftLoads, mock.draft.Version
	mock.mu.Unlock()
	if loadsAfter != loadsBefore || versionAfter != versionBefore {
		t.Fatalf("close/open reloaded or rewrote draft: loads %d/%d version %d/%d", loadsBefore, loadsAfter, versionBefore, versionAfter)
	}
	command("geometry_probe", map[string]any{"geometry": map[string]int{"x": -1920, "y": -240, "width": 1920, "height": 1080}, "workarea": map[string]int{"x": -1920, "y": -200, "width": 3840, "height": 1000}, "width": 26, "height": 52})
	probe := read()
	if probe["x"] != float64(-26) || probe["y"] != float64(740) {
		t.Fatal("negative monitor fixed anchor", probe)
	}
	command("geometry_probe", map[string]any{"geometry": map[string]int{"x": -1920, "y": -240, "width": 1920, "height": 1080}, "workarea": map[string]int{"x": -1880, "y": -200, "width": 1832, "height": 1000}, "width": 26, "height": 52})
	probe = read()
	if probe["x"] != float64(-74) || probe["y"] != float64(740) {
		t.Fatal("right reserved taskbar fixed anchor", probe)
	}
	t.Log("PASS GTK single arrow: 26x52 fixed lower right; real button and owned pointer open; exclusive icon/panel; borderless non-draggable editor; four complete task rows; close/Alt+F4/WM-close three cycles; ShowEntry preserves open editor; draft/version unchanged; real service version; negative monitor/taskbar geometry")
}

func TestCompactLinuxShowEntryRejectsExitedAndClosingProcess(t *testing.T) {
	done := make(chan struct{})
	calls := 0
	native := &compactLinuxCloser{compactProcessCloser: &compactProcessCloser{closeFn: func() error { return nil }}, done: done, send: func(value any) error {
		calls++
		if value.(map[string]any)["action"] != "show_entry" {
			t.Fatalf("wrong entry action: %#v", value)
		}
		return nil
	}}
	if err := native.ShowEntry(); err != nil || calls != 1 {
		t.Fatalf("show entry: %d %v", calls, err)
	}
	close(done)
	if err := native.ShowEntry(); err != errCompactClosed || calls != 1 {
		t.Fatalf("exited show entry: %d %v", calls, err)
	}
	active := &compactLinuxCloser{compactProcessCloser: &compactProcessCloser{closeFn: func() error { return nil }}, done: make(chan struct{}), send: native.send}
	if err := active.Close(); err != nil {
		t.Fatal(err)
	}
	if err := active.ShowEntry(); err != errCompactClosed || calls != 1 {
		t.Fatalf("closing show entry: %d %v", calls, err)
	}
}
