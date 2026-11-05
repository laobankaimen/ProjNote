#!/usr/bin/env python3
"""Exercise v0.3.3/schema6 in private Chinese directories using a real binary.

This emulates the desktop task/record HTTP requests without opening native
windows. It GETs successful HTML print artifacts, never clicks Print, never
creates a background PDF, and never selects the user's default data directory.
Existing QA fixtures are not overwritten; rerun with --output-dir /tmp/....
"""
import argparse
from datetime import date, datetime, timezone
from html.parser import HTMLParser
import io
import json
from pathlib import Path
import socket
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET
import zipfile

from smoke_v02 import BinaryServer
from smoke_v032 import completed_job, digest, export_job, long_issue_fields, upload_template


SHEET = "{http://schemas.openxmlformats.org/spreadsheetml/2006/main}"
FORMULA_TEXT = ['=HYPERLINK("https://example.invalid","合成公式文本")', "+SUM(1,2)", "-10+20", "@SUM(1,2)"]
HTML_TEXT = ['<script>alert("合成验收")</script>', '<img src=x onerror="alert(1)">', 'A&B <输入> "引用"']
HAZARDS = FORMULA_TEXT + HTML_TEXT
FILENAMES = [("smoke-个人月报", "worklogs"), ("smoke-项目日报", "project_daily_logs"), ("smoke-问题单", "issues")]


def query(path, **filters):
    return path + "?" + urllib.parse.urlencode(filters)


def raw_get(server, path, status=200):
    try:
        with urllib.request.urlopen(server.url + path, timeout=30) as response:
            body = response.read()
            assert response.status == status, (path, response.status, body)
            return body, response.headers
    except urllib.error.HTTPError as exc:
        body = exc.read()
        assert exc.code == status, (path, exc.code, body)
        return body, exc.headers


