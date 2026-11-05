package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"projnote/internal/backup"
	"projnote/internal/files"
	"projnote/internal/store"
	"projnote/internal/webui"
)

type Server struct {
	Store        *store.Store
	DataDir      string
	Host         string
	Version      string
	RuntimeToken string
	Shutdown     func()
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := make([]byte, 12)
		_, _ = rand.Read(id)
		w.Header().Set("X-Request-Id", hex.EncodeToString(id))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("request panic", "request_id", hex.EncodeToString(id))
				writeError(w, 500, "internal_error", "请求失败，请查看运行日志")
			}
		}()
		if path.Clean(r.URL.Path) != r.URL.Path {
			http.NotFound(w, r)
			return
		}
		if r.Host != s.Host {
			writeError(w, 403, "invalid_host", "访问地址不受信任")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+s.Host {
			writeError(w, 403, "invalid_origin", "请求来源不受信任")
			return
		}
		if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			writeError(w, 403, "cross_site_request", "不允许跨站请求")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && r.Header.Get("X-ProjNote-Request") != "1" {
			writeError(w, 403, "request_header_required", "缺少本地请求标识")
			return
		}
		if r.Method == "DELETE" {
			writeError(w, 405, "deletion_unavailable", "首版尚未提供回收站，暂不开放删除")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
			s.api(w, r)
			return
		}
		s.static(w, r)
	})
}
func jsonResult(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
}
func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message}})
}
func handleErr(w http.ResponseWriter, err error) {
	var ae *store.AppError
	if errors.As(err, &ae) {
		writeError(w, ae.Status, ae.Code, ae.Message)
		return
	}
	slog.Error("operation failed", "error", err)
	writeError(w, 500, "internal_error", "操作失败，请查看运行日志")
}
func body(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if media != "application/json" {
		writeError(w, 415, "unsupported_content_type", "请使用 JSON 请求")
		return nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	dec := json.NewDecoder(r.Body)
	var input map[string]any
	if err := dec.Decode(&input); err != nil || input == nil {
		writeError(w, 400, "invalid_json", "请求内容不是有效 JSON")
		return nil, false
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		writeError(w, 400, "invalid_json", "只允许一个 JSON 对象")
		return nil, false
	}
	return input, true
}
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	p := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/"), "/"), "/")
	kind := p[0]
	switch kind {
	case "health":
		if r.Method != "GET" {
			break
		}
		jsonResult(w, 200, map[string]any{"version": s.Version, "status": "ok", "instance": s.RuntimeToken})
		return
	case "today":
		if r.Method != "GET" {
			break
		}
		result, err := s.Store.Today()
		if err != nil {
			handleErr(w, err)
		} else {
			jsonResult(w, 200, result)
		}
		return
	case "search":
		if r.Method != "GET" {
			break
		}
		if len([]rune(r.URL.Query().Get("q"))) > 200 {
			writeError(w, 400, "query_too_long", "搜索词不能超过 200 字")
			return
		}
		items, err := s.Store.Search(r.URL.Query().Get("q"))
		if err != nil {
			handleErr(w, err)
		} else {
			start, end := pageBounds(r, len(items), 30)
			jsonResult(w, 200, map[string]any{"items": items[start:end], "total": len(items), "capped": len(items) == 500})
		}
		return
	case "settings":
		if r.Method == "PUT" {
			input, ok := body(w, r)
			if !ok {
				return
			}
			if err := s.Store.SetSettings(input); err != nil {
				handleErr(w, err)
				return
			}
		}
		if r.Method == "GET" || r.Method == "PUT" {
			result, err := s.Store.Settings()
			if err != nil {
				handleErr(w, err)
			} else {
				result["data_dir"] = s.DataDir
				result["version"] = s.Version
				jsonResult(w, 200, result)
			}
			return
		}
	case "demo":
		if r.Method != "POST" {
			break
		}
		if err := s.Store.Demo(); err != nil {
			handleErr(w, err)
		} else {
			jsonResult(w, 201, map[string]any{"message": "示例数据已创建"})
		}
		return
	case "audit":
		if len(p) == 2 && p[1] == "verify" && r.Method == "GET" {
			result, err := s.Store.VerifyAudit()
			if err != nil {
				handleErr(w, err)
			} else {
				jsonResult(w, 200, result)
			}
			return
		}
	case "backups":
		s.backups(w, r, p)
		return
	case "attachments":
		s.attachments(w, r, p)
		return
	case "system":
		if len(p) == 2 && p[1] == "shutdown" && r.Method == "POST" {
			jsonResult(w, 200, map[string]any{"message": "服务即将退出"})
			if s.Shutdown != nil {
				go func() { time.Sleep(150 * time.Millisecond); s.Shutdown() }()
			}
			return
		}
	case "projects", "tasks", "records", "issues", "worklogs":
		if kind == "worklogs" && len(p) == 2 && p[1] == "generate" && r.Method == "POST" {
			input, ok := body(w, r)
			if !ok {
				return
			}
			result, err := s.Store.GenerateLog(input)
			if err != nil {
				handleErr(w, err)
			} else {
				jsonResult(w, 200, result)
			}
			return
		}
		if len(p) == 1 {
			switch r.Method {
			case "GET":
				filters := map[string]string{}
				for k, vs := range r.URL.Query() {
					filters[k] = vs[0]
				}
				items, total, err := s.Store.List(kind, filters)
				if err != nil {
					handleErr(w, err)
				} else {
					jsonResult(w, 200, map[string]any{"items": items, "total": total})
				}
				return
			case "POST":
				input, ok := body(w, r)
				if !ok {
					return
				}
				if kind == "records" && !s.checkAttachments(w, input) {
					return
				}
				result, err := s.Store.Create(kind, input)
				if err != nil {
					handleErr(w, err)
				} else {
					jsonResult(w, 201, result)
				}
				return
			}
		}
		if len(p) == 2 || (kind == "issues" && len(p) == 3 && p[2] == "transition" && r.Method == "POST") {
			switch r.Method {
			case "GET":
				result, err := s.Store.Get(kind, p[1])
				if err != nil {
					handleErr(w, err)
				} else {
					jsonResult(w, 200, result)
				}
				return
			case "PATCH", "POST":
				input, ok := body(w, r)
				if !ok {
					return
				}
				if kind == "records" && !s.checkAttachments(w, input) {
					return
				}
				result, err := s.Store.Update(kind, p[1], input)
				if err != nil {
					handleErr(w, err)
				} else {
					jsonResult(w, 200, result)
				}
				return
			case "DELETE":
				version, _ := strconv.Atoi(r.URL.Query().Get("version"))
				if err := s.Store.Delete(kind, p[1], version); err != nil {
					handleErr(w, err)
				} else {
					jsonResult(w, 200, map[string]any{"deleted": true})
				}
				return
			}
		}
	}
	writeError(w, 404, "not_found", "接口不存在或请求方法不正确")
}
func (s *Server) checkAttachments(w http.ResponseWriter, input map[string]any) bool {
	value, exists := input["attachments"]
	if !exists {
		return true
	}
	items, ok := value.([]any)
	if !ok || len(items) > 9 {
		writeError(w, 400, "invalid_attachments", "单条记录最多 9 张图片")
		return false
	}
	normalized := []any{}
	for _, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			writeError(w, 400, "invalid_attachment", "附件无效")
			return false
		}
		id, _ := m["id"].(string)
		a, err := (files.Storage{Root: s.DataDir}).Lookup(id)
		if err != nil {
			writeError(w, 400, "invalid_attachment", "附件不存在，请重新上传")
			return false
		}
		normalized = append(normalized, a)
	}
	input["attachments"] = normalized
	return true
}
func (s *Server) attachments(w http.ResponseWriter, r *http.Request, p []string) {
	storage := files.Storage{Root: s.DataDir}
	if len(p) == 1 && r.Method == "POST" {
		r.Body = http.MaxBytesReader(w, r.Body, files.MaxSize+(1<<20))
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			writeError(w, 413, "upload_too_large", "图片不能超过 20 MB")
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			writeError(w, 400, "file_required", "请选择一张图片")
			return
		}
		defer f.Close()
		s.Store.Mu.Lock()
		a, err := storage.Save(h.Filename, f)
		s.Store.Mu.Unlock()
		if err != nil {
			writeError(w, 400, "invalid_image", err.Error())
		} else {
			jsonResult(w, 201, a)
		}
		return
	}
	if len(p) == 2 && (r.Method == "GET" || r.Method == "HEAD") {
		a, err := storage.Lookup(p[1])
		if err != nil {
			writeError(w, 404, "not_found", "图片不存在")
			return
		}
		thumb := r.URL.Query().Get("thumb") == "1"
		local, err := storage.Path(p[1], thumb)
		if err != nil {
			writeError(w, 404, "not_found", "图片不存在")
			return
		}
		if thumb {
			w.Header().Set("Content-Type", "image/png")
		} else {
			w.Header().Set("Content-Type", a.MIME)
		}
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": a.Filename}))
		http.ServeFile(w, r, local)
		return
	}
	writeError(w, 404, "not_found", "附件接口不存在")
}
func (s *Server) backups(w http.ResponseWriter, r *http.Request, p []string) {
	if len(p) == 1 && r.Method == "GET" {
		items, err := backup.List(s.DataDir)
		if err != nil {
			handleErr(w, err)
		} else {
			start, end := pageBounds(r, len(items), 20)
			jsonResult(w, 200, map[string]any{"items": items[start:end], "total": len(items)})
		}
		return
	}
	if len(p) == 1 && r.Method == "POST" {
		s.Store.Mu.Lock()
		info, err := backup.Create(s.DataDir, s.Store.DB)
		s.Store.Mu.Unlock()
		if err != nil {
			handleErr(w, err)
		} else {
			if e := s.Store.RecordEvent("backup", "system", info.Name, map[string]any{"size": info.Size, "automatic": false}); e != nil {
				handleErr(w, e)
				return
			}
			jsonResult(w, 201, info)
		}
		return
	}
	if len(p) == 3 && p[2] == "download" && r.Method == "GET" && backup.SafeName(p[1]) {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": p[1]}))
		w.Header().Set("Content-Type", "application/zip")
		http.ServeFile(w, r, s.DataDir+"/backups/"+p[1])
		return
	}
	writeError(w, 404, "not_found", "备份接口不存在")
}
func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		writeError(w, 405, "method_not_allowed", "此路径只允许读取")
		return
	}
	assets, err := fs.Sub(webui.Assets, "dist")
	if err != nil {
		writeError(w, 500, "missing_assets", "前端资源不可用")
		return
	}
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "." || name == "" {
		name = "index.html"
	}
	b, err := fs.ReadFile(assets, name)
	if err != nil {
		if strings.HasPrefix(name, "assets/") || strings.HasPrefix(name, "api") || path.Ext(name) != "" {
			http.NotFound(w, r)
			return
		}
		name = "index.html"
		b, err = fs.ReadFile(assets, name)
	}
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ct := mime.TypeByExtension(path.Ext(name))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	if name == "index.html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public,max-age=31536000,immutable")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	if r.Method == "GET" {
		_, _ = w.Write(b)
	}
}

func URL(host string) string { return (&url.URL{Scheme: "http", Host: host}).String() }

func pageBounds(r *http.Request, total, defaultLimit int) (int, int) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 {
		limit = defaultLimit
	}
	if limit > 500 {
		limit = 500
	}
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return offset, end
}
