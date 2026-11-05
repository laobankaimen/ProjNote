package exporter

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"sort"
	"strings"
	"testing"
)

const testWordNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

type testZIPEntry struct {
	name string
	data []byte
}

func testZIP(t *testing.T, entries []testZIPEntry) []byte {
	t.Helper()
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for _, entry := range entries {
		w, err := z.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func testDOCX(t *testing.T, body string, extras map[string][]byte) []byte {
	t.Helper()
	parts := map[string][]byte{
		"[Content_Types].xml":          []byte(`<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`),
		"_rels/.rels":                  []byte(`<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`),
		"word/document.xml":            []byte(`<?xml version="1.0"?><w:document xmlns:w="` + testWordNS + `"><w:body>` + body + `<w:sectPr/></w:body></w:document>`),
		"word/styles.xml":              []byte(`<?xml version="1.0"?><w:styles xmlns:w="` + testWordNS + `"><w:style w:type="paragraph" w:styleId="Normal"><w:name w:val="Normal"/></w:style><w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/></w:style></w:styles>`),
		"word/_rels/document.xml.rels": []byte(`<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`),
	}
	for name, data := range extras {
		parts[name] = data
	}
	names := make([]string, 0, len(parts))
	for name := range parts {
		names = append(names, name)
	}
	sort.Strings(names)
	entries := []testZIPEntry{}
	for _, name := range names {
		entries = append(entries, testZIPEntry{name, parts[name]})
	}
	return testZIP(t, entries)
}
func testReadDOCX(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string][]byte{}
	for _, f := range z.File {
		if _, exists := parts[f.Name]; exists {
			t.Fatalf("duplicate package entry %s", f.Name)
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		parts[f.Name] = b
	}
	return parts
}

// encoding/xml permits duplicate attributes; Word does not. Check them explicitly
// so an output that merely happens to decode in Go cannot pass this QA check.
func testStrictXML(t *testing.T, name string, data []byte) {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(data))
	var stack []xml.Name
	roots := 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("invalid XML in %s: %v", name, err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			if len(stack) == 0 {
				roots++
			}
			stack = append(stack, token.Name)
			seen := map[xml.Name]bool{}
			for _, attr := range token.Attr {
				if seen[attr.Name] {
					t.Fatalf("duplicate attribute %v on <%s> in %s", attr.Name, token.Name.Local, name)
				}
				seen[attr.Name] = true
			}
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1] != token.Name {
				t.Fatalf("mismatched XML closing element in %s", name)
			}
			stack = stack[:len(stack)-1]
		}
	}
	if roots != 1 || len(stack) != 0 {
		t.Fatalf("invalid XML root count %d or incomplete tree in %s", roots, name)
	}
}
func testText(t *testing.T, data []byte) string {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(data))
	var b strings.Builder
	inText := false
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			if token.Name.Local == "t" {
				inText = true
			}
		case xml.EndElement:
			if token.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				b.Write(token)
			}
		}
	}
	return b.String()
}
func testCountElement(t *testing.T, data []byte, element string) int {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(data))
	count := 0
	for {
		token, err := d.Token()
		if err == io.EOF {
			return count
		}
		if err != nil {
			t.Fatal(err)
		}
		if tag, ok := token.(xml.StartElement); ok && tag.Name.Local == element {
			count++
		}
	}
}
func testValidateRendered(t *testing.T, data []byte) map[string][]byte {
	t.Helper()
	parts := testReadDOCX(t, data)
	for name, b := range parts {
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".rels") {
			testStrictXML(t, name, b)
		}
	}
	if parts["word/document.xml"] == nil || parts["[Content_Types].xml"] == nil {
		t.Fatal("rendered package is missing required Word parts")
	}
	return parts
}

