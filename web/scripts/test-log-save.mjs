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

globalThis.location={hash:'#/project-journal?project_id=p&log_date=2026-11-13'}
const requests=[]
let mode='personal',failOverwrite=false,sourceConflict=false
const existing={id:'original',version:7,project_id:'p',period_type:'day',log_date:'2026-11-13',title:'原始日志',variant:'',content:'原始内容不可偷偷覆盖',source_snapshot:{generated_at:'old'}}
let stored=[{...existing}]
function response(data,status=200,code='conflict'){return new Response(JSON.stringify(status===200?{data}:{error:{code,message:status===409?'已有日志或内容已更新':'请求失败'}}),{status,headers:{'Content-Type':'application/json'}})}
globalThis.fetch=async(url,options={})=>{
 const method=options.method||'GET',body=options.body?JSON.parse(options.body):undefined;requests.push({url,method,body})
 const route=mode==='personal'?'/api/worklogs':'/api/project-daily-logs'
 if(url.startsWith('/api/team-daily-entries')||url.startsWith('/api/projects/p/tree')||url.includes('/members'))return response({items:[],total:0})
 if(url===route+'/generate')return response({project_id:'p',period_type:'day',log_date:'2026-11-13',content:'新生成草稿',source_snapshot:{generated_at:'new'}})
 if(url.startsWith(route+'?'))return response({items:stored,total:stored.length})
 if(url===route+'/original'&&method==='GET')return response(stored.find(x=>x.id==='original'))
 if(url===route+'/original'&&method==='PATCH'){
  const original=stored.find(x=>x.id==='original')
  if(failOverwrite){failOverwrite=false;original.version++;original.content='另一窗口刚修改';return response(null,409,'version_conflict')}
  assert.equal(body.version,original.version,'overwrite uses explicitly reviewed current version')
  stored=stored.map(log=>log.id==='original'?{...log,...body,version:log.version+1}:log);return response(stored.find(x=>x.id==='original'))
 }
 if(url===route&&method==='POST'){
  if(sourceConflict){sourceConflict=false;return response(null,409,'source_conflict')}
  if(!body.save_as_new)return response(null,409)
  const saved={...existing,...body,id:'copy',variant:'random-copy',version:1};stored.push(saved);return response(saved)
 }
 throw new Error('Unexpected request '+method+' '+url)
}
let app
try{
 await symlink(path.join(webRoot,'node_modules'),path.join(temp,'node_modules'),'dir')
 for(const file of ['WorklogsPage.vue','ProjectJournalPage.vue']){const source=await readFile(path.join(webRoot,'src/pages',file),'utf8');assert.equal((source.match(/<LogDuplicateDialog\b/g)||[]).length,1,file+' renders exactly one duplicate-choice dialog')}
 await compile('src/pages/WorklogsPage.vue');await compile('src/pages/ProjectJournalPage.vue')
 const {ui}=await import(pathToFileURL(path.join(temp,'src/api.mjs')).href)
 ui.datasetId='journal-save-regression';ui.projects=[{id:'p',name:'开发项目',status:'active'}]
 for(const [kind,file,generateText,contentId,titleId] of [
  ['personal','WorklogsPage','生成草稿','worklog-content','worklog-title'],
  ['project','ProjectJournalPage','生成项目日志','project-journal-content','project-journal-title']
 ]){
  mode=kind;stored=[{...existing}];requests.length=0
  const {default:Page}=await import(pathToFileURL(path.join(temp,`src/pages/${file}.mjs`)).href)
  const root=node('root');app=renderer.createApp(Page,{revision:0});app.mount(root);await settle()
  const button=label=>walk(root,n=>n.type==='button'&&text(n)===label)
  const input=()=>walk(root,n=>n.id===contentId)
  await button(generateText).onClick();await settle()
  assert.ok(input(),'generated draft is editable')
  assert.equal(requests.filter(r=>r.method==='PATCH').length,0,'generation never updates existing saved journal')
  input()['onUpdate:modelValue']('我的新正文\n后续计划');walk(root,n=>n.id===titleId)['onUpdate:modelValue']('我的标题');await settle()
  await button('保存日志').onClick();await settle()
  let dialog=walk(root,n=>n.type==='dialog'&&n.open);assert.ok(dialog,'duplicate scope opens choice dialog')
  let openDialogs=0;function countDialogs(n){if(n.type==='dialog'&&n.open)openDialogs++;n.children.forEach(countDialogs)}countDialogs(root);assert.equal(openDialogs,1,'duplicate conflict opens exactly one native dialog')
  assert.ok(text(dialog).includes('原始内容不可偷偷覆盖'),'confirmation shows latest fetched original body')
  assert.equal(requests.filter(r=>r.method==='PATCH').length,0,'duplicate failure never silently overwrites')
  await button('保留已有，不覆盖').onClick();await settle()
  assert.equal(input().value,'我的新正文\n后续计划','keep existing preserves editor')
  assert.equal(stored[0].content,existing.content)
  await button('保存日志').onClick();await settle();failOverwrite=true
  await button('覆盖已有日志').onClick();await settle()
  assert.equal(input().value,'我的新正文\n后续计划','optimistic conflict preserves local body')
  assert.ok(text(root).includes('数据库内容已更新'),'stale overwrite produces comparison notice')
  assert.equal(stored[0].content,'另一窗口刚修改','409 preserves remote update')
  await button('保存日志').onClick();await settle()
  await button('覆盖已有日志').onClick();await settle()
  assert.equal(stored[0].content,'我的新正文\n后续计划')
  assert.equal(stored[0].log_date,existing.log_date,'overwrite does not change journal period')
  await button(generateText).onClick();await settle();input()['onUpdate:modelValue']('第二份正文');await settle()
  await button('保存日志').onClick();await settle()
  dialog=walk(root,n=>n.type==='dialog'&&n.open)
  const name=walk(dialog,n=>n.type==='input');name.onInput({target:{value:'评审补充日志'}});await settle()
  await button('另存为新日志').onClick();await settle()
  const copy=stored.find(x=>x.id==='copy');assert.equal(copy.title,'评审补充日志');assert.equal(copy.content,'第二份正文');assert.equal(copy.log_date,existing.log_date);assert.equal(copy.project_id,'p');assert.equal(stored[0].content,'我的新正文\n后续计划','save as keeps original')
  const sent=requests.findLast(r=>r.method==='POST'&&r.body?.save_as_new);assert.equal(sent.body.save_as_new,true);assert.equal(sent.body.title,'评审补充日志')
  assert.ok(text(root).includes('评审补充日志'),'new title is visible in saved list')
  if(kind==='project'){await button(generateText).onClick();await settle();sourceConflict=true;await button('保存日志').onClick();await settle();assert.equal(walk(root,n=>n.type==='dialog'&&n.open),undefined,'source_conflict never opens a duplicate overwrite dialog');assert.equal(input().value,'新生成草稿','stale source rejection preserves generated text')}
  app.unmount();app=undefined
 }
 console.log('PASS: personal/project logs generate without overwrite, duplicate three choices, keep preserves draft, explicit latest-version overwrite, 409 preserves both sides, same-period renamed save-as and visible title (real Vue SFC handlers)')
}finally{try{app?.unmount()}catch{}await rm(temp,{recursive:true,force:true})}
