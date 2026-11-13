<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { Bell, Check, Plus } from 'lucide-vue-next'
import Modal from '../components/Modal.vue'
import LocalDateTimePicker from '../components/LocalDateTimePicker.vue'
import Pager from '../components/Pager.vue'
import DraftNotice from '../components/DraftNotice.vue'
import ConflictNotice from '../components/ConflictNotice.vue'
import { useConflict,useDraft } from '../drafts'
import { all, api, dateTimeLabel, dateTimeLocal, isProjectReadOnly, messageOf, notify, query, ui } from '../api'
import { pendingTaskReminder } from '../taskReminders'
import type { TaskReminder,TaskReminderStatus } from '../taskReminders'
import type { Page,Task } from '../types'
const props=defineProps<{revision:number}>();const emit=defineEmits<{changed:[];record:[id:string]}>()
const reminders=ref<TaskReminder[]>([]),tasks=ref<Task[]>([]),total=ref(0),offset=ref(0),status=ref('pending'),project=ref(''),loading=ref(false),tasksLoading=ref(false),error=ref(''),open=ref(false),saving=ref(false),completing=ref(''),editing=ref<TaskReminder>(),formError=ref(''),delivery=ref<TaskReminderStatus>({supported:false,description:'正在检查桌面通知能力…'});const limit=30
const form=reactive({project_id:'',task_id:'',due_local:''}),draft=useDraft(()=>`task_reminders:${editing.value?.id||'new'}`,form,()=>open.value,()=>editing.value?.version),conflict=useConflict()
const writableProjects=computed(()=>ui.projects.filter(p=>!isProjectReadOnly(p.id)))
const availableTasks=computed(()=>tasks.value.filter(task=>task.project_id===form.project_id&&!task.has_children&&!['done','canceled'].includes(task.status)))
const readonly=computed(()=>!delivery.value.supported||!!editing.value&&(!pendingTaskReminder(editing.value)||isProjectReadOnly(editing.value.project_id)))
let request=0,taskRequest=0
async function load(){const current=++request;loading.value=true;error.value='';try{const [page,state]=await Promise.all([api<Page<TaskReminder>>(`/api/task-reminders?${query({status:status.value,project_id:project.value,limit,offset:offset.value})}`),api<TaskReminderStatus>('/api/task-reminders/status')]);if(current===request){reminders.value=page.items;total.value=page.total;delivery.value=state}}catch(e){if(current===request){error.value=messageOf(e);delivery.value={supported:false,description:'无法确认桌面通知状态，请重试。'}}}finally{if(current===request)loading.value=false}}
async function loadTasks(){const current=++taskRequest;tasksLoading.value=true;try{const data=await all<Task>('/api/tasks',{project_id:form.project_id});if(current===taskRequest)tasks.value=data}catch(e){if(current===taskRequest)formError.value=messageOf(e)}finally{if(current===taskRequest)tasksLoading.value=false}}
function assign(reminder?:TaskReminder){Object.assign(form,{project_id:reminder?.project_id||project.value||writableProjects.value[0]?.id||'',task_id:reminder?.task_id||'',due_local:dateTimeLocal(reminder?.due_at||new Date(Math.ceil((Date.now()+5*60000)/60000)*60000).toISOString())})}
async function edit(reminder?:TaskReminder){if(open.value&&!draft.canLeave())return;if(!reminder&&!delivery.value.supported)return;editing.value=reminder;assign(reminder);open.value=true;formError.value='';conflict.clear();await draft.reset();if(!reminder)await loadTasks()}
async function changeProject(){if(editing.value)return;form.task_id='';tasks.value=[];await loadTasks()}
async function openEntity(id:string){try{await edit(await api<TaskReminder>(`/api/task-reminders/${id}`))}catch(e){notify(messageOf(e),true)}}
async function save(){
 if(saving.value||readonly.value||!form.due_local||!form.task_id)return
 const due=new Date(form.due_local);if(!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(form.due_local)||Number.isNaN(due.valueOf())||dateTimeLocal(due.toISOString())!==form.due_local){formError.value='请选择有效的提醒时间';return}
 saving.value=true;formError.value=''
 const payload={due_at:due.toISOString(),...(editing.value?{version:editing.value.version}:{task_id:form.task_id})}
 try{const saved=await api<TaskReminder>(editing.value?`/api/task-reminders/${editing.value.id}`:'/api/task-reminders',{method:editing.value?'PATCH':'POST',body:JSON.stringify(payload)});draft.discard();open.value=false;notify(`任务提醒已保存：${dateTimeLabel(saved.due_at)}`);emit('changed');await load()}catch(e){formError.value=messageOf(e);draft.persist();if(editing.value)await conflict.capture(e,`/api/task-reminders/${editing.value.id}`,payload)}finally{saving.value=false}
}
async function reloadConflict(){const remote=conflict.current.value?.remote as unknown as TaskReminder;if(!remote)return;draft.persist();editing.value=remote;assign(remote);conflict.clear();await draft.reset()}
async function complete(reminder:TaskReminder){if(completing.value||!pendingTaskReminder(reminder)||isProjectReadOnly(reminder.project_id))return;completing.value=reminder.id;error.value='';try{await api(`/api/task-reminders/${reminder.id}/done`,{method:'POST',body:JSON.stringify({version:reminder.version})});notify('提醒已结束，任务进度未修改');emit('changed');await load()}catch(e){error.value=messageOf(e)}finally{completing.value=''}}
function close(){if(!saving.value&&draft.canLeave())open.value=false}
function page(next:number){offset.value=next;void load()}
onMounted(load);watch(()=>props.revision,load);watch([status,project],()=>{offset.value=0;void load()});defineExpose({openEntity,canLeave:()=>!open.value||!saving.value&&draft.canLeave()})
</script>
<template>
<div class="page-heading"><div><h1>任务提醒</h1><p class="page-description">选择待完成的任务，到时通过桌面或客户端消息提醒。</p></div><button class="button primary" :disabled="!delivery.supported" @click="edit()"><Plus :size="17"/>添加任务提醒</button></div>
<p class="input-hint reminder-delivery" :role="delivery.supported?undefined:'status'">{{delivery.description}}</p>
<div class="filter-bar"><select v-model="status" aria-label="提醒状态"><option value="pending">待提醒 / 待处理</option><option value="done">已结束</option><option value="all">全部状态</option></select><select v-model="project" aria-label="提醒所属项目"><option value="">全部项目</option><option v-for="p in ui.projects" :key="p.id" :value="p.id">{{p.name}}</option></select></div>
<div v-if="error" class="error-banner" role="alert">{{error}}<button class="text-button" @click="load">重试</button></div>
<div class="surface"><p v-if="loading" class="loading-message">正在读取任务提醒…</p><div v-else-if="!reminders.length" class="empty-state"><Bell :size="36"/><h2>{{status==='done'?'暂无已结束提醒':'没有待处理的任务提醒'}}</h2><p>从项目中选择未完成的任务，并安排提醒时间。</p><button class="button subtle small" :disabled="!delivery.supported" @click="edit()">添加任务提醒</button></div>
<ul v-else class="reminder-list"><li v-for="reminder in reminders" :key="reminder.id"><Bell :size="17" :class="{overdue:pendingTaskReminder(reminder)&&new Date(reminder.due_at).valueOf()<Date.now()}"/><div><button class="table-title" @click="edit(reminder)">{{reminder.task_name}}</button><p>{{dateTimeLabel(reminder.due_at)}}　{{reminder.project_name}}</p><p v-if="reminder.last_error&&!reminder.done_at" class="reminder-error" role="status">通知未送达：{{reminder.last_error}}</p><small v-else-if="reminder.notified_at&&!reminder.done_at" class="muted">已于 {{dateTimeLabel(reminder.notified_at)}} 发出通知</small></div><span v-if="reminder.done_at||!pendingTaskReminder(reminder)" class="badge done">已结束</span><button v-else class="button subtle small" :disabled="!!completing||isProjectReadOnly(reminder.project_id)" @click="complete(reminder)"><Check :size="15"/>{{completing===reminder.id?'保存中…':'结束提醒'}}</button></li></ul><Pager :offset="offset" :limit="limit" :total="total" @change="page"/></div>
<Modal :open="open" :title="editing?'任务提醒详情':'添加任务提醒'" @close="close"><form class="modal-body" @submit.prevent="save"><DraftNotice :value="draft.available.value" :values="draft.values.value" :error="draft.storageError.value" :recovery="draft.recovery.value" @select="draft.select" @restore="draft.restore" @force="draft.restoreForced" @cancel-recovery="draft.clearRecovery" @discard="draft.discardAvailable"/><ConflictNotice :value="conflict.current.value" @keep="draft.persist();conflict.clear()" @reload="reloadConflict"/>
<fieldset :disabled="readonly||saving"><template v-if="!editing"><label>项目<select v-model="form.project_id" required @change="changeProject"><option value="" disabled>选择项目</option><option v-for="p in writableProjects" :key="p.id" :value="p.id">{{p.name}}</option></select></label><label>待完成任务<select v-model="form.task_id" required :disabled="tasksLoading||!form.project_id"><option value="" disabled>{{tasksLoading?'正在读取任务…':'选择未完成的任务'}}</option><option v-for="task in availableTasks" :key="task.id" :value="task.id">{{task.name}}{{task.assignee?' · '+task.assignee:''}}</option></select><span v-if="!tasksLoading&&!availableTasks.length" class="input-hint">该项目暂无未完成的明细任务，请先在项目计划中新增任务。</span></label></template><p v-else class="reminder-task-name"><strong>{{editing.task_name}}</strong><span>{{editing.project_name}}</span></p><LocalDateTimePicker v-model="form.due_local" :disabled="readonly||saving"/></fieldset>
<p class="input-hint">完成或取消任务后，提醒自动结束。结束提醒只取消本次通知，不会把任务标记为完成。</p><p v-if="formError" class="form-error" role="alert">{{formError}}</p><div class="modal-footer"><span v-if="readonly" class="muted">已结束、项目只读或当前无法发送通知</span><button v-if="!readonly" class="button primary" :disabled="saving||tasksLoading||!form.task_id">{{saving?'正在保存…':'保存任务提醒'}}</button></div></form></Modal>
</template>
<style scoped>.reminder-delivery{margin:-8px 0 20px;line-height:1.8}.reminder-error{color:#b74c3c!important}.reminder-task-name{margin-bottom:20px;line-height:1.8}.reminder-task-name span{display:block;color:var(--muted);font-size:12px}</style>