func TestBuiltinTemplatesRenderValidWordPackages(t *testing.T) {
	for _, id := range []string{"builtin-journal", "builtin-issue"} {
		t.Run(id, func(t *testing.T) {
			meta, template, err := LoadTemplate(t.TempDir(), id)
			if err != nil {
				t.Fatal(err)
			}
			if meta.ID != id || meta.SHA256 != Digest(template) || len(meta.Fields) == 0 {
				t.Fatal("invalid builtin template metadata", meta)
			}
			snapshot := map[string]any{"title": "中文标题 <验收> & 汇总", "project_name": "示例工程", "log_date": "2026-11-05", "export_operator": "张工", "source_version": 3, "source_kind": "worklogs", "content": "# 完成工作\n完成现场联调。\n## 明日计划\n核对验收材料。", "rows": []any{map[string]any{"label": "问题等级", "value": "主要"}, map[string]any{"label": "验证结果", "value": "复测通过"}}}
			rendered, err := Render(template, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			parts := testValidateRendered(t, rendered)
			text := testText(t, parts["word/document.xml"])
			for _, want := range []string{"中文标题 <验收> & 汇总", "示例工程", "2026-11-05", "张工"} {
				if !strings.Contains(text, want) {
					t.Fatalf("rendered builtin missing %q: %s", want, text)
				}
			}
			if strings.Contains(text, "{{") || strings.Contains(text, "}}") {
				t.Fatal("unresolved template placeholders", text)
			}
			if id == "builtin-journal" && !strings.Contains(text, "核对验收材料。") {
				t.Fatal("journal content missing")
			}
			if id == "builtin-issue" && (!strings.Contains(text, "问题等级主要") || !strings.Contains(text, "验证结果复测通过")) {
				t.Fatal("issue table rows missing", text)
			}
		})
	}
}
func TestCrossRunPlaceholdersKeepSurroundingTextAndSingleXMLSpace(t *testing.T) {
	template := testDOCX(t, `<w:p><w:r><w:rPr><w:b/></w:rPr><w:t xml:space="preserve">前缀 {{ti</w:t></w:r><w:r><w:rPr><w:i/></w:rPr><w:t xml:space="preserve">tle}} 尾巴，{{project_name}}。</w:t></w:r></w:p><w:p><w:r><w:t xml:space="preserve"> 不含占位符的保留文本 </w:t></w:r></w:p>`, nil)
	if _, err := Validate(template); err != nil {
		t.Fatal(err)
	}
	rendered, err := Render(template, map[string]any{"title": "中文 <题目> & 内容", "project_name": "甲项目"})
	if err != nil {
		t.Fatal(err)
	}
	parts := testValidateRendered(t, rendered)
	doc := parts["word/document.xml"]
	want := "前缀 中文 <题目> & 内容 尾巴，甲项目。 不含占位符的保留文本 "
	if got := testText(t, doc); got != want {
		t.Fatalf("cross-run replacement lost text: got %q want %q", got, want)
	}
	if testCountElement(t, doc, "b") != 1 || testCountElement(t, doc, "i") != 1 {
		t.Fatal("run-level bold/italic formatting lost")
	}
	var structure struct {
		Body struct {
			Paragraphs []struct {
				Runs []struct {
					Properties struct {
						Bold   *struct{} `xml:"b"`
						Italic *struct{} `xml:"i"`
					} `xml:"rPr"`
					Text string `xml:"t"`
				} `xml:"r"`
			} `xml:"p"`
		} `xml:"body"`
	}
	if err = xml.Unmarshal(doc, &structure); err != nil {
		t.Fatal(err)
	}
	runs := structure.Body.Paragraphs[0].Runs
	if len(runs) != 2 || runs[0].Properties.Bold == nil || runs[0].Text != "前缀 中文 <题目> & 内容" || runs[1].Properties.Italic == nil || runs[1].Text != " 尾巴，甲项目。" {
		t.Fatalf("replacement text was moved into a run with different formatting: %+v", runs)
	}
}
func TestMultilineContentAndTableRowsRenderWithoutTouchingOtherParts(t *testing.T) {
	body := `<w:p><w:pPr><w:spacing w:after="140"/></w:pPr><w:r><w:t>{{content}}</w:t></w:r></w:p><w:tbl><w:tblPr><w:tblW w:w="9000" w:type="dxa"/></w:tblPr><w:tr><w:tc><w:p><w:r><w:t>字段</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>值</w:t></w:r></w:p></w:tc></w:tr><w:tr><w:tc><w:p><w:r><w:t>{{#ro</w:t></w:r><w:r><w:t>ws}}{{label}}</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>{{value}}{{/rows}}</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`
	opaque := []byte{0, 1, 2, 3, 255}
	template := testDOCX(t, body, map[string][]byte{"word/media/original.bin": opaque})
	before := testReadDOCX(t, template)
	snapshot := map[string]any{"content": "# 标题\r\n第一行\n第二行 & <检查>\n\n## 分节\n### 小节\n- 计划", "rows": []map[string]any{{"label": "负责人", "value": "王工"}, {"label": "状态", "value": "已完成"}}}
	rendered, err := Render(template, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	parts := testValidateRendered(t, rendered)
	doc := parts["word/document.xml"]
	for _, want := range []string{"标题第一行第二行 & <检查>分节小节- 计划", "负责人王工", "状态已完成"} {
		if !strings.Contains(testText(t, doc), want) {
			t.Fatalf("multiline/loop content missing %q", want)
		}
	}
	if count := testCountElement(t, doc, "tr"); count != 3 {
		t.Fatalf("table loop produced %d rows, expected header + 2", count)
	}
	if strings.Contains(string(doc), "{{") {
		t.Fatal("loop markers survived rendering")
	}
	if !bytes.Equal(parts["word/media/original.bin"], opaque) || !bytes.Equal(parts["word/styles.xml"], before["word/styles.xml"]) {
		t.Fatal("render altered an unrelated original package part")
	}
	rendered, err = Render(template, map[string]any{"content": "仅正文", "rows": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	if count := testCountElement(t, testValidateRendered(t, rendered)["word/document.xml"], "tr"); count != 1 {
		t.Fatalf("empty data should retain only table header, got %d rows", count)
	}
}
func TestRejectUnknownIncompleteAndMisplacedPlaceholders(t *testing.T) {
	cases := map[string]string{"unknown": `<w:p><w:r><w:t>{{not_supported}}</w:t></w:r></w:p>`, "incomplete": `<w:p><w:r><w:t>{{title</w:t></w:r></w:p>`, "cross-paragraph": `<w:p><w:r><w:t>{{ti</w:t></w:r></w:p><w:p><w:r><w:t>tle}}</w:t></w:r></w:p>`, "row-outside-table": `<w:p><w:r><w:t>{{#rows}}{{label}}{{/rows}}</w:t></w:r></w:p>`, "unpaired-loop": `<w:tbl><w:tr><w:tc><w:p><w:r><w:t>{{#rows}}{{label}}</w:t></w:r></w:p></w:tc></w:tr></w:tbl>`, "no-placeholder": `<w:p><w:r><w:t>只有固定文字</w:t></w:r></w:p>`, "unsupported-field": `<w:p><w:fldSimple w:instr="DATE"><w:r><w:t>{{title}}</w:t></w:r></w:fldSimple></w:p>`}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			template := testDOCX(t, body, nil)
			if _, err := Validate(template); err == nil {
				t.Fatal("invalid template unexpectedly validated")
			}
			if _, err := Render(template, map[string]any{"title": "must not render"}); err == nil {
				t.Fatal("renderer silently accepted an invalid template")
			}
		})
	}
}
func TestRejectExternalRelationshipsRegardlessOfXMLQuoting(t *testing.T) {
	for _, attribute := range []string{`TargetMode="External"`, `TargetMode='External'`, `TargetMode = "External"`, `TargetMode = 'External'`} {
		t.Run(attribute, func(t *testing.T) {
			relationship := []byte(`<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="image" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="https://example.invalid/tracking.png" ` + attribute + `/></Relationships>`)
			template := testDOCX(t, `<w:p><w:r><w:t>{{title}}</w:t></w:r></w:p>`, map[string][]byte{"word/_rels/document.xml.rels": relationship})
			if _, err := Validate(template); err == nil {
				t.Fatal("external Word relationship was accepted")
			}
		})
	}
	internal := testDOCX(t, `<w:p><w:r><w:t>{{title}}</w:t></w:r></w:p>`, nil)
	if _, err := Validate(internal); err != nil {
		t.Fatal("normal internal package relationship rejected", err)
	}
}

type testRepeatReader byte

func (r testRepeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(r)
	}
	return len(p), nil
}
func testExpandedArchive(t *testing.T, sizes ...int64) []byte {
	t.Helper()
	base := testReadDOCX(t, testDOCX(t, `<w:p><w:r><w:t>{{title}}</w:t></w:r></w:p>`, nil))
	var out bytes.Buffer
	z := zip.NewWriter(&out)
	for name, b := range base {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	for i, size := range sizes {
		w, err := z.Create("word/media/large-" + string(rune('a'+i)) + ".bin")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = io.CopyN(w, testRepeatReader(0), size); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestRejectUnsafeZIPPathsDuplicatesAndLimits(t *testing.T) {
	base := testReadDOCX(t, testDOCX(t, `<w:p><w:r><w:t>{{title}}</w:t></w:r></w:p>`, nil))
	entries := []testZIPEntry{}
	for name, b := range base {
		entries = append(entries, testZIPEntry{name, b})
	}
	for _, name := range []string{"../escape.xml", "/absolute.xml", "word/../escape.xml", `word\escape.xml`, "C:/escape.xml"} {
		t.Run(name, func(t *testing.T) {
			items := append(append([]testZIPEntry{}, entries...), testZIPEntry{name, []byte("unsafe")})
			if _, err := Validate(testZIP(t, items)); err == nil {
				t.Fatal("unsafe ZIP name accepted")
			}
		})
	}
	t.Run("duplicate-document", func(t *testing.T) {
		items := append(append([]testZIPEntry{}, entries...), testZIPEntry{"word/document.xml", base["word/document.xml"]})
		if _, err := Validate(testZIP(t, items)); err == nil {
			t.Fatal("duplicate ZIP path accepted")
		}
	})
	t.Run("compressed-input-limit", func(t *testing.T) {
		if _, err := Validate(make([]byte, MaxTemplate+1)); err == nil {
			t.Fatal("oversized template accepted")
		}
	})
	t.Run("single-expanded-entry", func(t *testing.T) {
		if _, err := Validate(testExpandedArchive(t, maxExpanded+1)); err == nil {
			t.Fatal("oversized expanded file accepted")
		}
	})
	t.Run("aggregate-expanded-limit", func(t *testing.T) {
		if _, err := Validate(testExpandedArchive(t, maxExpanded/2, maxExpanded/2)); err == nil {
			t.Fatal("aggregate expanded package exceeds cap but was accepted")
		}
	})
	t.Run("entry-count", func(t *testing.T) {
		items := append([]testZIPEntry{}, entries...)
		for i := 0; i < 1025; i++ {
			items = append(items, testZIPEntry{"word/media/" + strings.Repeat("x", i/100) + string(rune(0x400+i)) + ".bin", nil})
		}
		if _, err := Validate(testZIP(t, items)); err == nil {
			t.Fatal("excessive ZIP entry count accepted")
		}
	})
}

func TestMultipleCrossRunTokensKeepOriginalOffsetsAndLiteralSourceBraces(t *testing.T) {
	body := `<w:p><w:r><w:t>【{{ti</w:t></w:r><w:r><w:t>tle}}】 / {{project_</w:t></w:r><w:r><w:t>name}} / {{source_</w:t></w:r><w:r><w:t>version}} / 再次{{title}}。</w:t></w:r></w:p>`
	template := testDOCX(t, body, nil)
	title := "长标题 {{source_kind}} & <原文>"
	rendered, err := Render(template, map[string]any{"title": title, "project_name": "中文工程项目", "source_version": 12, "source_kind": "issues"})
	if err != nil {
		t.Fatal(err)
	}
	parts := testValidateRendered(t, rendered)
	expected := "【" + title + "】 / 中文工程项目 / 12 / 再次" + title + "。"
	if got := testText(t, parts["word/document.xml"]); got != expected {
		t.Fatalf("reverse replacement changed offsets or interpreted literal source text: got %q want %q", got, expected)
	}
}
