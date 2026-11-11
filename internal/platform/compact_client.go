package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// compactClient deliberately has fixed actions/paths and rejects redirects;
// native controls and the helper cannot choose a remote URL or arbitrary API.
type compactClient struct {
	base string
	http *http.Client
}
type compactRequest struct {
	Seq     int            `json:"seq"`
	Action  string         `json:"action"`
	Payload map[string]any `json:"payload,omitempty"`
}
type compactReply struct {
	Seq    int    `json:"seq"`
	Action string `json:"action"`
	Data   any    `json:"data,omitempty"`
	Error  string `json:"error,omitempty"`
	Status int    `json:"status,omitempty"`
}
type compactDraft struct {
	Content   string `json:"content"`
	Mode      string `json:"mode"`
	ProjectID string `json:"project_id"`
	Version   int    `json:"version"`
	UpdatedAt string `json:"updated_at"`
}
type compactProject struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}
type compactTask struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ProjectID   string `json:"project_id"`
	PlanEnd     string `json:"plan_end"`
	Status      string `json:"status"`
	Progress    int    `json:"progress"`
	Version     int    `json:"version"`
	HasChildren bool   `json:"has_children"`
	CanComplete bool   `json:"can_complete"`
}
type compactHTTPError struct {
	status  int
	message string
}

