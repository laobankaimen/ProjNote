#!/usr/bin/env python3
"""Check plan and requirement versions, exports, restart and full restore in /tmp."""
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
    parser.add_argument('--expected-version', default='0.5.1')
    parser.add_argument('--expected-schema',type=int,default=9)
    args = parser.parse_args()
    binary = str(Path(args.binary).resolve(strict=True))
    with tempfile.TemporaryDirectory(prefix='projnote-versions-') as tmp:
        root = Path(tmp)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1', 0))
            port = sock.getsockname()[1]
        server = BinaryServer(binary, root / '计划需求 中文原目录', port)
        restored = BinaryServer(binary, root / '恢复 中文目录', port)
        try:
            server.start()
            assert server.api('GET', '/api/health')['version'].startswith(args.expected_version)
            server.api('PUT', '/api/settings', {'operator':'版本验收人', 'backup_auto':'0'})
            p = server.api('POST', '/api/projects', {'name':'设备接口计划'}, 201)
            pid = p['id']
            task = server.api('POST', '/api/tasks', {'project_id':pid, 'name':'现场接口开发', 'assignee':'张三', 'plan_start':'2026-11-05', 'plan_end':'2026-11-06'}, 201)
            removable = server.api('POST', '/api/tasks', {'project_id':pid, 'name':'待移除项'}, 201)
            for n in range(51):
                server.api('POST', '/api/tasks', {'project_id':pid, 'name':f'其他计划任务{n:02d}'}, 201)
            path = f'/api/projects/{pid}/plan-versions'
            first = server.api('POST', path, {'name':'首次确认计划', 'description':'完整任务快照', 'project_version':p['version']}, 201)
            assert first['task_count'] == 53 and len(first['snapshot']['tasks']) == 53
            assert json.loads(server.download(f'/api/plan-versions/{first["id"]}/download')) == first
            req = server.api('POST', '/api/requirements', {'project_id':pid, 'title':'接口重连', 'description':'重连时保留现场输入', 'acceptance_criteria':'恢复后内容无丢失且无重复提交', 'owner':'张三', 'source':'MTG会议决定', 'task_ids':[task['id']]}, 201)
            rid, reqpath = req['id'], '/api/requirements/' + req['id']
            reqfirst = server.api('GET', reqpath + '/revisions/1')
            assert reqfirst['snapshot']['linked_tasks'][0]['name'] == '现场接口开发'
            task = server.api('PATCH', '/api/tasks/' + task['id'], {'version':task['version'], 'name':'接口开发改期', 'assignee':'李四', 'plan_end':'2026-11-09'})
            server.api('DELETE', '/api/tasks/' + removable['id'], {'version':removable['version']})
            newtask = server.api('POST', '/api/tasks', {'project_id':pid, 'name':'新增验证项'}, 201)
            comp = server.api('GET', path + '/compare?' + urllib.parse.urlencode({'from':first['id'], 'to':'current'}))
            assert comp['summary']['added'] == 1 and comp['summary']['removed'] == 1 and comp['summary']['changed'] >= 1, comp
            second = server.api('POST', path, {'name':'第二版协调计划', 'project_version':p['version']}, 201)
            assert first == server.api('GET', '/api/plan-versions/' + first['id'])
            comp = server.api('GET', path + '/compare?' + urllib.parse.urlencode({'from':first['id'], 'to':second['id']}))
            assert comp['summary']['added'] == 1 and comp['summary']['removed'] == 1
            server.api('PATCH', '/api/plan-versions/' + first['id'], {'name':'覆盖旧计划'}, 409)
            server.api('DELETE', '/api/plan-versions/' + first['id'], {}, 409)
            req = server.api('PATCH', reqpath, {'version':req['version'], 'description':'保留输入并防止重复请求', 'task_ids':[task['id'], newtask['id']], 'change_note':'增加验证项并补充重复提交约束'})
            assert req['revision'] == 2
            assert server.api('GET', reqpath + '/revisions/1') == reqfirst
            comp = server.api('GET', reqpath + '/revisions/compare?from=1&to=2')
            assert comp['changed'] and newtask['id'] in comp['task_links']['added']
            server.api('PATCH', reqpath, {'version':1, 'description':'旧页面覆盖', 'change_note':'旧稿'}, 409)
            server.api('PATCH', reqpath, {'version':req['version'], 'status':'accepted', 'change_note':'尚缺验收说明'}, 400)
            req = server.api('PATCH', reqpath, {'version':req['version'], 'status':'accepted', 'acceptance_note':'按准则复测，输入完整且没有重复请求', 'change_note':'复测完成并确认验收'})
            assert req['revision'] == 3 and req['status'] == 'accepted'
            server.api('PATCH', reqpath, {'version':req['version'], 'description':'验收后不明确重开', 'change_note':'不能静默修改'}, 409)
            server.api('PATCH', reqpath + '/revisions/1', {'snapshot':{}}, 409)
            assert server.api('GET', '/api/search?' + urllib.parse.urlencode({'q':'接口重连','kind':'requirements'}))['total'] == 1
            assert server.api('GET', '/api/search?' + urllib.parse.urlencode({'q':'首次确认计划','kind':'plan_versions'}))['total'] == 1
            exported = []
            for kind, source in [('plan_versions',first), ('requirements',req)]:
                for fmt in ['docx','md','json','xlsx','html']:
                    job = completed_job(server, export_job(server, kind, source, fmt))
                    content = server.download(f'/api/export-jobs/{job["id"]}/download')
                    assert digest(content) == job['output_sha256']
                    if fmt == 'docx':
                        text = read_docx(content)
                    elif fmt == 'xlsx':
                        with zipfile.ZipFile(io.BytesIO(content)) as archive:
                            assert archive.testzip() is None
                            text = ''.join(archive.read(n).decode() for n in archive.namelist() if n.endswith('.xml'))
                    elif fmt == 'json':
                        text = json.loads(content)['content']
                    else:
                        text = content.decode()
                    if kind == 'plan_versions':
                        assert '首次确认计划' in text and '现场接口开发' in text
                        assert '接口开发改期' not in text
                    else:
                        assert '接口重连' in text and '恢复后内容无丢失且无重复提交' in text
                    exported.append(job)
            draft = server.api('GET','/api/desktop/draft')
            draft = server.api('PUT','/api/desktop/draft', {'version':draft['version'],'mode':'task','content':'后台保留草稿','project_id':pid})
            server.stop()
            server.start()
            assert server.api('GET','/api/plan-versions/' + first['id']) == first
            assert server.api('GET',reqpath) == req
            assert server.api('GET',reqpath + '/revisions/1') == reqfirst
            backup = server.api('POST','/api/backups',{},201)
            filename = root / '完整版本备份.zip'
            filename.write_bytes(server.download('/api/backups/' + backup['name'] + '/download'))
            with zipfile.ZipFile(filename) as archive:
                assert int.from_bytes(archive.read('app.db')[60:64], 'big') == args.expected_schema
            server.stop()
            assert '校验通过' in restored.cli('--verify-backup',filename).stdout
            assert '恢复完成' in restored.cli('--restore',filename).stdout
            restored.start()
            assert restored.api('GET','/api/plan-versions/' + first['id']) == first
            assert restored.api('GET',reqpath) == req
            assert restored.api('GET',reqpath + '/revisions/1') == reqfirst
            assert restored.api('GET','/api/desktop/draft') == draft
            for job in exported:
                after = restored.get('export-jobs',job['id'])
                assert after['state'] == 'failed' and after['source_snapshot'] == job['source_snapshot']
            completed_job(restored,export_job(restored,'plan_versions',first,'json'))
            completed_job(restored,export_job(restored,'requirements',req,'docx'))
            assert restored.api('GET','/api/audit/verify')['ok']
            restored.stop()
            print('PASS: complete 53-task plans/compare/frozen snapshots, explicit requirement revisions/acceptance/task links, 10 exports, restart and full backup/restore/re-export with drafts')
        finally:
            server.cleanup()
            restored.cleanup()


if __name__ == '__main__':
    main()
