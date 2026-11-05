#!/usr/bin/env python3
"""Exercise schema5 notes, next-plan sources and real exported files.

A real supplied binary runs only in private temporary Chinese data directories.
The user's default data directory is never opened. Generated DOCX QA fixtures
and a machine-readable scope report are written under --output-dir.

Queued/running interruption is covered by Store restart tests; this script does
not tamper with SQLite or pretend a forced queued-state restart was exercised.
"""
import argparse
import copy
from datetime import date, datetime, time as day_time, timedelta, timezone
import hashlib
import io
import json
from pathlib import Path
import socket
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import xml.etree.ElementTree as ET
import zipfile

from smoke_v02 import BinaryServer


WORD = "{http://schemas.openxmlformats.org/wordprocessingml/2006/main}"


def query(path, **filters):
    return path + "?" + urllib.parse.urlencode(filters)


def digest(data):
    return hashlib.sha256(data).hexdigest()


def upload_template(server, content, name):
    boundary = "ProjNoteV032WordTemplateBoundary"
    body = (
        f'--{boundary}\r\nContent-Disposition: form-data; name="name"\r\n\r\n{name}\r\n'
        f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="qa-template.docx"\r\n'
        "Content-Type: application/vnd.openxmlformats-officedocument.wordprocessingml.document\r\n\r\n"
    ).encode() + content + f"\r\n--{boundary}--\r\n".encode()
    request = urllib.request.Request(
        server.url + "/api/export-templates", body,
        headers={"Content-Type": "multipart/form-data; boundary=" + boundary,
                 "X-ProjNote-Request": "1"},
    )
    with urllib.request.urlopen(request, timeout=30) as response:
        data = response.read()
        assert response.status == 201, (response.status, data)
        return json.loads(data)["data"]


def read_docx(content):
    """Check ZIP integrity and parse every XML part; return visible body text."""
    with zipfile.ZipFile(io.BytesIO(content)) as document:
        assert document.testzip() is None
        assert len(document.namelist()) == len(set(document.namelist()))
        assert "word/document.xml" in document.namelist()
        for filename in document.namelist():
            if filename.endswith((".xml", ".rels")):
                ET.fromstring(document.read(filename))
        body = ET.fromstring(document.read("word/document.xml"))
        return "".join(item.text or "" for item in body.iter(WORD + "t"))


def export_job(server, kind, source, format_name, template=None):
    payload = {
        "source_kind": kind, "source_id": source["id"],
        "source_version": source["version"], "format": format_name,
    }
    if template is not None and format_name == "docx":
        payload.update(template_id=template["id"], template_hash=template["sha256"])
    created = server.api("POST", "/api/export-jobs", payload, 201)
    assert created["source_version"] == source["version"]
    assert created["source_snapshot"]["source_version"] == source["version"]
    return created


def completed_job(server, job):
    deadline = time.monotonic() + 45
    while time.monotonic() < deadline:
        result = server.get("export-jobs", job["id"])
        assert result["state"] in {"queued", "running", "succeeded", "failed"}, result
        if result["state"] == "succeeded":
            assert result["output_name"] and result["output_sha256"] and result["size"] > 0, result
            assert result["source_id"] == job["source_id"]
            assert result["source_version"] == job["source_version"]
            return result
        assert result["state"] != "failed", result
        time.sleep(.04)
    raise AssertionError(("export timed out", job["id"]))


def verified_download(server, job):
    data = server.download(f'/api/export-jobs/{job["id"]}/download')
    assert digest(data) == job["output_sha256"], job
    assert len(data) == job["size"], job
    snapshot = job["source_snapshot"]
    if job["format"] == "json":
        assert json.loads(data) == snapshot
    elif job["format"] == "md":
        assert data.decode() == snapshot["content"]
    else:
        text = read_docx(data)
        expected_title = (snapshot["fields"]["title"] if job["source_kind"] == "issues"
                          else snapshot["title"])
        assert expected_title in text, (expected_title, text)
        assert snapshot["project_name"] in text
        if job["source_kind"] == "issues":
            assert snapshot["fields"]["code"] in text
            for key in ("description", "root_cause", "solution", "verify_result"):
                # Word separates paragraphs/runs with XML, not literal newlines.
                for line in snapshot["fields"][key].splitlines():
                    if line.strip():
                        assert line in text, (key, line, text)
        else:
            # The DOCX renderer turns source Markdown into Word paragraphs.
            for marker in ("现场联调", "验收材料"):
                assert marker in text, (marker, text)
    return data