class PrintHTML(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.scripts = []
        self.inline_script = []
        self.data = []
        self.event_attributes = []
        self.tags = []
        self.inside_script = False

    def handle_starttag(self, tag, attrs):
        self.tags.append(tag)
        attributes = dict(attrs)
        self.event_attributes.extend(key for key in attributes if key.lower().startswith("on"))
        if tag == "script":
            self.inside_script = True
            self.scripts.append(attributes.get("src"))

    def handle_endtag(self, tag):
        if tag == "script":
            self.inside_script = False

    def handle_data(self, value):
        if self.inside_script:
            self.inline_script.append(value)
        self.data.append(value)


def verify_xlsx(data, snapshot):
    """Parse the real package and ensure dangerous-looking strings stay text."""
    with zipfile.ZipFile(io.BytesIO(data)) as workbook:
        assert workbook.testzip() is None
        assert len(workbook.namelist()) == len(set(workbook.namelist()))
        assert "xl/workbook.xml" in workbook.namelist()
        assert not any("vba" in name.lower() or "externallinks" in name.lower() for name in workbook.namelist())
        sheets = []
        for name in workbook.namelist():
            if name.endswith((".xml", ".rels")):
                part = ET.fromstring(workbook.read(name))
                if name.endswith(".rels"):
                    assert all(rel.attrib.get("TargetMode", "").lower() != "external" for rel in part)
                if name.startswith("xl/worksheets/") and name.endswith(".xml"):
                    sheets.append(part)
        assert len(sheets) == 3
        book = ET.fromstring(workbook.read("xl/workbook.xml"))
        assert [item.attrib["name"] for item in book.findall(SHEET + "sheets/" + SHEET + "sheet")] == ["工作报告", "来源明细", "来源快照"]
        inline = []
        numeric_cells = 0
        for sheet in sheets:
            assert not list(sheet.iter(SHEET + "f")), "source text became a formula"
            for cell in sheet.iter(SHEET + "c"):
                if cell.attrib.get("t") == "inlineStr":
                    inline.append("".join(item.text or "" for item in cell.iter(SHEET + "t")))
                else:
                    assert cell.attrib.get("t", "n") == "n", cell.attrib
                    assert cell.find(SHEET + "v") is not None
                    numeric_cells += 1
        text = "\n".join(inline)
        for marker in [snapshot["title"], snapshot["project_name"], snapshot["source_id"], "source_version", "现场联调", "验收材料"]:
            assert marker in text, (marker, text)
        assert numeric_cells > 0, "numeric versions/minutes should retain numeric cells"
        stripped = {item.rstrip("\r\n") for item in inline}
        for marker in HAZARDS:
            assert marker in stripped, ("inlineStr source missing", marker)
        if snapshot["source_kind"] == "worklogs":
            assert "month" in inline and snapshot["log_date"] in inline
        return {"worksheets": len(sheets), "inline_string_cells": len(inline), "numeric_cells": numeric_cells, "formula_cells": 0}


def verify_html(data, snapshot):
    source = data.decode("utf-8")
    html = PrintHTML()
    html.feed(source)
    html.close()
    text = "\n".join(html.data)
    assert html.scripts == ["/api/export-print.js"], html.scripts
    assert not "".join(html.inline_script).strip(), "unexpected inline executable script"
    assert not html.event_attributes, html.event_attributes
    assert "img" not in html.tags, "source markup became an image element"
    assert "&lt;script&gt;" in source and "&lt;img" in source and "A&amp;B" in source
    for marker in [snapshot["title"], snapshot["project_name"], "现场联调", "验收材料", "来源版本"] + HAZARDS:
        assert marker in text, (marker, text)
    assert "@media print" in source and "@page" in source
    return {"trusted_script": html.scripts[0], "source_event_attributes": 0, "escaped_source_markup": True}


def verify_print(server, job, downloaded):
    path = f'/api/export-jobs/{job["id"]}/print'
    if job["format"] != "html":
        body, _ = raw_get(server, path, 400)
        assert json.loads(body)["error"]["code"] == "not_print_report"
        return
    body, headers = raw_get(server, path)
    assert body == downloaded and digest(body) == job["output_sha256"]
    assert headers.get_content_type() == "text/html", dict(headers)
    assert headers.get("X-Content-Type-Options") == "nosniff"
    assert headers.get("Content-Disposition", "").startswith("inline"), dict(headers)
    csp = headers.get("Content-Security-Policy", "")
    scripts = next((part.strip() for part in csp.split(";") if part.strip().startswith("script-src ")), "")
    assert scripts and "'self'" in scripts and "'unsafe-inline'" not in scripts and "'unsafe-eval'" not in scripts, csp
    assert "object-src 'none'" in csp and "frame-ancestors 'none'" in csp and "base-uri 'none'" in csp
    js, js_headers = raw_get(server, "/api/export-print.js")
    javascript = js.decode()
    assert js_headers.get_content_type() in {"text/javascript", "application/javascript"}
    assert 'addEventListener("click"' in javascript and "window.print()" in javascript
    assert "window.onload" not in javascript and "DOMContentLoaded" not in javascript


def verified_export(server, job):
    data, headers = raw_get(server, f'/api/export-jobs/{job["id"]}/download')
    assert digest(data) == job["output_sha256"] and len(data) == job["size"], job
    expected_type = {"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "html": "text/html"}[job["format"]]
    assert headers.get_content_type() == expected_type, dict(headers)
    assert headers.get("Content-Disposition", "").startswith("attachment")
    snapshot = job["source_snapshot"]
    result = verify_xlsx(data, snapshot) if job["format"] == "xlsx" else verify_html(data, snapshot)
    verify_print(server, job, data)
    return data, result


def monthly_text():
    return """## 负责人手写月度总结

【合成验收资料】本月围绕产线设备现场联调、接口一致性核对与交付证据整理开展工作。样本使用虚构项目和人员，只用于检查月报、导出和备份恢复的完整性。本月按张工有效工时条目汇总为150分钟，不把已保存日志重复计入工时。

现场联调先核对设备配置、接线标识、通信参数与软件版本，再按相同输入复现边界场景。无法立即确认的现象保留原始观察，注明待验证判断和负责人，避免把猜测写成已经完成的结论。

验收材料包括配置记录、测试结果和问题复测说明。按日期和证据编号归档，保留尚未解决的限制；计划发生变化时说明影响和协调请求，工时数字只描述投入，不自动代表完成比例。

## 负责人手写后续计划

先核对交付清单，再安排停机窗口内的接口复测。这段计划必须保留，重新生成草稿不能覆盖人工保存的内容。

## 字面文本安全验收

""" + "\n".join(HAZARDS)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary")
    parser.add_argument("--expected-version",default="0.3.3",help="期望程序版本前缀，用于后续增量回归")
    parser.add_argument("--expected-schema", type=int, default=6, help="期望备份schema，用于后续增量回归")
    parser.add_argument("--output-dir", type=Path,
                        default=Path(__file__).resolve().parent.parent / "outputs" / "qa-v0.3.3")
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve(strict=True))
    output_dir = args.output_dir.resolve()
    reserved = [name + "." + fmt for name, _ in FILENAMES for fmt in ("xlsx", "html")] + ["smoke_v033-report.json"]
    if any((output_dir / name).exists() for name in reserved):
        parser.error("QA输出已存在；复验请指定 --output-dir /tmp/projnote-v033-qa-...，避免覆盖首次样例")
    report = {"binary": binary, "expected_schema": args.expected_schema, "started_at": datetime.now(timezone.utc).isoformat(), "scope": [], "not_exercised": [
        "Native desktop window controls/hotkeys: task and record saves are emulated over the same HTTP APIs",
        "Actual print dialog, physical printer and browser Save as PDF: only the successful frozen HTML print URL is read",
        "Excel application/print-layout visual review and schema5-to-schema6 real old-binary upgrade: separate QA",
        "Forced queued/running crash: no SQLite state injection; persistence and interruption are covered by Store tests",
    ]}
    with tempfile.TemporaryDirectory(prefix="projnote-v033-") as temp:
        temp_root = Path(temp)
        root = temp_root / "原始 中文 工作区"
        restored_root = temp_root / "完整备份 恢复中文工作区"
        root.mkdir()
        restored_root.mkdir()
        busy = socket.socket()
        busy.bind(("127.0.0.1", 0))
        busy.listen()
        port = busy.getsockname()[1]
        assert 1024 <= port <= 65436, port
        server = BinaryServer(binary, root, port)
        restored = None
        try:
            server.start()
            health = server.api("GET", "/api/health")
            assert health["version"].startswith(args.expected_version), health
            report["app_version"] = health["version"]
            settings = server.api("GET", "/api/settings")
            assert settings["desktop_default"] == "1"
            person = "验收张工"
            settings = server.api("PUT", "/api/settings", {"operator": person, "desktop_default": "0", "backup_auto": "0", "backup_keep_count": "5", "backup_max_mb": "0"})
            dataset_id = settings["dataset_id"]
            assert settings["desktop_default"] == "0"
            server.api("PUT", "/api/settings", {"desktop_default": "invalid"}, 400)
            p = server.api("POST", "/api/projects", {"name": "【合成验收】单机月报与现场联调项目"}, 201)
            pid = p["id"]
            draft = server.api("GET", "/api/desktop/draft")
            assert draft["mode"] == "task" and draft["version"] == 1 and draft["content"] == ""
            draft = server.api("PUT", "/api/desktop/draft", {"version": draft["version"], "content": "待办正文尚未入库", "project_id": pid, "mode": "task"})
            assert draft["mode"] == "task" and draft["version"] == 2
            draft = server.api("PUT", "/api/desktop/draft", {"version": draft["version"], "content": "速记草稿仍未入库", "project_id": pid, "mode": "record"})
            assert draft["mode"] == "record" and draft["version"] == 3
            server.api("PUT", "/api/desktop/draft", {"version": 2, "content": "过期窗口不得清空", "project_id": "", "mode": "task"}, 409)
            server.api("PUT", "/api/desktop/draft", {"version": draft["version"], "content": "无效模式", "project_id": pid, "mode": "other"}, 400)
            draft = server.api("PUT", "/api/desktop/draft", {"version": draft["version"], "content": "保留记录模式的未提交草稿", "project_id": pid})
            assert draft["mode"] == "record" and draft["version"] == 4
            assert server.api("GET", "/api/records")["total"] == 0
            assert server.api("GET", "/api/tasks")["total"] == 0
            server.stop()
            server.start()
            assert server.api("GET", "/api/settings")["desktop_default"] == "0"
            assert server.api("GET", "/api/desktop/draft") == draft
            assert server.api("GET", "/api/records")["total"] == 0
            assert server.api("GET", "/api/tasks")["total"] == 0
            report["scope"].append("desktop_default 1->0 persists; task/record draft modes preserve omitted mode, reject stale/invalid saves and survive restart without creating business entities")

            today = date.today().isoformat()
            task_text = "现场联调与验收材料核对\n保留完整便签正文，不能把待办误存为现场记录。"
            task = server.api("POST", "/api/tasks", {"project_id": pid, "name": task_text.splitlines()[0], "description": task_text,
                              "assignee": person, "plan_start": today, "plan_end": today}, 201)
            assert task["status"] == "todo" and task["assignee"] == person and task["description"] == task_text
            view = server.api("GET", "/api/today")
            assert any(item["id"] == task["id"] and item["has_children"] is False for item in view["tasks"])
            assert server.api("GET", "/api/records")["total"] == 0 and view["stats"]["records_today"] == 0
            record = server.api("POST", "/api/records", {"project_id": pid, "type": "note", "title": "独立现场速记", "content": "现场联调观察记录，不应创建待办任务。"}, 201)
            assert server.api("GET", "/api/tasks")["total"] == 1
            assert server.api("GET", "/api/records")["total"] == 1
            assert any(item["id"] == record["id"] for item in server.api("GET", "/api/today")["records"])
            report["scope"].append("desktop-equivalent POST tasks with current operator/local-today dates appears immediately in Today; explicit record save stays separate from tasks")

            for title, value in [("月首纳入", datetime(2024, 2, 1)), ("闰月末纳入", datetime(2024, 2, 29, 23, 59, 59)),
                                 ("上月末排除", datetime(2024, 1, 31, 23, 59, 59)), ("下月首排除", datetime(2024, 3, 1))]:
                server.api("POST", "/api/records", {"project_id": pid, "title": title, "occurred_at": value.astimezone().isoformat()}, 201)
            for work_day, name, minutes in [("2024-02-01", person, 60), ("2024-02-29", person, 90),
                                             ("2024-01-31", person, 70), ("2024-03-01", person, 80), ("2024-02-15", "验收李工", 180)]:
                server.api("POST", "/api/time-entries", {"project_id": pid, "work_date": work_day, "assignee": name, "minutes": minutes}, 201)
            month_input = {"project_id": pid, "period_type": "month", "date": "2024-02-29", "assignee": person}
            generated = server.api("POST", "/api/worklogs/generate", month_input)
            assert set(generated) == {"content", "log_date", "period_type", "project_id"}
            assert generated["period_type"] == "month" and generated["log_date"] == "2024-02-01"
            assert "工作月报" in generated["content"] and "合计 150 分钟" in generated["content"]
            assert all(marker in generated["content"] for marker in ("月首纳入", "闰月末纳入"))
            assert all(marker not in generated["content"] for marker in ("上月末排除", "下月首排除"))
            generated["content"] += "\n\n" + monthly_text()
            month = server.api("POST", "/api/worklogs", generated, 201)
            server.api("POST", "/api/worklogs", {"project_id": pid, "period_type": "month", "log_date": "2024-02-15", "content": "同月不得重复保存"}, 409)
            again = server.api("POST", "/api/worklogs/generate", {**month_input, "date": "2024-02-15"})
            assert again["log_date"] == "2024-02-01"
            assert server.get("worklogs", month["id"]) == month, "generation overwrote handwritten saved month"
            server.api("POST", "/api/worklogs/generate", {**month_input, "date": "2023-02-29"}, 400)
            year_end = server.api("POST", "/api/worklogs/generate", {**month_input, "date": "2026-12-31"})
            assert year_end["log_date"] == "2026-12-01" and len(year_end) == 4
            for period in ("day", "week"):
                assert len(server.api("POST", "/api/worklogs/generate", {**month_input, "period_type": period})) == 4
            report["scope"].append("four-field monthly draft anchors month start, handles leap-day/local boundary and year-end dates; same-month save409, person-only time total and generation never overwrites handwritten saved text")

            other_task = server.api("POST", "/api/tasks", {"project_id": pid, "name": "接口复测与验收材料整理", "assignee": "验收李工"}, 201)
            for name, tid, minutes in [(person, task["id"], 210), ("验收李工", other_task["id"], 90)]:
                server.api("POST", "/api/team-daily-entries", {"project_id": pid, "log_date": today, "person_name": name,
                           "content": "现场联调完成本轮合成范围，验收材料保留来源。", "next_plan": "负责人手写后续计划", "task_ids": [tid]}, 201)
                server.api("POST", "/api/time-entries", {"project_id": pid, "work_date": today, "assignee": name, "minutes": minutes}, 201)
            project_log = server.api("POST", "/api/project-daily-logs/generate", {"project_id": pid, "log_date": today})
            assert project_log["source_snapshot"]["total_minutes"] == 300
            project_log["content"] += "\n\n## 字面文本安全验收\n\n" + "\n".join(HAZARDS)
            project_log = server.api("POST", "/api/project-daily-logs", project_log, 201)
            fields = long_issue_fields()
            fields["description"] += "\n\n" + "\n".join(HAZARDS)
            issue = server.api("POST", "/api/issues", {"project_id": pid, "title": "【合成验收】重连后的现场状态一致性", "severity": "major", "owner": person, "description": fields["description"]}, 201)
            for patch in [{"status": "analyzing"}, {"status": "fixing", "root_cause": fields["root_cause"], "solution": fields["solution"]},
                          {"status": "verifying", "verify_result": fields["verify_result"]}, {"status": "closed"}]:
                issue = server.api("PATCH", f'/api/issues/{issue["id"]}', {"version": issue["version"], **patch})
            assert issue["status"] == "closed"
            builtin = server.download("/api/export-templates/builtin-journal/download")
            custom = upload_template(server, builtin, "v0.3.3中文自定义月报模板")
            assert custom["sha256"] == digest(builtin)

            source_specs = [("worklogs", month), ("project_daily_logs", project_log), ("issues", issue)]
            exported = {}
            for kind, source in source_specs:
                for fmt in ("xlsx", "html"):
                    job = completed_job(server, export_job(server, kind, source, fmt))
                    assert job["template_id"] == {"xlsx": "builtin-excel", "html": "builtin-print-html"}[fmt]
                    assert len(job["template_hash"]) == 64
                    assert job["source_snapshot"]["fields"]["version"] == source["version"]
                    data, detail = verified_export(server, job)
                    exported[(kind, fmt)] = job
            assert len(exported) == 6
            server.api("POST", "/api/export-jobs", {"source_kind": "worklogs", "source_id": month["id"], "source_version": month["version"], "format": "pdf"}, 400)
            assert server.api("GET", "/api/export-jobs")["total"] == 6
            assert not list((root / "exports").glob("*.pdf")), "backend unexpectedly generated PDF"
            report["scope"].append("6 asynchronous XLSX/HTML jobs across saved personal month/project daily/closed issue: hashes/sizes/versions, three-sheet XML packages, numeric cells, formula-like literals inlineStr, source HTML escaping and successful HTML-only print routes with CSP")

            server.stop()
            server.start()
            for old in exported.values():
                job = server.get("export-jobs", old["id"])
                assert job == old and job["state"] == "succeeded", "completed job changed across restart"
                verified_export(server, job)
            report["scope"].append("all six completed persistent jobs and frozen snapshots survive a clean service restart with intact artifacts")
            backup = server.api("POST", "/api/backups", {}, 201)
            archive = temp_root / f"完整 schema{args.expected_schema} 中文备份.zip"
            archive.write_bytes(server.download(f'/api/backups/{backup["name"]}/download'))
            with zipfile.ZipFile(archive) as saved:
                assert int.from_bytes(saved.read("app.db")[60:64], "big") == args.expected_schema, "backup schema differs from expected schema"
                assert saved.read(f'templates/docx/{custom["id"]}.docx') == builtin
                assert not any(name.startswith("exports/") for name in saved.namelist())
            edited = server.api("PATCH", f'/api/worklogs/{month["id"]}', {"version": month["version"], "content": month["content"] + "\n备份后修改，不应改写旧来源快照。"})
            for fmt in ("xlsx", "html"):
                unchanged = server.get("export-jobs", exported[("worklogs", fmt)]["id"])
                assert unchanged["source_snapshot"]["content"] == month["content"] and unchanged["source_version"] == month["version"]
                assert edited["version"] > unchanged["source_version"]
                server.api("POST", "/api/export-jobs", {"source_kind": "worklogs", "source_id": month["id"], "source_version": month["version"], "format": fmt}, 409)
            server.api("PUT", "/api/desktop/draft", {"version": draft["version"], "mode": "task", "content": "备份后的任务模式，恢复后应回到record", "project_id": pid})
            server.stop()
            assert "校验通过" in server.cli("--verify-backup", archive).stdout
            restored = BinaryServer(binary, restored_root, port)
            assert "恢复完成" in restored.cli("--restore", archive).stdout
            restored.start()
            settings = restored.api("GET", "/api/settings")
            assert settings["dataset_id"] == dataset_id and settings["desktop_default"] == "0"
            assert restored.api("GET", "/api/desktop/draft") == draft
            assert restored.get("worklogs", month["id"]) == month
            assert restored.get("project-daily-logs", project_log["id"])["source_snapshot"] == project_log["source_snapshot"]
            assert restored.download(f'/api/export-templates/{custom["id"]}/download') == builtin
            assert custom["id"] in {entry["id"] for entry in restored.api("GET", "/api/export-templates")["items"]}
            assert restored.api("GET", "/api/export-jobs")["total"] == 6
            for old in exported.values():
                failed = restored.get("export-jobs", old["id"])
                assert failed["state"] == "failed" and failed["error"] and failed["output_name"] == "", failed
                assert failed["source_snapshot"] == old["source_snapshot"] and failed["source_version"] == old["source_version"]
                restored.api("GET", f'/api/export-jobs/{old["id"]}/download', status=409)
                raw_get(restored, f'/api/export-jobs/{old["id"]}/print', 409 if old["format"] == "html" else 400)
            assert restored.api("GET", "/api/audit/verify")["ok"] is True
            report["scope"].append(f"schema{args.expected_schema} complete backup verified/restored through stopped-service CLI in a fresh Chinese directory: settings/draft mode/month/custom template/frozen sources preserved; absent generated outputs become auditable failed jobs")

            regenerated = {}
            for kind, source in source_specs:
                for fmt in ("xlsx", "html"):
                    job = completed_job(restored, export_job(restored, kind, source, fmt))
                    data, detail = verified_export(restored, job)
                    assert job["id"] != exported[(kind, fmt)]["id"]
                    regenerated[(kind, fmt)] = (job, data, detail)
            assert not list((restored_root / "exports").glob("*.pdf"))
            assert restored.api("GET", "/api/audit/verify")["ok"] is True
            restored.stop()
            report["scope"].append("all six source/format combinations re-export successfully after restore; no PDF artifact is generated by the server")

            output_dir.mkdir(parents=True, exist_ok=True)
            report["fixtures"] = []
            for prefix, kind in FILENAMES:
                for fmt in ("xlsx", "html"):
                    job, data, detail = regenerated[(kind, fmt)]
                    path = output_dir / (prefix + "." + fmt)
                    path.write_bytes(data)
                    report["fixtures"].append({"path": str(path), "sha256": digest(data), "size": len(data), "format": fmt,
                        "source_kind": kind, "source_id": job["source_id"], "source_version": job["source_version"],
                        "template_id": job["template_id"], "template_hash": job["template_hash"], "job_id": job["id"], "inspection": detail})
            report["matrix_jobs"] = len(exported)
            report["regenerated_jobs"] = len(regenerated)
            report["completed_at"] = datetime.now(timezone.utc).isoformat()
            report["status"] = "passed"
            report_path = output_dir / "smoke_v033-report.json"
            report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
            print(f"PASS {health['version']}: monthly drafts/uniqueness/leap bounds; desktop modes/settings/tasks; 6 persistent XLSX/HTML exports; literal-source safety and HTML print headers; schema{args.expected_schema} full backup restore and re-export")
            for item in report["fixtures"]:
                print(f'  {item["path"]} ({item["size"]} bytes, sha256={item["sha256"]})')
            print("Report:", report_path)
            print("Scope excludes actual native windows, print-dialog interaction, PDF generation and visual Excel/HTML layout review.")
        finally:
            if restored is not None:
                restored.cleanup()
            server.cleanup()
            busy.close()


if __name__ == "__main__":
    main()
