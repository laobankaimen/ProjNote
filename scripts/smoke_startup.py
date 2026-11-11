#!/usr/bin/env python3
"""Check login registration and task completion with disposable Linux fixtures.

XDG_CONFIG_HOME is private to this script and its child processes. No real
login entry, desktop settings, or existing workspace is changed.
"""
import argparse
from datetime import date
import json
import os
from pathlib import Path
import socket
import sys
import tempfile

from smoke_v02 import BinaryServer


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary')
    args = parser.parse_args()
    if not sys.platform.startswith('linux'):
        parser.error('此脚本仅验证Linux临时XDG目录；Windows使用平台单测和实机验证')
    binary = str(Path(args.binary).resolve(strict=True))
    with tempfile.TemporaryDirectory(prefix='projnote-login-') as tmp:
        root = Path(tmp)
        os.environ['XDG_CONFIG_HOME'] = str(root / '用户配置')
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        server = BinaryServer(binary, root / '工作空间 中文', port)
        try:
            server.start()
            server.api('PUT', '/api/settings', {'backup_auto': '0', 'operator': '登录验收人'})
            path = '/api/system/autostart'
            state = server.api('GET', path)
            assert state['supported'] and not state['enabled'] and not state['needs_repair'], state
            entry = root / '用户配置/autostart/org.projnote.ProjNote.desktop'
            assert not entry.exists(), 'read-only status registered a login entry'
            state = server.api('PUT', path, {'enabled': True})
            assert state['enabled'] and state['matches_current'] and not state['needs_repair'], state
            assert Path(state['entry_path']) == entry and state['executable'] == binary
            assert Path(state['data_dir']) == server.root
            text = entry.read_text()
            assert binary in text and str(server.root) in text
            for flag in ['--browser', '--compact', '--no-browser', '--no-desktop']:
                assert flag not in text, text
            server.api('PUT', path, {'enabled': True, 'executable': '/tmp/other'}, 400)
            assert entry.read_text() == text
            entry.write_text(text.replace('Exec=', 'Exec=/tmp/obsolete '))
            state = server.api('GET', path)
            assert state['enabled'] and not state['matches_current'] and state['needs_repair'], state
            state = server.api('PUT', path, {'enabled': True})
            assert state['matches_current'] and not state['needs_repair'] and entry.read_text() == text

            today = date.today().isoformat()
            project = server.api('POST', '/api/projects', {'name': '每日任务验收'}, 201)
            parent = server.api('POST', '/api/tasks', {'project_id': project['id'], 'name': '父任务'}, 201)
            task = server.api('POST', '/api/tasks', {'project_id': project['id'], 'parent_id': parent['id'], 'name': '完成一项', 'plan_start': today, 'plan_end': today}, 201)
            other = server.api('POST', '/api/tasks', {'project_id': project['id'], 'parent_id': parent['id'], 'name': '保留待办', 'plan_start': today, 'plan_end': today}, 201)
            draft = server.api('GET', '/api/desktop/draft')
            draft = server.api('PUT', '/api/desktop/draft', {'version': draft['version'], 'content': '第一行\n第二行仍保留', 'project_id': project['id'], 'mode': 'record'})
            done = server.api('PATCH', '/api/tasks/' + task['id'], {'version': task['version'], 'status': 'done', 'progress': 100})
            assert done['status'] == 'done' and done['progress'] == 100
            server.api('PATCH', '/api/tasks/' + task['id'], {'version': task['version'], 'status': 'done', 'progress': 100}, 409)
            items = server.api('GET', '/api/today')['tasks']
            assert task['id'] not in {t['id'] for t in items}
            assert other['id'] in {t['id'] for t in items}
            assert next(t for t in items if t['id'] == parent['id'])['has_children']
            assert server.get('tasks', parent['id'])['progress'] == 50
            assert server.api('GET', '/api/desktop/draft') == draft
            note = server.api('POST', '/api/records', {'content': '网页第一行\n网页第二行', 'type': 'note'}, 201)
            assert note['content'] == '网页第一行\n网页第二行'

            server.stop()
            server.start()
            assert server.api('GET', path)['matches_current']
            assert server.api('GET', '/api/desktop/draft') == draft
            state = server.api('PUT', path, {'enabled': False})
            assert not state['enabled'] and not entry.exists()
            assert server.api('GET', '/api/health')['version'].startswith('0.5.1')
            assert server.api('GET', '/api/audit/verify')['ok']
            server.stop()
            print('PASS: isolated login entry enable/repair/disable, fixed paths and restart; single-task completion/rollup/409; multiline note and desktop draft preserved')
        finally:
            server.cleanup()


if __name__ == '__main__':
    main()
