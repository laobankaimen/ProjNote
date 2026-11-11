package server

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAutoStartOwnerValidationAndFailures(t *testing.T) {
	s := testServer(t)
	path := "/api/system/autostart"
	got := exportJSON(t, s, "GET", path, nil, 200)
	if got["supported"] != false || got["enabled"] != false {
		t.Fatal(got)
	}
	exportJSON(t, s, "PUT", path, map[string]any{"enabled": true}, 501)
	enabled, changes := false, 0
	s.AutoStartStatus = func() (map[string]any, error) {
		return map[string]any{"supported": true, "enabled": enabled, "matches_current": enabled, "needs_repair": false}, nil
	}
	s.SetAutoStart = func(value bool) (map[string]any, error) {
		enabled = value
		changes++
		return s.AutoStartStatus()
	}
	for _, input := range []map[string]any{{}, {"enabled": "1"}, {"enabled": nil}, {"enabled": true, "executable": "/tmp/arbitrary"}, {"enabled": true, "data_dir": "/tmp/other"}} {
		exportJSON(t, s, "PUT", path, input, 400)
	}
	if changes != 0 {
		t.Fatal("invalid requests changed registration")
	}
	for _, value := range []bool{true, false} {
		got = exportJSON(t, s, "PUT", path, map[string]any{"enabled": value}, 200)
		if got["enabled"] != value {
			t.Fatal(got)
		}
	}
	if changes != 2 {
		t.Fatal(changes)
	}
	s.SetAutoStart = func(bool) (map[string]any, error) { return nil, errors.New("配置不可写") }
	exportJSON(t, s, "PUT", path, map[string]any{"enabled": true}, 503)
	s.AutoStartStatus = func() (map[string]any, error) { return nil, errors.New("读取失败") }
	exportJSON(t, s, "GET", path, nil, 503)
	exportJSON(t, s, "POST", path, map[string]any{"enabled": true}, 405)
}

func TestAutoStartRejectsForeignOriginAndLAN(t *testing.T) {
	s := testServer(t)
	called := false
	s.SetAutoStart = func(bool) (map[string]any, error) { called = true; return map[string]any{}, nil }
	for _, origin := range []string{"https://foreign.example", "http://localhost:18600"} {
		r := httptest.NewRequest("PUT", "http://"+s.Host+"/api/system/autostart", strings.NewReader(`{"enabled":true}`))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-ProjNote-Request", "1")
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("PUT", "https://127.0.0.1:18680/api/system/autostart", strings.NewReader(`{"enabled":true}`))
	r.TLS = &tls.ConnectionState{}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-ProjNote-Request", "1")
	r.Header.Set("Origin", "https://127.0.0.1:18680")
	w := httptest.NewRecorder()
	s.LANHandler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || called {
		t.Fatal(w.Code, called, w.Body.String())
	}
}
