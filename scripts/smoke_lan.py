#!/usr/bin/env python3
"""Exercise optional LAN HTTPS with two member sessions and isolated real data."""
import argparse
import http.cookiejar
import io
import json
from pathlib import Path
import socket
import ssl
import tempfile
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from datetime import date
from smoke_v02 import BinaryServer
from smoke_v032 import completed_job, export_job

class Member:
    def __init__(self,base,certificate):
        self.base=base
        context=ssl.create_default_context(cafile=str(certificate))
        self.jar=http.cookiejar.CookieJar()
        self.opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=context),urllib.request.HTTPCookieProcessor(self.jar))
    def api(self,method,path,value=None,status=200):
        raw=None if value is None else json.dumps(value,ensure_ascii=False).encode()
        req=urllib.request.Request(self.base+path,raw,method=method,headers={'Content-Type':'application/json','X-ProjNote-Request':'1','Origin':self.base})
        try:
            response=self.opener.open(req,timeout=30)
        except urllib.error.HTTPError as e:
            response=e
        with response:
            data=response.read()
            assert response.code==status,(method,path,response.code,data)
            result=json.loads(data)
            return result.get('data',result)

def free_port():
    with socket.socket() as sock:
        sock.bind(('127.0.0.1',0))
        return sock.getsockname()[1]

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('binary')
    args=parser.parse_args()
    binary=str(Path(args.binary).resolve(strict=True))
    with tempfile.TemporaryDirectory(prefix='projnote-lan-') as tmp:
        root=Path(tmp)
        server=BinaryServer(binary,root/'负责人 中文',free_port())
        restored=BinaryServer(binary,root/'恢复 中文',free_port())
        today=date.today().isoformat()
        try:
            server.start()
            server.api('PUT','/api/settings',{'backup_auto':'0','operator':'验收负责人'})
            assert server.api('GET','/api/lan/status')['running'] is False
            p=server.api('POST','/api/projects',{'name':'甲项目'},201)
            other=server.api('POST','/api/projects',{'name':'乙项目'},201)
            t=server.api('POST','/api/tasks',{'project_id':p['id'],'name':'成员甲任务','description':'非成员可见私密备注'},201)
            second=server.api('POST','/api/tasks',{'project_id':other['id'],'name':'成员乙任务'},201)
            m=server.api('POST','/api/lan/members',{'name':'同名成员','project_ids':[p['id']],'task_ids':[t['id']]},201)
            n=server.api('POST','/api/lan/members',{'name':'同名成员','project_ids':[other['id']],'task_ids':[second['id']]},201)
            lanport=free_port()
            info=server.api('POST','/api/lan/control',{'enabled':True,'port':lanport})
            assert info['running'] and info['certificate_sha256'] and info['port']==lanport
            cert=root/'信任的服务证书.crt'
            cert.write_bytes(server.download('/api/lan/certificate'))
            base='https://127.0.0.1:'+str(lanport)
            a,b=Member(base,cert),Member(base,cert)
            for person,client in [(m,a),(n,b)]:
                pairing=server.api('POST','/api/lan/members/'+person['id']+'/pairing',{'version':person['version']},201)
                pair=client.api('POST','/api/lan/pair',{'code':pairing['code'],'device_name':'真实二进制验收'})
                assert 'token' not in pair
                assert any(cookie.secure and cookie.has_nonstandard_attr('HttpOnly') for cookie in client.jar)
                client.api('POST','/api/lan/pair',{'code':pairing['code']},401)
            workspace=a.api('GET','/api/lan/workspace')
            assert len(workspace['projects'])==1 and workspace['projects'][0]['id']==p['id']
            assert '私密备注' not in json.dumps(workspace,ensure_ascii=False)
            for path in ['/api/settings','/api/backups','/api/records','/api/projects','/api/search?q=test','/api/lan/members','/api/lan/daily-entries','/api/export-jobs','/api/trash']:
                a.api('GET',path,status=403)
            current=next(task for task in workspace['tasks'] if task['id']==t['id'])
            a.api('PATCH','/api/lan/tasks/'+second['id'],{'version':1,'progress':20},403)
            a.api('PATCH','/api/lan/tasks/'+t['id'],{'version':current['version'],'progress':60,'status':'doing'})
            a.api('PATCH','/api/lan/tasks/'+t['id'],{'version':current['version'],'progress':70},409)
            a.api('PATCH','/api/lan/tasks/'+t['id'],{'version':current['version'],'assignee':'伪造负责人'},400)
            entry=a.api('POST','/api/lan/my-daily',{'project_id':p['id'],'work_date':today,'content':'甲项目完成接口联调','next_plan':'明天核对材料','blockers':'','minutes':120})
            b.api('POST','/api/lan/my-daily',{'project_id':other['id'],'work_date':today,'content':'乙项目完成安装','next_plan':'复测','blockers':'','minutes':90})
            assert a.api('GET','/api/lan/my-daily?member_id='+n['id'])['total']==1
            a.api('POST','/api/lan/my-daily',{'project_id':other['id'],'work_date':today,'content':'跨项目尝试','minutes':1},403)
            entries=server.api('GET','/api/lan/daily-entries')
            assert entries['total']==2 and {x['member_id'] for x in entries['items']}=={m['id'],n['id']}
            draft=server.api('POST','/api/project-daily-logs/generate',{'project_id':p['id'],'log_date':today})
            assert '甲项目完成接口联调' in draft['content'] and '乙项目完成安装' not in draft['content']
            assert draft['source_snapshot']['total_minutes']==120
            saved=server.api('POST','/api/project-daily-logs',draft,201)
            entry=a.api('POST','/api/lan/my-daily',{**{k:entry[k] for k in ['id','version','project_id','work_date']},'content':'甲项目更新后的内容','next_plan':'继续核对','blockers':'','minutes':180})
            assert server.api('GET','/api/project-daily-logs/'+saved['id'])['content']==saved['content']
            updated=server.api('POST','/api/project-daily-logs/generate',{'project_id':p['id'],'log_date':today})
            assert updated['source_snapshot']['total_minutes']==180
            server.api('POST','/api/lan/members/'+m['id']+'/revoke',{'version':m['version']})
            a.api('GET','/api/lan/workspace',status=401)
            # Preserve a real session in backup; restored service must revoke it.
            backup=server.api('POST','/api/backups',{},201)
            archive=root/'成员完整备份.zip'
            archive.write_bytes(server.download('/api/backups/'+backup['name']+'/download'))
            with zipfile.ZipFile(archive) as z:
                assert int.from_bytes(z.read('app.db')[60:64],'big')==9
                assert not any(name.startswith('lan/') for name in z.namelist())
            assert server.api('GET','/api/audit/verify')['ok']
            server.api('POST','/api/lan/control',{'enabled':False,'port':lanport})
            server.stop()
            assert '恢复完成' in restored.cli('--restore',archive).stdout
            restored.start()
            restored_members=restored.api('GET','/api/lan/members')
            assert restored_members['total']==2
            assert restored.api('GET','/api/lan/daily-entries')['total']==2
            # Saved LAN settings start a new TLS listener and certificate. Use
            # its own new trust, then offer the old cookie: it must be rejected.
            restored_info=restored.api('GET','/api/lan/status')
            assert restored_info['running']
            newcert=root/'恢复后证书.crt'
            newcert.write_bytes(restored.download('/api/lan/certificate'))
            old=Member('https://127.0.0.1:'+str(restored_info['port']),newcert)
            for cookie in b.jar:old.jar.set_cookie(cookie)
            old.api('GET','/api/lan/workspace',status=401)
            assert restored.api('GET','/api/audit/verify')['ok']
            restored.api('POST','/api/lan/control',{'enabled':False,'port':restored_info['port']})
            restored.stop()
            print('PASS: optional trusted TLS, two same-name scoped members, one-time pairing/cookies, route isolation, task409, own logs/hours, leader summary, revocation, schema9 backup/restore without reviving sessions')
        finally:
            server.cleanup();restored.cleanup()

if __name__=='__main__':main()
