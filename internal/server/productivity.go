package server

import (
	"net/http"
	"strconv"
	"strings"
)

// productivity keeps the URL spelling separate from the SQL resource names.
func (s *Server) productivity(w http.ResponseWriter, r *http.Request, p []string) {
	kind := p[0]
	if len(p) == 2 && p[1] == "summary" && kind == "time-entries" && r.Method == "GET" {
		result, err := s.Store.TimeSummary(filtersOf(r))
		if err != nil {
			handleErr(w, err)
		} else {
			jsonResult(w, 200, result)
		}
		return
	}
	if len(p) == 1 && r.Method == "GET" {
		var result map[string]any
		var err error
		if kind == "time-entries" {
			result, err = s.Store.ListTimeEntries(filtersOf(r))
		} else {
			result, err = s.Store.ListReminders(filtersOf(r))
		}
		if err != nil {
			handleErr(w, err)
		} else {
			jsonResult(w, 200, result)
		}
		return
	}
	if len(p) == 1 && r.Method == "POST" {
		input, ok := body(w, r)
		if !ok {
			return
		}
		var result map[string]any
		var err error
		if kind == "time-entries" {
			result, err = s.Store.CreateTimeEntry(input)
		} else {
			result, err = s.Store.CreateReminder(input)
		}
		if err != nil {
			handleErr(w, err)
		} else {
			jsonResult(w, 201, result)
		}
		return
	}
	if len(p) == 3 && p[2] == "done" && kind == "reminders" && r.Method == "POST" {
		input, ok := body(w, r)
		if !ok {
			return
		}
		result, err := s.Store.CompleteReminder(p[1], input)
		if err != nil {
			handleErr(w, err)
		} else {
			jsonResult(w, 200, result)
		}
		return
	}
	if len(p) == 2 {
		switch r.Method {
		case "GET":
			resource := strings.ReplaceAll(kind, "-", "_")
			result, err := s.Store.Get(resource, p[1])
			if err != nil {
				handleErr(w, err)
			} else {
				jsonResult(w, 200, result)
			}
			return
		case "PATCH":
			input, ok := body(w, r)
			if !ok {
				return
			}
			var result map[string]any
			var err error
			if kind == "time-entries" {
				result, err = s.Store.UpdateTimeEntry(p[1], input)
			} else {
				result, err = s.Store.UpdateReminder(p[1], input)
			}
			if err != nil {
				handleErr(w, err)
			} else {
				jsonResult(w, 200, result)
			}
			return
		case "DELETE":
			version, _ := strconv.Atoi(r.URL.Query().Get("version"))
			input := map[string]any{"version": version}
			if r.ContentLength > 0 {
				var ok bool
				input, ok = body(w, r)
				if !ok {
					return
				}
			}
			var result map[string]any
			var err error
			if kind == "time-entries" {
				result, err = s.Store.DeleteTimeEntry(p[1], input)
			} else {
				result, err = s.Store.SoftDelete("reminders", p[1], input)
			}
			if err != nil {
				handleErr(w, err)
			} else {
				jsonResult(w, 200, result)
			}
			return
		}
	}
	writeError(w, 404, "not_found", "接口不存在或请求方法不正确")
}
func (s *Server) history(w http.ResponseWriter, r *http.Request, kind, id string) {
	result, err := s.Store.EntityHistory(strings.ReplaceAll(kind, "-", "_"), id, filtersOf(r))
	if err != nil {
		handleErr(w, err)
	} else {
		jsonResult(w, 200, result)
	}
}
