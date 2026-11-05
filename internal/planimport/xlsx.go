// Package planimport reads the documented task-plan subset of XLSX. It never
// evaluates formulas or follows workbook links.
package planimport

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"math"
	"path"
	"strconv"
	"strings"
	"time"
)

const MaxFileBytes = 10 << 20
const MaxExpandedBytes = 48 << 20
const MaxRows = 5000

type Issue struct {
	Row     int    `json:"row"`
	Field   string `json:"field"`
	Message string `json:"message"`
}
type Result struct {
	Rows     []map[string]any `json:"rows"`
	Errors   []Issue          `json:"errors"`
	Warnings []Issue          `json:"warnings"`
	Sheet    string           `json:"sheet"`
}
type xcell struct {
	Ref     string   `xml:"r,attr"`
	Type    string   `xml:"t,attr"`
	Style   int      `xml:"s,attr"`
	Value   string   `xml:"v"`
	Formula *string  `xml:"f"`
	Inline  richText `xml:"is"`
}
type richText struct {
	Text string `xml:"t"`
	Runs []struct {
		Text string `xml:"t"`
	} `xml:"r"`
}

func (r richText) String() string {
	var s strings.Builder
	s.WriteString(r.Text)
	for _, t := range r.Runs {
		s.WriteString(t.Text)
	}
	return s.String()
}

type row struct {
	Number int     `xml:"r,attr"`
	Cells  []xcell `xml:"c"`
}

