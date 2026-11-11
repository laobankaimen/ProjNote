package server

import "net/http"

// System startup registration belongs to this machine and its current OS user.
// This route is available only through the loopback owner handler, never LAN.
func (s *Server) autoStartAPI(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if s.AutoStartStatus == nil {
			jsonResult(w, 200, map[string]any{"supported": false, "enabled": false, "matches_current": false, "needs_repair": false, "message": "当前启动方式不支持管理系统自启动"})
			return
		}
		result, err := s.AutoStartStatus()
		if err != nil {
			writeError(w, 503, "autostart_unavailable", "无法读取系统自启动状态："+err.Error())
			return
		}
		jsonResult(w, 200, result)
	case http.MethodPut:
		input, ok := bodyLimit(w, r, 4096)
		if !ok {
			return
		}
		enabled, ok := input["enabled"].(bool)
		if len(input) != 1 || !ok {
			writeError(w, 400, "invalid_input", "仅接受 enabled 布尔值")
			return
		}
		if s.SetAutoStart == nil {
			writeError(w, 501, "autostart_unavailable", "当前启动方式不支持管理系统自启动")
			return
		}
		result, err := s.SetAutoStart(enabled)
		if err != nil {
			writeError(w, 503, "autostart_unavailable", "系统自启动设置未能完成："+err.Error())
			return
		}
		jsonResult(w, 200, result)
	default:
		w.Header().Set("Allow", "GET, PUT")
		writeError(w, 405, "method_not_allowed", "仅支持读取或设置系统自启动")
	}
}
