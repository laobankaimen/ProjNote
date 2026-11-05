package exporter

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

type xlsxTestCell struct {
	Ref    string `xml:"r,attr"`
	Type   string `xml:"t,attr"`
	Style  int    `xml:"s,attr"`
	Value  string `xml:"v"`
	Inline struct {
		Text string `xml:"t"`
	} `xml:"is"`
	Formula *string `xml:"f"`
}
type xlsxTestRow struct {
	Ref   int            `xml:"r,attr"`
	Cells []xlsxTestCell `xml:"c"`
}
type xlsxTestSheet struct {
	XMLName xml.Name
	Rows    []xlsxTestRow `xml:"sheetData>row"`
	Pane    struct {
		State string `xml:"state,attr"`
		Split string `xml:"ySplit,attr"`
	} `xml:"sheetViews>sheetView>pane"`
	Page struct {
		Paper  string `xml:"paperSize,attr"`
		Width  string `xml:"fitToWidth,attr"`
		Height string `xml:"fitToHeight,attr"`
	} `xml:"pageSetup"`
}

func xlsxReadTest(t *testing.T, data []byte) (map[string][]byte, []xlsxTestSheet) {
	t.Helper()
	book, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	parts := map[string][]byte{}
	for _, f := range book.File {
		if _, exists := parts[f.Name]; exists {
			t.Fatalf("duplicate part %s", f.Name)
		}
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		_ = r.Close()
		if e != nil {
			t.Fatal(e)
		}
		parts[f.Name] = b
		testStrictXML(t, f.Name, b)
	}
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "xl/workbook.xml", "xl/_rels/workbook.xml.rels", "xl/styles.xml"} {
		if len(parts[name]) == 0 {
			t.Fatalf("missing part %s", name)
		}
	}
	var sheets []xlsxTestSheet
	for i := 1; i <= 3; i++ {
		var sheet xlsxTestSheet
		name := "xl/worksheets/sheet" + strconv.Itoa(i) + ".xml"
		if e := xml.Unmarshal(parts[name], &sheet); e != nil {
			t.Fatal(e)
		}
		if sheet.XMLName.Space != xlsxNamespace || len(sheet.Rows) < 2 {
			t.Fatalf("invalid worksheet %s: %#v", name, sheet)
		}
		for j, row := range sheet.Rows {
			if row.Ref != j+1 || row.Ref > xlsxMaxRows {
				t.Fatalf("invalid row %d", row.Ref)
			}
			for _, cell := range row.Cells {
				if cell.Formula != nil {
					t.Fatalf("source became formula: %#v", cell)
				}
				if cell.Type != "inlineStr" && cell.Type != "n" {
					t.Fatalf("unexpected cell type: %#v", cell)
				}
				value := xlsxCellText(cell)
				if !utf8.ValidString(value) || utf8.RuneCountInString(value) > 32767 || xlsxUnits(value) > 32767 {
					t.Fatalf("invalid/oversized cell %s", cell.Ref)
				}
			}
		}
		if sheet.Pane.State != "frozen" || sheet.Pane.Split != "2" || sheet.Page.Paper != "9" || sheet.Page.Width != "1" || sheet.Page.Height != "0" {
			t.Fatalf("missing view/print setup: %#v", sheet)
		}
		sheets = append(sheets, sheet)
	}
	return parts, sheets
}

var xlsxTestEscape = regexp.MustCompile(`_[xX]([0-9a-fA-F]{4})_`)

func xlsxCellText(cell xlsxTestCell) string {
	if cell.Type == "n" {
		return cell.Value
	}
	return xlsxTestEscape.ReplaceAllStringFunc(cell.Inline.Text, func(value string) string { n, _ := strconv.ParseUint(value[2:6], 16, 16); return string(rune(n)) })
}
func xlsxBody(t *testing.T, sheet xlsxTestSheet) string {
	t.Helper()
	var body strings.Builder
	started := false
	for _, row := range sheet.Rows {
		if len(row.Cells) < 2 {
			continue
		}
		if started {
			body.WriteString(xlsxCellText(row.Cells[1]))
		}
		if xlsxCellText(row.Cells[0]) == "正文原文" {
			started = true
		}
	}
	if !started {
		t.Fatal("missing body")
	}
	return body.String()
}
func xlsxSnapshotValues(sheet xlsxTestSheet) map[string]string {
	values := map[string]string{}
	current := ""
	for _, row := range sheet.Rows[2:] {
		if len(row.Cells) < 3 {
			continue
		}
		if p := xlsxCellText(row.Cells[0]); p != "" {
			current = p
		}
		values[current] += xlsxCellText(row.Cells[2])
	}
	return values
}

