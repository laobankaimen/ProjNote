package server

import "net/http"

func (s *Server) projectMembersAPI(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 3 && r.Method == "GET" {
		result, err := s.Store.ProjectMembers(p[1], filtersOf(r))
		resourceResult(w, result, err, 200)
		return
	}
	if len(p) == 3 && r.Method == "POST" {
		input, ok := body(w, r)
		if !ok {
			return
		}
		result, err := s.Store.AddProjectMember(p[1], input)
		resourceResult(w, result, err, 201)
		return
	}
	if len(p) == 4 && r.Method == "PATCH" {
		input, ok := body(w, r)
		if !ok {
			return
		}
		result, err := s.Store.UpdateProjectMember(p[1], p[3], input)
		resourceResult(w, result, err, 200)
		return
	}
	writeError(w, 405, "method_not_allowed", "请求方法无效")
}
