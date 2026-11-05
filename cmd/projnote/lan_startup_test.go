package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLANStartupDefaultExplicitAndSavedPreferences(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		enabled                bool
		port                   int
		explicit, explicitPort bool
		settings               map[string]any
		want                   lanStartupOptions
	}{
		{"fresh stays loopback only", false, 18680, false, false, nil, lanStartupOptions{false, 18680, false}},
		{"saved enabled", false, 18680, false, false, map[string]any{"lan_enabled": "1", "lan_port": "19380"}, lanStartupOptions{true, 19380, false}},
		{"explicit first enable", true, 18680, true, false, nil, lanStartupOptions{true, 18680, true}},
		{"explicit new port", true, 19381, true, true, nil, lanStartupOptions{true, 19381, true}},
		{"port alone does not enable", false, 19381, false, true, nil, lanStartupOptions{false, 19381, true}},
		{"explicit disabled overrides saved", false, 18680, true, false, map[string]any{"lan_enabled": "1", "lan_port": "19380"}, lanStartupOptions{false, 19380, true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := selectLANStartup(tc.enabled, tc.port, tc.explicit, tc.explicitPort, tc.settings); got != tc.want {
				t.Fatal(got, tc.want)
			}
		})
	}
}
func TestExistingLANControlStaysOnVerifiedLoopbackOwnerSurface(t *testing.T) {
	var received map[string]any
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/settings" {
			_, _ = w.Write([]byte(`{"data":{"lan_port":"19380"}}`))
			return
		}
		if r.Method != "POST" || r.URL.Path != "/api/lan/control" || r.Header.Get("X-ProjNote-Request") != "1" || r.Header.Get("Content-Type") != "application/json" {
			t.Error("unsafe control request", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"data":{"running":true,"addresses":["https://192.168.1.2:19380"]}}`))
	}))
	defer service.Close()
	if existingLANPort(service.URL) != 19380 {
		t.Fatal("second launch ignored the saved LAN port")
	}
	if err := requestExistingLAN(service.URL, true, 19380); err != nil {
		t.Fatal(err)
	}
	if received["enabled"] != true || received["port"] != float64(19380) {
		t.Fatal(received)
	}
	for _, url := range []string{"https://127.0.0.1:18600", "http://localhost:18600", "http://192.168.1.2:18600", service.URL + "/other", service.URL + "?q=a", service.URL + "#fragment"} {
		if requestExistingLAN(url, true, 18680) == nil {
			t.Fatal("unverified owner address accepted", url)
		}
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, service.URL, 302) }))
	defer redirect.Close()
	if requestExistingLAN(redirect.URL, true, 19380) == nil {
		t.Fatal("control request followed a redirect")
	}
}
