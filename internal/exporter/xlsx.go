package exporter

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	xlsxNamespace = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
	xlsxMaxRows   = 1048576
	xlsxMaxCell   = 32767
	// Short display pieces keep wrapped paragraphs printable rather than hiding
	// thousands of characters behind Excel's maximum row height.
	xlsxPieceSize = 240
	xlsxMaxOutput = 64 << 20
)

var xlsxEscapedLiteral = regexp.MustCompile(`_[xX][0-9a-fA-F]{4}_`)

// RenderXLSX exports the frozen source supplied by Store.ExportSnapshot. Text
// never becomes a formula, dates stay explicit text, and all source structure is
// retained in a separate sheet without inferring people, tasks or past states.
func RenderXLSX(snapshot map[string]any) ([]byte, error) {
	if snapshot == nil {
		return nil, errors.New("Excel导出缺少来源快照")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("Excel来源快照无效: %w", err)
	}
	if !xlsxValidStrings(reflect.ValueOf(snapshot)) {
		return nil, errors.New("Excel来源文本不是有效UTF-8")
	}
	// Normalize the same JSON representation that is persisted in export_jobs.
	// UseNumber prevents integer source versions from passing through float64.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var source map[string]any
	if err = decoder.Decode(&source); err != nil {
		return nil, err
	}
	output := &xlsxBuffer{maximum: xlsxMaxOutput}
	book := zip.NewWriter(output)
	names := []string{"工作报告", "来源明细", "来源快照"}
	lastRows := make([]int, len(names))
	for i, name := range names {
		part, e := book.Create(fmt.Sprintf("xl/worksheets/sheet%d.xml", i+1))
		if e != nil {
			return nil, e
		}
		widths := []float64{16, 90, 12}
		if i > 0 {
			widths = []float64{38, 22, 78, 12}
		}
		sheet := newXLSXSheet(part, name, widths, i > 0)
		switch i {
		case 0:
			e = sheet.report(source)
		case 1:
			e = sheet.details(source)
		case 2:
			e = sheet.snapshot(source)
		}
		if e == nil {
			e = sheet.finish()
		}
		if e != nil {
			return nil, e
		}
		lastRows[i] = sheet.row
	}
	parts := []struct{ name, content string }{
		{"[Content_Types].xml", xlsxContentTypes},
		{"_rels/.rels", xlsxRootRelationships},
		{"xl/_rels/workbook.xml.rels", xlsxWorkbookRelationships},
		{"xl/styles.xml", xlsxStyles},
		{"xl/workbook.xml", xlsxWorkbook(names, lastRows)},
	}
	for _, part := range parts {
		w, e := book.Create(part.name)
		if e != nil {
			return nil, e
		}
		if _, e = io.WriteString(w, part.content); e != nil {
			return nil, e
		}
	}
	if err = book.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

type xlsxBuffer struct {
	bytes.Buffer
	maximum int
}

func (b *xlsxBuffer) Write(p []byte) (int, error) {
	if len(p) > b.maximum-b.Len() {
		return 0, errors.New("Excel导出结果超过64MB")
	}
	return b.Buffer.Write(p)
}

type xlsxXML struct {
	w   io.Writer
	err error
}

func (x *xlsxXML) write(format string, args ...any) {
	if x.err == nil {
		_, x.err = fmt.Fprintf(x.w, format, args...)
	}
}
func (x *xlsxXML) text(value string) {
	if x.err != nil {
		return
	}
	if !utf8.ValidString(value) {
		x.err = errors.New("Excel来源文本不是有效UTF-8")
		return
	}
	// Escape SpreadsheetML's own _xHHHH_ syntax before XML escaping. XML 1.0
	// control characters use that syntax, preserving their exact source value.
	value = xlsxEscapedLiteral.ReplaceAllStringFunc(value, func(s string) string { return "_x005F_" + s[1:] })
	var safe strings.Builder
	for _, r := range value {
		if r == '\t' || r == '\n' || r == '\r' || r >= 0x20 && r <= 0xD7FF || r >= 0xE000 && r <= 0xFFFD || r >= 0x10000 && r <= 0x10FFFF {
			safe.WriteRune(r)
		} else {
			fmt.Fprintf(&safe, "_x%04X_", r)
		}
	}
	x.err = xml.EscapeText(x.w, []byte(safe.String()))
}

type xlsxCell struct {
	value any
	style int
}

type xlsxSheet struct {
	x          *xlsxXML
	name       string
	row, cols  int
	filterRows bool
	widths     []float64
}

func newXLSXSheet(w io.Writer, name string, widths []float64, filterRows bool) *xlsxSheet {
	s := &xlsxSheet{x: &xlsxXML{w: w}, name: name, cols: len(widths), filterRows: filterRows, widths: widths}
	s.x.write(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="%s"><sheetPr><pageSetUpPr fitToPage="1"/></sheetPr><sheetViews><sheetView workbookViewId="0"><pane ySplit="2" topLeftCell="A3" activePane="bottomLeft" state="frozen"/><selection pane="bottomLeft" activeCell="A3" sqref="A3"/></sheetView></sheetViews><sheetFormatPr defaultRowHeight="22"/><cols>`, xlsxNamespace)
	for i, width := range widths {
		s.x.write(`<col min="%d" max="%d" width="%.1f" customWidth="1"/>`, i+1, i+1, width)
	}
	s.x.write(`</cols><sheetData>`)
	return s
}

func (s *xlsxSheet) add(cells []xlsxCell, height float64) error {
	if s.row >= xlsxMaxRows {
		return fmt.Errorf("Excel工作表%s超过1048576行，未截断来源", s.name)
	}
	if len(cells) > s.cols {
		return errors.New("Excel导出列数超过工作表定义")
	}
	s.row++
	s.x.write(`<row r="%d" ht="%.1f" customHeight="1">`, s.row, height)
	for i, cell := range cells {
		ref := fmt.Sprintf("%c%d", 'A'+i, s.row)
		if number, ok := xlsxNumeric(cell.value); ok {
			s.x.write(`<c r="%s" s="3" t="n"><v>%s</v></c>`, ref, number)
			continue
		}
		text := xlsxString(cell.value)
		if utf8.RuneCountInString(text) > xlsxMaxCell || xlsxUnits(text) > xlsxMaxCell {
			return errors.New("Excel单元格超过32767字符，未截断来源")
		}
		s.x.write(`<c r="%s" s="%d" t="inlineStr"><is><t xml:space="preserve">`, ref, cell.style)
		s.x.text(text)
		s.x.write(`</t></is></c>`)
	}
	s.x.write(`</row>`)
	return s.x.err
}

func (s *xlsxSheet) heading(title string, columns []string) error {
	if err := s.add([]xlsxCell{{title, 1}}, 30); err != nil {
		return err
	}
	cells := make([]xlsxCell, len(columns))
	for i, value := range columns {
		cells[i] = xlsxCell{value, 2}
	}
	return s.add(cells, 24)
}

// Pieces occupy continuation rows with a visible piece counter. Other columns
// are blank once exhausted, so concatenating a column never repeats source data.
func (s *xlsxSheet) expanded(values ...any) error {
	parts := make([][]any, len(values))
	count := 1
	for i, value := range values {
		if _, ok := xlsxNumeric(value); ok {
			parts[i] = []any{value}
		} else {
			maximum := xlsxPieceSize
			if i < len(s.widths) {
				if printable := int((s.widths[i]-2)/2) * 14; printable < maximum {
					maximum = max(40, printable)
				}
			}
			for _, piece := range xlsxPieces(xlsxString(value), maximum) {
				parts[i] = append(parts[i], piece)
			}
		}
		if len(parts[i]) > count {
			count = len(parts[i])
		}
	}
	for i := 0; i < count; i++ {
		cells := make([]xlsxCell, len(values)+1)
		lines := 1
		for col := range values {
			if i < len(parts[col]) {
				cells[col].value = parts[col][i]
				if n := xlsxWrappedLines(xlsxString(parts[col][i]), s.widths[col]); n > lines {
					lines = n
				}
			}
		}
		cells[len(values)].value = fmt.Sprintf("%d/%d", i+1, count)
		// Conservative width estimate for CJK and emoji; chunks stay well below
		// Excel's 409-point row-height limit, including line breaks.
		height := math.Min(360, math.Max(24, 18*float64(lines)+6))
		if err := s.add(cells, height); err != nil {
			return err
		}
	}
	return nil
}

func (s *xlsxSheet) report(source map[string]any) error {
	if err := s.heading("工作报告", []string{"字段 / 原文行", "内容", "片段"}); err != nil {
		return err
	}
	for _, field := range []struct{ key, label string }{
		{"title", "标题"}, {"scope", "范围"}, {"project_name", "项目"}, {"log_date", "日志日期"}, {"period_type", "周期"},
		{"person_name", "负责人/录入人"}, {"entered_by", "实际录入人"}, {"export_operator", "导出操作人"},
		{"source_kind", "来源类型"}, {"source_id", "来源ID"}, {"source_version", "来源版本"}, {"captured_at", "快照时间"},
	} {
		if value, exists := source[field.key]; exists {
			if err := s.expanded(field.label, value); err != nil {
				return err
			}
		}
	}
	if err := s.add([]xlsxCell{{"正文原文", 2}, {"按原文逐行展示；长行继续拆片，不截断", 2}, {"片段", 2}}, 24); err != nil {
		return err
	}
	for i, line := range strings.SplitAfter(xlsxString(source["content"]), "\n") {
		if err := s.expanded(strconv.Itoa(i+1), line); err != nil {
			return err
		}
	}
	return nil
}

func (s *xlsxSheet) details(source map[string]any) error {
	if err := s.heading("来源字段明细", []string{"字段 key", "名称 label", "值 value", "片段"}); err != nil {
		return err
	}
	if value, exists := source["rows"]; exists && value != nil {
		rows, ok := value.([]any)
		if !ok {
			return errors.New("Excel来源rows不是字段行数组")
		}
		for _, row := range rows {
			m, ok := row.(map[string]any)
			if !ok {
				return errors.New("Excel来源rows包含无效字段行")
			}
			if err := s.expanded(m["key"], m["label"], m["value"]); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *xlsxSheet) snapshot(source map[string]any) error {
	if err := s.heading("原始来源快照", []string{"JSON路径", "类型", "值", "片段"}); err != nil {
		return err
	}
	var flatten func(string, any) error
	flatten = func(path string, value any) error {
		switch v := value.(type) {
		case map[string]any:
			if err := s.expanded(path, "object", ""); err != nil {
				return err
			}
			keys := make([]string, 0, len(v))
			for key := range v {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if err := flatten(path+"/"+strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1"), v[key]); err != nil {
					return err
				}
			}
		case []any:
			if err := s.expanded(path, "array", json.Number(strconv.Itoa(len(v)))); err != nil {
				return err
			}
			for i, item := range v {
				if err := flatten(path+"/"+strconv.Itoa(i), item); err != nil {
					return err
				}
			}
		default:
			kind := "string"
			switch v.(type) {
			case nil:
				kind = "null"
			case json.Number:
				kind = "number"
			case bool:
				kind = "boolean"
			}
			return s.expanded(path, kind, v)
		}
		return nil
	}
	return flatten("", source)
}

func (s *xlsxSheet) finish() error {
	s.x.write(`</sheetData>`)
	if s.filterRows && s.row > 2 {
		s.x.write(`<autoFilter ref="A2:%c%d"/>`, 'A'+s.cols-1, s.row)
	}
	s.x.write(`<mergeCells count="1"><mergeCell ref="A1:%c1"/></mergeCells><printOptions horizontalCentered="1"/><pageMargins left="0.35" right="0.35" top="0.5" bottom="0.5" header="0.2" footer="0.2"/><pageSetup paperSize="9" orientation="%s" fitToWidth="1" fitToHeight="0"/><headerFooter><oddFooter>&amp;C第 &amp;P 页 / 共 &amp;N 页</oddFooter></headerFooter></worksheet>`, 'A'+s.cols-1, map[bool]string{true: "landscape", false: "portrait"}[s.filterRows])
	return s.x.err
}

func xlsxString(value any) string {
	if value == nil {
		return ""
	}
	if s, ok := value.(string); ok {
		return s
	}
	return fmt.Sprint(value)
}
func xlsxNumeric(value any) (string, bool) {
	n, ok := value.(json.Number)
	if !ok {
		return "", false
	}
	text := n.String()
	number, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
		return "", false
	}
	mantissa := strings.SplitN(strings.ToLower(text), "e", 2)[0]
	digits := strings.TrimLeft(strings.ReplaceAll(strings.TrimPrefix(mantissa, "-"), ".", ""), "0")
	if len(digits) > 15 {
		return "", false // Excel would round the saved source value.
	}
	return text, true
}
func xlsxUnits(value string) int {
	n := 0
	for _, r := range value {
		n++
		if r > 0xffff {
			n++
		}
	}
	return n
}
func xlsxPieces(value string, maximum int) []string {
	if maximum < 1 || maximum > xlsxMaxCell {
		maximum = xlsxMaxCell
	}
	if value == "" {
		return []string{""}
	}
	parts := []string{}
	start, count, units := 0, 0, 0
	for pos, r := range value {
		width := 1
		if r > 0xffff {
			width = 2
		}
		if count == maximum || units+width > xlsxMaxCell {
			parts = append(parts, value[start:pos])
			start, count, units = pos, 0, 0
		}
		count++
		units += width
		if r == '\n' || r == '\r' && (pos+1 == len(value) || value[pos+1] != '\n') {
			end := pos + utf8.RuneLen(r)
			parts = append(parts, value[start:end])
			start, count, units = end, 0, 0
		}
	}
	if start < len(value) {
		parts = append(parts, value[start:])
	}
	return parts
}

func xlsxWrappedLines(value string, width float64) int {
	available := max(1, int(width)-2)
	lines, used := 1, 0
	for _, r := range value {
		if r == '\n' {
			lines++
			used = 0
			continue
		}
		if r == '\r' {
			continue
		}
		units := 1
		if r > 127 {
			units = 2
		}
		if used+units > available {
			lines++
			used = 0
		}
		used += units
	}
	return lines
}
func xlsxValidStrings(value reflect.Value) bool {
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.String:
		return utf8.ValidString(value.String())
	case reflect.Interface, reflect.Pointer:
		if !value.IsNil() {
			return xlsxValidStrings(value.Elem())
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			if !xlsxValidStrings(iter.Key()) || !xlsxValidStrings(iter.Value()) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if !xlsxValidStrings(value.Index(i)) {
				return false
			}
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if !xlsxValidStrings(value.Field(i)) {
				return false
			}
		}
	}
	return true
}

func xlsxWorkbook(names []string, lastRows []int) string {
	var b strings.Builder
	x := &xlsxXML{w: &b}
	x.write(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="%s" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><bookViews><workbookView/></bookViews><sheets>`, xlsxNamespace)
	for i, name := range names {
		x.write(`<sheet name="`)
		x.text(name)
		x.write(`" sheetId="%d" r:id="rId%d"/>`, i+1, i+1)
	}
	x.write(`</sheets><definedNames>`)
	for i, name := range names {
		x.write(`<definedName name="_xlnm.Print_Titles" localSheetId="%d">`, i)
		x.text("'" + name + "'!$1:$2")
		x.write(`</definedName><definedName name="_xlnm.Print_Area" localSheetId="%d">`, i)
		lastColumn := "D"
		if i == 0 {
			lastColumn = "C"
		}
		x.text(fmt.Sprintf("'%s'!$A$1:$%s$%d", name, lastColumn, lastRows[i]))
		x.write(`</definedName>`)
	}
	x.write(`</definedNames></workbook>`)
	return b.String()
}

const xlsxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/worksheets/sheet2.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/worksheets/sheet3.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`
const xlsxRootRelationships = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`
const xlsxWorkbookRelationships = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet3.xml"/><Relationship Id="rId4" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`
const xlsxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><fonts count="3"><font><sz val="11"/><color rgb="FF24334A"/><name val="Microsoft YaHei"/><family val="2"/></font><font><b/><sz val="16"/><color rgb="FFFFFFFF"/><name val="Microsoft YaHei"/></font><font><b/><sz val="11"/><color rgb="FF24334A"/><name val="Microsoft YaHei"/></font></fonts><fills count="4"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FF2465CE"/><bgColor indexed="64"/></patternFill></fill><fill><patternFill patternType="solid"><fgColor rgb="FFDFE5EE"/><bgColor indexed="64"/></patternFill></fill></fills><borders count="2"><border><left/><right/><top/><bottom/><diagonal/></border><border><left style="thin"><color rgb="FFDFE5EE"/></left><right style="thin"><color rgb="FFDFE5EE"/></right><top style="thin"><color rgb="FFDFE5EE"/></top><bottom style="thin"><color rgb="FFDFE5EE"/></bottom><diagonal/></border></borders><cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs><cellXfs count="4"><xf numFmtId="49" fontId="0" fillId="0" borderId="1" xfId="0" applyNumberFormat="1" applyAlignment="1"><alignment vertical="top" wrapText="1"/></xf><xf numFmtId="49" fontId="1" fillId="2" borderId="0" xfId="0" applyAlignment="1" applyFont="1" applyFill="1"><alignment vertical="center" wrapText="1"/></xf><xf numFmtId="49" fontId="2" fillId="3" borderId="1" xfId="0" applyAlignment="1" applyFont="1" applyFill="1"><alignment vertical="center" wrapText="1"/></xf><xf numFmtId="0" fontId="0" fillId="0" borderId="1" xfId="0" applyAlignment="1"><alignment horizontal="right" vertical="top" wrapText="1"/></xf></cellXfs><cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles></styleSheet>`