def weekly_text():
    paragraphs = [
        "【合成验收资料】本周围绕产线设备联调、接口一致性核对与交付证据整理开展工作。"
        "本样本用于检查中文长段落、分页、标点和模板替换，不代表真实客户项目或已完成的正式验收。",
        "现场联调首先核对设备型号、接线标识及网络地址，在复用已有参数前再次确认版本。"
        "针对现场反馈的偶发现象，我们保留操作顺序、观察时间和截图编号，并区分当前事实、待验证判断与后续处理建议。"
        "遇到无法当场确认的信息时，明确登记负责人和截止日期，避免把推测写成结论。",
        "接口核对覆盖请求字段、边界值、失败重试和恢复后的状态一致性。"
        "对于涉及多方的数据，我们记录样例输入、预期输出和实际结果；发生差异时保留原始证据，"
        "补充问题单并安排复测，不通过直接修改测试结论来掩盖偏差。",
        "验收材料按项目、日期和证据类别整理。会议纪要、配置记录与测试结果分别保持来源，"
        "在汇总周报时给出可追溯编号，并注明尚未解决的限制。"
        "这份长段落特意包含中文括号（检查内容）、顿号、分号；以及 XML 字符 A&B、<输入>，验证导出后文字完整。",
        "进度更新只描述本次确认的范围。已经完成的工作保留依据，存在阻碍的工作说明影响与协调请求。"
        "与计划相比发生变化时，记录变更原因及影响，不把工时数字简单等同于完成比例。",
        "明日工作将继续核对边界场景、复测修正项并完善交付清单。"
        "负责人确认资源后，逐项补齐验收材料；对于仍依赖外部条件的事项，提前列出替代安排与可接受的等待窗口。",
    ]
    return (
        "# 本周工作总结\n\n" + "\n\n".join(paragraphs) +
        "\n\n## 已完成工作\n\n- 现场联调与接口检查完成本轮合成测试范围。"
        "\n- 已整理验收材料目录，关联问题与复测记录。"
        "\n\n## 风险与待协调事项\n\n- 外部设备停机窗口仍需确认。"
        "\n- 关键接口边界测试将继续补充证据。"
        "\n\n## 明日计划\n\n- 保留负责人手写计划：先核对交付清单，再安排复测。"
        "\n- 确认张工与李工各自负责的后续工作。"
    )


