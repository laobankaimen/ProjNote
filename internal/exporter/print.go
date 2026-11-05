package exporter

import (
	"bytes"
	"fmt"
	"html/template"
	"strings"
)

type printLine struct {
	Text    string
	Heading bool
}
type printRow struct{ Label, Value string }
type printReport struct {
	Title, Project, Date, Operator, Version string
	Lines                                   []printLine
	Rows                                    []printRow
}

var printTemplate = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title>
<style>
:root{font-family:"Noto Sans CJK SC","Microsoft YaHei","PingFang SC",sans-serif;color:#24334a;background:#f3f6fa}*{box-sizing:border-box}body{margin:0}.print-toolbar{position:sticky;top:0;display:flex;align-items:center;justify-content:space-between;gap:20px;padding:14px max(24px,calc((100vw - 840px)/2));background:#fff;border-bottom:1px solid #dfe5ee;font-size:13px}.print-toolbar p{margin:0;line-height:1.6}.print-toolbar button{border:0;border-radius:5px;background:#2465ce;color:#fff;padding:10px 18px;font:inherit;white-space:nowrap;cursor:pointer}.print-toolbar button:disabled{opacity:.55;cursor:default}.print-toolbar button:focus-visible{outline:3px solid #8bb7ff;outline-offset:3px}.report{max-width:840px;margin:28px auto;padding:45px 48px;background:#fff;border:1px solid #dfe5ee}h1{font-size:24px;line-height:1.5;margin:0 0 16px;overflow-wrap:anywhere}.report-meta{font-size:12px;line-height:1.9;color:#53647a;border-bottom:2px solid #a8bddb;padding-bottom:16px;margin-bottom:24px}.report-meta span{display:inline-block;margin-right:22px}.report-content h2{font-size:17px;line-height:1.7;margin:24px 0 10px;break-after:avoid}.report-content p{font-size:14px;line-height:1.9;margin:8px 0;white-space:pre-wrap;overflow-wrap:anywhere}.report-content .blank{min-height:7px;margin:0}table{border-collapse:collapse;width:100%;font-size:12px;margin-top:28px;table-layout:fixed}caption{text-align:left;font-weight:600;padding:8px 0}th,td{border:1px solid #dfe5ee;padding:9px 11px;text-align:left;vertical-align:top;white-space:pre-wrap;overflow-wrap:anywhere}th{background:#f3f6fa;font-weight:500}th:first-child{width:25%}thead{display:table-header-group}.report-footer{font-size:11px;color:#6c7c91;border-top:1px solid #dfe5ee;padding-top:16px;margin-top:30px}
@page{size:A4;margin:18mm 17mm}@media print{:root,body{background:#fff;color:#000}.print-toolbar{display:none}.report{max-width:none;margin:0;padding:0;border:0}h1{font-size:18pt}.report-content h2{font-size:13pt}.report-content p{font-size:10.5pt;orphans:3;widows:3}.report-meta,table{font-size:9pt}tr{break-inside:avoid}h1,h2,caption{break-after:avoid}.report-footer{font-size:8pt}}@media(max-width:700px){.report{margin:12px;padding:24px 20px}.print-toolbar{padding:12px;align-items:flex-start}.print-toolbar p{max-width:26ch}}
</style><script src="/api/export-print.js" defer></script></head><body>
<div class="print-toolbar"><p>打印稿已按保存的内容生成。点击打印或按 Ctrl+P，可选择打印机或“另存为 PDF”。</p><button id="print-report" type="button" disabled>打印 / 保存 PDF</button></div>
<main class="report"><h1>{{.Title}}</h1><div class="report-meta"><span>项目：{{.Project}}</span>{{if .Date}}<span>日期：{{.Date}}</span>{{end}}<span>操作人：{{.Operator}}</span><span>数据版本：{{.Version}}</span></div><article class="report-content">{{range .Lines}}{{if .Heading}}<h2>{{.Text}}</h2>{{else if .Text}}<p>{{.Text}}</p>{{else}}<p class="blank" aria-hidden="true"></p>{{end}}{{end}}</article>
{{if .Rows}}<table><caption>来源信息</caption><thead><tr><th scope="col">字段</th><th scope="col">内容</th></tr></thead><tbody>{{range .Rows}}<tr><td>{{.Label}}</td><td>{{.Value}}</td></tr>{{end}}</tbody></table>{{end}}<footer class="report-footer">ProjNote · 已保存内容的打印稿 · 来源版本 {{.Version}}</footer></main></body></html>`))

// RenderHTML returns a self-contained, escaped report. The only script is a
// fixed same-origin print button handler; source content is never HTML.
func RenderHTML(snapshot map[string]any) ([]byte, error) {
	str := func(v any) string {
		if v == nil {
			return ""
		}
		return fmt.Sprint(v)
	}
	report := printReport{Title: str(snapshot["title"]), Project: str(snapshot["project_name"]), Date: str(snapshot["log_date"]), Operator: str(snapshot["export_operator"]), Version: str(snapshot["source_version"])}
	for _, line := range strings.Split(strings.ReplaceAll(str(snapshot["content"]), "\r\n", "\n"), "\n") {
		heading := false
		for _, prefix := range []string{"### ", "## ", "# "} {
			if strings.HasPrefix(line, prefix) {
				line = strings.TrimPrefix(line, prefix)
				heading = true
				break
			}
		}
		report.Lines = append(report.Lines, printLine{Text: line, Heading: heading})
	}
	add := func(row map[string]any) {
		report.Rows = append(report.Rows, printRow{Label: str(row["label"]), Value: str(row["value"])})
	}
	switch rows := snapshot["rows"].(type) {
	case []any:
		for _, row := range rows {
			if value, ok := row.(map[string]any); ok {
				add(value)
			}
		}
	case []map[string]any:
		for _, row := range rows {
			add(row)
		}
	}
	var out bytes.Buffer
	if err := printTemplate.Execute(&out, report); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
