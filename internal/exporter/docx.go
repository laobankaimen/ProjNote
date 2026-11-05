// Package exporter renders a deliberately small, validated DOCX template subset.
package exporter

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
)

const MaxTemplate = 8 << 20
const maxExpanded = 32 << 20

var tokenRE = regexp.MustCompile(`\{\{([^{}]+)\}\}`)
var known = map[string]bool{"title": true, "content": true, "project_name": true, "date": true, "person_name": true, "operator": true, "source_version": true, "source_kind": true, "meeting_date": true, "location": true, "attendees": true, "acceptance_criteria": true, "acceptance_note": true, "change_note": true, "revision": true, "code": true, "status": true, "owner": true, "description": true, "root_cause": true, "solution": true, "verify_result": true, "label": true, "value": true, "#rows": true, "/rows": true}

type node struct {
	Name     string
	Attrs    []xml.Attr
	Text     string
	Children []*node
}

func parse(data []byte) (*node, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	root := &node{}
	stack := []*node{root}
	for {
		t, e := d.RawToken()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, e
		}
		switch v := t.(type) {
		case xml.StartElement:
			seenAttrs := map[string]bool{}
			for _, a := range v.Attr {
				k := qname(a.Name)
				if seenAttrs[k] {
					return nil, errors.New("重复的XML属性")
				}
				seenAttrs[k] = true
			}
			n := &node{Name: qname(v.Name), Attrs: append([]xml.Attr(nil), v.Attr...)}
			parent := stack[len(stack)-1]
			parent.Children = append(parent.Children, n)
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) < 2 || stack[len(stack)-1].Name != qname(v.Name) {
				return nil, errors.New("不匹配的XML结束标签")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 1 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, &node{Text: string(v)})
			}
		case xml.Directive:
			return nil, errors.New("不支持XML指令/DTD")
		}
	}
	elements := 0
	for _, c := range root.Children {
		if c.Name != "" {
			elements++
		}
	}
	if len(stack) != 1 || elements != 1 {
		return nil, errors.New("文档XML为空或不完整")
	}
	return root, nil
}
func qname(n xml.Name) string {
	if n.Space != "" {
		return n.Space + ":" + n.Local
	}
	return n.Local
}
func local(n string) string {
	if i := strings.IndexByte(n, ':'); i >= 0 {
		return n[i+1:]
	}
	return n
}
func escape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
func (n *node) write(b *strings.Builder) {
	if n.Name == "" {
		b.WriteString(escape(n.Text))
		for _, c := range n.Children {
			c.write(b)
		}
		return
	}
	b.WriteByte('<')
	b.WriteString(n.Name)
	for _, a := range n.Attrs {
		b.WriteByte(' ')
		b.WriteString(qname(a.Name))
		b.WriteString(`="`)
		b.WriteString(escape(a.Value))
		b.WriteByte('"')
	}
	b.WriteByte('>')
	for _, c := range n.Children {
		c.write(b)
	}
	b.WriteString("</" + n.Name + ">")
}
func (n *node) clone() *node {
	c := &node{Name: n.Name, Text: n.Text, Attrs: append([]xml.Attr(nil), n.Attrs...)}
	for _, child := range n.Children {
		c.Children = append(c.Children, child.clone())
	}
	return c
}
func textOf(n *node) string {
	var b strings.Builder
	var walk func(*node)
	walk = func(x *node) {
		if local(x.Name) == "t" {
			for _, c := range x.Children {
				b.WriteString(c.Text)
			}
			return
		}
		for _, c := range x.Children {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}
func replaceParagraph(p *node, values map[string]string) error {
	var texts []*node
	var walk func(*node)
	walk = func(n *node) {
		if local(n.Name) == "t" {
			texts = append(texts, n)
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(p)
	if len(texts) == 0 {
		return nil
	}
	parts := make([]string, len(texts))
	full := ""
	for i, n := range texts {
		for _, c := range n.Children {
			parts[i] += c.Text
		}
		full += parts[i]
	}
	matches := tokenRE.FindAllStringSubmatchIndex(full, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		key := strings.TrimSpace(full[m[2]:m[3]])
		value, ok := values[key]
		if !ok {
			return fmt.Errorf("模板占位符尚未支持或位置无效：%s", key)
		}
		start, end := m[0], m[1]
		a, b, offset := 0, 0, 0
		for j, part := range parts {
			if start >= offset && start < offset+len(part) {
				a = j
				break
			}
			offset += len(part)
		}
		startOffset := start - offset
		endOffset := 0
		cursor := 0
		for j, part := range parts {
			if end > cursor && end <= cursor+len(part) {
				b = j
				endOffset = end - cursor
				break
			}
			cursor += len(part)
		}
		if a == b {
			parts[a] = parts[a][:startOffset] + value + parts[a][endOffset:]
		} else {
			parts[a] = parts[a][:startOffset] + value
			for j := a + 1; j < b; j++ {
				parts[j] = ""
			}
			parts[b] = parts[b][endOffset:]
		}
	}
	for i, n := range texts {
		setPreserveSpace(n)
		n.Children = []*node{{Text: strings.ReplaceAll(parts[i], "\r\n", "\n")}}
	}
	expandLineBreaks(p)
	return nil
}
func setPreserveSpace(n *node) {
	attrs := n.Attrs[:0]
	for _, a := range n.Attrs {
		if a.Name.Space == "xml" && a.Name.Local == "space" {
			continue
		}
		attrs = append(attrs, a)
	}
	n.Attrs = append(attrs, xml.Attr{Name: xml.Name{Space: "xml", Local: "space"}, Value: "preserve"})
}
func expandLineBreaks(n *node) {
	children := []*node{}
	for _, c := range n.Children {
		if local(n.Name) == "r" && local(c.Name) == "t" && strings.Contains(textOf(c), "\n") {
			for i, line := range strings.Split(textOf(c), "\n") {
				if i > 0 {
					children = append(children, wordNode("br"))
				}
				t := c.clone()
				t.Children = []*node{{Text: line}}
				children = append(children, t)
			}
		} else {
			expandLineBreaks(c)
			children = append(children, c)
		}
	}
	n.Children = children
}
func wordNode(name string, children ...*node) *node {
	return &node{Name: "w:" + name, Children: children}
}
func contentParagraphs(original *node, content string) []*node {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	out := []*node{}
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		p := wordNode("p")
		style := "Normal"
		switch {
		case strings.HasPrefix(line, "### "):
			style = "Heading2"
			line = strings.TrimPrefix(line, "### ")
		case strings.HasPrefix(line, "## "):
			style = "Heading1"
			line = strings.TrimPrefix(line, "## ")
		case strings.HasPrefix(line, "# "):
			style = "Heading1"
			line = strings.TrimPrefix(line, "# ")
		}
		if style == "Normal" {
			for _, c := range original.Children {
				if local(c.Name) == "pPr" {
					p.Children = append(p.Children, c.clone())
				}
			}
		} else {
			p.Children = append(p.Children, wordNode("pPr", &node{Name: "w:pStyle", Attrs: []xml.Attr{{Name: xml.Name{Space: "w", Local: "val"}, Value: style}}}))
		}
		p.Children = append(p.Children, wordNode("r", &node{Name: "w:t", Attrs: []xml.Attr{{Name: xml.Name{Space: "xml", Local: "space"}, Value: "preserve"}}, Children: []*node{{Text: line}}}))
		out = append(out, p)
	}
	if len(out) == 0 {
		out = append(out, wordNode("p"))
	}
	return out
}
func renderTree(root *node, values map[string]string, rows []map[string]string) error {
	var visit func(*node, map[string]string) error
	visit = func(n *node, v map[string]string) error {
		children := []*node{}
		for _, c := range n.Children {
			if local(c.Name) == "tr" && strings.Contains(textOf(c), "{{#rows}}") {
				txt := textOf(c)
				if strings.Count(txt, "{{#rows}}") != 1 || strings.Count(txt, "{{/rows}}") != 1 {
					return errors.New("表格循环须在同一行中成对写{{#rows}}与{{/rows}}")
				}
				for _, row := range rows {
					copy := c.clone()
					scoped := map[string]string{}
					for k, val := range v {
						scoped[k] = val
					}
					for k, val := range row {
						scoped[k] = val
					}
					scoped["#rows"] = ""
					scoped["/rows"] = ""
					if e := visit(copy, scoped); e != nil {
						return e
					}
					children = append(children, copy)
				}
				continue
			}
			if local(c.Name) == "p" && strings.TrimSpace(textOf(c)) == "{{content}}" {
				children = append(children, contentParagraphs(c, v["content"])...)
				continue
			}
			if e := visit(c, v); e != nil {
				return e
			}
			children = append(children, c)
		}
		n.Children = children
		if local(n.Name) == "p" {
			return replaceParagraph(n, v)
		}
		return nil
	}
	return visit(root, values)
}
func readPackage(data []byte) (map[string][]byte, error) {
	if len(data) > MaxTemplate {
		return nil, errors.New("Word模板超过8MB")
	}
	z, e := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if e != nil {
		return nil, errors.New("请上传有效的未加密.docx文件")
	}
	if len(z.File) > 1024 {
		return nil, errors.New("模板文件条目过多")
	}
	parts := map[string][]byte{}
	var size uint64
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !f.Mode().IsRegular() {
			return nil, errors.New("模板包含非普通文件")
		}
		name := f.Name
		if name != path.Clean(name) || strings.Contains(name, "\\") || strings.HasPrefix(name, "/") || strings.HasPrefix(name, "../") || strings.Contains(name, ":") {
			return nil, errors.New("模板包含不安全路径")
		}
		if _, ok := parts[name]; ok {
			return nil, errors.New("模板包含重复文件")
		}
		if f.UncompressedSize64 > maxExpanded || size > maxExpanded-f.UncompressedSize64 {
			return nil, errors.New("模板解压容量超过32MB")
		}
		size += f.UncompressedSize64
		r, e := f.Open()
		if e != nil {
			return nil, e
		}
		b, e := io.ReadAll(io.LimitReader(r, maxExpanded+1))
		_ = r.Close()
		if e != nil {
			return nil, e
		}
		if strings.Contains(strings.ToLower(name), "vbaproject") || strings.HasPrefix(name, "word/embeddings/") || strings.HasPrefix(name, "customXml/") {
			return nil, errors.New("模板包含未支持的宏、嵌入对象或自定义XML")
		}
		if strings.HasSuffix(name, ".rels") {
			root, e := parse(b)
			if e != nil {
				return nil, errors.New("模板关系XML损坏")
			}
			var check func(*node) error
			check = func(n *node) error {
				if local(n.Name) == "Relationship" {
					for _, a := range n.Attrs {
						if a.Name.Local == "TargetMode" && strings.EqualFold(strings.TrimSpace(a.Value), "External") {
							return errors.New("模板含外部关系，请先移除外链")
						}
					}
				}
				for _, c := range n.Children {
					if e := check(c); e != nil {
						return e
					}
				}
				return nil
			}
			if e = check(root); e != nil {
				return nil, e
			}
		}
		parts[name] = b
	}
	if parts["word/document.xml"] == nil || parts["[Content_Types].xml"] == nil {
		return nil, errors.New("模板缺少Word正文或内容类型")
	}
	return parts, nil
}
func Validate(data []byte) ([]string, error) {
	parts, e := readPackage(data)
	if e != nil {
		return nil, e
	}
	fields := map[string]bool{}
	count := 0
	for name, b := range parts {
		if !strings.HasPrefix(name, "word/") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		root, e := parse(b)
		if e != nil {
			return nil, fmt.Errorf("模板XML损坏：%s", name)
		}
		var walk func(*node, bool) error
		walk = func(n *node, inRow bool) error {
			tag := local(n.Name)
			if tag == "tr" {
				txt := textOf(n)
				starts := strings.Count(txt, "{{#rows}}")
				ends := strings.Count(txt, "{{/rows}}")
				if starts != ends || starts > 1 {
					return errors.New("表格循环须在同一行中成对写一次{{#rows}}与{{/rows}}")
				}
				inRow = starts == 1
			}
			switch tag {
			case "txbxContent", "altChunk", "object", "fldSimple", "instrText", "del", "ins", "sdt":
				return errors.New("模板含未支持的文本框、域、修订或内容控件，请先简化")
			}
			if tag == "p" {
				text := textOf(n)
				for _, m := range tokenRE.FindAllStringSubmatch(text, -1) {
					key := strings.TrimSpace(m[1])
					if !known[key] {
						return fmt.Errorf("未知模板占位符：%s", key)
					}
					if !supportedPart(name) {
						return fmt.Errorf("此Word部件不支持占位符：%s", name)
					}
					if (key == "#rows" || key == "/rows" || key == "label" || key == "value") && !inRow {
						return errors.New("rows/label/value仅支持表格行循环")
					}
					fields[key] = true
					count++
				}
				clean := tokenRE.ReplaceAllString(text, "")
				if strings.Contains(clean, "{{") || strings.Contains(clean, "}}") {
					return errors.New("模板占位符不完整或跨段落")
				}
			}
			if tag == "tr" {
				text := textOf(n)
				if strings.Count(text, "{{#rows}}") != strings.Count(text, "{{/rows}}") {
					return errors.New("表格循环标记必须在同一行成对出现")
				}
			}
			for _, c := range n.Children {
				if e := walk(c, inRow); e != nil {
					return e
				}
			}
			return nil
		}
		if e = walk(root, false); e != nil {
			return nil, e
		}
	}
	if count == 0 {
		return nil, errors.New("模板至少包含一个受支持的占位符")
	}
	list := []string{}
	for key := range fields {
		list = append(list, key)
	}
	sort.Strings(list)
	return list, nil
}
func supportedPart(name string) bool {
	return name == "word/document.xml" || (strings.HasPrefix(name, "word/header") || strings.HasPrefix(name, "word/footer")) && strings.HasSuffix(name, ".xml")
}
func Render(data []byte, snapshot map[string]any) ([]byte, error) {
	if _, e := Validate(data); e != nil {
		return nil, e
	}
	parts, e := readPackage(data)
	if e != nil {
		return nil, e
	}
	values := map[string]string{}
	for k := range known {
		if k != "label" && k != "value" && k != "#rows" && k != "/rows" {
			values[k] = ""
		}
	}
	for k, v := range snapshot {
		if v != nil {
			values[k] = fmt.Sprint(v)
		}
	}
	values["date"] = values["log_date"]
	if values["date"] == "" && len(values["captured_at"]) >= 10 {
		values["date"] = values["captured_at"][:10]
	}
	values["operator"] = values["export_operator"]
	if fields, ok := snapshot["fields"].(map[string]any); ok {
		for k, v := range fields {
			if _, exists := snapshot[k]; !exists {
				values[k] = fmt.Sprint(v)
			}
		}
	}
	rows := []map[string]string{}
	appendRow := func(m map[string]any) {
		r := map[string]string{}
		for k, v := range m {
			r[k] = fmt.Sprint(v)
		}
		rows = append(rows, r)
	}
	switch rs := snapshot["rows"].(type) {
	case []map[string]any:
		for _, r := range rs {
			appendRow(r)
		}
	case []any:
		for _, r := range rs {
			if m, ok := r.(map[string]any); ok {
				appendRow(m)
			}
		}
	}
	names := []string{}
	for name := range parts {
		names = append(names, name)
	}
	sort.Strings(names)
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, name := range names {
		b := parts[name]
		if supportedPart(name) {
			root, e := parse(b)
			if e != nil {
				return nil, e
			}
			if e = renderTree(root, values, rows); e != nil {
				return nil, e
			}
			var text strings.Builder
			root.write(&text)
			b = []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + text.String())
		}
		w, e := z.Create(name)
		if e != nil {
			return nil, e
		}
		if _, e = w.Write(b); e != nil {
			return nil, e
		}
	}
	if e = z.Close(); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
