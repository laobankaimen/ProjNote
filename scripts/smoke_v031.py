#!/usr/bin/env python3
"""Exercise v0.3.1 single-user import/journals against a real compiled binary.

Only temporary synthetic workspaces are used. No default/user dataset, GUI
launcher, LAN listener, Office process or external service is selected.
"""
import argparse
import copy
import hashlib
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

REPOSITORY = Path(__file__).resolve().parents[1]
TEMPLATE = REPOSITORY / "internal/server/assets/task-import-template.xlsx"


def query(path, **filters):
    return path + "?" + urllib.parse.urlencode(filters)


def upload_preview(server, project_id, content, name="plan.xlsx", status=200):
    boundary = "ProjNoteV031XlsxBoundary"
    body = (
        f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{name}"\r\n'
        "Content-Type: application/vnd.openxmlformats-officedocument.spreadsheetml.sheet\r\n\r\n"
    ).encode() + content + f"\r\n--{boundary}--\r\n".encode()
    request = urllib.request.Request(
        server.url + f"/api/projects/{project_id}/import/preview", body,
        headers={"Content-Type": f"multipart/form-data; boundary={boundary}", "X-ProjNote-Request": "1"},
    )
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            raw = response.read()
            assert response.status == status, (response.status, raw)
            return json.loads(raw)["data"]
    except urllib.error.HTTPError as error:
        raw = error.read()
        assert error.code == status, (error.code, raw)
        return json.loads(raw)


def confirm_payload(preview):
    result = {key: copy.deepcopy(preview[key]) for key in ["rows", "source_hash", "project_version", "confirm_token"]}
    if preview.get("import_id"):
        result["import_id"] = preview["import_id"]
    return result


def workbook_with_formula(content):
    """Keep a real workbook but replace one task-hours cell with a cached formula."""
    source, target = io.BytesIO(content), io.BytesIO()
    namespace = "{http://schemas.openxmlformats.org/spreadsheetml/2006/main}"
    with zipfile.ZipFile(source) as old, zipfile.ZipFile(target, "w") as new:
        for entry in old.infolist():
            data = old.read(entry.filename)
            if entry.filename == "xl/worksheets/sheet1.xml":
                sheet = ET.fromstring(data)
                cell = next(item for item in sheet.iter(namespace + "c") if item.get("r") == "G3")
                formula = ET.Element(namespace + "f")
                formula.text = "1+1"
                cell.insert(0, formula)
                data = ET.tostring(sheet, encoding="utf-8", xml_declaration=True)
            new.writestr(entry, data)
    return target.getvalue()


def workbook_with_comment(content):
    target = io.BytesIO(content)
    with zipfile.ZipFile(target, "a") as workbook:
        workbook.comment = b"Whole-file hash fixture: unchanged task cells, changed ZIP comment."
    return target.getvalue()


def get_history(server, resource, entity_id):
    result = server.api("GET", f"/api/{resource}/{entity_id}/history?limit=100")
    assert result["total"] == len(result["items"]), result
    ids = [item["id"] for item in result["items"]]
    assert ids == sorted(ids, reverse=True), ids
    for item in result["items"]:
        assert item["entity_type"] == resource.replace("-", "_") and item["entity_id"] == entity_id, item
        assert json.loads(item["before_json"]) == item["before"]
        assert json.loads(item["after_json"]) == item["after"]
        assert item["operator"] and item["created_at"] and item["hash"]
    return result


def search_ids(server, text, entity_type):
    result = server.api("GET", query("/api/search", q=text, entity_type=entity_type, limit=100))
    return {item["entity_id"] for item in result["items"]}


