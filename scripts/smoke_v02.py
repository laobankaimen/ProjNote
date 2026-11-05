#!/usr/bin/env python3
"""Exercise v0.2 APIs and stopped-service CLI using a real compiled binary.

All data, images, backups and relocated directories are private temporary
fixtures. This script never selects the user's default data directory.
"""
import argparse
import base64
import json
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request


class BinaryServer:
    def __init__(self, binary, root, port):
        self.binary = binary
        self.root = root
        self.port = port
        self.process = None
        self.url = ""

    def cli(self, *args, success=True):
        result = subprocess.run(
            [self.binary, "--data-dir", str(self.root), *map(str, args)],
            capture_output=True, text=True, timeout=60,
        )
        if success:
            assert result.returncode == 0, (args, result.stdout, result.stderr)
        else:
            assert result.returncode != 0, (args, result.stdout, result.stderr)
        return result

    def start(self):
        assert self.process is None
        self.process = subprocess.Popen(
            [self.binary, "--data-dir", str(self.root), "--port", str(self.port), "--no-browser"],
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
        )
        deadline = time.monotonic() + 20
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                raise AssertionError(self.process.communicate()[0])
            runtime_path = self.root / "runtime.json"
            if runtime_path.exists():
                try:
                    runtime = json.loads(runtime_path.read_text())
                    with urllib.request.urlopen(runtime["url"] + "/api/health", timeout=1) as response:
                        health = json.load(response)["data"]
                    if health.get("instance") == runtime.get("instance"):
                        self.url = runtime["url"]
                        return
                except (OSError, ValueError, KeyError):
                    pass
            time.sleep(.05)
        raise AssertionError("v0.2 service did not become ready")

    def stop(self):
        assert self.process is not None
        self.cli("--stop")
        out, _ = self.process.communicate(timeout=30)
        assert self.process.returncode == 0, out
        self.process = None
        self.url = ""

    def cleanup(self):
        if self.process is not None:
            if self.process.poll() is None:
                self.process.terminate()
            self.process.communicate(timeout=30)
            self.process = None

    def api(self, method, path, data=None, status=200):
        payload = json.dumps(data, ensure_ascii=False).encode() if data is not None else None
        request = urllib.request.Request(
            self.url + path, payload, method=method,
            headers={"Content-Type": "application/json", "X-ProjNote-Request": "1"},
        )
        try:
            with urllib.request.urlopen(request, timeout=30) as response:
                body = response.read()
                assert response.status == status, (method, path, response.status, body)
                return json.loads(body)["data"]
        except urllib.error.HTTPError as exc:
            body = exc.read()
            assert exc.code == status, (method, path, exc.code, body)
            return json.loads(body)

    def get(self, kind, entity_id):
        return self.api("GET", f"/api/{kind}/{entity_id}")

    def upload_png(self, image):
        boundary = "ProjNoteV02EvidenceBoundary"
        upload = (
            f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="evidence.png"\r\n'
            'Content-Type: image/png\r\n\r\n'
        ).encode() + image + f"\r\n--{boundary}--\r\n".encode()
        request = urllib.request.Request(
            self.url + "/api/attachments", upload,
            headers={"Content-Type": "multipart/form-data; boundary=" + boundary,
                     "X-ProjNote-Request": "1"},
        )
        with urllib.request.urlopen(request, timeout=30) as response:
            assert response.status == 201
            return json.load(response)["data"]

    def download(self, path):
        with urllib.request.urlopen(self.url + path, timeout=30) as response:
            return response.read()


def item_version(item):
    return {"id": item["id"], "version": item["version"]}