func (e *compactHTTPError) Error() string { return e.message }
func newCompactClient(value string) (*compactClient, error) {
	u, err := localCompactURL(value)
	if err != nil {
		return nil, err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &compactClient{base: u.Scheme + "://" + u.Host, http: &http.Client{Timeout: 8 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("原生便签拒绝HTTP重定向") }}}, nil
}
func (c *compactClient) request(ctx context.Context, method, path string, input, out any) error {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return err
	}
	if method != "GET" {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-ProjNote-Request", "1")
		req.Header.Set("Origin", c.base)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("本机服务连接失败，输入仍保留: %w", err)
	}
	defer resp.Body.Close()
	var envelope struct {
		Data  json.RawMessage `json:"data"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&envelope); err != nil {
		return fmt.Errorf("本机服务响应无效，输入仍保留: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := envelope.Error.Message
		if message == "" {
			message = resp.Status
		}
		return &compactHTTPError{resp.StatusCode, message}
	}
	if out != nil {
		return json.Unmarshal(envelope.Data, out)
	}
	return nil
}
func (c *compactClient) version(ctx context.Context) (string, error) {
	var health struct {
		Version string `json:"version"`
	}
	if err := c.request(ctx, "GET", "/api/health", nil, &health); err != nil {
		return "", err
	}
	if health.Version == "" {
		return "", errors.New("服务端未返回版本号")
	}
	return "v" + strings.TrimPrefix(health.Version, "v"), nil
}
func (c *compactClient) draft(ctx context.Context) (compactDraft, error) {
	var out compactDraft
	err := c.request(ctx, "GET", "/api/desktop/draft", nil, &out)
	if err == nil && out.Version < 1 {
		err = errors.New("服务端尚未确认有效草稿版本，输入仍保留")
	}
	if err == nil && out.Mode == "" {
		out.Mode = "record"
	}
	return out, err
}
func (c *compactClient) putDraft(ctx context.Context, d compactDraft) (compactDraft, error) {
	var out compactDraft
	input := map[string]any{"version": d.Version, "content": d.Content, "project_id": d.ProjectID}
	if d.Mode != "" {
		input["mode"] = d.Mode
	}
	err := c.request(ctx, "PUT", "/api/desktop/draft", input, &out)
	if err == nil && out.Version <= d.Version {
		err = errors.New("服务端尚未确认新草稿版本，输入仍保留")
	}
	if err == nil && out.Mode == "" {
		out.Mode = d.Mode
		if out.Mode == "" {
			out.Mode = "record"
		}
	}
	return out, err
}
func (c *compactClient) projects(ctx context.Context) ([]compactProject, error) {
	items := []compactProject{}
	for offset := 0; ; offset += 100 {
		var page struct {
			Items []compactProject `json:"items"`
			Total int              `json:"total"`
		}
		if err := c.request(ctx, "GET", "/api/projects?limit=100&offset="+strconv.Itoa(offset), nil, &page); err != nil {
			return nil, err
		}
		for _, p := range page.Items {
			if p.Status != "archived" && p.Status != "closed" {
				items = append(items, p)
			}
		}
		if len(page.Items) == 0 || offset+len(page.Items) >= page.Total {
			return items, nil
		}
		if offset > 100000 {
			return nil, errors.New("项目数量超过原生面板读取范围")
		}
	}
}
func (c *compactClient) today(ctx context.Context) ([]compactTask, error) {
	var out struct {
		Tasks []compactTask `json:"tasks"`
	}
	if err := c.request(ctx, "GET", "/api/today", nil, &out); err != nil {
		return nil, err
	}
	projects, err := c.projects(ctx)
	if err != nil {
		return nil, err
	}
	writable := map[string]bool{}
	for _, p := range projects {
		writable[p.ID] = true
	}
	for i := range out.Tasks {
		t := &out.Tasks[i]
		t.CanComplete = writable[t.ProjectID] && !t.HasChildren && t.Version > 0 && (t.Status == "todo" || t.Status == "doing")
	}
	return out.Tasks, nil
}

// Complete one selected leaf task using the exact version shown in the list.
// Rechecking its project does not advance that version or retry a stale write.
func (c *compactClient) completeTask(ctx context.Context, id string, version int) (compactTask, error) {
	var task compactTask
	if id == "" || len(id) > 128 || strings.TrimSpace(id) != id || strings.ContainsAny(id, "/\\?#") || version < 1 {
		return task, errors.New("请选择一个有效任务后完成")
	}
	path := "/api/tasks/" + url.PathEscape(id)
	if err := c.request(ctx, "GET", path, nil, &task); err != nil {
		return task, err
	}
	if task.Version != version {
		return task, &compactHTTPError{409, "任务已被其他入口修改，请查看刷新后的任务再操作"}
	}
	if task.HasChildren {
		return task, errors.New("父任务进度由子任务汇总，请完成具体子任务")
	}
	if task.Status != "todo" && task.Status != "doing" {
		return task, errors.New("任务已结束，请刷新今日任务")
	}
	if task.ProjectID == "" || strings.ContainsAny(task.ProjectID, "/\\?#") {
		return task, errors.New("任务项目无效，请在完整工作台核对")
	}
	var project compactProject
	if err := c.request(ctx, "GET", "/api/projects/"+url.PathEscape(task.ProjectID), nil, &project); err != nil {
		return task, err
	}
	if project.Status == "closed" || project.Status == "archived" || project.ID != task.ProjectID {
		return task, errors.New("该项目只读，无法完成任务")
	}
	err := c.request(ctx, "PATCH", path, map[string]any{"version": version, "status": "done", "progress": 100}, &task)
	if err == nil && (task.ID != id || task.Version <= version || task.Status != "done" || task.Progress != 100) {
		err = errors.New("任务完成结果尚未确认，请查看刷新后的列表；请勿重复提交")
	}
	return task, err
}
func (c *compactClient) saveRecord(ctx context.Context, payload map[string]any) (map[string]any, error) {
	content, _ := payload["content"].(string)
	project, _ := payload["project_id"].(string)
	if strings.TrimSpace(content) == "" {
		return nil, errors.New("请填写速记正文")
	}
	input := map[string]any{"content": content, "project_id": project}
	var result map[string]any
	if err := c.request(ctx, "POST", "/api/records", input, &result); err != nil {
		return nil, err
	}
	if id, ok := result["id"].(string); !ok || id == "" {
		return nil, errors.New("保存结果缺少记录ID，输入仍保留；请先在完整工作台核对，避免重复提交")
	}
	return result, nil
}
func compactTaskInput(d compactDraft, operator string) (map[string]any, error) {
	if strings.TrimSpace(d.ProjectID) == "" {
		return nil, errors.New("请选择项目后添加任务")
	}
	name := ""
	for _, line := range strings.Split(d.Content, "\n") {
		if name = strings.TrimSpace(line); name != "" {
			break
		}
	}
	if name == "" {
		return nil, errors.New("请填写任务标题，第一行将作为任务名称")
	}
	if len([]rune(name)) > 500 {
		return nil, errors.New("任务标题不能超过500个字符，请缩短第一行；正文仍保留")
	}
	operator = strings.TrimSpace(operator)
	if operator == "" {
		operator = "本人"
	}
	date := time.Now().Format("2006-01-02")
	return map[string]any{"project_id": d.ProjectID, "name": name, "description": d.Content, "assignee": operator, "plan_start": date, "plan_end": date, "status": "todo"}, nil
}
func (c *compactClient) saveTask(ctx context.Context, d compactDraft) (map[string]any, error) {
	// Validate the draft before asking settings or sending any write.
	if _, err := compactTaskInput(d, ""); err != nil {
		return nil, err
	}
	var settings struct {
		Operator string `json:"operator"`
	}
	if err := c.request(ctx, "GET", "/api/settings", nil, &settings); err != nil {
		return nil, err
	}
	input, err := compactTaskInput(d, settings.Operator)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err = c.request(ctx, "POST", "/api/tasks", input, &result); err != nil {
		return nil, err
	}
	if id, ok := result["id"].(string); !ok || id == "" {
		return nil, errors.New("保存结果缺少任务ID，输入仍保留；请先在完整工作台核对，避免重复提交")
	}
	return result, nil
}
func (c *compactClient) saveDraftContent(ctx context.Context, d compactDraft) (map[string]any, error) {
	switch d.Mode {
	case "task":
		return c.saveTask(ctx, d)
	case "", "record":
		return c.saveRecord(ctx, map[string]any{"content": d.Content, "project_id": d.ProjectID})
	default:
		return nil, errors.New("便签模式无效，输入仍保留")
	}
}
func (c *compactClient) dispatch(ctx context.Context, r compactRequest) compactReply {
	reply := compactReply{Seq: r.Seq, Action: r.Action}
	var err error
	switch r.Action {
	case "version":
		reply.Data, err = c.version(ctx)
	case "load_draft":
		reply.Data, err = c.draft(ctx)
	case "projects":
		reply.Data, err = c.projects(ctx)
	case "today":
		reply.Data, err = c.today(ctx)
	case "complete_task":
		raw, _ := json.Marshal(r.Payload)
		var input struct {
			ID      string `json:"id"`
			Version int    `json:"version"`
		}
		if err = json.Unmarshal(raw, &input); err == nil {
			reply.Data, err = c.completeTask(ctx, input.ID, input.Version)
		}
	case "draft":
		raw, _ := json.Marshal(r.Payload)
		var d compactDraft
		if err = json.Unmarshal(raw, &d); err == nil {
			reply.Data, err = c.putDraft(ctx, d)
		}
	case "save":
		raw, _ := json.Marshal(r.Payload)
		var d compactDraft
		if err = json.Unmarshal(raw, &d); err == nil {
			reply.Data, err = c.saveDraftContent(ctx, d)
		}
	default:
		err = errors.New("未知原生便签操作")
	}
	if err != nil {
		reply.Data = nil
		reply.Error = err.Error()
		var he *compactHTTPError
		if errors.As(err, &he) {
			reply.Status = he.status
		} else {
			reply.Status = 503
		}
	}
	return reply
}
