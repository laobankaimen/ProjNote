#!/usr/bin/env python3
"""Smoke-test coverage and frozen export maintenance in a private temporary dataset."""
import argparse
import copy
from html.parser import HTMLParser
import io
import json
from pathlib import Path
import socket
import tempfile
import xml.etree.ElementTree as ET
import zipfile

from smoke_v02 import BinaryServer, item_version
from smoke_v032 import completed_job, digest, export_job, query, read_docx


FORMATS = ("docx", "md", "json", "xlsx", "html")
SHEET = "{http://schemas.openxmlformats.org/spreadsheetml/2006/main}"
OLD_DESCRIPTION = "历史需求正文原版，导出必须保留这一版。"
OLD_CRITERIA = "旧版验收准则：告警与记录各出现一次。"
LIVE_DESCRIPTION = "当前需求已经改为新版正文，不得混入历史修订。"
OLD_LOG = "清理前的已保存日志正文，重新生成必须保留这段旧内容。"
LIVE_LOG = "日志现已修改为新内容，旧导出重建时不可读取此版本。"


class ReportText(HTMLParser):
    def __init__(self):
        super().__init__(convert_charrefs=True)
        self.text = []

    def handle_data(self, data):
        self.text.append(data)


def verify_artifact(server, job, markers):
    """Read every real artifact and check its hash, package and frozen content."""
    data = server.download(f'/api/export-jobs/{job["id"]}/download')
    assert digest(data) == job["output_sha256"] and len(data) == job["size"], job
    snapshot = job["source_snapshot"]
    format_name = job["format"]
    if format_name == "json":
        assert json.loads(data) == snapshot
        text = snapshot["content"]
    elif format_name == "md":
        text = data.decode("utf-8")
        assert text == snapshot["content"]
    elif format_name == "docx":
        text = read_docx(data)
    elif format_name == "xlsx":
        parts = []
        with zipfile.ZipFile(io.BytesIO(data)) as workbook:
            assert workbook.testzip() is None
            assert len(workbook.namelist()) == len(set(workbook.namelist()))
            for name in workbook.namelist():
                if name.endswith((".xml", ".rels")):
                    tree = ET.fromstring(workbook.read(name))
                    if name.startswith("xl/worksheets/") and name.endswith(".xml"):
                        assert not list(tree.iter(SHEET + "f")), "source became a formula"
                        parts.append(tree)
            assert len(parts) == 3, "full report/source/snapshot workbook expected"
        text = "\n".join(node.text or "" for part in parts for node in part.iter(SHEET + "t"))
    else:
        parser = ReportText()
        parser.feed(data.decode("utf-8"))
        parser.close()
        text = "\n".join(parser.text)
    for marker in markers:
        assert marker in text, (job["source_kind"], format_name, marker, text[:1000])
    return data