def search_for(server, text):
    return server.api("GET", "/api/search?" + urllib.parse.urlencode({"q": text, "limit": 100}))


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary")
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve(strict=True))
    with tempfile.TemporaryDirectory(prefix="projnote-v02-") as temp:
        temp_root = Path(temp)
        root = temp_root / "原始 工作数据"
        root.mkdir()
        target = temp_root / "迁移 后 数据"
        target.mkdir()
        busy = socket.socket()
        busy.bind(("127.0.0.1", 0))
        busy.listen()
        port = busy.getsockname()[1]
        server = BinaryServer(binary, root, port)
        relocated = None
        try:
            server.start()
            assert int(server.url.rsplit(":", 1)[1]) > port, "occupied port was not skipped"
            settings = server.api("GET", "/api/settings")
            dataset_id = settings["dataset_id"]
            assert dataset_id and settings["gjb_level"] == "2", settings
            assert settings["setup_completed"] == "0", settings
            server.api("PUT", "/api/settings", {"dataset_id": "cannot-replace"}, 400)
            server.api("PUT", "/api/settings", {"gjb_level": "3"}, 400)
            settings = server.api("PUT", "/api/settings", {
                "operator": "二级验收人员", "setup_completed": "1", "backup_auto": "0",
                "backup_keep_count": "2", "backup_max_mb": "0",
            })
            assert settings["dataset_id"] == dataset_id and settings["gjb_level"] == "2"
            assert settings["setup_completed"] == "1"
            info = server.api("GET", "/api/system/info")
            assert info["dataset_id"] == dataset_id and info["gjb_level"] == "2", info
            assert Path(info["data_dir"]) == root, info

            project = server.api("POST", "/api/projects", {"name": "二级闭环项目"}, 201)
            pid = project["id"]
            parent_a = server.api("POST", "/api/tasks", {"project_id": pid, "name": "准备阶段"}, 201)
            parent_b = server.api("POST", "/api/tasks", {"project_id": pid, "name": "交付阶段"}, 201)
            leaf = server.api("POST", "/api/tasks", {
                "project_id": pid, "parent_id": parent_a["id"], "name": "二级任务叶子", "estimate_hours": 3,
            }, 201)
            parent_a = server.get("tasks", parent_a["id"])
            parent_b = server.get("tasks", parent_b["id"])
            leaf = server.get("tasks", leaf["id"])
            server.api("POST", f'/api/tasks/{leaf["id"]}/move', {
                "version": leaf["version"], "parent_id": parent_b["id"],
                "expected_versions": {parent_b["id"]: parent_b["version"] + 1},
            }, 409)
            assert server.get("tasks", leaf["id"])["parent_id"] == parent_a["id"]
            leaf = server.api("POST", f'/api/tasks/{leaf["id"]}/move', {
                "version": leaf["version"], "parent_id": parent_b["id"],
                "expected_versions": {parent_a["id"]: parent_a["version"], parent_b["id"]: parent_b["version"]},
            })
            parent_b = server.get("tasks", parent_b["id"])
            assert leaf["parent_id"] == parent_b["id"]
            assert leaf["wbs_code"].startswith(parent_b["wbs_code"] + "."), leaf
            server.api("POST", f'/api/tasks/{parent_b["id"]}/move', {
                "version": parent_b["version"], "parent_id": leaf["id"],
            }, 400)
            leaf = server.api("PATCH", f'/api/tasks/{leaf["id"]}', {"version": leaf["version"], "status": "done"})
            assert server.get("tasks", parent_b["id"])["status"] == "done"
            server.api("POST", f'/api/tasks/{leaf["id"]}/reopen', {"version": leaf["version"], "reason": ""}, 400)
            leaf = server.api("POST", f'/api/tasks/{leaf["id"]}/reopen', {
                "version": leaf["version"], "reason": "重新核对交付物",
            })
            assert leaf["status"] == "doing" and leaf["actual_end"] == "" and leaf["progress"] == 0
            assert server.get("tasks", parent_b["id"])["status"] == "doing"

            first = server.api("POST", "/api/tasks", {"project_id": pid, "name": "批量任务一"}, 201)
            second = server.api("POST", "/api/tasks", {"project_id": pid, "name": "批量任务二"}, 201)
            first = server.get("tasks", first["id"])
            second = server.get("tasks", second["id"])
            stale_second = item_version(second)
            second = server.api("PATCH", f'/api/tasks/{second["id"]}', {
                "version": second["version"], "assignee": "另一标签页",
            })
            server.api("POST", "/api/tasks/batch", {
                "items": [item_version(first), stale_second], "changes": {"assignee": "不应部分保存"},
            }, 409)
            assert server.get("tasks", first["id"])["assignee"] == first["assignee"]
            assert server.get("tasks", first["id"])["version"] == first["version"]
            assert server.get("tasks", second["id"])["assignee"] == "另一标签页"
            batch = server.api("POST", "/api/tasks/batch", {
                "items": [item_version(first), item_version(second)],
                "changes": {"assignee": "批量验收负责人", "plan_end": "2026-12-01"},
            })
            assert batch["total"] == 2
            for task_id in [first["id"], second["id"]]:
                task = server.get("tasks", task_id)
                assert task["assignee"] == "批量验收负责人" and task["plan_end"] == "2026-12-01"
            first = server.get("tasks", first["id"])
            second = server.get("tasks", second["id"])
            server.api("POST", f'/api/tasks/{second["id"]}/move', {
                "version": second["version"], "parent_id": "", "before_id": first["id"],
                "expected_versions": {first["id"]: first["version"]},
            })
            first_after_move = server.get("tasks", first["id"])
            second_after_move = server.get("tasks", second["id"])
            assert int(second_after_move["wbs_code"]) < int(first_after_move["wbs_code"])

            image = base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
            attachment = server.upload_png(image)
            marker = "二级回收原图证据"
            record = server.api("POST", "/api/records", {
                "content": marker, "tags": ["二级"], "attachments": [attachment],
            }, 201)
            other_record = server.api("POST", "/api/records", {"content": "二级待归类记录"}, 201)
            unclassified = server.api("GET", "/api/records?unclassified=1")
            assert {record["id"], other_record["id"]}.issubset({item["id"] for item in unclassified["items"]})
            classified = server.api("POST", "/api/records/batch-classify", {
                "items": [item_version(record), item_version(other_record)], "project_id": pid,
            })
            assert classified["total"] == 2 and all(item["project_id"] == pid for item in classified["items"])
            assert server.api("GET", "/api/records?unclassified=1")["total"] == 0
            record = server.get("records", record["id"])
            other_record = server.get("records", other_record["id"])
            server.api("POST", "/api/records/batch-classify", {"items": [item_version(other_record)], "project_id": ""})
            unclassified = server.api("GET", "/api/records?unclassified=1")
            assert [item["id"] for item in unclassified["items"]] == [other_record["id"]]
            assert record["content"] == marker and record["attachments"][0]["id"] == attachment["id"]
            assert "二级" in record["tags"]

            deleted = server.api("DELETE", f'/api/records/{record["id"]}?version={record["version"]}')
            assert deleted["deleted"] is True
            server.api("GET", f'/api/records/{record["id"]}', status=404)
            assert record["id"] not in {item["entity_id"] for item in search_for(server, marker)["items"]}
            trash = server.api("GET", "/api/trash?kind=records")
            trashed_record = next(item for item in trash["items"] if item["id"] == record["id"])
            assert trashed_record["deleted_at"] and trashed_record["entity_type"] == "records"
            assert server.download(attachment["url"]) == image, "soft delete physically removed original image"
            server.api("POST", f'/api/trash/records/{record["id"]}/restore', {"version": record["version"]}, 409)
            record = server.api("POST", f'/api/trash/records/{record["id"]}/restore', {"version": trashed_record["version"]})
            assert not record["deleted_at"] and record["content"] == marker
            assert record["id"] in {item["entity_id"] for item in search_for(server, marker)["items"]}
            other_record = server.get("records", other_record["id"])
            server.api("DELETE", f'/api/records/{other_record["id"]}?version={other_record["version"]}')

            # Force real verified full archives through retention, then retain one externally.
            for _ in range(3):
                snapshot = server.api("POST", "/api/backups", {}, 201)
            backups = server.api("GET", "/api/backups?limit=100")
            ordinary = [item for item in backups["items"] if not item["protected"]]
            assert len(ordinary) <= 2 and any(item["name"] == snapshot["name"] for item in ordinary), backups
            health = server.api("GET", "/api/system/info")["backup"]
            assert health["last_success"] and health["last_error"] == "" and health["in_progress"] is False, health
            archive = temp_root / "二级 完整备份.zip"
            archive.write_bytes(server.download(f'/api/backups/{snapshot["name"]}/download'))
            plan = server.api("POST", "/api/system/maintenance-plan", {"action": "relocate", "target_dir": str(target)})
            assert Path(plan["target_dir"]) == target and len(plan["commands"]) >= 3
            assert any("--relocate" in command and str(target) in command for command in plan["commands"]), plan
            server.cli("--relocate", target, success=False)
            assert not list(target.iterdir()), "active-service relocate changed target"
            record = server.api("PATCH", f'/api/records/{record["id"]}', {
                "version": record["version"], "content": "备份之后修改，应恢复为原证据",
            })
            after_snapshot = server.api("POST", "/api/records", {"content": "备份后创建，应恢复移除"}, 201)
            server.stop()
            check = server.cli("--verify-backup", archive)
            assert "校验通过" in check.stdout, check.stdout
            result = server.cli("--restore", archive)
            assert "恢复完成" in result.stdout and "回滚备份" in result.stdout, result.stdout
            server.start()
            assert server.api("GET", "/api/settings")["dataset_id"] == dataset_id
            assert server.api("GET", "/api/settings")["setup_completed"] == "1"
            assert server.get("records", record["id"])["content"] == marker
            server.api("GET", f'/api/records/{after_snapshot["id"]}', status=404)
            server.api("GET", f'/api/records/{other_record["id"]}', status=404)
            assert other_record["id"] in {item["id"] for item in server.api("GET", "/api/trash")["items"]}
            assert server.download(attachment["url"]) == image
            assert server.api("GET", "/api/audit/verify")["ok"] is True
            assert any(item["protected"] for item in server.api("GET", "/api/backups?limit=100")["items"])
            server.stop()

            result = server.cli("--relocate", target)
            assert "迁移完成" in result.stdout and "原目录" in result.stdout, result.stdout
            assert (root / "app.db").is_file() and (target / "app.db").is_file()
            assert (root / "attachments" / attachment["id"] / "original").read_bytes() == image
            assert (target / "attachments" / attachment["id"] / "original").read_bytes() == image
            relocated = BinaryServer(binary, target, port)
            relocated.start()
            assert relocated.api("GET", "/api/settings")["dataset_id"] == dataset_id
            assert relocated.api("GET", "/api/settings")["gjb_level"] == "2"
            assert relocated.get("records", record["id"])["content"] == marker
            assert relocated.get("tasks", leaf["id"])["status"] == "doing"
            assert relocated.download(attachment["url"]) == image
            assert relocated.api("GET", "/api/audit/verify")["ok"] is True
            assert any(item["protected"] for item in relocated.api("GET", "/api/backups?limit=100")["items"])
            relocated.stop()
            # The original remains separately readable with unchanged dataset identity.
            server.start()
            assert server.api("GET", "/api/settings")["dataset_id"] == dataset_id
            assert server.get("records", record["id"])["content"] == marker
            assert server.api("GET", "/api/audit/verify")["ok"] is True
            server.stop()
            print("PASS v0.2: fixed level/setup/dataset, move/reopen, atomic batch 409, record classification, soft delete/search/trash restore, verified retention, v2 full backup/restore, protected rollback, Chinese spaced-path relocation with source and dataset preserved")
        finally:
            if relocated is not None:
                relocated.cleanup()
            server.cleanup()
            busy.close()


if __name__ == "__main__":
    main()
