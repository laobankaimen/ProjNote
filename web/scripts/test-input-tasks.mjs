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
const base={id:'leaf',version:4,project_id:'active',name:'完成联调',status:'doing',progress:50,has_children:false}
let tasks=[base,{...base,id:'parent',name:'汇总任务',has_children:true},{...base,id:'closed',project_id:'closed',name:'结项任务'},{...base,id:'archived',project_id:'archived',name:'归档任务'}]
let conflict=false,autostartFailure=false
let autostartState={supported:true,enabled:false,matches_current:true,needs_repair:false,entry_path:'/tmp/mock-autostart/ProjNote.desktop',executable:'/tmp/mock/ProjNote',data_dir:'/tmp/mock-data',message:''}
function response(data,status=200){return new Response(JSON.stringify(status===200?{data}:{error:{code:'version_conflict',message:'任务版本已更新'}}),{status,headers:{'Content-Type':'application/json'}})}
globalThis.fetch=async(url,options={})=>{
 requests.push({url,method:options.method||'GET',body:options.body?JSON.parse(options.body):undefined})
 if(url==='/api/system/autostart'){if(autostartFailure)return new Response(JSON.stringify({error:{code:'system_error',message:'无法读取系统启动项'}}),{status:503});if(options.method==='PUT')autostartState={...autostartState,enabled:JSON.parse(options.body).enabled,needs_repair:false,matches_current:true};return response(autostartState)}
 if(url==='/api/settings')return response({operator:'测试人员',desktop_default:'1'})
 if(url==='/api/health')return response({version:'0.5.1-dev'})
 if(url==='/api/system/info')return response({data_dir:'/tmp/mock-data',backup:{available_bytes:1024,last_error:'',warning:'',in_progress:false}})
 if(url.startsWith('/api/backups'))return response({items:[],total:0})
 if(url.startsWith('/api/tags'))return response({items:['测试'],total:1})
 if(url==='/api/today')return response({date:'2026-11-11',tasks:tasks.filter(t=>t.status!=='done'),records:[],reminders:[],stats:{projects:3,tasks_open:tasks.filter(t=>t.status!=='done').length,issues_open:0,records_today:0}})
 if(url==='/api/records')return response({id:'record',version:1,...JSON.parse(options.body)})
 if(url.startsWith('/api/tasks/')){if(conflict)return response(null,409);const body=JSON.parse(options.body);tasks=tasks.map(t=>t.id===url.split('/').pop()?{...t,...body,version:t.version+1}:t);return response(tasks.find(t=>t.id===url.split('/').pop()))}
 throw new Error('Unexpected request '+url)
}
let app
try{
 await symlink(path.join(webRoot,'node_modules'),path.join(temp,'node_modules'),'dir')
 await compile('src/components/RecordModal.vue');await compile('src/pages/CompactPage.vue');await compile('src/pages/TodayPage.vue');await compile('src/pages/SettingsPage.vue')
 const {ui}=await import(pathToFileURL(path.join(temp,'src/api.mjs')).href)
 ui.datasetId='keyboard-regression';ui.projects=[{id:'active',name:'研发',code:'RD',status:'active'},{id:'closed',name:'已结项',status:'closed'},{id:'archived',name:'已归档',status:'archived'}]
 const {default:RecordModal}=await import(pathToFileURL(path.join(temp,'src/components/RecordModal.mjs')).href)
 let root=node('root');app=renderer.createApp(RecordModal,{open:true});app.mount(root);await settle()
 let textarea=walk(root,n=>n.type==='textarea')
 textarea['onUpdate:modelValue']('第一行');await settle()
 for(const event of [key(),key({shiftKey:true}),key({ctrlKey:true,isComposing:true}),key({ctrlKey:true,keyCode:229})]){textarea.onKeydown(event);assert.equal(event.defaultPrevented,false,'newline and IME events must remain native')}
 assert.equal(requests.filter(r=>r.method==='POST').length,0,'ordinary Enter never saves')
 textarea['onUpdate:modelValue']('@');textarea.selectionStart=1;await settle();textarea.onInput();await settle()
 assert.ok(walk(root,n=>n.class==='completion-list'),'project completion suggestions are present')
 const enter=key();textarea.onKeydown(enter);assert.equal(enter.defaultPrevented,false,'suggestions cannot consume Enter')
 const tab=key({key:'Tab',keyCode:9});textarea.onKeydown(tab);await settle();assert.equal(tab.defaultPrevented,true);assert.equal(textarea.value,'@研发 ','Tab explicitly accepts the suggestion')
 textarea['onUpdate:modelValue']('第一行\n第二行');await settle()
 const ctrl=key({ctrlKey:true});textarea.onKeydown(ctrl);await settle();assert.equal(ctrl.defaultPrevented,true)
 assert.equal(requests.filter(r=>r.url==='/api/records').at(-1).body.content,'第一行\n第二行')
 const meta=key({metaKey:true});textarea.onKeydown(meta);await settle();assert.equal(meta.defaultPrevented,true)
 assert.equal(requests.filter(r=>r.url==='/api/records').length,2,'Ctrl and Command shortcuts both save explicitly')
 app.unmount()
 const {default:CompactPage}=await import(pathToFileURL(path.join(temp,'src/pages/CompactPage.mjs')).href)
 root=node('root');app=renderer.createApp(CompactPage,{revision:0});app.mount(root);await settle()
 textarea=walk(root,n=>n.type==='textarea');textarea['onUpdate:modelValue']('保留\n这份速记');await settle()
 const compactEnter=key();textarea.onKeydown(compactEnter);assert.equal(compactEnter.defaultPrevented,false)
 let finish=walk(root,n=>n['aria-label']==='完成任务 完成联调');assert.ok(finish)
 assert.equal(walk(root,n=>n['aria-label']==='完成任务 汇总任务'),undefined,'parent task has no completion control')
 assert.equal(walk(root,n=>n['aria-label']==='完成任务 结项任务').disabled,true)
 assert.equal(walk(root,n=>n['aria-label']==='完成任务 归档任务').disabled,true)
 conflict=true;await finish.onClick();await settle()
 assert.equal(textarea.value,'保留\n这份速记','409 leaves note editor untouched')
 assert.ok(text(root).includes('任务版本已更新'),'conflict is shown')
 conflict=false;finish=walk(root,n=>n['aria-label']==='完成任务 完成联调');await finish.onClick();await settle()
 assert.equal(walk(root,n=>n['aria-label']==='完成任务 完成联调'),undefined,'confirmed completion leaves today list')
 assert.equal(textarea.value,'保留\n这份速记')
 assert.deepEqual(requests.filter(r=>r.url==='/api/tasks/leaf').at(-1).body,{version:4,status:'done',progress:100})
 app.unmount()
 tasks=[base,...tasks.filter(t=>t.id!=='leaf')]
 const {default:TodayPage}=await import(pathToFileURL(path.join(temp,'src/pages/TodayPage.mjs')).href)
 root=node('root');app=renderer.createApp(TodayPage,{revision:0});app.mount(root);await settle()
 assert.equal(walk(root,n=>n['aria-label']==='完成任务 汇总任务'),undefined)
 assert.equal(walk(root,n=>n['aria-label']==='完成任务 结项任务').disabled,true)
 await walk(root,n=>n['aria-label']==='完成任务 完成联调').onClick();await settle()
 assert.equal(walk(root,n=>n['aria-label']==='完成任务 完成联调'),undefined)
 app.unmount()
 const {default:SettingsPage}=await import(pathToFileURL(path.join(temp,'src/pages/SettingsPage.mjs')).href)
 root=node('root');app=renderer.createApp(SettingsPage);app.mount(root);await settle()
 assert.equal(requests.filter(r=>r.url==='/api/system/autostart'&&r.method==='PUT').length,0,'settings never registers autostart on load')
 let startupForm=walk(root,n=>n.class==='autostart-preference')
 let startupCheckbox=walk(startupForm,n=>n.type==='checkbox')
 assert.equal(startupCheckbox.disabled,false);assert.equal(startupCheckbox.checked,false)
 startupCheckbox['onUpdate:modelValue'](true);await settle();await startupForm.onSubmit(key());await settle()
 assert.equal(autostartState.enabled,true);assert.equal(requests.filter(r=>r.url==='/api/system/autostart'&&r.method==='PUT').length,1)
 assert.equal(walk(root,n=>n.class==='desktop-startup-preference').children[0].children.find(n=>n.type==='checkbox').checked,true,'desktop visibility remains independent')
 autostartState={...autostartState,needs_repair:true,matches_current:false}
 await walk(startupForm,n=>n.tagName==='BUTTON'&&text(n)==='重新检查').onClick();await settle()
 await walk(startupForm,n=>n.tagName==='BUTTON'&&text(n)==='修复启动项').onClick();await settle();assert.equal(autostartState.needs_repair,false)
 startupCheckbox=walk(startupForm,n=>n.type==='checkbox');startupCheckbox['onUpdate:modelValue'](false);await settle();await startupForm.onSubmit(key());await settle();assert.equal(autostartState.enabled,false)
 const before=requests.filter(r=>r.url==='/api/system/autostart'&&r.method==='PUT').length
 autostartFailure=true;await walk(startupForm,n=>n.tagName==='BUTTON'&&text(n)==='重新检查').onClick();await settle()
 assert.equal(walk(startupForm,n=>n.type==='checkbox').disabled,true);assert.ok(text(startupForm).includes('无法读取系统启动项'))
 assert.equal(walk(startupForm,n=>n.tagName==='BUTTON'&&text(n)==='保存自动启动设置').disabled,true)
 await startupForm.onSubmit(key());await settle();assert.equal(requests.filter(r=>r.url==='/api/system/autostart'&&r.method==='PUT').length,before,'failed status read cannot mutate system settings')
 console.log('PASS: real SFC handlers preserve Enter/IME, Tab completion and Ctrl/Cmd save; leaf completion/version payload, parent/read-only guards, 409 retains note, refreshed Today/Compact lists; independent autostart load/enable/repair/disable/error guard (in-memory Vue renderer)')
}finally{try{app?.unmount()}catch{}await rm(temp,{recursive:true,force:true})}
