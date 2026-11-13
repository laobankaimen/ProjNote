import assert from 'node:assert/strict'
import { mkdtemp, readFile, writeFile, mkdir, symlink, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { parse, compileScript } from '@vue/compiler-sfc'
import ts from 'typescript'
import { createRenderer, nextTick } from 'vue'

// Exercise the real SFC event handlers and rendered controls with an in-memory
// renderer. Native textarea newline insertion is verified by leaving Enter free.
const webRoot=fileURLToPath(new URL('..',import.meta.url))
const temp=await mkdtemp(path.join(tmpdir(),'projnote-people-lan-'))
const transformed=new Set()
async function compile(relative){
 if(transformed.has(relative))return
 transformed.add(relative)
 const input=path.join(webRoot,relative),output=path.join(temp,relative.replace(/\.(vue|ts)$/,'.mjs'))
 await mkdir(path.dirname(output),{recursive:true})
 let source=await readFile(input,'utf8')
 if(relative.endsWith('.vue')){const {descriptor}=parse(source,{filename:input});source=compileScript(descriptor,{id:relative,inlineTemplate:true}).content}
 for(const match of [...source.matchAll(/(?:from\s*|import\s*)['"](\.[^'"]+)['"]/g)]){
  let target=path.posix.normalize(path.posix.join(path.posix.dirname(relative),match[1]))
  if(!/\.(vue|ts)$/.test(target))target+='.ts'
  await compile(target)
  source=source.replaceAll(`'${match[1]}'`,`'${match[1].replace(/\.(vue|ts)$/,'')}.mjs'`).replaceAll(`"${match[1]}"`,`"${match[1].replace(/\.(vue|ts)$/,'')}.mjs"`)
 }
 await writeFile(output,ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText)
}
function storage(){const values=new Map();return {get length(){return values.size},key:i=>[...values.keys()][i],getItem:k=>values.get(k)||null,setItem:(k,v)=>values.set(k,v),removeItem:k=>values.delete(k)}}
globalThis.sessionStorage=storage();globalThis.localStorage=storage()
globalThis.window={addEventListener(){},removeEventListener(){},confirm:()=>true}
globalThis.Document=class {};globalThis.ShadowRoot=class {};globalThis.HTMLInputElement=class {}
globalThis.document={activeElement:null,visibilityState:'visible',addEventListener(){},removeEventListener(){}}
const node=(type='node')=>({type,tagName:type.toUpperCase(),children:[],parent:null,value:'',multiple:false,selectionStart:0,open:false,addEventListener(){},removeEventListener(){},setAttribute(){},removeAttribute(){},querySelector(){return null},getRootNode(){return document},focus(){document.activeElement=this},showModal(){this.open=true},close(){this.open=false},get options(){return this.children.filter(c=>c.type==='option')}})
const renderer=createRenderer({createElement:node,createText:text=>({...node('text'),text}),createComment:text=>({...node('comment'),text}),insert(child,parent,anchor){child.parent=parent;const i=anchor?parent.children.indexOf(anchor):-1;if(i<0)parent.children.push(child);else parent.children.splice(i,0,child)},remove(child){if(child.parent)child.parent.children=child.parent.children.filter(n=>n!==child)},setText(n,text){n.text=text},setElementText(n,text){n.text=text;n.children=[]},parentNode:n=>n.parent,nextSibling(n){if(!n.parent)return null;return n.parent.children[n.parent.children.indexOf(n)+1]||null},patchProp(n,key,_previous,value){n[key]=value}})
function walk(n,predicate){if(predicate(n))return n;for(const child of n.children){const found=walk(child,predicate);if(found)return found}}
function text(n){return [n.text||'',...n.children.map(text)].join('')}
function key(properties={}){return {key:'Enter',isComposing:false,keyCode:13,ctrlKey:false,metaKey:false,altKey:false,shiftKey:false,defaultPrevented:false,preventDefault(){this.defaultPrevented=true},stopPropagation(){},...properties}}
async function settle(){for(let i=0;i<5;i++){await nextTick();await new Promise(resolve=>setImmediate(resolve))}}
const requests=[]
let clipboardFails=false,rosterVersion=1
Object.defineProperty(globalThis,'navigator',{configurable:true,value:{clipboard:{writeText:async()=>{if(clipboardFails)throw Error('denied')}}}})
let members=[{id:'member-aaaa',name:'张同事',enabled:true,project_ids:['project'],task_ids:['active','done'],version:1},{id:'member-bbbb',name:'张同事',enabled:true,project_ids:['project'],task_ids:[],version:1}]
const tasks=[{id:'active',project_id:'project',name:'进行中任务',assignee:'张同事',assignee_member_id:'member-aaaa',status:'doing',progress:30,version:4,has_children:false,wbs_code:'1',plan_start:'2026-11-01',plan_end:'2026-11-30'},{id:'done',project_id:'project',name:'已完成任务',assignee:'张同事',assignee_member_id:'member-aaaa',status:'done',progress:100,version:2,has_children:false,wbs_code:'2',plan_start:'2026-11-01',plan_end:'2026-11-30'},{id:'canceled',project_id:'project',name:'已取消任务',assignee:'',status:'canceled',progress:0,version:2,has_children:false,wbs_code:'3'},{id:'parent',project_id:'project',name:'父任务',assignee:'',status:'doing',progress:50,version:1,has_children:true,wbs_code:'4'}]
function response(data,status=200){return new Response(JSON.stringify(status===200?{data}:{error:{message:'版本已变化'}}),{status,headers:{'Content-Type':'application/json'}})}
globalThis.fetch=async(url,options={})=>{
 const body=options.body?JSON.parse(options.body):undefined;requests.push({url,method:options.method||'GET',body});url=url.split('?')[0]
 if(url==='/api/projects/project/members'){if(options.method==='POST'){const member={id:'member-new-'+rosterVersion++,name:body.name,enabled:true,project_ids:['project'],task_ids:[],version:1};members.push(member);return response(member)}return response({items:members,total:members.length})}
 if(url.startsWith('/api/lan/members/')&&url.endsWith('/pairing'))return response({member:members.find(m=>url.includes(m.id)),code:'a'.repeat(64),expires_at:'2026-11-13T12:00:00Z'})
 if(url==='/api/lan/members')return response({items:members,total:members.length})
 if(url==='/api/lan/status')return response({running:true,enabled:true,port:18680,addresses:[],certificate_sha256:'hash'})
 if(url==='/api/projects/project/tree')return response({items:tasks,total:tasks.length})
 if(url==='/api/lan/session')return response({member:members[0]})
 if(url==='/api/lan/workspace')return response({member:members[0],projects:[{id:'project',name:'研发',status:'active',version:1}],tasks:tasks.map(t=>({...t,depth:0,can_update:t.id==='active'})),total:tasks.length,dataset_id:'test-data'})
 if(url.startsWith('/api/lan/my-daily')){if(options.method==='POST')return response({id:'entry',version:1,member_id:members[0].id,...body});return response({items:[],total:0})}
 if(url.startsWith('/api/lan/daily-entries'))return response({items:[],total:0})
 if(url==='/api/project-daily-logs/generate')return response({content:'日志',source_snapshot:{}})
 if(url.startsWith('/api/tasks/'))return response({...tasks[0],...body,version:5})
 throw Error('Unexpected request '+url)
}
let app
try{
 await symlink(path.join(webRoot,'node_modules'),path.join(temp,'node_modules'),'dir')
 for(const source of ['src/components/ProjectMemberSelect.vue','src/components/ProjectMembers.vue','src/components/ProjectAttendees.vue','src/components/TaskInlineCell.vue','src/components/LanSettings.vue','src/pages/MemberWorkspacePage.vue'])await compile(source)
 const {ui}=await import(pathToFileURL(path.join(temp,'src/api.mjs')).href);ui.projects=[{id:'project',name:'研发',status:'active'}];ui.datasetId='people-lan-regression'
 let root=node('root'),picked='',person
 const {default:ProjectMemberSelect}=await import(pathToFileURL(path.join(temp,'src/components/ProjectMemberSelect.mjs')).href)
 app=renderer.createApp(ProjectMemberSelect,{projectId:'project',stable:true,modelValue:'member-aaaa',legacyName:'张同事','onUpdate:modelValue':value=>picked=value,onChosen:value=>person=value});app.mount(root);await settle()
 let select=walk(root,n=>n.type==='select');assert.equal(select.children.filter(n=>n.type==='option'&&text(n).includes('张同事')).length,2,'same-name people remain independent visible ID choices')
 select.onChange({target:{value:'member-bbbb'}});assert.equal(picked,'member-bbbb');assert.equal(person.id,'member-bbbb');app.unmount()
 const {default:ProjectMembers}=await import(pathToFileURL(path.join(temp,'src/components/ProjectMembers.mjs')).href)
 root=node('root');app=renderer.createApp(ProjectMembers,{projectId:'project'});app.mount(root);await settle()
 const nameInput=walk(root,n=>n.type==='input');nameInput['onUpdate:modelValue']('新增同事');await settle();await walk(root,n=>n.type==='button'&&text(n)==='加入项目').onClick();await settle()
 assert.equal(requests.filter(r=>r.url==='/api/projects/project/members'&&r.method==='POST').at(-1).body.name,'新增同事');assert.ok(text(root).includes('新增同事'));app.unmount()
 const {default:ProjectAttendees}=await import(pathToFileURL(path.join(temp,'src/components/ProjectAttendees.mjs')).href)
 let attendees='旧参会人，客户代表';root=node('root');app=renderer.createApp(ProjectAttendees,{projectId:'project',modelValue:attendees,'onUpdate:modelValue':value=>attendees=value});app.mount(root);await settle()
 assert.ok(text(root).includes('旧参会人，客户代表'));assert.equal(attendees,'旧参会人，客户代表','opening an old meeting never changes its participants text')
 let sameNameChoice=walk(root,n=>n.type==='label'&&text(n).includes('member-a'));walk(sameNameChoice,n=>n.type==='checkbox').onChange({target:{checked:true}});assert.equal(attendees,'旧参会人，客户代表、张同事（member-a）','a new attendee comes from roster and keeps same-name identity visible');app.unmount()
 const {default:TaskInlineCell}=await import(pathToFileURL(path.join(temp,'src/components/TaskInlineCell.mjs')).href)
 root=node('root');app=renderer.createApp(TaskInlineCell,{task:tasks[0],field:'assignee',label:'负责人'});app.mount(root);await settle();await walk(root,n=>n.type==='button'&&n['aria-label']==='编辑进行中任务的负责人').onClick();await settle()
 select=walk(root,n=>n.type==='select');select.onChange({target:{value:'member-bbbb'}});await settle();await walk(root,n=>n.type==='button'&&text(n)==='保存').onClick();await settle()
 assert.deepEqual(requests.filter(r=>r.url==='/api/tasks/active').at(-1).body,{assignee_member_id:'member-bbbb',version:4},'task assignee update uses stable member ID, never a name');app.unmount()
 const {default:LanSettings}=await import(pathToFileURL(path.join(temp,'src/components/LanSettings.mjs')).href)
 root=node('root');app=renderer.createApp(LanSettings);app.mount(root);await settle()
 const mainDetails=walk(root,n=>n.type==='details'&&String(n.class).includes('lan-settings'));mainDetails.onToggle({target:{open:true}});await settle()
 const row=walk(root,n=>n.type==='tr'&&text(n).includes('member-bbbb'));await walk(row,n=>n.type==='button'&&text(n)==='编辑授权').onClick();await settle()
 const editor=walk(root,n=>n.type==='dialog'&&text(n).includes('编辑成员与授权'))
 assert.ok(!text(walk(editor,n=>n.class==='scope-tasks')).includes('已完成任务'),'completed task cannot be newly assigned to another member')
 assert.ok(!text(walk(editor,n=>n.class==='scope-tasks')).includes('已取消任务'),'canceled task cannot be newly assigned')
 await walk(editor,n=>n.type==='button'&&text(n)==='取消').onClick();await settle()
 const ownRow=walk(root,n=>n.type==='tr'&&text(n).includes('member-aaaa'));await walk(ownRow,n=>n.type==='button'&&text(n)==='编辑授权').onClick();await settle()
 const historical=walk(root,n=>n.type==='label'&&text(n).includes('已完成任务')&&text(n).includes('历史分配'));assert.ok(historical);assert.equal(walk(historical,n=>n.type==='checkbox').disabled,true)
 await walk(walk(root,n=>n.type==='dialog'&&text(n).includes('编辑成员与授权')),n=>n.type==='button'&&text(n)==='取消').onClick();await settle()
 await walk(ownRow,n=>n.type==='button'&&text(n)==='生成一次配对码').onClick();await settle()
 const pairDialog=walk(root,n=>n.type==='dialog'&&text(n).includes('一次配对码')&&text(n).includes('复制配对码'));let copy=walk(pairDialog,n=>n.type==='button'&&text(n)==='复制配对码');await copy.onClick();await settle();assert.equal(text(copy),'已复制');assert.ok(walk(pairDialog,n=>n.role==='status'&&text(n).includes('配对码已复制')),'success feedback is inside active pairing modal')
 clipboardFails=true;await copy.onClick();await settle();assert.ok(walk(pairDialog,n=>n.role==='alert'&&text(n).includes('手动复制')),'clipboard failure is also visible inside pairing modal');app.unmount()
 const {default:MemberWorkspacePage}=await import(pathToFileURL(path.join(temp,'src/pages/MemberWorkspacePage.mjs')).href)
 root=node('root');app=renderer.createApp(MemberWorkspacePage);app.mount(root);await settle()
 const hours=walk(root,n=>n['aria-label']==='项目工时小时'),minutes=walk(root,n=>n['aria-label']==='项目工时分钟');assert.ok(hours&&minutes,'LAN work duration exposes separate hours/minutes controls')
 hours['onUpdate:modelValue'](1);minutes['onUpdate:modelValue'](35);await settle();await walk(root,n=>n.type==='form'&&n.id==='member-daily-form').onSubmit(key());await settle()
 assert.equal(requests.filter(r=>r.url==='/api/lan/my-daily'&&r.method==='POST').at(-1).body.minutes,95,'1 h 35 min keeps integer-minute API storage')
 hours['onUpdate:modelValue'](24);minutes['onUpdate:modelValue'](1);await settle();assert.equal(walk(root,n=>n.tagName==='BUTTON'&&n.form==='member-daily-form').disabled,true,'duration over one day is blocked')
 console.log('PASS: real SFC project roster management without LAN, same-name stable ID selection, historical meeting attendees and task assignment, terminal-task assignment guard/history, pairing-modal success/failure feedback, LAN hours/minutes payload and daily cap (in-memory Vue renderer)')
}finally{try{app?.unmount()}catch{}await rm(temp,{recursive:true,force:true})}