def long_issue_fields():
    return {
        "description": (
            "【合成验收问题】在设备重连后执行连续状态查询，界面曾显示上一次连接的缓存状态，"
            "与本轮采集结果不一致。复现步骤为：建立连接、读取状态、断开连接、调整设备状态后重连，"
            "再连续发起查询。此处只描述可观察现象，不预设最终原因。\n\n"
            "现场记录保留请求时间、设备响应、界面截图与软件版本。为排除操作差异，"
            "使用相同输入重复执行三轮，并记录每轮前置条件。不同轮次出现差异时，"
            "先对照原始日志，再判断是否属于同一问题，避免把不相关现象混为一个结论。"
        ),
        "root_cause": (
            "分析发现连接恢复路径与常规轮询路径对缓存刷新时机的约定不一致。"
            "恢复成功事件先于首个有效采样结果到达，界面在该窗口内沿用了旧值。"
            "这是一条合成根因，用于验证导出字段完整性；真实项目须以日志与复现证据支持根因。"
        ),
        "solution": (
            "统一连接代次与采样代次的检查规则，恢复连接时将旧状态标记为待刷新；"
            "只有收到本代次的有效结果后才更新展示。同时补充失败路径提示，"
            "保留必要的诊断信息，确保无法取得新状态时不会误报为已确认结果。\n\n"
            "方案检查还包括重复重连、短时抖动、超时与取消操作。修改不以增加重试次数替代状态判断，"
            "并为后续维护说明输入边界与预期行为。"
        ),
        "verify_result": (
            "按原复现步骤执行复测，并追加连续断连、恢复期间取消和无有效响应场景。"
            "本合成样本中，界面在首个有效采样前保持待刷新状态，随后与本代次响应一致；"
            "未再显示跨代次缓存。核对现场联调记录、验收材料和变更说明后，填写验证结果。"
        ),
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary")
    parser.add_argument("--output-dir", type=Path,
                        default=Path(__file__).resolve().parent.parent / "outputs" / "qa-v0.3.2")
    parser.add_argument("--expected-version", default="0.3.2", help="期望程序版本前缀（脚本兼容后续增量）")
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve(strict=True))
    output_dir = args.output_dir.resolve()
    report = {"binary": binary, "started_at": datetime.now(timezone.utc).isoformat(),
              "scope": [], "not_exercised": [
                  "Forced queued/running process interruption: covered by persistent Store tests; no SQLite state injection here",
                  "Native desktop window controls and OS hotkeys", "Word/LibreOffice visual layout: DOCX fixtures require separate rendering",
              ]}
    with tempfile.TemporaryDirectory(prefix="projnote-v032-") as temp:
        temp_root = Path(temp)
        root = temp_root / "原始 中文 工作区"
        root.mkdir()
        restored_root = temp_root / "完整备份 恢复中文工作区"
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
            person = "张工"
            settings = server.api("PUT", "/api/settings", {
                "operator": person, "backup_auto": "0", "backup_keep_count": "5", "backup_max_mb": "0",
            })
            dataset_id = settings["dataset_id"]
            project = server.api("POST", "/api/projects", {"name": "【合成验收】产线联调与交付项目"}, 201)
            pid = project["id"]
            draft = server.api("GET", "/api/desktop/draft")
            assert draft["version"] == 1 and draft["content"] == "", draft
            draft_marker = "便签草稿尚未入库，保留负责人临时想法。"
            draft = server.api("PUT", "/api/desktop/draft", {
                "version": draft["version"], "content": draft_marker, "project_id": pid,
            })
            server.api("PUT", "/api/desktop/draft", {"version": 1, "content": "旧页不应覆盖", "project_id": pid}, 409)
            assert server.api("GET", "/api/desktop/draft")["content"] == draft_marker
            assert server.api("GET", "/api/records")["total"] == 0
            assert server.api("GET", "/api/today")["stats"]["records_today"] == 0
            assert server.api("GET", query("/api/search", q=draft_marker))["total"] == 0
            server.stop()
            server.start()
            assert server.api("GET", "/api/desktop/draft") == draft
            assert server.api("GET", "/api/records")["total"] == 0
            report["scope"].append("desktop draft save/409/restart persistence without business records/search/statistics")

            today = date.today()
            tomorrow = today + timedelta(days=1)
            parent = server.api("POST", "/api/tasks", {"project_id": pid, "name": "汇总父项不应选取", "assignee": person}, 201)
            task = server.api("POST", "/api/tasks", {
                "project_id": pid, "parent_id": parent["id"], "name": "明日现场联调与验收材料核对",
                "assignee": person, "plan_start": tomorrow.isoformat(), "plan_end": tomorrow.isoformat(),
            }, 201)
            overdue = server.api("POST", "/api/tasks", {
                "project_id": pid, "name": "逾期接口复测", "assignee": person,
                "plan_start": (today - timedelta(days=3)).isoformat(), "plan_end": (today - timedelta(days=1)).isoformat(),
            }, 201)
            other = server.api("POST", "/api/tasks", {"project_id": pid, "name": "李工工作不可归张工", "assignee": "李工"}, 201)
            future = server.api("POST", "/api/tasks", {"project_id": pid, "name": "后续排程不在明日范围", "assignee": person,
                          "plan_start": (tomorrow + timedelta(days=2)).isoformat()}, 201)
            record = server.api("POST", "/api/records", {"project_id": pid, "title": "当天现场记录100%", "content": "待负责人手动选择，不能自动写入明日计划。"}, 201)
            yesterday = datetime.combine(today - timedelta(days=1), day_time(12)).astimezone()
            old_record = server.api("POST", "/api/records", {"project_id": pid, "title": "前日记录不进入当天候选",
                                   "occurred_at": yesterday.isoformat()}, 201)
            before_audit = server.api("GET", "/api/audit/verify")["total"]
            candidates = server.api("POST", "/api/logs/next-plan-candidates", {
                "date": today.isoformat(), "project_id": pid, "assignee": person, "limit": 1,
            })
            assert candidates["total"] == 3 and candidates["has_more"] is True, candidates
            assert candidates["target_date"] == tomorrow.isoformat() and candidates["assignee"] == person
            selected = []
            for offset in range(candidates["total"]):
                page = server.api("POST", "/api/logs/next-plan-candidates", {
                    "date": today.isoformat(), "project_id": pid, "assignee": person, "limit": 1, "offset": offset,
                })
                selected.extend(page["candidates"])
            keys = {(item["kind"], item["id"]) for item in selected}
            assert keys == {("tasks", task["id"]), ("tasks", overdue["id"]), ("records", record["id"])}, selected
            for item in selected:
                assert item["key"] == item["kind"] + ":" + item["id"] and item["version"] > 0 and item["reason"]
            matched = server.api("POST", "/api/logs/next-plan-candidates", {"date": today.isoformat(), "project_id": pid,
                                 "assignee": person, "q": "%"})
            assert matched["total"] == 1 and matched["candidates"][0]["id"] == record["id"]
            assert server.api("GET", "/api/worklogs")["total"] == 0
            assert server.api("GET", "/api/team-daily-entries")["total"] == 0
            assert server.api("GET", "/api/audit/verify")["total"] == before_audit
            assert server.get("tasks", task["id"])["version"] == task["version"]
            assert all(item["id"] not in {parent["id"], other["id"], future["id"], old_record["id"]} for item in selected)
            report["scope"].append("versioned next-plan task/record candidates with person/date/project/search/pagination and no automatic writes")

            templates = server.api("GET", "/api/export-templates")
            builtin_journal = next(item for item in templates["items"] if item["id"] == "builtin-journal")
            builtin_issue = next(item for item in templates["items"] if item["id"] == "builtin-issue")
            builtin_data = server.download("/api/export-templates/builtin-journal/download")
            assert digest(builtin_data) == builtin_journal["sha256"]
            custom = upload_template(server, builtin_data, "中文验收周报模板")
            assert custom["id"] not in {"builtin-journal", "builtin-issue"} and len(custom["id"]) == 32
            assert custom["sha256"] == digest(builtin_data)
            assert server.download(f'/api/export-templates/{custom["id"]}/download') == builtin_data
            assert custom["id"] in {item["id"] for item in server.api("GET", "/api/export-templates")["items"]}
            report["scope"].append("builtin template download and multipart custom DOCX upload with immutable ID and SHA256")

            weekly = server.api("POST", "/api/worklogs/generate", {
                "date": today.isoformat(), "period_type": "week", "project_id": pid, "assignee": person,
            })
            assert set(weekly) == {"content", "log_date", "period_type", "project_id"}
            weekly["content"] = weekly_text()
            weekly = server.api("POST", "/api/worklogs", weekly, 201)
            team = server.api("POST", "/api/team-daily-entries", {
                "project_id": pid, "log_date": today.isoformat(), "person_name": person,
                "content": "现场联调完成本轮范围，验收材料已建立证据索引。", "next_plan": "负责人手写原计划。\n- 明日现场联调与验收材料核对",
                "blockers": "外部停机窗口待确认。", "task_ids": [task["id"]],
            }, 201)
            server.api("POST", "/api/team-daily-entries", {
                "project_id": pid, "log_date": today.isoformat(), "person_name": "李工",
                "content": "核对接口返回与设备地址，补齐验收材料。", "next_plan": "继续复核设备边界。", "task_ids": [other["id"]],
            }, 201)
            for name, minutes in [(person, 210), ("李工", 90)]:
                server.api("POST", "/api/time-entries", {"project_id": pid, "work_date": today.isoformat(), "minutes": minutes, "assignee": name}, 201)
            project_log = server.api("POST", "/api/project-daily-logs/generate", {"project_id": pid, "log_date": today.isoformat()})
            assert project_log["source_snapshot"]["total_minutes"] == 300
            assert any(item["id"] == team["id"] and item["version"] == team["version"] for item in project_log["source_snapshot"]["team_entries"])
            project_log["content"] += "\n\n## 负责人说明\n\n" + weekly_text().split("\n\n")[1]
            project_log = server.api("POST", "/api/project-daily-logs", project_log, 201)
            issue_fields = long_issue_fields()
            issue = server.api("POST", "/api/issues", {
                "project_id": pid, "title": "【合成验收】重连后状态显示一致性问题", "severity": "major", "owner": person,
                "description": issue_fields["description"],
            }, 201)
            for fields in [{"status": "analyzing"}, {"status": "fixing", "root_cause": issue_fields["root_cause"], "solution": issue_fields["solution"]},
                           {"status": "verifying", "verify_result": issue_fields["verify_result"]}, {"status": "closed"}]:
                issue = server.api("PATCH", f'/api/issues/{issue["id"]}', {"version": issue["version"], **fields})
            assert issue["status"] == "closed" and issue["closed_at"]

            source_specs = [("worklogs", weekly, custom), ("project_daily_logs", project_log, builtin_journal), ("issues", issue, builtin_issue)]
            exported = {}
            result_data = {}
            for kind, source, template in source_specs:
                for format_name in ["docx", "md", "json"]:
                    job = completed_job(server, export_job(server, kind, source, format_name, template))
                    data = verified_download(server, job)
                    exported[(kind, format_name)] = job
                    result_data[(kind, format_name)] = data
            assert len(exported) == 9
            server.api("POST", "/api/export-jobs", {"format": "md", "source_kind": "worklogs", "source_id": weekly["id"], "source_version": 0}, 409)
            server.api("POST", "/api/export-jobs", {"format": "md", "source_kind": "worklogs", "source_id": weekly["id"],
                       "source_version": weekly["version"], "source_snapshot": {"content": "不得接受客户端伪造"}}, 400)
            report["scope"].append("9 real asynchronous DOCX/MD/JSON exports across saved personal weekly log, project daily log and closed issue; download hashes/sizes/frozen source versions")

            tampered = exported[("project_daily_logs", "md")]
            original_snapshot = copy.deepcopy(tampered["source_snapshot"])
            artifact = root / "exports" / tampered["output_name"]
            artifact.write_bytes(b"tampered result bytes")
            error = server.api("GET", f'/api/export-jobs/{tampered["id"]}/download', status=410)
            assert error["error"]["code"] == "artifact_unavailable", error
            failed = server.get("export-jobs", tampered["id"])
            assert failed["state"] == "failed" and failed["error"] and failed["output_name"] == ""
            assert failed["source_snapshot"] == original_snapshot
            server.api("GET", f'/api/export-jobs/{tampered["id"]}/download', status=409)
            assert server.api("GET", "/api/audit/verify")["ok"] is True
            report["scope"].append("manual generated-file tampering causes download410 and audited failed state without changing frozen sources")

            backup = server.api("POST", "/api/backups", {}, 201)
            archive = temp_root / "完整 schema5 中文备份.zip"
            archive.write_bytes(server.download(f'/api/backups/{backup["name"]}/download'))
            with zipfile.ZipFile(archive) as saved:
                names = saved.namelist()
                assert any(name == f'templates/docx/{custom["id"]}.docx' for name in names), names
                assert not any(name.startswith("exports/") for name in names), names
            weekly_after = server.api("PATCH", f'/api/worklogs/{weekly["id"]}', {
                "version": weekly["version"], "content": weekly["content"] + "\n备份后修改正文，旧导出不得改变。",
            })
            old_job = server.get("export-jobs", exported[("worklogs", "json")]["id"])
            assert old_job["source_version"] == weekly["version"]
            assert old_job["source_snapshot"]["fields"]["content"] == weekly["content"]
            assert weekly_after["version"] > old_job["source_version"]
            server.api("POST", "/api/export-jobs", {"format": "md", "source_kind": "worklogs", "source_id": weekly["id"], "source_version": weekly["version"]}, 409)
            fresh_job = completed_job(server, export_job(server, "worklogs", weekly_after, "json"))
            assert json.loads(verified_download(server, fresh_job))["content"] == weekly_after["content"]
            server.api("PUT", "/api/desktop/draft", {"version": draft["version"], "content": "备份后便签更改，应在新目录恢复旧稿。", "project_id": pid})
            server.stop()
            assert "校验通过" in server.cli("--verify-backup", archive).stdout

            restored = BinaryServer(binary, restored_root, port)
            assert "恢复完成" in restored.cli("--restore", archive).stdout
            restored.start()
            assert restored.api("GET", "/api/settings")["dataset_id"] == dataset_id
            assert restored.api("GET", "/api/desktop/draft") == draft
            assert restored.download(f'/api/export-templates/{custom["id"]}/download') == builtin_data
            assert restored.get("worklogs", weekly["id"])["content"] == weekly["content"]
            assert restored.get("project-daily-logs", project_log["id"])["source_snapshot"] == project_log["source_snapshot"]
            assert restored.get("issues", issue["id"])["verify_result"] == issue_fields["verify_result"]
            restored_jobs = restored.api("GET", query("/api/export-jobs", limit=100))
            assert restored_jobs["total"] == 9, restored_jobs
            assert fresh_job["id"] not in {item["id"] for item in restored_jobs["items"]}
            for old in exported.values():
                job = restored.get("export-jobs", old["id"])
                assert job["state"] == "failed" and job["error"], job
                assert job["source_snapshot"] == old["source_snapshot"]
                restored.api("GET", f'/api/export-jobs/{job["id"]}/download', status=409)
            assert restored.api("GET", "/api/audit/verify")["ok"] is True
            report["scope"].append("full backup restored into a fresh Chinese directory keeps dataset/templates/desktop draft/saved sources/job snapshots; absent generated files become failed at startup")

            regenerated = {}
            for kind, source, template in source_specs:
                job = completed_job(restored, export_job(restored, kind, source, "docx", template))
                data = verified_download(restored, job)
                assert job["id"] != exported[(kind, "docx")]["id"]
                regenerated[kind] = (job, data)
            report["scope"].append("all three saved source kinds can produce new verified DOCX jobs after restore using retained templates")
            assert restored.api("GET", "/api/audit/verify")["ok"] is True
            restored.stop()

            output_dir.mkdir(parents=True, exist_ok=True)
            files = [("smoke-个人周报.docx", "worklogs"), ("smoke-问题闭环单.docx", "issues")]
            report["fixtures"] = []
            for filename, kind in files:
                job, data = regenerated[kind]
                path = output_dir / filename
                path.write_bytes(data)
                report["fixtures"].append({"path": str(path), "sha256": digest(data), "size": len(data),
                                           "source_kind": kind, "source_id": job["source_id"],
                                           "source_version": job["source_version"], "template_id": job["template_id"],
                                           "template_hash": job["template_hash"], "job_id": job["id"]})
            report["completed_at"] = datetime.now(timezone.utc).isoformat()
            report["status"] = "passed"
            report["matrix_jobs"] = len(exported)
            report_path = output_dir / "smoke_v032-report.json"
            report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
            print(f"PASS {health['version']}: desktop draft/version/restart; next-plan read-only person/date candidates; custom DOCX template; 9 real export jobs with hashes and frozen sources; tamper410; fresh-directory backup restore and regenerated DOCX")
            print("QA fixtures:")
            for item in report["fixtures"]:
                print(f'  {item["path"]} ({item["size"]} bytes, sha256={item["sha256"]})')
            print("Report:", report_path)
            print("Not forced here: queued/running interruption (Store restart tests); visual Word layout and native desktop window behavior.")
        finally:
            if restored is not None:
                restored.cleanup()
            server.cleanup()
            busy.close()


if __name__ == "__main__":
    main()
