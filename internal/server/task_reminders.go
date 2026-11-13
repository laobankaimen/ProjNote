package server

import (
	"net/http"
	"projnote/internal/platform"
)

func (s *Server) taskRemindersAPI(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 2 && p[1] == "status" && r.Method == "GET" {
		jsonResult(w, 200, platform.TaskNotificationStatus())
		return
	}
	if len(p) == 1 && r.Method == "GET" {
		result, err := s.Store.ListTaskReminders(filtersOf(r))
		resourceResult(w, result, err, 200)
		return
	}
	if len(p) == 2 && r.Method == "GET" {
		result, err := s.Store.GetTaskReminder(p[1])
		resourceResult(w, result, err, 200)
		return
	}
	if r.Method != "POST" && r.Method != "PATCH" {
		writeError(w, 405, "method_not_allowed", "请求方法无效")
		return
	}
	input, ok := body(w, r)
	if !ok {
		return
	}
	if len(p) == 1 && r.Method == "POST" {
		result, err := s.Store.CreateTaskReminder(input)
		resourceResult(w, result, err, 201)
		return
	}
	if len(p) == 2 && r.Method == "PATCH" {
		result, err := s.Store.UpdateTaskReminder(p[1], input)
		resourceResult(w, result, err, 200)
		return
	}
	if len(p) == 3 && p[2] == "done" && r.Method == "POST" {
		result, err := s.Store.DismissTaskReminder(p[1], input)
		resourceResult(w, result, err, 200)
		return
	}
	writeError(w, 404, "not_found", "接口不存在")
}
