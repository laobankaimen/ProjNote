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
globalThis.HTMLInputElement=class {};globalThis.Document=class {};globalThis.ShadowRoot=class {}
globalThis.document={activeElement:null,visibilityState:'visible',addEventListener(){},removeEventListener(){}}
const node=(type='node')=>({type,tagName:type.toUpperCase(),children:[],parent:null,value:'',multiple:false,selectionStart:0,open:false,addEventListener(){},removeEventListener(){},setAttribute(){},removeAttribute(){},querySelector(){return null},getRootNode(){return document},focus(){document.activeElement=this},showModal(){this.open=true},close(){this.open=false},get options(){return this.children.filter(c=>c.type==='option')}})
const renderer=createRenderer({createElement:node,createText:text=>({...node('text'),text}),createComment:text=>({...node('comment'),text}),insert(child,parent,anchor){child.parent=parent;const i=anchor?parent.children.indexOf(anchor):-1;if(i<0)parent.children.push(child);else parent.children.splice(i,0,child)},remove(child){if(child.parent)child.parent.children=child.parent.children.filter(n=>n!==child)},setText(n,text){n.text=text},setElementText(n,text){n.text=text;n.children=[]},parentNode:n=>n.parent,nextSibling(n){if(!n.parent)return null;return n.parent.children[n.parent.children.indexOf(n)+1]||null},patchProp(n,key,_previous,value){n[key]=value}})
function walk(n,predicate){if(predicate(n))return n;for(const child of n.children){const found=walk(child,predicate);if(found)return found}}
function text(n){return [n.text||'',...n.children.map(text)].join('')}
function key(properties={}){return {key:'Enter',isComposing:false,keyCode:13,ctrlKey:false,metaKey:false,altKey:false,shiftKey:false,defaultPrevented:false,preventDefault(){this.defaultPrevented=true},stopPropagation(){},...properties}}
async function setDateTime(dialog,date,hour,minute){
 const picker=walk(dialog,n=>n.class==='local-datetime');assert.ok(picker,'separate date and time picker exists')
 walk(picker,n=>n.type==='date').onInput({target:{value:date}});await settle()
 const selects=picker.children.filter(n=>n.type==='label').map(n=>walk(n,c=>c.type==='select')).filter(Boolean)
 selects[0].onChange({target:{value:hour}});await settle();selects[1].onChange({target:{value:minute}});await settle()
 assert.equal(selects[0].children.filter(n=>n.type==='option').length,25);assert.equal(selects[1].children.filter(n=>n.type==='option').length,61)
}
async function settle(){for(let i=0;i<5;i++){await nextTick();await new Promise(resolve=>setImmediate(resolve))}}


