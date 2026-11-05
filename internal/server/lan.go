package server

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"projnote/internal/store"
)

const lanCookie = "projnote_member"

// LANHandler is a separate listener surface. It never delegates an unknown
// remote API route to the trusted, loopback-only owner handler.
func (s *Server) LANHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		if r.TLS == nil {
			writeError(w, 403, "tls_required", "成员连接必须使用HTTPS")
			return
		}
		if !validLANHost(r.Host) {
			writeError(w, 403, "invalid_host", "成员访问地址无效")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "https://"+r.Host {
			writeError(w, 403, "invalid_origin", "请求来源不受信任")
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeError(w, 403, "cross_site_request", "不允许跨站访问")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Header.Get("X-ProjNote-Request") != "1" {
			writeError(w, 403, "request_header_required", "缺少成员请求标识")
			return
		}
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			if r.Method != "GET" && r.Method != "HEAD" {
				writeError(w, 405, "method_not_allowed", "请求方法无效")
				return
			}
			s.static(w, r)
			return
		}
		if r.URL.Path == "/api/health" && r.Method == "GET" {
			jsonResult(w, 200, map[string]any{"status": "ok", "mode": "member", "version": s.Version})
			return
		}
		parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/"), "/"), "/")
		if len(parts) < 2 || parts[0] != "lan" {
			writeError(w, 403, "member_forbidden", "成员仅能访问自己的任务、日志和工时")
			return
		}
		s.lanMemberAPI(w, r, parts, true)
	})
}
func validLANHost(host string) bool {
	name, port, err := net.SplitHostPort(host)
	if err != nil {
		return false
	}
	n, e := strconv.Atoi(port)
	return e == nil && n >= 1024 && n <= 65436 && (name == "localhost" || net.ParseIP(name) != nil)
}
func (s *Server) lanOwnerAPI(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) < 2 {
		writeError(w, 404, "not_found", "接口不存在")
		return
	}
	// These endpoints are exposed only by Handler's strict loopback Host/Origin
	// checks. The remote listener never calls this owner switch.
	if p[1] == "session" || p[1] == "pair" || p[1] == "logout" || p[1] == "workspace" || p[1] == "my-daily" || p[1] == "tasks" {
		s.lanMemberAPI(w, r, p, false)
		return
	}
	if len(p) == 2 && p[1] == "status" && r.Method == "GET" {
		if s.LANInfo == nil {
			jsonResult(w, 200, map[string]any{"running": false, "enabled": false, "port": 18680, "addresses": []string{}})
		} else {
			jsonResult(w, 200, s.LANInfo())
		}
		return
	}
	if len(p) == 3 && p[1] == "certificate" && p[2] == "renew" && r.Method == "POST" {
		if s.RenewLANCertificate == nil {
			writeError(w, 501, "lan_unavailable", "当前启动方式未提供证书管理")
			return
		}
		if s.LANInfo != nil && s.LANInfo()["running"] == true {
			writeError(w, 409, "lan_running", "请先关闭局域网服务，再重新生成证书")
			return
		}
		result, err := s.RenewLANCertificate()
		if err != nil {
			handleLANErr(w, err)
			return
		}
		jsonResult(w, 200, result)
		return
	}
	if len(p) == 2 && p[1] == "certificate" && r.Method == "GET" {
		filename := filepath.Join(s.DataDir, "lan", "server.crt")
		info, err := os.Lstat(filename)
		if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
			writeError(w, 404, "certificate_unavailable", "请先启用局域网协同以生成证书")
			return
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			handleErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="ProjNote-LAN.crt"`)
		_, _ = w.Write(data)
		return
	}
	if len(p) == 2 && p[1] == "control" && r.Method == "POST" {
		input, ok := body(w, r)
		if !ok {
			return
		}
		for key := range input {
			if key != "enabled" && key != "port" {
				writeError(w, 400, "invalid_input", "未知协同设置字段："+key)
				return
			}
		}
		enabled, ok := input["enabled"].(bool)
		if !ok {
			writeError(w, 400, "invalid_input", "请选择是否启用局域网协同")
			return
		}
		port := 18680
		if value, exists := input["port"]; exists {
			n := exportVersion(value)
			if n < 1024 || n > 65436 {
				writeError(w, 400, "invalid_port", "端口须为1024～65436的整数")
				return
			}
			port = int(n)
		}
		if s.StartLAN == nil || s.StopLAN == nil {
			writeError(w, 501, "lan_unavailable", "当前启动方式未提供局域网服务")
			return
		}
		if enabled {
			result, err := s.StartLAN(port)
			if err != nil {
				handleLANErr(w, err)
				return
			}
			if err = s.Store.SetSettings(map[string]any{"lan_enabled": "1", "lan_port": strconv.Itoa(port)}); err != nil {
				_ = s.StopLAN()
				handleErr(w, err)
				return
			}
			jsonResult(w, 200, result)
		} else {
			err := s.StopLAN()
			if err != nil {
				handleLANErr(w, err)
				return
			}
			if err = s.Store.SetSettings(map[string]any{"lan_enabled": "0"}); err != nil {
				handleErr(w, err)
				return
			}
			if s.LANInfo == nil {
				jsonResult(w, 200, map[string]any{"running": false, "enabled": false})
			} else {
				jsonResult(w, 200, s.LANInfo())
			}
		}
		return
	}
	if p[1] == "members" {
		if len(p) == 2 {
			switch r.Method {
			case "GET":
				result, err := s.Store.ListLANMembers(filtersOf(r))
				resourceResult(w, result, err, 200)
				return
			case "POST":
				input, ok := body(w, r)
				if !ok {
					return
				}
				result, err := s.Store.CreateLANMember(input)
				resourceResult(w, result, err, 201)
				return
			}
		}
		if len(p) == 3 && r.Method == "PATCH" {
			input, ok := body(w, r)
			if !ok {
				return
			}
			result, err := s.Store.UpdateLANMember(p[2], input)
			resourceResult(w, result, err, 200)
			return
		}
		if len(p) == 4 && r.Method == "POST" {
			input, ok := body(w, r)
			if !ok {
				return
			}
			if p[3] == "pairing" {
				result, err := s.Store.CreateLANPairing(p[2], input)
				resourceResult(w, result, err, 201)
				return
			}
			if p[3] == "revoke" {
				result, err := s.Store.RevokeLANMember(p[2], input)
				resourceResult(w, result, err, 200)
				return
			}
		}
	}
	if len(p) == 2 && p[1] == "daily-entries" && r.Method == "GET" {
		result, err := s.Store.LANDailyEntries("", filtersOf(r))
		resourceResult(w, result, err, 200)
		return
	}
	writeError(w, 404, "not_found", "接口不存在或请求方法不正确")
}
func handleLANErr(w http.ResponseWriter, err error) {
	var ae *store.AppError
	if errors.As(err, &ae) {
		handleErr(w, err)
		return
	}
	writeError(w, 503, "lan_unavailable", err.Error())
}
func lanToken(r *http.Request) string {
	cookie, err := r.Cookie(lanCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}
func (s *Server) lanMemberAPI(w http.ResponseWriter, r *http.Request, p []string, secure bool) {
	if len(p) == 2 && p[1] == "pair" && r.Method == "POST" {
		input, ok := bodyLimit(w, r, 16<<10)
		if !ok {
			return
		}
		for key := range input {
			if key != "code" && key != "device_name" {
				writeError(w, 400, "invalid_input", "未知配对字段")
				return
			}
		}
		code, _ := input["code"].(string)
		device, _ := input["device_name"].(string)
		if len(code) != 64 || len([]rune(device)) > 100 {
			writeError(w, 400, "invalid_pairing", "请输入负责人提供的完整配对码")
			return
		}
		result, err := s.Store.ExchangeLANPairing(code, device)
		if err != nil {
			handleErr(w, err)
			return
		}
		token, _ := result["token"].(string)
		expires, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(result["expires_at"]))
		http.SetCookie(w, &http.Cookie{Name: lanCookie, Value: token, Path: "/", Secure: secure, HttpOnly: true, SameSite: http.SameSiteStrictMode, Expires: expires, MaxAge: 7 * 24 * 60 * 60})
		delete(result, "token")
		jsonResult(w, 200, result)
		return
	}
	if len(p) == 2 && p[1] == "logout" && r.Method == "POST" {
		if err := s.Store.LogOutLAN(lanToken(r)); err != nil {
			handleErr(w, err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: lanCookie, Value: "", Path: "/", Secure: secure, HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
		jsonResult(w, 200, map[string]any{"logged_out": true})
		return
	}
	member, err := s.Store.LANSession(lanToken(r))
	if err != nil {
		handleErr(w, err)
		return
	}
	id := fmt.Sprint(member["id"])
	if len(p) == 2 {
		switch p[1] {
		case "session":
			if r.Method == "GET" {
				jsonResult(w, 200, map[string]any{"member": member})
				return
			}
		case "workspace":
			if r.Method == "GET" {
				result, err := s.Store.LANWorkspace(id)
				resourceResult(w, result, err, 200)
				return
			}
		case "my-daily":
			if r.Method == "GET" {
				result, err := s.Store.LANDailyEntries(id, filtersOf(r))
				resourceResult(w, result, err, 200)
				return
			}
			if r.Method == "POST" {
				input, ok := body(w, r)
				if !ok {
					return
				}
				result, err := s.Store.SaveLANDailyEntry(id, input)
				resourceResult(w, result, err, 200)
				return
			}
		}
	}
	if len(p) == 3 && p[1] == "tasks" && r.Method == "PATCH" {
		input, ok := body(w, r)
		if !ok {
			return
		}
		result, err := s.Store.UpdateLANTask(id, p[2], input)
		resourceResult(w, result, err, 200)
		return
	}
	writeError(w, 403, "member_forbidden", "该操作仅供本机负责人使用")
}