func TestXLSXWorkbookSavedSourcesAndInjectionAreText(t *testing.T) {
	for _, kind := range []string{"worklogs", "project_daily_logs", "issues"} {
		t.Run(kind, func(t *testing.T) {
			content := "=SUM(1,2)\n+设备联调\n@张三\n-HYPERLINK(说明)\n中文 🚀 & <输入> \"引号\" '单引号'\n"
			source := map[string]any{"source_kind": kind, "source_id": "001234567890123456789", "source_version": 7, "title": "测试中文报告", "content": content, "project_name": "项目甲", "rows": []map[string]any{{"key": "source_version", "label": "来源版本", "value": 7}, {"key": "literal", "label": "文本", "value": "=1+1"}}, "fields": map[string]any{"owner": "张三"}}
			before, _ := json.Marshal(source)
			data, err := RenderXLSX(source)
			if err != nil {
				t.Fatal(err)
			}
			// Optional disposable fixture for independent spreadsheet-reader QA.
			if target := os.Getenv("PROJNOTE_XLSX_FIXTURE"); target != "" && kind == "project_daily_logs" {
				if err = os.WriteFile(target, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			parts, sheets := xlsxReadTest(t, data)
			if xlsxBody(t, sheets[0]) != content {
				t.Fatalf("lost source body: %q", xlsxBody(t, sheets[0]))
			}
			var workbook struct {
				Sheets []struct {
					Name string `xml:"name,attr"`
					ID   string `xml:"id,attr"`
				} `xml:"sheets>sheet"`
				Names []struct {
					Name  string `xml:"name,attr"`
					Value string `xml:",chardata"`
				} `xml:"definedNames>definedName"`
			}
			if err = xml.Unmarshal(parts["xl/workbook.xml"], &workbook); err != nil {
				t.Fatal(err)
			}
			if len(workbook.Sheets) != 3 || workbook.Sheets[0].Name != "工作报告" || workbook.Sheets[1].Name != "来源明细" || len(workbook.Names) != 6 {
				t.Fatalf("invalid workbook metadata: %#v", workbook)
			}
			for _, sheet := range workbook.Sheets {
				if sheet.ID == "" || strings.ContainsAny(sheet.Name, "[]:*?/\\") || utf8.RuneCountInString(sheet.Name) > 31 {
					t.Fatalf("invalid sheet: %#v", sheet)
				}
			}
			row := sheets[1].Rows[2]
			if row.Cells[2].Type != "n" || row.Cells[2].Value != "7" {
				t.Fatalf("version should be exact numeric: %#v", row)
			}
			for _, sheet := range sheets {
				for _, row := range sheet.Rows {
					for _, cell := range row.Cells {
						text := xlsxCellText(cell)
						if strings.HasPrefix(text, "=") || strings.HasPrefix(text, "+") || strings.HasPrefix(text, "@") || text == source["source_id"] {
							if cell.Type != "inlineStr" {
								t.Fatalf("unsafe/coerced text: %#v", cell)
							}
						}
					}
				}
			}
			after, _ := json.Marshal(source)
			if !bytes.Equal(before, after) {
				t.Fatal("renderer mutated frozen source")
			}
		})
	}
}

func TestXLSXLongUnicodeAndBlankLinesAreComplete(t *testing.T) {
	content := "  原文开头\r\n\n" + strings.Repeat("测🚀&<>", 9000) + "\n" + strings.Repeat("\n", 270) + "结尾保留空格  \n"
	source := map[string]any{"title": "长正文", "content": content, "rows": []map[string]any{{"key": "long", "label": "长字段", "value": content}}, "source_snapshot": map[string]any{"task_snapshot_semantics": "生成时当前状态", "team_entries": []map[string]any{{"person_name": "李四", "content": "真实团队正文"}}, "tasks": []map[string]any{{"id": "原任务ID", "version": 3}}}}
	data, err := RenderXLSX(source)
	if err != nil {
		t.Fatal(err)
	}
	_, sheets := xlsxReadTest(t, data)
	if got := xlsxBody(t, sheets[0]); got != content {
		t.Fatalf("long body was changed: %d/%d bytes", len(got), len(content))
	}
	var detail strings.Builder
	for _, row := range sheets[1].Rows[2:] {
		detail.WriteString(xlsxCellText(row.Cells[2]))
	}
	if detail.String() != content {
		t.Fatal("long detail value was truncated/duplicated")
	}
	values := xlsxSnapshotValues(sheets[2])
	for key, want := range map[string]string{"/content": content, "/source_snapshot/team_entries/0/content": "真实团队正文", "/source_snapshot/tasks/0/id": "原任务ID", "/source_snapshot/tasks/0/version": "3", "/source_snapshot/task_snapshot_semantics": "生成时当前状态"} {
		if values[key] != want {
			t.Fatalf("snapshot value %s lost: got %d want %d", key, len(values[key]), len(want))
		}
	}
	if len(sheets[0].Rows) < 400 {
		t.Fatal("long source/blank lines were not split into readable rows")
	}
}

func TestXLSXEscapeLiteralsControlsAndNumericPrecision(t *testing.T) {
	content := "原样_x0041_/_x000A_/_x005F_\x00\x01\t\r\n🚀"
	source := map[string]any{"content": content, "rows": []map[string]any{{"key": "large", "label": "精确大整数", "value": json.Number("9007199254740991")}, {"key": "string", "label": "保留文本", "value": "00123"}, {"key": "decimal", "label": "实际数值", "value": 12.5}}}
	data, err := RenderXLSX(source)
	if err != nil {
		t.Fatal(err)
	}
	_, sheets := xlsxReadTest(t, data)
	if xlsxBody(t, sheets[0]) != content {
		t.Fatalf("escape/control source changed: %q", xlsxBody(t, sheets[0]))
	}
	for i, want := range []struct{ kind, value string }{{"inlineStr", "9007199254740991"}, {"inlineStr", "00123"}, {"n", "12.5"}} {
		cell := sheets[1].Rows[i+2].Cells[2]
		if cell.Type != want.kind || xlsxCellText(cell) != want.value {
			t.Fatalf("precision/coercion: %#v", cell)
		}
	}
}

func TestXLSXBoundsRejectInsteadOfTruncating(t *testing.T) {
	for _, value := range []string{strings.Repeat("中", 70000), strings.Repeat("🚀", 40000)} {
		pieces := xlsxPieces(value, xlsxMaxCell)
		if len(pieces) < 2 || strings.Join(pieces, "") != value {
			t.Fatal("invalid unicode splitting")
		}
		for _, piece := range pieces {
			if utf8.RuneCountInString(piece) > 32767 || xlsxUnits(piece) > 32767 {
				t.Fatal("exceeded Excel cell bounds")
			}
		}
	}
	sheet := newXLSXSheet(io.Discard, "测试", []float64{10}, false)
	sheet.row = xlsxMaxRows
	if err := sheet.add([]xlsxCell{{"overflow", 0}}, 20); err == nil {
		t.Fatal("row overflow accepted")
	}
	sheet.row = 0
	if err := sheet.add([]xlsxCell{{strings.Repeat("🚀", 16384), 0}}, 20); err == nil {
		t.Fatal("UTF16 cell overflow accepted")
	}
	out := &xlsxBuffer{maximum: 4}
	if _, err := out.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write([]byte("5")); err == nil || out.String() != "1234" {
		t.Fatal("output limit truncated successful bytes")
	}
}

func TestXLSXRejectsInvalidSnapshots(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	for _, source := range []map[string]any{nil, cyclic, {"rows": "not rows"}, {"rows": []any{"not object"}}, {"content": string([]byte{0xff})}, {"content": func() {}}} {
		if data, err := RenderXLSX(source); err == nil || data != nil {
			t.Fatalf("invalid snapshot accepted: %#v", source)
		}
	}
}
