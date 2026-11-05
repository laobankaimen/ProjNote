#!/usr/bin/env python3
"""Verify backup-before-migration ordering with real legacy and current binaries."""
import argparse
import json
from pathlib import Path
import sqlite3
import subprocess
import tempfile
import time
import urllib.request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('legacy_binary')
    parser.add_argument('new_binary')
    parser.add_argument('--legacy-schema',type=int,default=1)
    parser.add_argument('--new-schema',type=int,default=3)
    parser.add_argument('--legacy-version',default='0.1')
    parser.add_argument('--new-version',default='0.3')
    args = parser.parse_args()
    legacy = str(Path(args.legacy_binary).resolve())
    current = str(Path(args.new_binary).resolve())
    with tempfile.TemporaryDirectory(prefix='projnote-upgrade-') as tmp:
        root = Path(tmp) / '中文工作空间'
        active = None
        def start(binary):
            process = subprocess.Popen([binary, '--data-dir', str(root), '--no-browser', '--port', '18900'], stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
            for _ in range(100):
                try:
                    runtime = json.loads((root / 'runtime.json').read_text())
                    with urllib.request.urlopen(runtime['url'] + '/api/health', timeout=1) as res:
                        health = json.load(res)['data']
                    return process, runtime['url'], health
                except (OSError, ValueError):
                    if process.poll() is not None:
                        raise AssertionError(process.communicate()[0])
                    time.sleep(.05)
            process.terminate()
            raise AssertionError('startup timed out')
        def stop(binary, process):
            result = subprocess.run([binary, '--data-dir', str(root), '--stop'], capture_output=True, text=True, timeout=30)
            assert result.returncode == 0, result.stdout + result.stderr
            out, _ = process.communicate(timeout=30)
            assert process.returncode == 0, out
            return out
        def api(url, method, path, payload=None):
            data = None if payload is None else json.dumps(payload, ensure_ascii=False).encode()
            request = urllib.request.Request(url + path, data=data, method=method, headers={'Content-Type':'application/json','X-ProjNote-Request':'1'})
            with urllib.request.urlopen(request, timeout=30) as response:
                return json.load(response)['data']
        try:
            active, url, health = start(legacy)
            assert health['version'].startswith(args.legacy_version), health
            project = api(url, 'POST', '/api/projects', {'name':'旧版本真实项目'})
            api(url, 'POST', '/api/records', {'project_id':project['id'], 'content':'升级前的现场事实'})
            stop(legacy, active)
            active = None
            with sqlite3.connect(root / 'app.db') as connection:
                assert connection.execute('PRAGMA user_version').fetchone()[0] == args.legacy_schema
            active, url, health = start(current)
            assert health['version'].startswith(args.new_version), health
            assert api(url,'GET','/api/projects/' + project['id'])['name'] == '旧版本真实项目'
            assert api(url,'GET','/api/records')['total'] == 1
            settings = api(url,'GET','/api/settings')
            assert settings['dataset_id'] and settings['gjb_level'] == '2'
            backups = api(url,'GET','/api/backups')['items']
            protected = [item for item in backups if item['protected']]
            assert protected, backups
            import zipfile
            with zipfile.ZipFile(root / 'backups' / protected[0]['name']) as archive:
                snapshot = Path(tmp) / 'pre-upgrade.db'
                snapshot.write_bytes(archive.read('app.db'))
            with sqlite3.connect(snapshot) as connection:
                assert connection.execute('PRAGMA user_version').fetchone()[0] == args.legacy_schema
                assert connection.execute('SELECT name FROM projects').fetchone()[0] == '旧版本真实项目'
            assert api(url,'GET','/api/audit/verify')['ok']
            out = stop(current, active)
            active = None
            assert '升级前完整备份' in out, out
            with sqlite3.connect(root / 'app.db') as connection:
                assert connection.execute('PRAGMA user_version').fetchone()[0] == args.new_schema
            print(f'PASS: real v{args.legacy_version}→v{args.new_version} migration, original data/audit preserved, protected pre-upgrade snapshot remains schema{args.legacy_schema}')
        finally:
            if active is not None and active.poll() is None:
                active.terminate()
                active.communicate(timeout=30)

if __name__ == '__main__':
    main()