def frozen_hash(snapshot):
    # Go's json.Marshal sorts map keys and escapes HTML characters. Fixture
    # numbers use integers so their canonical representation is unambiguous.
    text = json.dumps(snapshot, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    for character, escape in (("&", "\\u0026"), ("<", "\\u003c"), (">", "\\u003e"),
                              ("\u2028", "\\u2028"), ("\u2029", "\\u2029")):
        text = text.replace(character, escape)
    return digest(text.encode("utf-8"))


def task(server, pid, name, **fields):
    return server.api("POST", "/api/tasks", {"project_id": pid, "name": name, **fields}, 201)


def requirement(server, pid, title, status="pending", task_ids=(), **fields):
    payload = {
        "project_id": pid, "title": title, "description": OLD_DESCRIPTION,
        "acceptance_criteria": OLD_CRITERIA, "owner": "覆盖验证人", "status": status,
        "task_ids": list(task_ids), **fields,
    }
    if status == "accepted":
        payload["acceptance_note"] = "负责人已明确验收；执行状态异常仍应提示。"
    return server.api("POST", "/api/requirements", payload, 201)


def coverage_fixture(server):
    project = server.api("POST", "/api/projects", {"name": "【合成验收】需求覆盖与冻结导出"}, 201)
    pid = project["id"]
    parent = task(server, pid, "关联父项")
    done = task(server, pid, "完成待验收", parent_id=parent["id"], status="done", estimate_hours=2)
    doing = task(server, pid, "原始执行任务", parent_id=parent["id"], status="doing", progress=20,
                 estimate_hours=2, plan_end="2026-11-06")
    child = task(server, pid, "子项不继承父关联", parent_id=parent["id"], estimate_hours=2)
    free = task(server, pid, "只有作废与回收需求关联")
    canceled = task(server, pid, "取消不是完成", status="canceled")
    recycled = task(server, pid, "关联后回收任务")
    ready = requirement(server, pid, "待人工验收需求", "doing", [done["id"]])
    accepted = requirement(server, pid, "已验收但任务未完成", "accepted", [doing["id"]])
    no_tasks = requirement(server, pid, "尚未关联任务")
    void = requirement(server, pid, "作废不计有效覆盖", "void", [free["id"]])
    recycled_link = requirement(server, pid, "只关联已回收任务", task_ids=[recycled["id"]])
    canceled_link = requirement(server, pid, "取消任务仍未完成", task_ids=[canceled["id"]])
    parent_link = requirement(server, pid, "只直接关联父任务", "doing", [parent["id"]])
    removed = requirement(server, pid, "回收需求不覆盖", task_ids=[free["id"]])
    server.api("DELETE", f'/api/requirements/{removed["id"]}', item_version(removed))
    server.api("DELETE", f'/api/tasks/{recycled["id"]}', item_version(recycled))
    path = f"/api/projects/{pid}/requirements/coverage"
    page = server.api("GET", path)
    expected = {
        "requirements_total": 7, "active_requirements": 6,
        "status_counts": {"pending": 3, "doing": 2, "accepted": 1, "void": 1},
        "requirements_without_tasks": 2, "ready_for_acceptance": 1,
        "accepted_with_incomplete_tasks": 1, "tasks_total": 6, "leaf_tasks": 5,
        "open_leaf_tasks": 3, "linked_leaf_tasks": 3, "unlinked_leaf_tasks": 2,
    }
    assert page["basis"] == "direct_links_current_state" and page["summary"] == expected, page
    assert page["total"] == 6
    items = {item["id"]: item for item in page["items"]}
    assert items[ready["id"]]["ready_for_acceptance"] is True
    assert items[accepted["id"]]["accepted_with_incomplete_tasks"] is True
    for item in (no_tasks, recycled_link):
        assert items[item["id"]]["no_tasks"] is True and items[item["id"]]["task_count"] == 0
    assert items[canceled_link["id"]]["ready_for_acceptance"] is False
    assert items[parent_link["id"]]["task_count"] == 1
    assert items[parent_link["id"]]["linked_tasks"][0]["id"] == parent["id"]
    for filters, total in (({"coverage": "no_tasks", "limit": 1}, 2),
                           ({"coverage": "ready_for_acceptance"}, 1),
                           ({"coverage": "accepted_with_incomplete_tasks"}, 1),
                           ({"q": "尚未关联任务", "status": "pending", "owner": "覆盖验证人", "limit": 1}, 1),
                           ({"owner": "不存在", "offset": 9999}, 0)):
        filtered = server.api("GET", query(path, **filters))
        assert filtered["summary"] == expected and filtered["total"] == total, filtered
    orphaned = server.api("GET", query(path, view="unlinked_tasks"))
    assert {item["id"] for item in orphaned["items"]} == {child["id"], free["id"]}
    assert orphaned["summary"] == expected
    inverse = server.api("GET", f'/api/tasks/{parent["id"]}/requirements')
    assert [item["id"] for item in inverse["items"]] == [parent_link["id"]]
    assert server.api("GET", f'/api/tasks/{child["id"]}/requirements')["total"] == 0
    assert server.api("GET", f'/api/tasks/{free["id"]}/requirements')["total"] == 0
    void_links = server.api("GET", query(f'/api/tasks/{free["id"]}/requirements', status="void"))
    assert [item["id"] for item in void_links["items"]] == [void["id"]]
    inverse = server.api("GET", query(f'/api/tasks/{done["id"]}/requirements', q="待人工验收", status="doing", owner="覆盖验证人", limit=1))
    assert inverse["total"] == 1 and inverse["items"][0]["id"] == ready["id"]
    assert server.api("GET", query(f'/api/tasks/{done["id"]}/requirements', offset=1))["items"] == []
    server.api("GET", f'/api/tasks/{recycled["id"]}/requirements', status=404)
    assert server.get("requirements", ready["id"]) == ready, "coverage wrote a false business revision"
    return project, ready, doing


def frozen_exports(server, project, original, changed_task):
    pid = project["id"]
    original_revision = server.api("GET", f'/api/requirements/{original["id"]}/revisions/1')
    current = server.api("PATCH", f'/api/requirements/{original["id"]}', {
        "version": original["version"], "description": LIVE_DESCRIPTION,
        "acceptance_criteria": "新版验收准则：扩大验证范围。", "change_note": "修订为当前版本。",
    })
    assert current["revision"] == 2
    project = server.get("projects", pid)
    first = server.api("POST", f"/api/projects/{pid}/plan-versions", {
        "name": "冻结原计划", "project_version": project["version"],
    }, 201)
    changed_task = server.api("PATCH", f'/api/tasks/{changed_task["id"]}', {
        "version": server.get("tasks", changed_task["id"])["version"],
        "name": "比较时的执行任务", "plan_end": "2026-11-10",
    })
    comparison_path = f"/api/projects/{pid}/plan-versions/compare"
    comparison = server.api("GET", query(comparison_path, **{"from": first["id"], "to": "current"}))
    assert comparison["summary"]["changed"] == 1
    jobs = []
    for format_name in FORMATS:
        job = export_job(server, "requirement_revisions", {"id": original["id"] + ":1", "version": 1}, format_name)
        job = completed_job(server, job)
        snapshot = job["source_snapshot"]
        assert snapshot["source_snapshot"] == original_revision
        assert snapshot["fields"] == original_revision["snapshot"]
        assert LIVE_DESCRIPTION not in snapshot["content"]
        verify_artifact(server, job, (OLD_DESCRIPTION, OLD_CRITERIA))
        jobs.append(job)
        payload = {
            "from": first["id"], "to": "current", "format": format_name,
            "from_hash": comparison["from"]["hash"], "to_hash": comparison["to"]["hash"],
        }
        job = server.api("POST", f"/api/projects/{pid}/plan-versions/compare-export", payload, 201)
        job = completed_job(server, job)
        snapshot = job["source_snapshot"]
        assert snapshot["fields"]["summary"] == comparison["summary"]
        for side in ("from", "to"):
            endpoint = snapshot["source_snapshot"][side]
            assert len(endpoint["tasks"]) == 6
            assert {task["id"] for task in endpoint["tasks"]} == {task["id"] for task in first["snapshot"]["tasks"]}
            assert all(task["project_id"] == pid for task in endpoint["tasks"])
            assert endpoint["project"]["id"] == pid
            assert frozen_hash(endpoint) == comparison[side]["hash"], (side, endpoint, comparison[side])
            assert snapshot["fields"][side + "_snapshot"] == endpoint
            assert snapshot["fields"][side]["hash"] == comparison[side]["hash"]
        assert snapshot["source_snapshot"]["from"] == first["snapshot"]
        verify_artifact(server, job, ("2026-11-06", "2026-11-10", "比较时的执行任务"))
        jobs.append(job)
        for side in ("from_hash", "to_hash"):
            stale = {**payload, side: "0" * 64}
            server.api("POST", f"/api/projects/{pid}/plan-versions/compare-export", stale, 409)
    assert server.api("GET", f'/api/requirements/{original["id"]}/revisions/1') == original_revision
    assert server.get("plan-versions", first["id"]) == first
    # A real current-plan edit must reject a previously valid comparison hash.
    server.api("PATCH", f'/api/tasks/{changed_task["id"]}', {
        "version": changed_task["version"], "name": "比较完成后再改当前任务", "plan_end": "2026-11-30",
    })
    server.api("POST", f"/api/projects/{pid}/plan-versions/compare-export", {
        "from": first["id"], "to": "current", "format": "json",
        "from_hash": comparison["from"]["hash"], "to_hash": comparison["to"]["hash"],
    }, 409)
    return jobs


def cleanup_and_regenerate(server, jobs):
    log = server.api("POST", "/api/worklogs", {"content": OLD_LOG}, 201)
    target = completed_job(server, export_job(server, "worklogs", log, "md"))
    verify_artifact(server, target, (OLD_LOG,))
    jobs = [*jobs, target]
    live = server.api("PATCH", f'/api/worklogs/{log["id"]}', {
        "version": log["version"], "content": LIVE_LOG,
    })
    assert live["version"] > log["version"]
    before = server.api("GET", "/api/export-storage")
    assert before["files"] == len(jobs) == 11 and before["jobs"]["succeeded"] == 11, before
    assert before["bytes"] == sum(job["size"] for job in jobs)
    preview = server.api("POST", "/api/export-storage/cleanup-preview", {"days": 0})
    assert preview["total"] == len(jobs) and preview["bytes"] == before["bytes"] and preview["skipped"] == 0
    assert {item["id"] for item in preview["items"]} == {job["id"] for job in jobs}
    selected = next(item for item in preview["items"] if item["id"] == target["id"])
    assert selected["version"] == target["version"] and selected["output_sha256"] == target["output_sha256"]
    snapshot = copy.deepcopy(target["source_snapshot"])
    result = server.api("POST", "/api/export-storage/cleanup", {"items": [item_version(selected)]})
    assert result["cleaned"] == 1 and result["failed"] == 0 and result["bytes"] == target["size"], result
    failed = server.get("export-jobs", target["id"])
    assert failed["state"] == "failed" and failed["error"] == "用户已清理导出文件，来源快照保留"
    assert failed["source_snapshot"] == snapshot and not failed["output_name"] and failed["size"] == 0
    assert not (server.root / "exports" / target["output_name"]).exists()
    server.api("GET", f'/api/export-jobs/{target["id"]}/download', status=409)
    after = server.api("GET", "/api/export-storage")
    assert after["files"] == 10 and after["bytes"] == before["bytes"] - target["size"]
    assert after["jobs"]["failed"] == 1 and after["jobs"]["succeeded"] == 10
    for job in jobs:
        if job["id"] != target["id"]:
            assert server.get("export-jobs", job["id"])["state"] == "succeeded"
            assert digest(server.download(f'/api/export-jobs/{job["id"]}/download')) == job["output_sha256"]
    path = f'/api/export-jobs/{target["id"]}/regenerate'
    server.api("POST", path, {"version": target["version"]}, 409)
    regenerated = completed_job(server, server.api("POST", path, {"version": failed["version"]}, 201))
    assert regenerated["id"] != target["id"] and regenerated["source_snapshot"] == snapshot
    assert regenerated["source_id"] == target["source_id"] and regenerated["source_version"] == target["source_version"]
    data = verify_artifact(server, regenerated, (OLD_LOG,))
    assert LIVE_LOG not in data.decode("utf-8")
    assert server.get("worklogs", log["id"]) == live, "regeneration changed the current source"
    assert server.get("export-jobs", target["id"])["source_snapshot"] == snapshot
    assert server.get("export-jobs", target["id"])["state"] == "failed"
    final = server.api("GET", "/api/export-storage")
    assert final["files"] == 11 and final["jobs"]["failed"] == 1 and final["jobs"]["succeeded"] == 11


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary")
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve(strict=True))
    with tempfile.TemporaryDirectory(prefix="projnote-extensions-") as temp:
        root = Path(temp) / "扩展功能临时工作区"
        root.mkdir()
        busy = socket.socket()
        busy.bind(("127.0.0.1", 0))
        busy.listen()
        port = busy.getsockname()[1]
        assert 1024 <= port <= 65436, port
        server = BinaryServer(binary, root, port)
        try:
            server.start()
            server.api("PUT", "/api/settings", {"operator": "扩展验收人", "backup_auto": "0"})
            project, original, changed_task = coverage_fixture(server)
            jobs = frozen_exports(server, project, original, changed_task)
            cleanup_and_regenerate(server, jobs)
            assert server.api("GET", "/api/audit/verify")["ok"] is True
            server.stop()
        finally:
            server.cleanup()
            busy.close()
    print("PASS extensions: coverage/direct task links; 10 verified historical/comparison exports; hash409; single-job cleanup; frozen regeneration after live edits")


if __name__ == "__main__":
    main()
