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
 let source=await readFile(input,'utf8');if(relative==='src/App.vue')source=source.replace('<Transition name="toast">','<Transition :css="false" name="toast">')
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
const listeners=new Map();globalThis.window={addEventListener(name,fn){if(!listeners.has(name))listeners.set(name,new Set());listeners.get(name).add(fn)},removeEventListener(name,fn){listeners.get(name)?.delete(fn)},confirm:()=>true}
globalThis.HTMLInputElement=class {};globalThis.Document=class {};globalThis.ShadowRoot=class {}
globalThis.document={querySelector(){return null},activeElement:null,visibilityState:'visible',addEventListener(){},removeEventListener(){}}
const node=(type='node')=>({type,tagName:type.toUpperCase(),children:[],parent:null,value:'',multiple:false,selectionStart:0,open:false,addEventListener(){},removeEventListener(){},setAttribute(){},removeAttribute(){},querySelector(){return null},getRootNode(){return document},focus(){document.activeElement=this},showModal(){this.open=true},close(){this.open=false},get options(){return this.children.filter(c=>c.type==='option')}})
const renderer=createRenderer({insertStaticContent(content,parent,anchor){const n=node('static');n.text=content;n.parent=parent;const at=anchor?parent.children.indexOf(anchor):-1;if(at<0)parent.children.push(n);else parent.children.splice(at,0,n);return[n,n]},createElement:node,createText:text=>({...node('text'),text}),createComment:text=>({...node('comment'),text}),insert(child,parent,anchor){child.parent=parent;const i=anchor?parent.children.indexOf(anchor):-1;if(i<0)parent.children.push(child);else parent.children.splice(i,0,child)},remove(child){if(child.parent)child.parent.children=child.parent.children.filter(n=>n!==child)},setText(n,text){n.text=text},setElementText(n,text){n.text=text;n.children=[]},parentNode:n=>n.parent,nextSibling(n){if(!n.parent)return null;return n.parent.children[n.parent.children.indexOf(n)+1]||null},patchProp(n,key,_previous,value){n[key]=value}})
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


globalThis.location={hash:'#/projects',protocol:'http:'}
const p={id:'p',name:'研发项目',status:'active',code:'P',version:1},q={...p,id:'q',name:'第二项目'}
const task={id:'one',project_id:'p',parent_id:'',wbs_code:'1',name:'导航验证任务',assignee:'',assignee_member_id:'',plan_start:'',plan_end:'',status:'doing',progress:10,version:4,priority:2,estimate_hours:1,description:'',has_children:false}
const requests=[]
function response(data){return new Response(JSON.stringify({data}),{status:200,headers:{'Content-Type':'application/json'}})}
globalThis.fetch=async(url,options={})=>{
 requests.push({url,method:options.method||'GET'})
 if(url==='/api/settings')return response({operator:'用户',dataset_id:'navigation-test',setup_completed:'1'})
 if(url.startsWith('/api/projects')&&!url.includes('/tree')&&!url.includes('/members')&&!url.includes('/plan-versions')&&!url.includes('/coverage'))return response({items:[p,q],total:2})
 if(url==='/api/projects/p/tree')return response({items:[task],total:1})
 if(url==='/api/tasks/one')return response(task)
 if(url==='/api/projects/p/members')return response({items:[],total:0})
 if(url.startsWith('/api/requirements?')||url.includes('/plan-versions?')||url.endsWith('/plan-versions'))return response({items:[],total:0})
 if(url==='/api/projects/p/coverage')return response({uncovered_requirements:[],uncovered_tasks:[],acceptance_attention:[],stats:{}})
 if(url.startsWith('/api/history')||url.includes('/history')||url.startsWith('/api/tasks/one/requirements'))return response({items:[],total:0})
 throw Error('Unexpected '+url)
}
let app
try{
 await symlink(path.join(webRoot,'node_modules'),path.join(temp,'node_modules'),'dir');await compile('src/App.vue')
 const {default:App}=await import(pathToFileURL(path.join(temp,'src/App.mjs')).href)
 let root=node('root');app=renderer.createApp(App);app.mount(root);await settle()
 const mainNav=walk(root,n=>n['aria-label']==='主导航');assert.ok(mainNav)
 assert.ok(!text(mainNav).includes('任务与 WBS'));assert.ok(!text(mainNav).includes('进度视图'));assert.ok(text(mainNav).includes('工作记录'))
 await walk(root,n=>n.type==='button'&&text(n)==='任务计划').onClick(key());await settle()
 assert.equal(location.hash,'#/projects/p/tasks');assert.ok(text(walk(root,n=>n['aria-label']==='面包屑')).includes('研发项目'))
 async function hash(value){location.hash=value;await Promise.all([...listeners.get('hashchange')].map(fn=>fn()));await settle()}
 await hash('#/projects/p/progress');assert.ok(requests.some(r=>r.url==='/api/projects/p/tree'));assert.ok(text(walk(root,n=>n['aria-label']==='面包屑')).includes('进度视图'))
 await hash('#/projects/p/plan-versions');assert.ok(requests.some(r=>r.url.startsWith('/api/projects/p/plan-versions')))
 await hash('#/projects/p/requirements');assert.ok(requests.some(r=>r.url.startsWith('/api/requirements?')&&r.url.includes('project_id=p')))
 await hash('#/tasks?id=one');assert.equal(location.hash,'#/projects/p/tasks?id=one','legacy entity link resolves its actual project')
 assert.ok(walk(root,n=>n.type==='dialog'&&n.open&&text(n).includes('编辑任务')),'task details open under project breadcrumbs')
 const before=requests.length;await hash('#/projects');assert.equal(location.hash,'#/projects');assert.equal(requests.filter(r=>r.method!=='GET').length,0,'navigation never writes business data')
 app.unmount();app=undefined
 location.hash='#/projects/p/progress';root=node('root');app=renderer.createApp(App);app.mount(root);await settle()
 assert.equal(location.hash,'#/projects/p/progress','refresh retains project route');assert.ok(text(walk(root,n=>n['aria-label']==='面包屑')).includes('研发项目'))
 console.log('PASS: real App project entry, reduced sidebar, scoped tabs and breadcrumbs, task deep-link context, refresh restores progress project; navigation performs no mutations')
}finally{app?.unmount();await rm(temp,{recursive:true,force:true})}
