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
const temp=await mkdtemp(path.join(tmpdir(),'projnote-input-tasks-'))
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
globalThis.Document=class {};globalThis.ShadowRoot=class {}
globalThis.document={activeElement:null,visibilityState:'visible',addEventListener(){},removeEventListener(){}}
const node=(type='node')=>({type,tagName:type.toUpperCase(),children:[],parent:null,value:'',multiple:false,selectionStart:0,open:false,addEventListener(){},removeEventListener(){},setAttribute(){},removeAttribute(){},querySelector(){return null},getRootNode(){return document},focus(){document.activeElement=this},showModal(){this.open=true},close(){this.open=false},get options(){return this.children.filter(c=>c.type==='option')}})
const renderer=createRenderer({createElement:node,createText:text=>({...node('text'),text}),createComment:text=>({...node('comment'),text}),insert(child,parent,anchor){child.parent=parent;const i=anchor?parent.children.indexOf(anchor):-1;if(i<0)parent.children.push(child);else parent.children.splice(i,0,child)},remove(child){if(child.parent)child.parent.children=child.parent.children.filter(n=>n!==child)},setText(n,text){n.text=text},setElementText(n,text){n.text=text;n.children=[]},parentNode:n=>n.parent,nextSibling(n){if(!n.parent)return null;return n.parent.children[n.parent.children.indexOf(n)+1]||null},patchProp(n,key,_previous,value){n[key]=value}})
function walk(n,predicate){if(predicate(n))return n;for(const child of n.children){const found=walk(child,predicate);if(found)return found}}
function text(n){return [n.text||'',...n.children.map(text)].join('')}
function key(properties={}){return {key:'Enter',isComposing:false,keyCode:13,ctrlKey:false,metaKey:false,altKey:false,shiftKey:false,defaultPrevented:false,preventDefault(){this.defaultPrevented=true},stopPropagation(){},...properties}}
async function settle(){for(let i=0;i<5;i++){await nextTick();await new Promise(resolve=>setImmediate(resolve))}}

