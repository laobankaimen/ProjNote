<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { Download, FilePenLine, Save, WandSparkles } from 'lucide-vue-next'
import Pager from '../components/Pager.vue'
import JournalTabs from '../components/JournalTabs.vue'
import NextPlanPicker from '../components/NextPlanPicker.vue'
import { appendTomorrowSection } from '../nextPlan'
import { queueWordExport, queuePrintExport } from '../exports'
import DraftNotice from '../components/DraftNotice.vue'
import ConflictNotice from '../components/ConflictNotice.vue'
import LogDuplicateDialog from '../components/LogDuplicateDialog.vue'
import { useDraft,useConflict } from '../drafts'
import { all, api, ApiError, downloadMarkdown, isArchived, localDate, messageOf, notify, projectName, query, softDelete, ui, worklogPeriodLabel } from '../api'
import type { Page, Worklog } from '../types'
const props=defineProps<{revision:number}>();const emit=defineEmits<{changed:[]}>()
type NamedWorklog=Worklog&{title?:string;variant?:string}
const exporting=ref(false)
const logs=ref<NamedWorklog[]>([]),total=ref(0),offset=ref(0),loading=ref(false),error=ref(''),saving=ref(false),generating=ref(false),draft=ref(false),dirty=ref(false),editing=ref<NamedWorklog>();const limit=20
const duplicateOpen=ref(false),duplicate=ref<NamedWorklog>(),duplicateTitle=ref(''),duplicateError=ref('')
const generator=reactive({project_id:'',period_type:'day' as 'day'|'week'|'month',date:localDate()});const form=reactive({project_id:'',period_type:'day' as 'day'|'week'|'month',log_date:'',title:'',content:''});const readonly=computed(()=>isArchived(form.project_id))
const localDraft=useDraft(()=>`worklogs:${editing.value?.id||'new'}`,form,()=>true,()=>editing.value?.version),conflict=useConflict()
async function load(){loading.value=true;try{const data=await api<Page<Worklog>>(`/api/worklogs?${query({limit,offset:offset.value})}`);logs.value=data.items;total.value=data.total}catch(e){error.value=messageOf(e)}finally{loading.value=false}}
onMounted(load);watch(()=>props.revision,load)
function canReplace(){return !saving.value&&!generating.value&&!duplicateOpen.value&&localDraft.canLeave()}
async function generate(){if(!canReplace())return;generating.value=true;error.value='';try{const data=await api<{project_id:string;period_type:'day'|'week'|'month';log_date:string;content:string}>('/api/worklogs/generate',{method:'POST',body:JSON.stringify({...generator,assignee:ui.operator})});localDraft.persist();editing.value=undefined;Object.assign(form,{project_id:data.project_id||'',period_type:data.period_type,log_date:data.log_date,title:'',content:data.content});draft.value=true;dirty.value=true;conflict.clear();await localDraft.reset();localDraft.markDirty();notify('草稿已生成，请核对内容后保存')}catch(e){error.value=messageOf(e)}finally{generating.value=false}}
async function select(log:NamedWorklog){if(!canReplace())return;editing.value=log;Object.assign(form,{project_id:log.project_id,period_type:log.period_type,log_date:log.log_date,title:log.title||'',content:log.content});draft.value=true;dirty.value=false;error.value='';conflict.clear();await localDraft.reset()}
function sameScope(log:NamedWorklog){return (log.project_id||'')===form.project_id&&log.period_type===form.period_type&&log.log_date===form.log_date}
async function showDuplicate(){
 const matches=(await all<NamedWorklog>('/api/worklogs',{project_id:form.project_id,period_type:form.period_type,log_date:form.log_date})).filter(sameScope)
 const existing=matches.find(log=>!log.variant)
 if(!existing)return false
 const latest=await api<NamedWorklog>(`/api/worklogs/${existing.id}`)
 if(!sameScope(latest))return false
 duplicate.value=latest;duplicateTitle.value=form.title.trim()||`${form.log_date} ${worklogPeriodLabel(form.period_type)}（副本）`;duplicateError.value='';duplicateOpen.value=true;error.value='';return true
}
async function persistLog(target?:NamedWorklog,saveAsNew=false){
 if(!form.content.trim()||saving.value||readonly.value)return
 saving.value=true;error.value='';duplicateError.value=''
 const payload={project_id:form.project_id,period_type:form.period_type,log_date:form.log_date,title:saveAsNew?duplicateTitle.value.trim():form.title,content:form.content,...(target?{version:target.version}:{}),...(saveAsNew?{save_as_new:true}:{})}
 try{
  const saved=await api<NamedWorklog>(target?`/api/worklogs/${target.id}`:'/api/worklogs',{method:target?'PATCH':'POST',body:JSON.stringify(payload)})
  localDraft.discard();editing.value=saved;Object.assign(form,{project_id:saved.project_id||'',period_type:saved.period_type,log_date:saved.log_date,title:saved.title||'',content:saved.content});duplicateOpen.value=false;duplicate.value=undefined;conflict.clear();dirty.value=false;await localDraft.reset();notify(saveAsNew?'新日志已另存，原日志已保留':'工作日志已保存');emit('changed');await load()
 }catch(e){
  error.value=messageOf(e);localDraft.persist()
  if(!target&&!saveAsNew&&e instanceof ApiError&&e.status===409&&e.code!=='source_conflict'){try{if(await showDuplicate())return}catch(readError){error.value=messageOf(readError)}}
  if(target){duplicateOpen.value=false;await conflict.capture(e,`/api/worklogs/${target.id}`,payload)}else if(duplicateOpen.value)duplicateError.value=messageOf(e)
 }finally{saving.value=false}
}
async function save(){await persistLog(editing.value)}
function keepExisting(){if(saving.value)return;duplicateOpen.value=false;duplicate.value=undefined;error.value='';notify('已有日志已保留，当前内容仍可继续编辑')}
async function reloadConflict(){const remote=conflict.current.value?.remote as unknown as NamedWorklog;if(!remote)return;localDraft.persist();editing.value=remote;Object.assign(form,{project_id:remote.project_id,period_type:remote.period_type,log_date:remote.log_date,title:remote.title||'',content:remote.content});conflict.clear();dirty.value=false;await localDraft.reset();error.value='已载入新日志，原修改保留在浏览器草稿中。'}
function restoreDraft(){localDraft.restore();draft.value=true;dirty.value=true}
async function openEntity(id:string){try{await select(await api<NamedWorklog>(`/api/worklogs/${id}`))}catch(e){notify(messageOf(e),true)}}
async function remove(){if(!editing.value||readonly.value)return;try{if(await softDelete('worklogs',editing.value,editing.value.title||`${editing.value.log_date} 工作日志`)){localDraft.discard();editing.value=undefined;draft.value=false;Object.assign(form,{project_id:'',period_type:'day',log_date:'',title:'',content:''});emit('changed');await load()}}catch(e){error.value=messageOf(e)}}
defineExpose({canLeave:canReplace,openEntity})
const planSourceDate=computed(()=>{const date=new Date(form.log_date+'T00:00:00');if(Number.isNaN(date.valueOf()))return form.log_date;if(form.period_type==='week')date.setDate(date.getDate()+6);if(form.period_type==='month'){date.setMonth(date.getMonth()+1,0)};return `${date.getFullYear()}-${String(date.getMonth()+1).padStart(2,'0')}-${String(date.getDate()).padStart(2,'0')}`})
async function printLog(){if(!editing.value||dirty.value||localDraft.dirty.value||exporting.value)return;exporting.value=true;try{await queuePrintExport('worklogs',editing.value)}catch(e){error.value=messageOf(e)}finally{exporting.value=false}}
function appendNextPlan(text:string){form.content=appendTomorrowSection(form.content,text);dirty.value=true;localDraft.markDirty()}
async function exportWord(){if(!editing.value||dirty.value||localDraft.dirty.value||exporting.value)return;exporting.value=true;try{await queueWordExport('worklogs',editing.value)}catch(e){error.value=messageOf(e)}finally{exporting.value=false}}
function download(){downloadMarkdown(form.content,`${form.title||form.log_date+'-'+worklogPeriodLabel(form.period_type)}.md`)}
function page(next:number){offset.value=next;void load()}
</script>
<template><div class="page-heading"><div><h1>工作日志</h1><p class="page-description">从实际记录生成草稿，整理成自己的日报、周报和月报。</p></div></div><JournalTabs active="personal"/><div class="generator-bar"><div><WandSparkles :size="19"/><strong>生成一份日志</strong></div><label class="sr-only" for="log-project">日志范围</label><select id="log-project" v-model="generator.project_id"><option value="">全部项目</option><option v-for="p in ui.projects" :key="p.id" :value="p.id">{{p.name}}</option></select><label class="sr-only" for="log-period">日志周期</label><select id="log-period" v-model="generator.period_type"><option value="day">日报</option><option value="week">周报</option><option value="month">月报</option></select><label class="sr-only" for="log-date">日志日期</label><input id="log-date" v-model="generator.date" type="date" required/><button class="button primary" :disabled="generating||!generator.date" @click="generate">{{generating?'正在生成…':'生成草稿'}}</button></div><div v-if="error" class="error-banner" role="alert">{{error}}</div><DraftNotice :value="localDraft.available.value" :error="localDraft.storageError.value" :values="localDraft.values.value" @select="localDraft.select" :recovery="localDraft.recovery.value" @force="localDraft.restoreForced();draft=true;dirty=true" @cancel-recovery="localDraft.clearRecovery" @restore="restoreDraft" @discard="localDraft.discardAvailable"/><ConflictNotice :value="conflict.current.value" @keep="localDraft.persist();conflict.clear()" @reload="reloadConflict"/><div class="worklog-layout"><aside class="surface saved-logs"><div class="section-heading"><h2>已保存 <span class="count">{{total}}</span></h2></div><p v-if="loading" class="loading-message">正在读取…</p><div v-else-if="!logs.length" class="empty-state compact"><FilePenLine :size="30"/><p>保存后的日志会放在这里，方便查阅和再次编辑。</p></div><div v-else class="log-list"><button v-for="log in logs" :key="log.id" :class="{selected:editing?.id===log.id}" @click="select(log)"><span class="log-date">{{log.log_date}}<span class="badge">{{worklogPeriodLabel(log.period_type)}}</span></span><span v-if="log.title" class="saved-log-title">{{log.title}}</span><span>{{log.project_id?projectName(log.project_id):'全部项目'}}</span></button></div><Pager :offset="offset" :limit="limit" :total="total" @change="page"/></aside><section class="surface log-editor"><template v-if="draft"><div class="section-heading"><div><h2>{{form.title||`${form.log_date} ${worklogPeriodLabel(form.period_type)}`}}<span v-if="dirty" class="draft-label">未保存</span></h2><p class="input-hint">{{form.project_id?projectName(form.project_id):'全部项目'}}<span v-if="readonly"> · 已归档，只读</span></p></div><div class="row-actions"><NextPlanPicker v-if="!readonly" :date="planSourceDate" :button-label="form.period_type==='day'?'选择明日计划':'选择后续计划'" :project-id="form.project_id" :assignee="ui.operator" :disabled="saving||generating" @append="appendNextPlan"/><button v-if="editing" class="button subtle small" :disabled="exporting||saving||dirty||localDraft.dirty.value" :title="dirty||localDraft.dirty.value?'请先保存修改':'导出已保存版本为 Word'" @click="exportWord">{{exporting?'正在创建…':'导出 Word'}}</button><button v-if="editing" class="button subtle small" :disabled="exporting||saving||dirty||localDraft.dirty.value" @click="printLog">打印 / PDF</button><button class="button subtle small" :disabled="!form.content" @click="download"><Download :size="15"/>导出 Markdown</button><button v-if="editing&&!readonly" class="button subtle small" @click="remove">移入回收站</button><button v-if="!readonly" class="button primary small" :disabled="saving||!form.content.trim()" @click="save"><Save :size="15"/>{{saving?'正在保存…':'保存日志'}}</button></div></div><label class="journal-title-field" for="worklog-title">日志标题（选填）<input id="worklog-title" v-model="form.title" maxlength="200" :readonly="readonly||saving||generating" placeholder="留空时使用日期与日志周期" @input="dirty=true"/></label><label class="sr-only" for="worklog-content">日志正文，Markdown 格式</label><textarea id="worklog-content" v-model="form.content" :readonly="readonly||saving||generating" class="markdown-editor" spellcheck="false" @input="dirty=true"/><div class="editor-footer">Markdown 格式<span>{{form.content.length}} 字</span></div></template><div v-else class="empty-state editor-empty"><FilePenLine :size="40"/><h2>把今天的工作整理成文</h2><p>选择项目与日期后生成草稿，或从左侧打开已保存的日志。</p></div></section></div><LogDuplicateDialog :open="duplicateOpen" :existing="duplicate" :fallback-title="`${form.log_date} ${worklogPeriodLabel(form.period_type)}`" v-model="duplicateTitle" :busy="saving" :error="duplicateError" @overwrite="persistLog(duplicate)" @keep="keepExisting" @save-new="persistLog(undefined,true)"/></template>

<style scoped>
.journal-title-field{display:block;padding:14px 22px 0;font-size:12px;color:var(--muted)}.journal-title-field input{margin-top:6px;color:var(--ink)}.saved-log-title{font-weight:600;color:var(--ink);overflow:hidden;text-overflow:ellipsis}
</style>
