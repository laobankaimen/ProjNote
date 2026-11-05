#!/usr/bin/env python3
"""Exercise v0.3 tree/history, independent work time and one-shot reminders.

Uses a real compiled binary, HTTP and stopped-service CLI. All data lives in a
private temporary directory; the user's default workspace is never selected.
"""
import argparse
from datetime import date, datetime, timedelta, timezone
import json
from pathlib import Path
import socket
import tempfile
import urllib.parse

from smoke_v02 import BinaryServer


def query(path, **filters):
    return path + "?" + urllib.parse.urlencode(filters)


def parse_instant(value):
    return datetime.fromisoformat(value.replace("Z", "+00:00"))


def history(server, kind, entity_id):
    result = server.api("GET", f"/api/{kind}/{entity_id}/history?limit=100")
    assert result["total"] == len(result["items"]), result
    numbers = [item["id"] for item in result["items"]]
    assert numbers == sorted(numbers, reverse=True), numbers
    for event in result["items"]:
        assert event["entity_id"] == entity_id, event
        assert event["entity_type"] == kind.replace("-", "_"), event
        assert isinstance(event["before_json"], str) and isinstance(event["after_json"], str), event
        assert "before" in event and "after" in event, event
        assert json.loads(event["before_json"]) == event["before"], event
        assert json.loads(event["after_json"]) == event["after"], event
        assert event["operator"] and event["created_at"] and event["hash"], event
    return result


def find_trash(server, kind, entity_id):
    items = server.api("GET", query("/api/trash", kind=kind, limit=100))["items"]
    return next(item for item in items if item["id"] == entity_id)


def summary(server, start, end, **filters):
    return server.api("GET", query("/api/time-entries/summary", date_from=start, date_to=end, **filters))


def assert_actual(item, minutes):
    assert item["actual_minutes"] == minutes, item
    assert abs(item["actual_hours"] - minutes / 60) < 1e-9, item


