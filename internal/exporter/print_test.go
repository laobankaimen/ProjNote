package exporter

import (
	"strings"
	"testing"
)

func TestPrintReportEscapesSourceAndKeepsSavedContent(t *testing.T) {
	out, e := RenderHTML(map[string]any{"title": "项目 <检查>", "project_name": "甲 & 乙", "export_operator": "张工", "source_version": 3, "log_date": "2026-11-05", "content": "## 明日计划\n<script>alert('输入')</script>\n=SUM(A1:A2)\n{{未解释占位符}}", "rows": []any{map[string]any{"label": "闭环字段", "value": "<img src=x onerror=alert(1)>"}}})
	if e != nil {
		t.Fatal(e)
	}
	html := string(out)
	for _, want := range []string{"项目 &lt;检查&gt;", "甲 &amp; 乙", "&lt;script&gt;", "&lt;img src=x onerror=alert(1)&gt;", "=SUM(A1:A2)", "{{未解释占位符}}", "<h2>明日计划</h2>", "数据版本：3", "@media print", "/api/export-print.js"} {
		if !strings.Contains(html, want) {
			t.Fatalf("missing %q", want)
		}
	}
	for _, bad := range []string{"<script>alert", "<img src=x", "onclick=", "window.print()"} {
		if strings.Contains(html, bad) {
			t.Fatalf("unsafe source or inline handler: %s", bad)
		}
	}
}