const requests=[]
let supported=true,stale=false
const reminder={id:'r1',version:3,task_id:'leaf',task_name:'需要桌面提醒的任务',project_id:'p',project_name:'研发项目',due_at:'2026-11-13T01:00:00Z',notified_at:'',done_at:'',last_error:'',task_status:'doing'}
let reminders=[{...reminder}]
const leaf={id:'leaf',version:4,project_id:'p',wbs_code:'1.1',name:'需要桌面提醒的任务',status:'doing',progress:50,has_children:false}
const taskFixtures=[leaf,{...leaf,id:'parent',name:'汇总任务',has_children:true},{...leaf,id:'done',name:'已完成任务',status:'done'},{...leaf,id:'canceled',name:'已取消任务',status:'canceled'},{...leaf,id:'closed',project_id:'closed',name:'结项项目任务'}]
function response(data,status=200){return new Response(JSON.stringify(status===200?{data}:{error:{code:'version_conflict',message:'提醒版本已更新，请重新核对'}}),{status,headers:{'Content-Type':'application/json'}})}
globalThis.fetch=async(url,options={})=>{
 const method=options.method||'GET',body=options.body?JSON.parse(options.body):undefined;requests.push({url,method,body})
 if(url==='/api/task-reminders/status')return response({supported,description:supported?'到时通过系统桌面通知提醒':'当前无法发送桌面通知'})
 if(url.startsWith('/api/task-reminders?'))return response({items:reminders.filter(x=>!x.done_at),total:reminders.filter(x=>!x.done_at).length})
 if(url.startsWith('/api/tasks?'))return response({items:taskFixtures,total:taskFixtures.length})
 if(url==='/api/task-reminders'&&method==='POST'){const result={...reminder,...body,id:'new',version:1};reminders.push(result);return response(result)}
 if(url==='/api/task-reminders/r1'&&method==='GET')return response(reminders[0])
 if(url==='/api/task-reminders/r1'&&method==='PATCH'){if(stale){stale=false;reminders[0].version++;return response(null,409)}reminders[0]={...reminders[0],...body,version:reminders[0].version+1};return response(reminders[0])}
 if(url.startsWith('/api/task-reminders/')&&url.endsWith('/done')){const id=url.split('/')[3];reminders=reminders.map(x=>x.id===id?{...x,done_at:new Date().toISOString(),version:x.version+1}:x);return response(reminders.find(x=>x.id===id))}
 if(url==='/api/today')return response({date:'2026-11-13',tasks:[leaf],records:[],reminders:[{id:'legacy',title:'旧自由提醒不再当任务提醒显示'}],task_reminders:reminders.filter(x=>!x.done_at),stats:{projects:2,tasks_open:1,issues_open:0,records_today:0,unclassified:0,task_reminders_due:reminders.filter(x=>!x.done_at).length}})
 if(url.startsWith('/api/tags'))return response({items:[],total:0})
 if(url==='/api/records'&&method==='POST')return response({id:'note',version:1,...body})
 throw new Error('Unexpected request '+method+' '+url)
}
let app
try{
 await symlink(path.join(webRoot,'node_modules'),path.join(temp,'node_modules'),'dir')
 await compile('src/pages/RemindersPage.vue');await compile('src/pages/TodayPage.vue');await compile('src/components/RecordModal.vue')
 const {ui}=await import(pathToFileURL(path.join(temp,'src/api.mjs')).href)
 ui.datasetId='task-reminder-regression';ui.projects=[{id:'p',name:'研发项目',status:'active'},{id:'closed',name:'结项项目',status:'closed'}]
 const {default:RemindersPage}=await import(pathToFileURL(path.join(temp,'src/pages/RemindersPage.mjs')).href)
 let root=node('root');app=renderer.createApp(RemindersPage,{revision:0});app.mount(root);await settle()
 const button=label=>walk(root,n=>n.type==='button'&&text(n)===label)
 assert.ok(text(root).includes('到时通过系统桌面通知提醒'),'delivery capability is visible')
 await button('添加任务提醒').onClick();await settle()
 let dialog=walk(root,n=>n.type==='dialog'&&n.open)
 const selects=[];function collect(n){if(n.type==='select')selects.push(n);n.children.forEach(collect)}collect(dialog)
 const projectOptions=text(selects[0]);assert.ok(!projectOptions.includes('结项项目'),'read-only projects cannot create reminders')
 const taskOptions=text(selects[1]);assert.ok(taskOptions.includes(leaf.name));for(const excluded of ['汇总任务','已完成任务','已取消任务','结项项目任务'])assert.ok(!taskOptions.includes(excluded),excluded+' cannot receive a new reminder')
 assert.equal(walk(dialog,n=>n.type==='input'&&n.type!=='datetime-local'),undefined,'no standalone free-text reminder title')
 selects[1]['onUpdate:modelValue']('leaf');walk(dialog,n=>n.type==='datetime-local')['onUpdate:modelValue']('2026-11-13T16:00');await settle()
 await walk(dialog,n=>n.type==='form').onSubmit(key());await settle()
 const created=requests.findLast(r=>r.url==='/api/task-reminders'&&r.method==='POST');assert.deepEqual(Object.keys(created.body).sort(),['due_at','task_id']);assert.equal(created.body.task_id,'leaf')
 await button(leaf.name).onClick();await settle();dialog=walk(root,n=>n.type==='dialog'&&n.open)
 const dateInput=walk(dialog,n=>n.type==='datetime-local');dateInput['onUpdate:modelValue']('2026-11-13T17:30');await settle();stale=true
 await walk(dialog,n=>n.type==='form').onSubmit(key());await settle()
 assert.equal(dateInput.value,'2026-11-13T17:30','409 retains edited reminder time');assert.ok(text(dialog).includes('数据库内容已更新'))
 assert.equal(requests.findLast(r=>r.method==='PATCH').body.version,3)
 await walk(dialog,n=>n['aria-label']==='关闭对话框').onClick();await settle();await button('结束提醒').onClick();await settle()
 assert.ok(reminders[0].done_at);assert.equal(leaf.status,'doing','ending reminder leaves task status unchanged');assert.equal(requests.filter(r=>r.url.startsWith('/api/tasks/')&&r.method==='PATCH').length,0)
 app.unmount();app=undefined
 reminders=[{...reminder}]
 const {default:TodayPage}=await import(pathToFileURL(path.join(temp,'src/pages/TodayPage.mjs')).href)
 root=node('root');app=renderer.createApp(TodayPage,{revision:0});app.mount(root);await settle()
 assert.ok(text(root).includes(reminder.task_name));assert.ok(!text(root).includes('旧自由提醒不再当任务提醒显示'))
 await button('结束提醒').onClick();await settle();assert.ok(reminders[0].done_at);assert.equal(requests.filter(r=>r.url.startsWith('/api/reminders')).length,0,'Today uses only new task-reminder API')
 app.unmount();app=undefined
 const {default:RecordModal}=await import(pathToFileURL(path.join(temp,'src/components/RecordModal.mjs')).href)
 root=node('root');app=renderer.createApp(RecordModal,{open:true});app.mount(root);await settle()
 assert.ok(!text(root).includes('一次提醒时间'));assert.ok(!text(root).includes('!今天'))
 const note=walk(root,n=>n.type==='textarea');note['onUpdate:modelValue']('!明天 09:00\n这只是记录正文');await settle();await walk(root,n=>n.type==='form').onSubmit(key());await settle()
 const savedNote=requests.findLast(r=>r.url==='/api/records');assert.equal(savedNote.body.reminder_due_at,null);assert.equal(savedNote.body.content,'!明天 09:00\n这只是记录正文','removed syntax is preserved as plain text')
 app.unmount();app=undefined
 supported=false;root=node('root');app=renderer.createApp(RemindersPage,{revision:1});app.mount(root);await settle()
 assert.equal(button('添加任务提醒').disabled,true,'unsupported delivery prevents creating nonfunctional reminders')
 console.log('PASS: task-only reminder creation, leaf/open project filtering, desktop delivery feedback, optimistic edit conflict, ending reminder keeps task progress, Today uses new API, removed record syntax explicitly disabled, unavailable notification guard (real Vue SFC handlers)')
}finally{try{app?.unmount()}catch{}await rm(temp,{recursive:true,force:true})}
