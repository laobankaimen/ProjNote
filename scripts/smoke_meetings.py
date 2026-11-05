#!/usr/bin/env python3
"""Exercise meeting CRUD, saved exports and complete backup recovery in /tmp."""
import argparse
import io
import json
from pathlib import Path
import socket
import tempfile
import urllib.parse
import zipfile

from smoke_v02 import BinaryServer
from smoke_v032 import completed_job, digest, export_job, read_docx


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary')
    parser.add_argument('--expected-version', default='0.5.0')
    parser.add_argument('--expected-schema', type=int, default=9)
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve(strict=True))
    with tempfile.TemporaryDirectory(prefix='projnote-meetings-') as temp:
        root = Path(temp)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        original = BinaryServer(binary, root / '会议原目录 中文', port)
        restored = BinaryServer(binary, root / '恢复目录 中文', port)
        try:
            original.start()
            assert original.api('GET', '/api/health')['version'].startswith(args.expected_version)
            original.api('PUT', '/api/settings', {'operator': '纪要验收人', 'backup_auto': '0'})
            project = original.api('POST', '/api/projects', {'name': '会议协调项目'}, 201)
            meeting = original.api('POST', '/api/meeting-minutes', {
                'title': '现场协调会议', 'meeting_date': '2026-11-05', 'location': '一号会议室',
                'attendees': '张三、李四', 'project_id': project['id'],
                'content': '## 讨论与决议\n周五完成联调\n## 待办\n张三整理交付材料\n<script>保留文本</script>',
            }, 201)
            assert meeting['code'].startswith('MTG-')
            path = '/api/meeting-minutes/' + meeting['id']
            original.api('PATCH', path, {'version': 0, 'content': '过期内容'}, 409)
            meeting = original.api('PATCH', path, {'version': meeting['version'], 'content': meeting['content'] + '\n李四负责复核'})
            query = urllib.parse.urlencode({'q': '一号会议室', 'kind': 'meeting_minutes', 'date_from': '2026-11-05', 'date_to': '2026-11-05'})
            assert original.api('GET', '/api/search?' + query)['total'] == 1
            exports = []
            for fmt in ['docx', 'md', 'json', 'xlsx', 'html']:
                job = completed_job(original, export_job(original, 'meeting_minutes', meeting, fmt))
                content = original.download('/api/export-jobs/' + job['id'] + '/download')
                assert digest(content) == job['output_sha256']
                if fmt == 'docx':
                    text = read_docx(content)
                elif fmt == 'xlsx':
                    with zipfile.ZipFile(io.BytesIO(content)) as document:
                        assert document.testzip() is None
                        text = ''.join(document.read(name).decode() for name in document.namelist() if name.endswith('.xml'))
                elif fmt == 'json':
                    text = json.loads(content)['content']
                else:
                    text = content.decode()
                for marker in ['现场协调会议', '2026-11-05', '一号会议室', '张三、李四', '周五完成联调', '李四负责复核']:
                    assert marker in text, (fmt, marker)
                if fmt == 'html':
                    assert '<script>保留文本' not in text
                    assert original.download('/api/export-jobs/' + job['id'] + '/print') == content
                assert job['source_snapshot']['fields'] == meeting
                exports.append(job)
            original.api('DELETE', path, {'version': meeting['version']})
            original.api('GET', path, status=404)
            assert original.api('GET', '/api/search?' + query)['total'] == 0
            recycled = original.api('GET', '/api/trash?kind=meeting_minutes')['items'][0]
            meeting = original.api('POST', '/api/trash/meeting_minutes/' + meeting['id'] + '/restore', {'version': recycled['version']})
            assert original.api('GET', path + '/history')['total'] == 4
            assert original.api('GET', '/api/search?' + query)['total'] == 1
            draft = original.api('GET', '/api/desktop/draft')
            draft = original.api('PUT', '/api/desktop/draft', {'version': draft['version'], 'mode': 'task', 'project_id': project['id'], 'content': '关闭后应保留的任务草稿'})
            package = original.api('POST', '/api/backups', {}, 201)
            archive = root / '完整会议备份.zip'
            archive.write_bytes(original.download('/api/backups/' + package['name'] + '/download'))
            with zipfile.ZipFile(archive) as document:
                assert int.from_bytes(document.read('app.db')[60:64], 'big') == args.expected_schema
                assert not any(name.startswith('exports/') for name in document.namelist())
            original.stop()
            assert '校验通过' in restored.cli('--verify-backup', archive).stdout
            assert '恢复完成' in restored.cli('--restore', archive).stdout
            restored.start()
            assert restored.api('GET', path) == meeting
            assert restored.api('GET', '/api/desktop/draft') == draft
            assert restored.api('GET', '/api/search?' + query)['total'] == 1
            assert restored.api('GET', path + '/history')['total'] == 4
            for job in exports:
                recovered = restored.get('export-jobs', job['id'])
                assert recovered['state'] == 'failed' and recovered['error']
                assert recovered['source_snapshot'] == job['source_snapshot']
            completed_job(restored, export_job(restored, 'meeting_minutes', meeting, 'docx'))
            assert restored.api('GET', '/api/audit/verify')['ok'] is True
            restored.stop()
            print('PASS: meeting CRUD/409/history/search/recycle, five exports with frozen metadata, complete backup/restore and draft preservation, re-export after recovery')
        finally:
            original.cleanup()
            restored.cleanup()


if __name__ == '__main__':
    main()
