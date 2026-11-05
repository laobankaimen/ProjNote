// Package files keeps original images immutable and addresses files by opaque IDs.
package files

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const MaxSize = 20 << 20

var validID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type Attachment struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	URL      string `json:"url"`
	ThumbURL string `json:"thumb_url"`
	MIME     string `json:"mime"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
}
type Storage struct{ Root string }

func (s Storage) Save(name string, r io.Reader) (Attachment, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxSize+1))
	if err != nil {
		return Attachment{}, err
	}
	if len(b) > MaxSize {
		return Attachment{}, fmt.Errorf("图片不能超过 20 MB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return Attachment{}, fmt.Errorf("首版支持 PNG、JPEG、GIF 图片")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 {
		return Attachment{}, fmt.Errorf("图片像素不能超过 4000 万")
	}
	mime := map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif"}[format]
	if mime == "" {
		return Attachment{}, fmt.Errorf("不支持的图片格式")
	}
	idb := make([]byte, 16)
	if _, err = rand.Read(idb); err != nil {
		return Attachment{}, err
	}
	id := hex.EncodeToString(idb)
	h := sha256.Sum256(b)
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if len(name) > 240 {
		name = "图片." + format
	}
	a := Attachment{ID: id, Filename: name, URL: "/api/attachments/" + id, ThumbURL: "/api/attachments/" + id + "?thumb=1", MIME: mime, Size: int64(len(b)), SHA256: hex.EncodeToString(h[:])}
	root := filepath.Join(s.Root, "attachments")
	if err = os.MkdirAll(root, 0700); err != nil {
		return a, err
	}
	temp, err := os.MkdirTemp(root, ".upload-")
	if err != nil {
		return a, err
	}
	defer os.RemoveAll(temp)
	if err = writeSync(filepath.Join(temp, "original"), b); err != nil {
		return a, err
	}
	img, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return a, err
	}
	w, hgt := cfg.Width, cfg.Height
	if w > 512 || hgt > 512 {
		if w >= hgt {
			hgt = hgt * 512 / w
			w = 512
		} else {
			w = w * 512 / hgt
			hgt = 512
		}
	}
	if w < 1 {
		w = 1
	}
	if hgt < 1 {
		hgt = 1
	}
	thumb := image.NewRGBA(image.Rect(0, 0, w, hgt))
	bounds := img.Bounds()
	for y := 0; y < hgt; y++ {
		for x := 0; x < w; x++ {
			thumb.Set(x, y, img.At(bounds.Min.X+x*cfg.Width/w, bounds.Min.Y+y*cfg.Height/hgt))
		}
	}
	var buf bytes.Buffer
	if err = png.Encode(&buf, thumb); err != nil {
		return a, err
	}
	if err = writeSync(filepath.Join(temp, "thumb.png"), buf.Bytes()); err != nil {
		return a, err
	}
	meta, err := json.Marshal(a)
	if err != nil {
		return a, err
	}
	if err = writeSync(filepath.Join(temp, "meta.json"), meta); err != nil {
		return a, err
	}
	if err = os.Rename(temp, filepath.Join(root, id)); err != nil {
		return a, err
	}
	return a, nil
}
func (s Storage) Lookup(id string) (Attachment, error) {
	var a Attachment
	if !validID.MatchString(id) {
		return a, os.ErrNotExist
	}
	b, err := os.ReadFile(filepath.Join(s.Root, "attachments", id, "meta.json"))
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(b, &a)
	if err != nil {
		return a, err
	}
	if a.ID != id {
		return a, os.ErrNotExist
	}
	if a.MIME != "image/png" && a.MIME != "image/jpeg" && a.MIME != "image/gif" {
		return a, fmt.Errorf("附件类型无效")
	}
	a.URL = "/api/attachments/" + id
	a.ThumbURL = a.URL + "?thumb=1"
	a.Filename = filepath.Base(strings.ReplaceAll(a.Filename, "\\", "/"))
	return a, nil
}
func (s Storage) Path(id string, thumb bool) (string, error) {
	if _, err := s.Lookup(id); err != nil {
		return "", err
	}
	name := "original"
	if thumb {
		name = "thumb.png"
	}
	return filepath.Join(s.Root, "attachments", id, name), nil
}
func writeSync(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if err == nil {
		err = f.Sync()
	}
	cerr := f.Close()
	if err == nil {
		err = cerr
	}
	return err
}

// CheckReferences validates every image referenced by a record before a restore.
func CheckReferences(root string, db *sql.DB) error {
	rows, err := db.Query("SELECT attachments FROM records")
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for rows.Next() {
		var raw string
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return err
		}
		var attachments []Attachment
		if err = json.Unmarshal([]byte(raw), &attachments); err != nil {
			rows.Close()
			return fmt.Errorf("记录附件信息损坏: %w", err)
		}
		for _, a := range attachments {
			if !validID.MatchString(a.ID) || a.URL != "/api/attachments/"+a.ID || a.ThumbURL != "/api/attachments/"+a.ID+"?thumb=1" {
				rows.Close()
				return fmt.Errorf("记录附件路径无效")
			}
			ids[a.ID] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	storage := Storage{Root: root}
	for id := range ids {
		a, err := storage.Lookup(id)
		if err != nil {
			return fmt.Errorf("附件 %s 缺失或无效: %w", id, err)
		}
		p, err := storage.Path(id, false)
		if err != nil {
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		cfg, format, decodeErr := image.DecodeConfig(f)
		if decodeErr != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 40_000_000 || map[string]string{"png": "image/png", "jpeg": "image/jpeg", "gif": "image/gif"}[format] != a.MIME {
			f.Close()
			return fmt.Errorf("附件 %s 图片格式无效", id)
		}
		if _, err = f.Seek(0, 0); err != nil {
			f.Close()
			return err
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		if n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
			return fmt.Errorf("附件 %s 原图校验失败", id)
		}
	}
	return nil
}