// Read accepts an unencrypted .xlsx workbook and reads its first visible sheet.
func Read(data []byte) (Result, error) {
	result := Result{Rows: []map[string]any{}, Errors: []Issue{}, Warnings: []Issue{}}
	if len(data) > MaxFileBytes {
		return result, fmt.Errorf("Excel文件超过10MB")
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return result, fmt.Errorf("不是有效的.xlsx文件；旧版.xls请另存为.xlsx")
	}
	entries := map[string]*zip.File{}
	var total uint64
	if len(z.File) > 2048 {
		return result, fmt.Errorf("Excel压缩包文件数量过多")
	}
	for _, f := range z.File {
		if strings.Contains(f.Name, "\\") || strings.HasPrefix(f.Name, "/") || path.Clean(f.Name) != strings.TrimSuffix(f.Name, "/") || strings.Contains(f.Name, ":") || strings.HasPrefix(f.Name, "../") {
			return result, fmt.Errorf("Excel包含不安全的压缩路径")
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if _, ok := entries[f.Name]; ok {
			return result, fmt.Errorf("Excel包含重复文件")
		}
		if f.UncompressedSize64 > MaxExpandedBytes || total > MaxExpandedBytes-f.UncompressedSize64 {
			return result, fmt.Errorf("Excel解压容量超过48MB")
		}
		total += f.UncompressedSize64
		entries[f.Name] = f
	}
	read := func(name string, target any) error {
		f := entries[name]
		if f == nil {
			return fmt.Errorf("Excel缺少%s", name)
		}
		r, e := f.Open()
		if e != nil {
			return e
		}
		defer r.Close()
		d := xml.NewDecoder(io.LimitReader(r, MaxExpandedBytes+1))
		if e = d.Decode(target); e != nil {
			return fmt.Errorf("Excel XML损坏：%s", name)
		}
		return nil
	}
	var book struct {
		Props struct {
			Date1904 string `xml:"date1904,attr"`
		} `xml:"workbookPr"`
		Sheets []struct {
			Name  string `xml:"name,attr"`
			ID    string `xml:"id,attr"`
			State string `xml:"state,attr"`
		} `xml:"sheets>sheet"`
	}
	if err = read("xl/workbook.xml", &book); err != nil {
		return result, err
	}
	var rels struct {
		Items []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
			Mode   string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	if err = read("xl/_rels/workbook.xml.rels", &rels); err != nil {
		return result, err
	}
	sheetPath := ""
	visible := 0
	for _, sheet := range book.Sheets {
		if sheet.State == "hidden" || sheet.State == "veryHidden" {
			continue
		}
		visible++
		if sheetPath != "" {
			continue
		}
		result.Sheet = sheet.Name
		for _, rel := range rels.Items {
			if rel.ID != sheet.ID {
				continue
			}
			if rel.Mode == "External" {
				return result, fmt.Errorf("不支持外部工作表链接")
			}
			target := rel.Target
			if strings.HasPrefix(target, "/") {
				sheetPath = strings.TrimPrefix(target, "/")
			} else {
				sheetPath = path.Clean(path.Join("xl", target))
			}
			if !strings.HasPrefix(sheetPath, "xl/") {
				return result, fmt.Errorf("工作表路径超出工作簿")
			}
		}
	}
	if sheetPath == "" {
		return result, fmt.Errorf("Excel没有可读取的可见工作表")
	}
	if visible > 1 {
		result.Warnings = append(result.Warnings, Issue{0, "sheet", "只导入首个可见工作表：" + result.Sheet})
	}
	var shared struct {
		Items []richText `xml:"si"`
	}
	if entries["xl/sharedStrings.xml"] != nil {
		if err = read("xl/sharedStrings.xml", &shared); err != nil {
			return result, err
		}
	}

	var styles struct {
		Formats []struct {
			ID   int    `xml:"numFmtId,attr"`
			Code string `xml:"formatCode,attr"`
		} `xml:"numFmts>numFmt"`
		Cells []struct {
			ID int `xml:"numFmtId,attr"`
		} `xml:"cellXfs>xf"`
	}
	percentStyles := map[int]bool{}
	if entries["xl/styles.xml"] != nil {
		if err = read("xl/styles.xml", &styles); err != nil {
			return result, err
		}
		percentIDs := map[int]bool{9: true, 10: true}
		for _, f := range styles.Formats {
			if strings.Contains(f.Code, "%") && !strings.Contains(f.Code, `\%`) && !strings.Contains(f.Code, `"%"`) {
				percentIDs[f.ID] = true
			}
		}
		for i, f := range styles.Cells {
			percentStyles[i] = percentIDs[f.ID]
		}
	}
	var sheet struct {
		Rows   []row `xml:"sheetData>row"`
		Merged []struct {
			Ref string `xml:"ref,attr"`
		} `xml:"mergeCells>mergeCell"`
	}
	if err = read(sheetPath, &sheet); err != nil {
		return result, err
	}
	if len(sheet.Merged) > 0 {
		result.Errors = append(result.Errors, Issue{0, "sheet", "导入区域不支持合并单元格，请先取消合并并补齐字段"})
	}
	if len(sheet.Rows) == 0 {
		return result, fmt.Errorf("工作表为空")
	}
	headers := map[int]string{}
	headerSeen := false
	date1904 := book.Props.Date1904 == "1" || book.Props.Date1904 == "true"
	for ri, source := range sheet.Rows {
		number := source.Number
		if number == 0 {
			number = ri + 1
		}
		values := map[int]string{}
		formula := map[int]bool{}
		percentage := map[int]bool{}
		nonempty := false
		for _, c := range source.Cells {
			col, e := column(c.Ref)
			if e != nil || col > 128 {
				return result, fmt.Errorf("第%d行单元格坐标无效或超出128列", number)
			}
			v := c.Value
			switch c.Type {
			case "s":
				idx, e := strconv.Atoi(v)
				if e != nil || idx < 0 || idx >= len(shared.Items) {
					return result, fmt.Errorf("第%d行共享文本索引无效", number)
				}
				v = shared.Items[idx].String()
			case "inlineStr":
				v = c.Inline.String()
			case "e":
				result.Errors = append(result.Errors, Issue{number, c.Ref, "单元格含Excel错误值"})
			}
			if _, exists := values[col]; exists {
				return result, fmt.Errorf("第%d行有重复单元格坐标", number)
			}
			values[col] = strings.TrimSpace(v)
			if v != "" || c.Formula != nil {
				nonempty = true
			}
			formula[col] = c.Formula != nil
			percentage[col] = percentStyles[c.Style] && (c.Type == "" || c.Type == "n")
		}
		if !nonempty {
			continue
		}
		if !headerSeen {
			headerSeen = true
			used := map[string]bool{}
			for col, v := range values {
				key := headerKey(v)
				if key == "" {
					if v != "" {
						result.Warnings = append(result.Warnings, Issue{number, v, "忽略未识别列"})
					}
					continue
				}
				if used[key] {
					result.Errors = append(result.Errors, Issue{number, v, "字段标题重复"})
				}
				headers[col] = key
				used[key] = true
			}
			if !used["name"] {
				result.Errors = append(result.Errors, Issue{number, "name", "首个非空行必须含“任务名称”标题"})
			}
			continue
		}
		if len(result.Rows) >= MaxRows {
			return result, fmt.Errorf("一次最多导入%d条任务，请拆分文件", MaxRows)
		}
		m := map[string]any{"key": "", "parent_key": "", "name": "", "assignee": "", "plan_start": "", "plan_end": "", "estimate_hours": float64(0), "priority": float64(2), "progress": float64(0), "status": "todo", "description": ""}
		populated := false
		for col, key := range headers {
			v := values[col]
			if v != "" {
				populated = true
			}
			if formula[col] {
				result.Errors = append(result.Errors, Issue{number, key, "不导入公式或缓存结果，请粘贴为值后重试"})
				continue
			}
			switch key {
			case "plan_start", "plan_end":
				if v == "" {
					continue
				}
				d, e := parseDate(v, date1904)
				if e != nil {
					result.Errors = append(result.Errors, Issue{number, key, e.Error()})
				} else {
					m[key] = d
				}
			case "estimate_hours", "progress", "priority":
				if v == "" {
					continue
				}
				if key == "priority" {
					switch v {
					case "高":
						v = "1"
					case "中":
						v = "2"
					case "低":
						v = "3"
					}
				}
				percent := strings.HasSuffix(v, "%")
				n, e := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
				if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
					result.Errors = append(result.Errors, Issue{number, key, "必须为有限数字"})
					continue
				}
				if percent && key != "progress" {
					result.Errors = append(result.Errors, Issue{number, key, "此字段不能使用百分号"})
					continue
				}
				if key == "progress" && percentage[col] && !percent {
					n *= 100
					if math.Abs(n-math.Round(n)) < 0.0000001 {
						n = math.Round(n)
					}
				}
				m[key] = n
			case "status":
				if v == "" {
					continue
				}
				switch v {
				case "未开始", "待办":
					v = "todo"
				case "进行中":
					v = "doing"
				case "已完成":
					v = "done"
				case "已取消":
					v = "canceled"
				}
				m[key] = v
			default:
				m[key] = v
			}
		}
		if !populated {
			continue
		}
		if m["key"] == "" {
			m["key"] = fmt.Sprint(len(result.Rows) + 1)
		}
		hasParent := false
		for _, h := range headers {
			if h == "parent_key" {
				hasParent = true
			}
		}
		if !hasParent {
			key := m["key"].(string)
			if dot := strings.LastIndex(key, "."); dot > 0 {
				m["parent_key"] = key[:dot]
			}
		}
		m["row"] = number
		result.Rows = append(result.Rows, m)
	}
	if !headerSeen || len(result.Rows) == 0 {
		result.Errors = append(result.Errors, Issue{0, "rows", "未找到任务数据行"})
	}
	return result, nil
}
func headerKey(v string) string {
	v = strings.TrimSpace(strings.TrimPrefix(v, "\ufeff"))
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(v, "*"), "（必填）"))
	switch strings.ToLower(v) {
	case "任务编号", "wbs", "wbs编码", "编号", "key":
		return "key"
	case "上级编号", "父任务编号", "parent_key":
		return "parent_key"
	case "任务名称", "名称", "name":
		return "name"
	case "负责人", "assignee":
		return "assignee"
	case "计划开始", "计划开始日期", "plan_start":
		return "plan_start"
	case "计划结束", "计划截止日期", "plan_end":
		return "plan_end"
	case "预计工时", "预计工时（小时）", "estimate_hours":
		return "estimate_hours"
	case "优先级", "priority":
		return "priority"
	case "进度", "进度%", "完成进度", "progress":
		return "progress"
	case "状态", "status":
		return "status"
	case "说明", "任务说明", "description":
		return "description"
	}
	return ""
}
func column(ref string) (int, error) {
	n := 0
	i := 0
	for ; i < len(ref) && ref[i] >= 'A' && ref[i] <= 'Z'; i++ {
		n = n*26 + int(ref[i]-'A'+1)
	}
	if i == 0 || i == len(ref) {
		return 0, fmt.Errorf("bad reference")
	}
	if _, e := strconv.Atoi(ref[i:]); e != nil {
		return 0, e
	}
	return n, nil
}
func parseDate(v string, date1904 bool) (string, error) {
	if t, e := time.Parse(time.RFC3339Nano, v); e == nil {
		if t.Hour() != 0 || t.Minute() != 0 || t.Second() != 0 || t.Nanosecond() != 0 {
			return "", fmt.Errorf("计划日期不能包含非零时刻")
		}
		return t.Format("2006-01-02"), nil
	}
	for _, layout := range []string{"2006-01-02", "2006/1/2", "2006/01/02", "2006年1月2日"} {
		if t, e := time.Parse(layout, v); e == nil {
			return t.Format("2006-01-02"), nil
		}
	}
	n, e := strconv.ParseFloat(v, 64)
	if e != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n > 2958465 || n != math.Trunc(n) {
		return "", fmt.Errorf("日期须为YYYY-MM-DD或无时刻的Excel日期")
	}
	days := int(n)
	var base time.Time
	if date1904 {
		base = time.Date(1904, 1, 1, 0, 0, 0, 0, time.UTC)
	} else {
		if days == 60 {
			return "", fmt.Errorf("Excel序号60对应不存在的1900-02-29")
		}
		base = time.Date(1899, 12, 31, 0, 0, 0, 0, time.UTC)
		if days > 60 {
			days--
		}
	}
	d := base.AddDate(0, 0, days)
	if d.Year() > 9999 {
		return "", fmt.Errorf("日期超出范围")
	}
	return d.Format("2006-01-02"), nil
}
