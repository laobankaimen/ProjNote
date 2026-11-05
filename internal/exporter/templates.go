package exporter

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

//go:embed assets/builtin-journal.docx
var journal []byte

//go:embed assets/builtin-issue.docx
var issue []byte

type Template struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Format      string   `json:"format"`
	Description string   `json:"description"`
	SHA256      string   `json:"sha256"`
	Fields      []string `json:"fields"`
}

func Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

var safeID = regexp.MustCompile(`^[a-f0-9]{32}$`)

func LoadTemplate(root, id string) (Template, []byte, error) {
	var meta Template
	var data []byte
	switch id {
	case "builtin-journal":
		data = journal
		meta = Template{ID: id, Name: "日志 Word 模板", Format: "docx", Description: "个人日报/周报/月报、项目日报与会议纪要"}
	case "builtin-issue":
		data = issue
		meta = Template{ID: id, Name: "问题单 Word 模板", Format: "docx", Description: "已保存问题详情与闭环字段"}
	default:
		if !safeID.MatchString(id) {
			return meta, nil, errors.New("模板ID无效")
		}
		path := filepath.Join(root, "templates", "docx", id+".docx")
		info, e := os.Lstat(path)
		if e != nil || !info.Mode().IsRegular() {
			return meta, nil, errors.New("模板不存在或不是普通文件")
		}
		b, e := os.ReadFile(filepath.Join(root, "templates", "docx", id+".json"))
		if e != nil || json.Unmarshal(b, &meta) != nil {
			return meta, nil, errors.New("模板元信息损坏")
		}
		data, e = os.ReadFile(path)
		if e != nil {
			return meta, nil, e
		}
		if Digest(data) != meta.SHA256 {
			return meta, nil, errors.New("模板文件哈希不一致")
		}
	}
	fields, e := Validate(data)
	if e != nil {
		return meta, nil, e
	}
	meta.SHA256 = Digest(data)
	meta.Fields = fields
	return meta, data, nil
}
func ListTemplates(root string) ([]Template, error) {
	out := []Template{}
	for _, id := range []string{"builtin-journal", "builtin-issue"} {
		meta, _, e := LoadTemplate(root, id)
		if e != nil {
			return nil, e
		}
		out = append(out, meta)
	}
	entries, e := os.ReadDir(filepath.Join(root, "templates", "docx"))
	if e != nil && !os.IsNotExist(e) {
		return nil, e
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".docx" {
			continue
		}
		id := entry.Name()[:len(entry.Name())-5]
		meta, _, e := LoadTemplate(root, id)
		if e != nil {
			return nil, e
		}
		out = append(out, meta)
	}
	return out, nil
}
func SaveTemplate(root, id, name string, data []byte) (Template, error) {
	fields, e := Validate(data)
	if e != nil {
		return Template{}, e
	}
	if !safeID.MatchString(id) {
		return Template{}, errors.New("模板ID无效")
	}
	dir := filepath.Join(root, "templates", "docx")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return Template{}, e
	}
	meta := Template{ID: id, Name: name, Format: "docx", Description: "自定义 Word 模板", SHA256: Digest(data), Fields: fields}
	raw, _ := json.Marshal(meta)
	p := filepath.Join(dir, id+".docx")
	if e = writeExclusive(p, data); e != nil {
		return meta, e
	}
	if e = writeExclusive(filepath.Join(dir, id+".json"), raw); e != nil {
		_ = os.Remove(p)
		return meta, e
	}
	return meta, nil
}

func writeExclusive(path string, data []byte) error {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	ok := false
	defer func() {
		_ = f.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, e = f.Write(data); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	ok = true
	return nil
}
