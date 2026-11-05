package planimport

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, sheet string, parts map[string]string) []byte {
	t.Helper()
	all := map[string]string{
		"xl/workbook.xml":            `<workbook xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="计划" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   `<worksheet><sheetData>` + sheet + `</sheetData></worksheet>`}
	for k, v := range parts {
		all[k] = v
	}
	var b bytes.Buffer
	z := zip.NewWriter(&b)
	for k, v := range all {
		w, e := z.Create(k)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write([]byte(v)); e != nil {
			t.Fatal(e)
		}
	}
	if e := z.Close(); e != nil {
		t.Fatal(e)
	}
	return b.Bytes()
}
func textCell(ref, value string) string {
	return fmt.Sprintf(`<c r="%s" t="inlineStr"><is><t>%s</t></is></c>`, ref, value)
}
func TestActualImportTemplate(t *testing.T) {
	data, e := os.ReadFile("../server/assets/task-import-template.xlsx")
	if e != nil {
		t.Fatal(e)
	}
	r, e := Read(data)
	if e != nil || len(r.Errors) > 0 || len(r.Rows) != 3 {
		t.Fatalf("template: %+v %v", r, e)
	}
	if r.Rows[1]["plan_start"] != "2026-11-05" || r.Rows[2]["parent_key"] != "1" || r.Rows[1]["estimate_hours"] != float64(8) {
		t.Fatalf("typed template values: %+v", r.Rows)
	}
}
func TestSharedStringsHierarchyFormulaAndDates(t *testing.T) {
	header := `<row r="1">` + textCell("A1", "任务编号") + textCell("B1", "任务名称") + textCell("C1", "计划开始") + textCell("D1", "进度") + `</row>`
	rows := header + `<row r="2">` + textCell("A2", "1") + `<c r="B2" t="s"><v>0</v></c><c r="C2"><v>61</v></c><c r="D2"><v>0</v></c></row><row r="3">` + textCell("A3", "1.1") + textCell("B3", "叶子") + textCell("C3", "2026/11/5") + textCell("D3", "40%") + `</row>`
	data := fixture(t, rows, map[string]string{"xl/sharedStrings.xml": `<sst><si><r><t>父</t></r><r><t>任务</t></r></si></sst>`})
	r, e := Read(data)
	if e != nil || len(r.Errors) > 0 || r.Rows[0]["name"] != "父任务" || r.Rows[0]["plan_start"] != "1900-03-01" || r.Rows[1]["parent_key"] != "1" || r.Rows[1]["progress"] != float64(40) {
		t.Fatalf("read %+v %v", r, e)
	}
	bad := strings.Replace(rows, `<v>61</v>`, `<f>NOW()</f><v>61</v>`, 1)
	r, e = Read(fixture(t, bad, map[string]string{"xl/sharedStrings.xml": `<sst><si><t>父任务</t></si></sst>`}))
	if e != nil || len(r.Errors) != 1 || r.Errors[0].Row != 2 || !strings.Contains(r.Errors[0].Message, "公式") {
		t.Fatalf("formula %+v %v", r, e)
	}
	for _, tc := range []struct {
		v    string
		mode bool
		want string
	}{{"1", false, "1900-01-01"}, {"59", false, "1900-02-28"}, {"61", false, "1900-03-01"}, {"0", true, "1904-01-01"}, {"44869", true, "2026-11-05"}} {
		got, e := parseDate(tc.v, tc.mode)
		if e != nil || got != tc.want {
			t.Fatalf("date %s mode%v: %s %v", tc.v, tc.mode, got, e)
		}
	}
	for _, v := range []string{"60", "2026-02-30", "123.5", "NaN", "-1"} {
		if _, e := parseDate(v, false); e == nil {
			t.Fatalf("accepted bad date %s", v)
		}
	}
}
func TestWorkbookBoundaries(t *testing.T) {
	rows := `<row r="1">` + textCell("A1", "任务名称") + `</row><row r="2">` + textCell("A2", "任务") + `</row>`
	cases := map[string]map[string]string{"escape": {"../x": "bad"}, "external": {"xl/_rels/workbook.xml.rels": `<Relationships><Relationship Id="rId1" Target="https://example.invalid/a" TargetMode="External"/></Relationships>`}, "bad_shared": {"xl/worksheets/sheet1.xml": `<worksheet><sheetData><row r="1"><c r="A1" t="s"><v>9</v></c></row></sheetData></worksheet>`}}
	for name, parts := range cases {
		t.Run(name, func(t *testing.T) {
			if _, e := Read(fixture(t, rows, parts)); e == nil {
				t.Fatal("accepted invalid workbook")
			}
		})
	}
	merged := `<worksheet><sheetData>` + rows + `</sheetData><mergeCells><mergeCell ref="A2:B2"/></mergeCells></worksheet>`
	r, e := Read(fixture(t, rows, map[string]string{"xl/worksheets/sheet1.xml": merged}))
	if e != nil || len(r.Errors) != 1 {
		t.Fatalf("merge errors %+v %v", r, e)
	}
	if _, e = Read(make([]byte, MaxFileBytes+1)); e == nil {
		t.Fatal("oversized file accepted")
	}
}

func TestNumericPercentageCellUsesItsFormat(t *testing.T) {
	rows := `<row r="1">` + textCell("A1", "任务名称") + textCell("B1", "进度") + `</row><row r="2">` + textCell("A2", "安装") + `<c r="B2" s="1"><v>0.4</v></c></row>`
	data := fixture(t, rows, map[string]string{"xl/styles.xml": `<styleSheet><cellXfs><xf numFmtId="0"/><xf numFmtId="9"/></cellXfs></styleSheet>`})
	result, err := Read(data)
	if err != nil || len(result.Errors) > 0 || result.Rows[0]["progress"] != float64(40) {
		t.Fatalf("percentage not preserved: %+v %v", result, err)
	}
}
