package server

import (
	"encoding/json"
	"mime"
	"net/http"
	"strconv"
)

func resourceResult(w http.ResponseWriter, result any, err error, status int) {
	if err != nil {
		handleErr(w, err)
		return
	}
	jsonResult(w, status, result)
}

func (s *Server) planVersionsAPI(w http.ResponseWriter, r *http.Request, p []string) {
	if p[0] == "projects" && len(p) >= 3 {
		if len(p) == 4 && p[3] == "compare-export" && r.Method == "POST" {
			s.exportPlanComparison(w, r, p[1])
			return
		}
		if len(p) == 3 {
			switch r.Method {
			case "GET":
				result, err := s.Store.ListPlanVersions(p[1], filtersOf(r))
				resourceResult(w, result, err, 200)
				return
			case "POST":
				input, ok := body(w, r)
				if !ok {
					return
				}
				result, err := s.Store.CreatePlanVersion(p[1], input)
				resourceResult(w, result, err, 201)
				return
			}
		}
		if len(p) == 4 && p[3] == "compare" && r.Method == "GET" {
			from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
			if to == "current" {
				to = ""
			}
			if from == "" || from == "current" {
				writeError(w, 400, "version_required", "请选择作为比较起点的已保存计划版本")
				return
			}
			result, err := s.Store.ComparePlanVersions(p[1], from, to)
			resourceResult(w, result, err, 200)
			return
		}
	} else if p[0] == "plan-versions" {
		if len(p) == 1 && r.Method == "GET" {
			result, err := s.Store.ListPlanVersions(r.URL.Query().Get("project_id"), filtersOf(r))
			resourceResult(w, result, err, 200)
			return
		}
		if len(p) == 3 && p[2] == "history" && r.Method == "GET" {
			s.history(w, r, "plan_versions", p[1])
			return
		}
		if (len(p) == 2 || len(p) == 3 && p[2] == "download") && r.Method == "GET" {
			result, err := s.Store.GetPlanVersion(p[1])
			if err != nil {
				handleErr(w, err)
				return
			}
			if len(p) == 3 {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "ProjNote-plan-" + p[1] + ".json"}))
				_ = json.NewEncoder(w).Encode(result)
			} else {
				jsonResult(w, 200, result)
			}
			return
		}
		if len(p) == 2 && (r.Method == "PATCH" || r.Method == "PUT" || r.Method == "DELETE" || r.Method == "POST") {
			writeError(w, 409, "read_only", "已保存计划版本只读，请从当前计划另存新版本")
			return
		}
	}
	writeError(w, 404, "not_found", "接口不存在或请求方法不正确")
}

func (s *Server) requirementRevisionsAPI(w http.ResponseWriter, r *http.Request, p []string) {
	if r.Method != "GET" {
		writeError(w, 409, "read_only", "需求历史版本只读，请编辑当前需求并保留变更说明")
		return
	}
	if len(p) == 3 {
		result, err := s.Store.RequirementRevisions(p[1], filtersOf(r))
		resourceResult(w, result, err, 200)
		return
	}
	if len(p) == 4 && p[3] == "compare" {
		from, firstErr := strconv.Atoi(r.URL.Query().Get("from"))
		to, secondErr := strconv.Atoi(r.URL.Query().Get("to"))
		if firstErr != nil || secondErr != nil || from < 1 || to < 1 {
			writeError(w, 400, "invalid_revision", "请选择两个有效的需求版本")
			return
		}
		result, err := s.Store.CompareRequirementRevisions(p[1], from, to)
		resourceResult(w, result, err, 200)
		return
	}
	if len(p) == 4 {
		revision, err := strconv.Atoi(p[3])
		if err != nil || revision < 1 {
			writeError(w, 400, "invalid_revision", "需求版本必须为正整数")
			return
		}
		result, err := s.Store.GetRequirementRevision(p[1], revision)
		resourceResult(w, result, err, 200)
		return
	}
	writeError(w, 404, "not_found", "接口不存在或请求方法不正确")
}