def trash_item(server, kind, entity_id):
    result = server.api("GET", query("/api/trash", kind=kind, limit=100))
    return next(item for item in result["items"] if item["id"] == entity_id)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary")
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve(strict=True))
    workbook = TEMPLATE.read_bytes()
    assert workbook.startswith(b"PK"), "repository XLSX template is missing or corrupt"
    with tempfile.TemporaryDirectory(prefix="projnote-v031-") as temp:
        root = Path(temp) / "单用户 项目日志 数据"
        root.mkdir()
        busy = socket.socket()
        busy.bind(("127.0.0.1", 0))
        busy.listen()
        server = BinaryServer(binary, root, busy.getsockname()[1])
        try:
            server.start()
            server.api("PUT", "/api/settings", {"operator": "实际代填人", "backup_auto": "0", "setup_completed": "1"})
            assert server.api("GET", "/api/settings")["gjb_level"] == "2"
            assert server.download("/api/import-template.xlsx") == workbook
            project = server.api("POST", "/api/projects", {"name": "单用户导入与项目日报"}, 201)
            pid = project["id"]
            assert server.api("GET", f"/api/projects/{pid}/tree")["total"] == 0
            before_audit = server.api("GET", "/api/audit/verify")["total"]
            preview = upload_preview(server, pid, workbook)
            assert preview["total"] == 3 and len(preview["rows"]) == 3 and preview["errors"] == [], preview
            assert preview["source_hash"] == hashlib.sha256(workbook).hexdigest(), preview
            assert preview["project_version"] == project["version"] and preview["confirm_token"], preview
            assert server.api("GET", f"/api/projects/{pid}/tree")["total"] == 0
            assert server.api("GET", "/api/audit/verify")["total"] == before_audit, "preview mutated business audit"
            payload = confirm_payload(preview)
            altered = copy.deepcopy(payload)
            expiry, signature = altered["confirm_token"].split(".")
            altered["confirm_token"] = expiry + "." + ("0" if signature[0] != "0" else "1") + signature[1:]
            server.api("POST", f"/api/projects/{pid}/import", altered, 409)
            altered = copy.deepcopy(payload)
            altered["rows"][1]["name"] = "token不能批准篡改后的任务行"
            server.api("POST", f"/api/projects/{pid}/import", altered, 409)
            altered = copy.deepcopy(payload)
            altered["source_hash"] = "0" * 64
            server.api("POST", f"/api/projects/{pid}/import", altered, 409)
            assert server.api("GET", f"/api/projects/{pid}/tree")["total"] == 0
            upload_preview(server, pid, b"this is not an XLSX archive", status=400)
            upload_preview(server, pid, workbook, name="legacy.xls", status=400)
            formula = upload_preview(server, pid, workbook_with_formula(workbook), name="formula.xlsx")
            assert formula["errors"] and not formula.get("confirm_token"), formula
            formula_confirm = {
                "rows": formula["rows"], "source_hash": formula["source_hash"],
                "project_version": formula["project_version"], "confirm_token": payload["confirm_token"],
            }
            server.api("POST", f"/api/projects/{pid}/import", formula_confirm, 409)
            comment_bytes = workbook_with_comment(workbook)
            comment_preview = upload_preview(server, pid, comment_bytes, name="same-cells-new-zip.xlsx")
            assert comment_preview["errors"] == [] and comment_preview["rows"] == preview["rows"]
            assert comment_preview["source_hash"] == hashlib.sha256(comment_bytes).hexdigest()
            assert comment_preview["source_hash"] != preview["source_hash"], "source hash ignored non-sheet workbook bytes"
            imported = server.api("POST", f"/api/projects/{pid}/import", payload, 201)
            assert imported["total"] == 3 and imported["already_imported"] is False, imported
            task_ids = {item["id"] for item in imported["items"]}
            repeat_audit = server.api("GET", "/api/audit/verify")["total"]
            repeated = server.api("POST", f"/api/projects/{pid}/import", payload, 201)
            assert repeated["already_imported"] is True and repeated["import_id"] == imported["import_id"]
            assert {item["id"] for item in repeated["items"]} == task_ids
            assert server.api("GET", f"/api/projects/{pid}/tree")["total"] == 3
            assert server.api("GET", "/api/audit/verify")["total"] == repeat_audit, "idempotent replay wrote new audit events"
            tree = server.api("GET", f"/api/projects/{pid}/tree")["items"]
            assert tree[0]["has_children"] is True and all(item["depth"] == 1 for item in tree[1:])
            zhang_task = next(item for item in tree if item["assignee"] == "张三")
            li_task = next(item for item in tree if item["assignee"] == "李四")

            # These are labels and declarations entered by one operator, not logins.
            log_date = "2026-11-05"
            def team(person, content, task_id):
                return server.api("POST", "/api/team-daily-entries", {
                    "project_id": pid, "log_date": log_date, "person_name": person,
                    "content": content, "next_plan": "下一步核对", "blockers": "", "task_ids": [task_id],
                }, 201)
            zhang = team("张三", "张三原始代填工作内容", zhang_task["id"])
            zhang_time = server.api("POST", "/api/time-entries", {
                "project_id": pid, "task_id": zhang_task["id"], "work_date": log_date,
                "minutes": 900, "assignee": "张三", "note": "张三声明工时",
            }, 201)
            li_time = server.api("POST", "/api/time-entries", {
                "project_id": pid, "task_id": li_task["id"], "work_date": log_date,
                "minutes": 720, "assignee": "李四", "note": "李四声明工时",
            }, 201)
            server.api("POST", "/api/time-entries", {
                "project_id": pid, "work_date": log_date, "minutes": 541, "assignee": "张三",
            }, 409)
            times = server.api("GET", query("/api/time-entries/summary", project_id=pid, date_from=log_date, date_to=log_date))
            assert times["total_minutes"] == 1620, times
            assert {item["assignee"]: item["minutes"] for item in times["by_person"]} == {"张三": 900, "李四": 720}, times
            assert all(item["minutes"] <= 1440 for item in times["by_person_day"])
            filtered = server.api("GET", query("/api/time-entries/summary", project_id=pid, assignee="张三", date_from=log_date, date_to=log_date))
            assert filtered["total_minutes"] == 900
            missing_draft = server.api("POST", "/api/project-daily-logs/generate", {"project_id": pid, "log_date": log_date})
            snapshot = missing_draft["source_snapshot"]
            assert snapshot["missing_people"] == ["李四"] and set(snapshot["expected_people"]) == {"张三", "李四"}, snapshot
            assert snapshot["total_minutes"] == 1620 and "生成时" in snapshot["task_snapshot_semantics"]
            assert {item["id"] for item in snapshot["tasks"]} == task_ids
            assert {item["id"] for item in snapshot["time_entries"]} == {zhang_time["id"], li_time["id"]}
            assert snapshot["team_entries"][0]["id"] == zhang["id"] and snapshot["team_entries"][0]["version"] == zhang["version"]
            assert "合计 1620 分钟" in missing_draft["content"]
            li = team("李四", "李四代填工作内容", li_task["id"])
            server.api("POST", "/api/project-daily-logs", missing_draft, 409)
            assert server.api("GET", query("/api/project-daily-logs", project_id=pid))["total"] == 0
            draft = server.api("POST", "/api/project-daily-logs/generate", {"project_id": pid, "log_date": log_date})
            assert draft["source_snapshot"]["missing_people"] == []
            assert {item["id"] for item in draft["source_snapshot"]["team_entries"]} == {zhang["id"], li["id"]}
            project_log = server.api("POST", "/api/project-daily-logs", draft, 201)
            original_snapshot = copy.deepcopy(project_log["source_snapshot"])
            assert project_log["source_snapshot"] == draft["source_snapshot"]
            server.api("POST", "/api/project-daily-logs", draft, 409)
            personal = server.api("POST", "/api/worklogs/generate", {"date": log_date, "period_type": "day", "project_id": pid})
            assert set(personal) == {"content", "log_date", "period_type", "project_id"}, personal
            personal["content"] += "\n个人日志独立正文"
            personal_saved = server.api("POST", "/api/worklogs", personal, 201)
            assert server.get("project-daily-logs", project_log["id"])["content"] == draft["content"]
            assert server.get("worklogs", personal_saved["id"])["content"].endswith("个人日志独立正文")

            stale_draft = server.api("POST", "/api/project-daily-logs/generate", {"project_id": pid, "log_date": log_date})
            zhang = server.api("PATCH", f'/api/team-daily-entries/{zhang["id"]}', {
                "version": zhang["version"], "content": "张三新来源工作内容",
            })
            stale_draft["version"] = project_log["version"]
            server.api("PATCH", f'/api/project-daily-logs/{project_log["id"]}', stale_draft, 409)
            journal_marker = "项目日报独立检索标记"
            project_log = server.api("PATCH", f'/api/project-daily-logs/{project_log["id"]}', {
                "version": project_log["version"], "content": journal_marker + "；只改正文保留旧来源",
            })
            assert project_log["source_snapshot"] == original_snapshot, "prose-only PATCH silently regenerated sources"
            assert next(item for item in project_log["source_snapshot"]["team_entries"] if item["id"] == zhang["id"])["content"] == "张三原始代填工作内容"
            assert project_log["id"] in search_ids(server, journal_marker, "project_daily_logs")
            journal_history = get_history(server, "project-daily-logs", project_log["id"])
            assert journal_history["items"][0]["after"]["source_snapshot"] == original_snapshot
            assert all(item["operator"] == "实际代填人" for item in get_history(server, "team-daily-entries", zhang["id"])["items"])
            assert server.api("GET", query("/api/team-daily-entries", project_id=pid, person_name="张三"))["total"] == 1
            server.api("DELETE", f'/api/team-daily-entries/{li["id"]}?version={li["version"]}')
            server.api("GET", f'/api/team-daily-entries/{li["id"]}', status=404)
            assert li["id"] not in search_ids(server, "李四代填工作内容", "team_daily_entries")
            li_trash = trash_item(server, "team_daily_entries", li["id"])
            server.api("POST", f'/api/trash/team_daily_entries/{li["id"]}/restore', {"version": li["version"]}, 409)
            li = server.api("POST", f'/api/trash/team_daily_entries/{li["id"]}/restore', {"version": li_trash["version"]})
            assert li["id"] in search_ids(server, "李四代填工作内容", "team_daily_entries")
            assert server.get("project-daily-logs", project_log["id"])["source_snapshot"] == original_snapshot
            regenerated = server.api("POST", "/api/project-daily-logs/generate", {"project_id": pid, "log_date": log_date})
            assert regenerated["source_snapshot"]["missing_people"] == []
            assert next(item for item in regenerated["source_snapshot"]["team_entries"] if item["id"] == zhang["id"])["version"] == zhang["version"]
            regenerated["version"] = project_log["version"]
            regenerated["content"] += "\n" + journal_marker
            project_log = server.api("PATCH", f'/api/project-daily-logs/{project_log["id"]}', regenerated)
            saved_snapshot = copy.deepcopy(project_log["source_snapshot"])
            assert saved_snapshot != original_snapshot
            assert server.get("worklogs", personal_saved["id"])["content"] == personal_saved["content"]
            server.api("DELETE", f'/api/project-daily-logs/{project_log["id"]}?version={project_log["version"]}')
            assert project_log["id"] not in search_ids(server, journal_marker, "project_daily_logs")
            project_trash = trash_item(server, "project_daily_logs", project_log["id"])
            project_log = server.api("POST", f'/api/trash/project_daily_logs/{project_log["id"]}/restore', {"version": project_trash["version"]})
            assert project_log["source_snapshot"] == saved_snapshot
            assert project_log["id"] in search_ids(server, journal_marker, "project_daily_logs")
            assert server.api("GET", "/api/audit/verify")["ok"] is True

            snapshot_zip = server.api("POST", "/api/backups", {}, 201)
            archive = Path(temp) / "schema4 单用户完整备份.zip"
            archive.write_bytes(server.download(f'/api/backups/{snapshot_zip["name"]}/download'))
            server.api("PATCH", f'/api/team-daily-entries/{zhang["id"]}', {"version": zhang["version"], "content": "备份后变动应撤销"})
            server.api("DELETE", f'/api/project-daily-logs/{project_log["id"]}?version={project_log["version"]}')
            server.stop()
            assert "校验通过" in server.cli("--verify-backup", archive).stdout
            assert "恢复完成" in server.cli("--restore", archive).stdout
            server.start()
            assert server.get("team-daily-entries", zhang["id"])["content"] == "张三新来源工作内容"
            assert server.get("team-daily-entries", li["id"])["id"] == li["id"]
            assert server.get("project-daily-logs", project_log["id"])["source_snapshot"] == saved_snapshot
            assert server.get("worklogs", personal_saved["id"])["content"] == personal_saved["content"]
            assert server.api("GET", query("/api/time-entries/summary", project_id=pid, date_from=log_date, date_to=log_date))["total_minutes"] == 1620
            # Tokens are process-local; preview again after restart, then replay the persistent import.
            after_restart_preview = upload_preview(server, pid, workbook)
            assert after_restart_preview["source_hash"] == hashlib.sha256(workbook).hexdigest()
            after_restart = server.api("POST", f"/api/projects/{pid}/import", confirm_payload(after_restart_preview), 201)
            assert after_restart["already_imported"] is True and after_restart["import_id"] == imported["import_id"]
            assert {item["id"] for item in after_restart["items"]} == task_ids
            assert server.api("GET", f"/api/projects/{pid}/tree")["total"] == 3
            assert server.api("GET", "/api/audit/verify")["ok"] is True
            server.stop()
            print("PASS v0.3.1: real XLSX preview/full-file hash/atomic import/token integrity/idempotence, per-person work-time, single-user team/project journals/source snapshots/missing people/source409/prose preservation, independent personal logs/history/search/trash, schema4 full backup restore with persistent import replay")
        finally:
            server.cleanup()
            busy.close()


if __name__ == "__main__":
    main()
