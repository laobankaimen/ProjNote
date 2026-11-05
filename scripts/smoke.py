#!/usr/bin/env python3
"""Exercise the real binary against temporary data, including offline restore."""
import argparse
import base64
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("binary")
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve())
    with tempfile.TemporaryDirectory(prefix="projnote-smoke-") as tmp:
        root = Path(tmp) / "data"
        root.mkdir()
        busy = socket.socket()
        busy.bind(("127.0.0.1", 0))
        busy.listen()
        port = busy.getsockname()[1]
        process = None

        def launch():
            p = subprocess.Popen([binary, "--data-dir", str(root), "--port", str(port), "--no-browser"], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            for _ in range(100):
                runtime = root / "runtime.json"
                if runtime.exists():
                    try:
                        info = json.loads(runtime.read_text())
                        urllib.request.urlopen(info["url"] + "/api/health", timeout=1).close()
                        return p, info["url"]
                    except (ValueError, OSError):
                        pass
                if p.poll() is not None:
                    raise AssertionError(p.communicate()[0])
                time.sleep(.05)
            p.terminate()
            raise AssertionError("service failed to start")

        def stop(p, check_drain=False):
            slow = None
            payload = json.dumps({"name": "排空期间的项目"}).encode()
            if check_drain:
                from urllib.parse import urlparse
                endpoint = urlparse(url)
                slow = socket.create_connection((endpoint.hostname, endpoint.port))
                slow.sendall((f"POST /api/projects HTTP/1.1\r\nHost: {endpoint.netloc}\r\nContent-Type: application/json\r\nX-ProjNote-Request: 1\r\nContent-Length: {len(payload)}\r\nConnection: close\r\n\r\n").encode() + payload[:1])
                time.sleep(.1)
            subprocess.run([binary, "--data-dir", str(root), "--stop"], check=True, capture_output=True, text=True)
            if slow is not None:
                time.sleep(.4)
                assert p.poll() is None, "shutdown did not drain active request"
                slow.sendall(payload[1:])
                slow.settimeout(10)
                response = bytearray()
                while True:
                    chunk = slow.recv(4096)
                    if not chunk:
                        break
                    response.extend(chunk)
                slow.close()
                assert b"201 Created" in response, response
            out, _ = p.communicate(timeout=20)
            assert p.returncode == 0, out

        try:
            process, url = launch()
            assert int(url.rsplit(":", 1)[1]) > port, "occupied port was not skipped"

            def api(method, path, data=None, status=200):
                payload = json.dumps(data, ensure_ascii=False).encode() if data is not None else None
                req = urllib.request.Request(url + path, payload, method=method, headers={"Content-Type": "application/json", "X-ProjNote-Request": "1"})
                try:
                    with urllib.request.urlopen(req, timeout=20) as res:
                        body = res.read()
                        assert res.status == status, body
                        return json.loads(body)["data"]
                except urllib.error.HTTPError as exc:
                    body = exc.read()
                    assert exc.code == status, (exc.code, body)
                    return json.loads(body)

            project = api("POST", "/api/projects", {"name": "现场联调", "manager": "测试人员"}, 201)
            pid = project["id"]
            parent = api("POST", "/api/tasks", {"project_id": pid, "name": "联调准备"}, 201)
            child = api("POST", "/api/tasks", {"project_id": pid, "parent_id": parent["id"], "name": "电源温升检查", "estimate_hours": 4}, 201)
            child = api("PATCH", "/api/tasks/" + child["id"], {"version": child["version"], "progress": 50, "status": "doing"})
            assert api("GET", "/api/tasks/" + parent["id"])["progress"] == 50
            api("PATCH", "/api/tasks/" + child["id"], {"version": 1, "progress": 60}, 409)
            image = base64.b64decode("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
            boundary = "ProjNoteSmokeBoundary"
            upload = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="evidence.png"\r\nContent-Type: image/png\r\n\r\n'.encode() + image + f"\r\n--{boundary}--\r\n".encode())
            req = urllib.request.Request(url + "/api/attachments", upload, headers={"Content-Type": "multipart/form-data; boundary=" + boundary, "X-ProjNote-Request": "1"})
            with urllib.request.urlopen(req) as response:
                attachment = json.load(response)["data"]
            record = api("POST", "/api/records", {"content": "电源温升正常 #巡检 @现场联调", "attachments": [attachment]}, 201)
            assert record["project_id"] == pid
            assert "巡检" in record["tags"]
            image_record = api("POST", "/api/records", {"attachments": [attachment]}, 201)
            assert len(image_record["attachments"]) == 1
            assert api("GET", "/api/search?q=" + urllib.parse.quote("电源温"))["total"] >= 2
            assert api("GET", "/api/search?q=" + urllib.parse.quote("巡检"))["total"] >= 1
            issue = api("POST", "/api/issues", {"project_id": pid, "title": "接线松动", "severity": "major"}, 201)
            for state in ["analyzing", "fixing", "verifying"]:
                issue = api("POST", "/api/issues/" + issue["id"] + "/transition", {"version": issue["version"], "status": state})
            api("POST", "/api/issues/" + issue["id"] + "/transition", {"version": issue["version"], "status": "closed"}, 400)
            issue = api("POST", "/api/issues/" + issue["id"] + "/transition", {"version": issue["version"], "status": "closed", "root_cause": "紧固不足", "solution": "重新紧固", "verify_result": "复检通过"})
            assert issue["closed_at"]
            today = api("GET", "/api/today")
            draft = api("POST", "/api/worklogs/generate", {"date": today["date"], "period_type": "day", "project_id": pid})
            assert "接线松动" in draft["content"]
            api("POST", "/api/worklogs", draft, 201)
            api("POST", "/api/worklogs", draft, 409)
            api("PUT", "/api/settings", {"operator": "验收人员"})
            backup = api("POST", "/api/backups", {}, 201)
            archive = Path(tmp) / "restore.zip"
            with urllib.request.urlopen(url + "/api/backups/" + backup["name"] + "/download") as response:
                archive.write_bytes(response.read())
            api("POST", "/api/records", {"content": "备份后新增，应被恢复移除"}, 201)
            assert api("GET", "/api/audit/verify")["ok"]
            second = subprocess.run([binary, "--data-dir", str(root), "--no-browser"], capture_output=True, text=True, timeout=10)
            assert second.returncode == 0 and "已在运行" in second.stdout, second.stdout
            stop(process)
            process = None
            result = subprocess.run([binary, "--verify-backup", str(archive)], check=True, capture_output=True, text=True, timeout=30)
            assert "校验通过" in result.stdout
            result = subprocess.run([binary, "--data-dir", str(root), "--restore", str(archive)], check=True, capture_output=True, text=True, timeout=30)
            assert "回滚备份" in result.stdout and "恢复完成" in result.stdout, result.stdout
            process, url = launch()
            records = api("GET", "/api/records")["items"]
            assert len(records) == 2, records
            assert api("GET", "/api/audit/verify")["ok"]
            with urllib.request.urlopen(url + attachment["url"]) as response:
                assert response.read() == image
            stop(process, check_drain=True)
            process = None
            print("PASS: port fallback, single instance, CRUD, tree rollup, 409, images, Chinese search, issue closure, logs, audit, independent backup, validation and offline restore")
        finally:
            busy.close()
            if process is not None and process.poll() is None:
                process.terminate()
                process.communicate(timeout=20)


if __name__ == "__main__":
    main()
