package files

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestOnlyRasterImagesAndCanonicalURLs(t *testing.T) {
	root := t.TempDir()
	storage := Storage{Root: root}
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 10, 10))); err != nil {
		t.Fatal(err)
	}
	a, err := storage.Save("../图.png", bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, "attachments", a.ID, "original"))
	if err != nil || !bytes.Equal(b, buf.Bytes()) {
		t.Fatal("original bytes changed")
	}
	a.URL = "https://untrusted.invalid/image"
	a.ThumbURL = "javascript:alert(1)"
	meta, _ := json.Marshal(a)
	file := filepath.Join(root, "attachments", a.ID, "meta.json")
	if err = os.WriteFile(file, meta, 0600); err != nil {
		t.Fatal(err)
	}
	canonical, err := storage.Lookup(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if canonical.URL != "/api/attachments/"+a.ID || canonical.ThumbURL != canonical.URL+"?thumb=1" {
		t.Fatal("attachment URLs were trusted")
	}
	a.MIME = "text/html"
	meta, _ = json.Marshal(a)
	if err = os.WriteFile(file, meta, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = storage.Lookup(a.ID); err == nil {
		t.Fatal("active content MIME accepted")
	}
	for _, body := range []string{"<svg xmlns='http://www.w3.org/2000/svg'></svg>", "<html><script>alert(1)</script></html>"} {
		if _, err = storage.Save("image.png", bytes.NewBufferString(body)); err == nil {
			t.Fatal("non-raster upload accepted")
		}
	}
}