def assert_actual_views(server, project_id, expected):
    """The detail, complete tree and ordinary list must use the same totals."""
    tree = server.api("GET", f"/api/projects/{project_id}/tree")
    listed = server.api("GET", query("/api/tasks", project_id=project_id, limit=100))
    tree_by_id = {item["id"]: item for item in tree["items"]}
    list_by_id = {item["id"]: item for item in listed["items"]}
    for task_id, minutes in expected.items():
        assert_actual(server.get("tasks", task_id), minutes)
        assert_actual(tree_by_id[task_id], minutes)
        assert_actual(list_by_id[task_id], minutes)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary")
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve(strict=True))
    with tempfile.TemporaryDirectory(prefix="projnote-v03-") as temp:
        root = Path(temp) / "工时 提醒 数据"
        root.mkdir()
        busy = socket.socket()
        busy.bind(("127.0.0.1", 0))
        busy.listen()
        server = BinaryServer(binary, root, busy.getsockname()[1])
        try:
            server.start()
            server.api("PUT", "/api/settings", {"operator": "v0.3集成验收", "backup_auto": "0", "setup_completed": "1"})
            project = server.api("POST", "/api/projects", {"name": "工时和提醒验收"}, 201)
            other_project = server.api("POST", "/api/projects", {"name": "跨项目工时验收"}, 201)
            pid, other_pid = project["id"], other_project["id"]
            assert history(server, "projects", pid)["items"][0]["after"]["name"] == "工时和提醒验收"
            issue = server.api("POST", "/api/issues", {"project_id": pid, "title": "问题审计历史接口"}, 201)
            assert history(server, "issues", issue["id"])["items"][0]["action"] == "create"
            parent = server.api("POST", "/api/tasks", {"project_id": pid, "name": "超过一页的任务分组"}, 201)
            leaves = []
            for number in range(51):
                leaves.append(server.api("POST", "/api/tasks", {
                    "project_id": pid, "parent_id": parent["id"], "name": f"完整任务 {number + 1:02d}",
                }, 201))
            other_task = server.api("POST", "/api/tasks", {"project_id": other_pid, "name": "另一项目任务"}, 201)
            tree = server.api("GET", f"/api/projects/{pid}/tree")
            assert tree["total"] == 52 and len(tree["items"]) == 52, "tree truncated beyond one list page"
            assert {item["id"] for item in tree["items"]} == {parent["id"], *(item["id"] for item in leaves)}
            assert tree["items"][0]["id"] == parent["id"] and tree["items"][0]["depth"] == 0
            assert tree["items"][0]["has_children"] is True
            assert all(item["depth"] == 1 and item["has_children"] is False for item in tree["items"][1:])
            assert [item["order"] for item in tree["items"]] == list(range(1, 53))
            leaf = server.get("tasks", leaves[0]["id"])
            old_version = leaf["version"]
            leaf = server.api("PATCH", f'/api/tasks/{leaf["id"]}', {
                "version": old_version, "assignee": "行内负责人", "progress": 25, "status": "doing",
            })
            task_history = history(server, "tasks", leaf["id"])
            update = next(item for item in task_history["items"] if item["action"] == "update")
            assert update["before"]["assignee"] == "" and update["after"]["assignee"] == "行内负责人"
            assert update["after"]["progress"] == 25
            server.api("PATCH", f'/api/tasks/{leaf["id"]}', {"version": old_version, "assignee": "不能覆盖"}, 409)
            assert history(server, "tasks", leaf["id"])["total"] == task_history["total"]
            parent = server.get("tasks", parent["id"])
            server.api("PATCH", f'/api/tasks/{parent["id"]}', {"version": parent["version"], "progress": 99}, 400)
            leaf = server.api("PATCH", f'/api/tasks/{leaf["id"]}', {"version": leaf["version"], "status": "done"})

            # Fixed dates share a Monday-based week and do not depend on wall time.
            work_day, next_day = "2026-11-02", "2026-11-03"
            def create_time(project_id, task_id, minutes, work_date=work_day, status=201, note="验收工时"):
                return server.api("POST", "/api/time-entries", {
                    "project_id": project_id, "task_id": task_id, "work_date": work_date,
                    "minutes": minutes, "note": note,
                }, status)

            create_time(pid, other_task["id"], 1, status=400)
            create_time(pid, parent["id"], 1, status=400)
            create_time(pid, leaf["id"], 1.5, status=400)
            create_time(pid, leaf["id"], 0, status=400)
            create_time(pid, leaf["id"], 1441, status=400)
            create_time(pid, leaf["id"], 1, work_date="2026-02-30", status=400)
            first = create_time(pid, leaf["id"], 300)
            second = create_time(pid, leaves[1]["id"], 240)
            day_summary = summary(server, work_day, work_day, project_id=pid)
            assert day_summary["total_minutes"] == 540, day_summary
            assert day_summary["by_day"] == [{"date": work_day, "minutes": 540, "over_eight_hours": True}], day_summary
            assert sum(item["minutes"] for item in day_summary["by_task"]) == 540
            assert summary(server, work_day, work_day, task_id=leaf["id"])["total_minutes"] == 300
            assert_actual_views(server, pid, {leaf["id"]: 300, leaves[1]["id"]: 240, parent["id"]: 540})
            server.api("DELETE", f'/api/time-entries/{second["id"]}?version={second["version"]}')
            assert_actual_views(server, pid, {leaf["id"]: 300, leaves[1]["id"]: 0, parent["id"]: 300})
            second_trash = find_trash(server, "time_entries", second["id"])
            second = server.api("POST", f'/api/trash/time_entries/{second["id"]}/restore', {"version": second_trash["version"]})
            assert_actual_views(server, pid, {leaf["id"]: 300, leaves[1]["id"]: 240, parent["id"]: 540})
            # Editing excludes the existing row from the cap calculation.
            second = server.api("PATCH", f'/api/time-entries/{second["id"]}', {"version": second["version"], "minutes": 1140})
            assert summary(server, work_day, work_day)["total_minutes"] == 1440
            assert_actual_views(server, pid, {leaf["id"]: 300, leaves[1]["id"]: 1140, parent["id"]: 1440})
            server.api("PATCH", f'/api/time-entries/{second["id"]}', {"version": second["version"], "minutes": 1141}, 409)
            assert server.get("time-entries", second["id"])["minutes"] == 1140
            second = server.api("PATCH", f'/api/time-entries/{second["id"]}', {"version": second["version"], "minutes": 240})
            foreign = create_time(other_pid, other_task["id"], 900)
            assert summary(server, work_day, work_day)["total_minutes"] == 1440
            create_time(pid, leaf["id"], 1, status=409)
            server.api("DELETE", f'/api/time-entries/{foreign["id"]}?version={foreign["version"]}')
            server.api("GET", f'/api/time-entries/{foreign["id"]}', status=404)
            assert summary(server, work_day, work_day)["total_minutes"] == 540
            foreign_trash = find_trash(server, "time_entries", foreign["id"])
            replacement = create_time(other_pid, other_task["id"], 1)
            server.api("POST", f'/api/trash/time_entries/{foreign["id"]}/restore', {"version": foreign_trash["version"]}, 409)
            assert summary(server, work_day, work_day)["total_minutes"] == 541, "restore leaked a partial write"
            server.api("DELETE", f'/api/time-entries/{replacement["id"]}?version={replacement["version"]}')
            foreign = server.api("POST", f'/api/trash/time_entries/{foreign["id"]}/restore', {"version": foreign_trash["version"]})
            assert foreign["minutes"] == 900 and summary(server, work_day, work_day)["total_minutes"] == 1440
            server.api("DELETE", f'/api/time-entries/{foreign["id"]}?version={foreign["version"]}')
            next_entry = create_time(pid, leaf["id"], 60, work_date=next_day)
            assert summary(server, work_day, next_day, project_id=pid)["total_minutes"] == 600
            assert_actual_views(server, pid, {leaf["id"]: 360, leaves[1]["id"]: 240, parent["id"]: 600})
            assert history(server, "time-entries", first["id"])["items"][0]["action"] == "create"
            second_history = history(server, "time-entries", second["id"])
            assert sum(item["action"] == "update" for item in second_history["items"]) == 2

            # Saved/edited daily and weekly text is never a second work-time source.
            daily_input = {"date": work_day, "period_type": "day", "project_id": pid}
            daily = server.api("POST", "/api/worklogs/generate", daily_input)
            assert "合计 540 分钟" in daily["content"], daily
            saved_day = server.api("POST", "/api/worklogs", {key: daily[key] for key in ["project_id", "period_type", "log_date", "content"]}, 201)
            saved_day = server.api("PATCH", f'/api/worklogs/{saved_day["id"]}', {
                "version": saved_day["version"], "content": saved_day["content"] + "\n人工描述：额外100小时文字不计工时",
            })
            daily_again = server.api("POST", "/api/worklogs/generate", daily_input)
            assert "合计 540 分钟" in daily_again["content"]
            assert summary(server, work_day, work_day, project_id=pid)["total_minutes"] == 540
            weekly = server.api("POST", "/api/worklogs/generate", {"date": next_day, "period_type": "week", "project_id": pid})
            assert weekly["log_date"] == work_day and "合计 600 分钟" in weekly["content"]
            server.api("POST", "/api/worklogs", {key: weekly[key] for key in ["project_id", "period_type", "log_date", "content"]}, 201)
            assert summary(server, work_day, next_day, project_id=pid)["total_minutes"] == 600
            assert server.get("worklogs", saved_day["id"])["content"].endswith("额外100小时文字不计工时")
            assert history(server, "worklogs", saved_day["id"])["items"][0]["after"]["content"].endswith("额外100小时文字不计工时")

            # Relative tokens use the service's local calendar day, then persist UTC.
            before_today = server.api("GET", "/api/today")["date"]
            quick = server.api("POST", "/api/records", {"project_id": pid, "content": "提醒语法验收 !明天"}, 201)
            after_today = server.api("GET", "/api/today")["date"]
            reminder_id = quick["reminder"]["id"]
            reminder = server.get("reminders", reminder_id)
            due_local = parse_instant(reminder["due_at"]).astimezone()
            allowed_dates = {date.fromisoformat(value) + timedelta(days=1) for value in [before_today, after_today]}
            assert due_local.date() in allowed_dates and (due_local.hour, due_local.minute) == (9, 0), reminder
            assert reminder["record_id"] == quick["id"] and reminder["project_id"] == pid
            quick = server.api("PATCH", f'/api/records/{quick["id"]}', {
                "version": quick["version"], "content": "普通编辑 !后天 不重新解析既有提醒",
            })
            linked = server.api("GET", query("/api/reminders", record_id=quick["id"], status="all"))
            assert linked["total"] == 1 and linked["items"][0]["id"] == reminder_id
            assert linked["items"][0]["due_at"] == reminder["due_at"]
            explicit_due = (datetime.now(timezone.utc) - timedelta(minutes=1)).isoformat()
            quick = server.api("PATCH", f'/api/records/{quick["id"]}', {
                "version": quick["version"], "reminder_due_at": explicit_due,
            })
            reminder = server.get("reminders", reminder_id)
            assert parse_instant(reminder["due_at"]) == parse_instant(explicit_due)
            today = server.api("GET", "/api/today")
            assert reminder_id in {item["id"] for item in today["reminders"]}, today
            # Independent reminder edits invalidate a previously opened record form.
            stale_record_version = quick["version"]
            reminder = server.api("PATCH", f"/api/reminders/{reminder_id}", {
                "version": reminder["version"], "title": "独立入口修改关联提醒",
            })
            server.api("PATCH", f'/api/records/{quick["id"]}', {
                "version": stale_record_version, "content": "旧记录页不能覆盖独立提醒更新", "reminder_due_at": explicit_due,
            }, 409)
            quick = server.get("records", quick["id"])
            assert quick["version"] > stale_record_version and quick["content"] == "普通编辑 !后天 不重新解析既有提醒"
            assert any(event["action"] == "reminder_change" for event in history(server, "records", quick["id"])["items"])
            pending_version = reminder["version"]
            reminder = server.api("POST", f'/api/reminders/{reminder_id}/done', {"version": pending_version})
            assert reminder["done_at"]
            server.api("POST", f'/api/reminders/{reminder_id}/done', {"version": pending_version}, 409)
            server.api("POST", f'/api/reminders/{reminder_id}/done', {"version": reminder["version"]}, 409)
            assert reminder_id not in {item["id"] for item in server.api("GET", "/api/today")["reminders"]}
            reminder_history = history(server, "reminders", reminder_id)
            assert sum(item["action"] == "complete" for item in reminder_history["items"]) == 1
            server.api("PATCH", f"/api/reminders/{reminder_id}", {"version": reminder["version"], "due_at": explicit_due}, 409)
            server.api("PATCH", f'/api/records/{quick["id"]}', {
                "version": quick["version"], "content": "完成提醒不能借记录编辑改期", "reminder_due_at": explicit_due,
            }, 409)
            assert server.get("records", quick["id"])["content"] == quick["content"]
            assert history(server, "records", quick["id"])["items"][0]["after"]["id"] == quick["id"]

            before_clock = datetime.now().astimezone()
            today_record = server.api("POST", "/api/records", {"project_id": pid, "content": "今天一次提醒 !今天"}, 201)
            after_clock = datetime.now().astimezone()
            today_due = parse_instant(today_record["reminder_due_at"])
            if (before_clock.hour, before_clock.minute) >= (9, 0):
                assert before_clock + timedelta(seconds=59) <= today_due <= after_clock + timedelta(seconds=61), today_record
            elif (after_clock.hour, after_clock.minute) < (9, 0):
                local_due = today_due.astimezone()
                assert local_due.date() == before_clock.date() and (local_due.hour, local_due.minute) == (9, 0), today_record

            clear_record = server.api("POST", "/api/records", {
                "project_id": pid, "content": "显式提醒可清除", "reminder_due_at": explicit_due,
            }, 201)
            clear_id = clear_record["reminder"]["id"]
            clear_record = server.api("PATCH", f'/api/records/{clear_record["id"]}', {
                "version": clear_record["version"], "reminder_due_at": None,
            })
            assert clear_record["reminder"] is None
            server.api("GET", f"/api/reminders/{clear_id}", status=404)
            bad_count = server.api("GET", "/api/records")["total"]
            audit_before_invalid = server.api("GET", "/api/audit/verify")["total"]
            server.api("POST", "/api/records", {"project_id": pid, "content": "坏提醒事务应回滚", "reminder_due_at": "2026-11-05T09:00:00"}, 400)
            assert server.api("GET", "/api/records")["total"] == bad_count
            assert server.api("GET", "/api/audit/verify")["total"] == audit_before_invalid
            invalid_date = server.api("POST", "/api/records", {"project_id": pid, "content": "不归一化无效日期 !2026-02-30"}, 201)
            assert invalid_date["reminder"] is None and "!2026-02-30" in invalid_date["content"]
            unknown = server.api("POST", "/api/records", {"project_id": pid, "content": "未知语法原样保留 !不认识"}, 201)
            assert "!不认识" in unknown["content"] and unknown["reminder"] is None

            independent = server.api("POST", "/api/reminders", {"title": "独立提醒", "project_id": pid, "due_at": explicit_due}, 201)
            independent = server.api("PATCH", f'/api/reminders/{independent["id"]}', {
                "version": independent["version"], "title": "独立提醒已编辑",
            })
            server.api("DELETE", f'/api/reminders/{independent["id"]}?version={independent["version"]}')
            server.api("GET", f'/api/reminders/{independent["id"]}', status=404)
            independent_trash = find_trash(server, "reminders", independent["id"])
            independent = server.api("POST", f'/api/trash/reminders/{independent["id"]}/restore', {"version": independent_trash["version"]})
            assert independent["title"] == "独立提醒已编辑"

            # Archive refuses time creation/update/delete/restore while keeping reads.
            archived_project = server.api("POST", "/api/projects", {"name": "归档工时规则"}, 201)
            archive_pid = archived_project["id"]
            archived_active = create_time(archive_pid, "", 15, work_date="2026-10-01")
            archived_deleted = create_time(archive_pid, "", 10, work_date="2026-10-01")
            server.api("DELETE", f'/api/time-entries/{archived_deleted["id"]}?version={archived_deleted["version"]}')
            archived_trash = find_trash(server, "time_entries", archived_deleted["id"])
            archived_project = server.get("projects", archive_pid)
            server.api("PATCH", f"/api/projects/{archive_pid}", {"version": archived_project["version"], "status": "archived"})
            create_time(archive_pid, "", 1, work_date="2026-10-01", status=409)
            server.api("PATCH", f'/api/time-entries/{archived_active["id"]}', {"version": archived_active["version"], "minutes": 16}, 409)
            server.api("DELETE", f'/api/time-entries/{archived_active["id"]}?version={archived_active["version"]}', status=409)
            server.api("POST", f'/api/trash/time_entries/{archived_deleted["id"]}/restore', {"version": archived_trash["version"]}, 409)
            assert summary(server, "2026-10-01", "2026-10-01", project_id=archive_pid)["total_minutes"] == 15
            assert server.api("GET", "/api/audit/verify")["ok"] is True

            # A single schema3 archive preserves time, reminder completion and history.
            snapshot = server.api("POST", "/api/backups", {}, 201)
            archive = Path(temp) / "工时 提醒 v3备份.zip"
            archive.write_bytes(server.download(f'/api/backups/{snapshot["name"]}/download'))
            server.api("DELETE", f'/api/time-entries/{first["id"]}?version={first["version"]}')
            assert_actual_views(server, pid, {leaf["id"]: 60, leaves[1]["id"]: 240, parent["id"]: 300})
            server.stop()
            assert "校验通过" in server.cli("--verify-backup", archive).stdout
            assert "恢复完成" in server.cli("--restore", archive).stdout
            server.start()
            assert summary(server, work_day, next_day, project_id=pid)["total_minutes"] == 600
            assert server.get("reminders", reminder_id)["done_at"] == reminder["done_at"]
            assert server.get("time-entries", first["id"])["minutes"] == 300
            assert_actual_views(server, pid, {leaf["id"]: 360, leaves[1]["id"]: 240, parent["id"]: 600})
            assert server.api("GET", f"/api/projects/{pid}/tree")["total"] == 52
            assert history(server, "reminders", reminder_id)["total"] == reminder_history["total"]
            assert server.api("GET", "/api/audit/verify")["ok"] is True
            server.stop()
            print("PASS v0.3: complete >50 task tree, inline PATCH/history/409, integer work-time/cross-project/day-cap/8h warning, task actual totals across detail/tree/list, soft-delete/restore, nonduplicating daily-weekly logs, one-shot quick reminders/edit/done/Today, archived write protection, schema3 full backup restore")
        finally:
            server.cleanup()
            busy.close()


if __name__ == "__main__":
    main()
