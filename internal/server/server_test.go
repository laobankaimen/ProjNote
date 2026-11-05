package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"projnote/internal/store"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Server{Store: st, DataDir: root, Host: "127.0.0.1:18600", Version: "test"}
}
func call(s *Server, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:18600"+path, strings.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func TestSameOriginProtection(t *testing.T) {
	s := testServer(t)
	for _, test := range []struct {
		name, method, path, body string
		headers                  map[string]string
		host                     string
		want                     int
	}{
		{name: "foreign origin", method: "GET", path: "/api/projects", headers: map[string]string{"Origin": "https://attacker.invalid"}, want: 403},
		{name: "cross site", method: "GET", path: "/api/projects", headers: map[string]string{"Sec-Fetch-Site": "cross-site"}, want: 403},
		{name: "missing header", method: "POST", path: "/api/projects", body: `{"name":"bad"}`, headers: map[string]string{"Content-Type": "application/json"}, want: 403},
		{name: "wrong content type", method: "POST", path: "/api/projects", body: `{"name":"bad"}`, headers: map[string]string{"X-ProjNote-Request": "1", "Content-Type": "text/plain"}, want: 415},
		{name: "valid create", method: "POST", path: "/api/projects", body: `{"name":"真实项目"}`, headers: map[string]string{"X-ProjNote-Request": "1", "Content-Type": "application/json"}, want: 201},
	} {
		t.Run(test.name, func(t *testing.T) {
			w := call(s, test.method, test.path, test.body, test.headers)
			if w.Code != test.want {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
	r := httptest.NewRequest("GET", "http://rebinding.invalid:18600/api/projects", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("accepted foreign Host: %d", w.Code)
	}
}
func TestImageUploadOriginalAndValidation(t *testing.T) {
	s := testServer(t)
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 10, 10))); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", "../现场.png")
	_, _ = part.Write(img.Bytes())
	_ = mw.Close()
	r := httptest.NewRequest("POST", "http://127.0.0.1:18600/api/attachments", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("X-ProjNote-Request", "1")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("upload: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		Data struct {
			ID  string `json:"id"`
			URL string `json:"url"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	got := call(s, "GET", result.Data.URL, "", nil)
	if got.Code != 200 || !bytes.Equal(got.Body.Bytes(), img.Bytes()) {
		t.Fatal("original image changed")
	}
	bad := call(s, "POST", "/api/records", `{"content":"bad","attachments":[{"id":"../../escape"}]}`, map[string]string{"Content-Type": "application/json", "X-ProjNote-Request": "1"})
	if bad.Code != 400 {
		t.Fatalf("accepted invalid attachment: %d %s", bad.Code, bad.Body.String())
	}
	if got := call(s, "DELETE", "/api/records/anything", "", map[string]string{"X-ProjNote-Request": "1"}); got.Code != 405 {
		t.Fatal("unsafe delete exposed")
	}
}
func TestMalformedJSONAndMissingStaticDoNotFallback(t *testing.T) {
	s := testServer(t)
	headers := map[string]string{"Content-Type": "application/json", "X-ProjNote-Request": "1"}
	for _, body := range []string{`null`, `{} {}`, `{"name":`} {
		if w := call(s, "POST", "/api/projects", body, headers); w.Code != 400 {
			t.Fatalf("accepted malformed body %q", body)
		}
	}
	for _, path := range []string{"/api/missing", "/assets/missing.js", "/files/../../etc/passwd"} {
		w := call(s, "GET", path, "", nil)
		b, _ := io.ReadAll(w.Result().Body)
		if w.Code != 404 {
			t.Fatalf("missing route returned %d: %s", w.Code, b)
		}
	}
}