function matches(root,predicate){const out=[];function visit(n){if(predicate(n))out.push(n);for(const c of n.children)visit(c)}visit(root);return out}
const requests=[],task={id:'one',project_id:'p',parent_id:'',wbs_code:'1',name:'原任务',assignee:'',assignee_member_id:'',plan_start:'2026-11-13',plan_end:'2026-11-16',status:'doing',progress:10,version:4,priority:2,estimate_hours:1,description:'',has_children:false}
const fixtures=[task,{...task,id:'two',wbs_code:'2',name:'第二任务',version:1},{...task,id:'done',wbs_code:'3',name:'已完成任务',status:'done',progress:100,version:1}]
function response(data,status=200){return new Response(JSON.stringify(status===200?{data}:{error:{code:'version_conflict',message:'任务已更新'}}),{status,headers:{'Content-Type':'application/json'}})}
globalThis.fetch=async(url,options={})=>{
 const method=options.method||'GET',body=options.body?JSON.parse(options.body):undefined;requests.push({url,method,body})
 if(url==='/api/projects/p/tree')return response({items:fixtures.map(t=>({...t})),total:fixtures.length})
 if(url==='/api/projects/p/members'||url==='/api/projects/q/members')return response({items:[],total:0})
 if(url==='/api/tasks/one'&&method==='PATCH'){assert.equal(body.version,task.version);Object.assign(task,body,{version:task.version+1});return response({...task})}
 if(url==='/api/tasks/one/move')return response({id:task.id})
 if(url.startsWith('/api/requirements?'))return response({items:[],total:0})
 throw Error('Unexpected request '+method+' '+url)
}
let app
try{
 await symlink(path.join(webRoot,'node_modules'),path.join(temp,'node_modules'),'dir')
 for(const file of ['src/pages/TasksPage.vue','src/projectRoutes.ts','src/explicitSave.ts'])await compile(file)
 const {ui}=await import(pathToFileURL(path.join(temp,'src/api.mjs')).href);ui.datasetId='explicit-task-workflow';ui.projects=[{id:'p',name:'研发项目',status:'active'},{id:'q',name:'另一个项目',status:'active'}]
 const {readRoute,projectHref}=await import(pathToFileURL(path.join(temp,'src/projectRoutes.mjs')).href)
 assert.deepEqual(readRoute(projectHref('项目 一','progress','t1')),{page:'progress',projectId:'项目 一',entityId:'t1'})
 for(const view of ['tasks','progress','plan-versions','requirements'])assert.equal(readRoute(projectHref('p',view)).projectId,'p')
 assert.deepEqual(readRoute('#/tasks?id=old'),{page:'tasks',projectId:'',entityId:'old'})
 const {preventImplicitSave}=await import(pathToFileURL(path.join(temp,'src/explicitSave.mjs')).href)
 globalThis.Element=class {constructor(form){this.form=form}closest(){return {closest:()=>this.form}}}
 let event=key({target:new Element({matches:()=>false})});preventImplicitSave(event);assert.ok(event.defaultPrevented,'plain Enter cannot implicitly save changes')
 event=key({target:new Element({matches:()=>true})});preventImplicitSave(event);assert.equal(event.defaultPrevented,false,'read-only queries keep Enter')
 const {default:TasksPage}=await import(pathToFileURL(path.join(temp,'src/pages/TasksPage.mjs')).href)
 let root=node('root');app=renderer.createApp(TasksPage,{revision:0,initialProject:'p'});app.mount(root);await settle()
 const button=(name)=>walk(root,n=>n.type==='button'&&text(n).trim()===name)
 assert.equal(walk(root,n=>n['aria-label']==='筛选项目'),undefined,'project is fixed by route')
 assert.equal(matches(root,n=>n.class==='task-drag-slot').length,3,'all rows retain the same leading geometry')
 assert.equal(matches(root,n=>String(n.class).split(' ').includes('drag-handle')).length,0,'no drag handles until movement is requested')
 const tabs=walk(root,n=>n['aria-label']==='项目功能');assert.equal(matches(tabs,n=>n.type==='a').length,4);for(const a of matches(tabs,n=>n.type==='a'))assert.ok(a.href.startsWith('#/projects/p/'),'all tabs retain the current project')
 await walk(root,n=>n['aria-label']==='编辑原任务的任务名称').onClick();await settle()
 const editor=walk(root,n=>n.class==='inline-edit'),input=walk(editor,n=>n.tagName==='INPUT'&&n.type==='text')
 input['onUpdate:modelValue']('手工修改');await settle()
 assert.equal(editor.onFocusout,undefined,'focus loss has no save handler')
 event=key({target:{closest:()=>null}});editor.onKeydown(event);await settle();assert.ok(event.defaultPrevented)
 assert.equal(requests.filter(r=>r.method==='PATCH').length,0,'Enter and changing the draft do not write')
 await walk(editor,n=>n.type==='button'&&text(n)==='保存').onClick();await settle()
 assert.deepEqual(requests.findLast(r=>r.method==='PATCH').body,{name:'手工修改',version:4})
 await button('移动任务').onClick();await settle()
 assert.equal(matches(root,n=>String(n.class).split(' ').includes('drag-handle')).length,2,'movement mode shows handles only for writable tasks')
 assert.equal(matches(root,n=>n.class==='task-drag-slot').length,3,'movement does not change row geometry')
 const firstRow=walk(root,n=>n['data-task-id']==='one')
 await walk(firstRow,n=>n.type==='button'&&text(n)==='移动').onClick();await settle()
 const moveDialog=walk(root,n=>n.type==='dialog'&&n.open);assert.ok(moveDialog)
 const target=walk(moveDialog,n=>n.type==='select');target['onUpdate:modelValue']('two');await settle()
 assert.equal(requests.filter(r=>r.url.endsWith('/move')).length,0,'selecting move destination is a preview')
 await walk(moveDialog,n=>n.type==='form').onSubmit(key());await settle()
 assert.equal(requests.filter(r=>r.url.endsWith('/move')).length,1,'only saving applies the move')
 await button('结束移动').onClick();await settle();assert.equal(matches(root,n=>String(n.class).split(' ').includes('drag-handle')).length,0)
 app.unmount();app=undefined
 console.log('PASS: project routes and context tabs, stable WBS slots, explicit move mode, inline edits never save on blur/Enter, click-save with version, move preview until save, implicit mutation submit blocked')
}finally{app?.unmount();await rm(temp,{recursive:true,force:true})}
