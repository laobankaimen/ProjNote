<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ListPlus, Search } from 'lucide-vue-next'
import Modal from './Modal.vue'
import Pager from './Pager.vue'
import { api, messageOf, projectName } from '../api'
import { candidatePlanText, type NextPlanCandidate } from '../nextPlan'
const props=defineProps<{date:string;projectId?:string;assignee:string;disabled?:boolean;buttonLabel?:string}>()
const emit=defineEmits<{append:[text:string]}>()
const open=ref(false),loading=ref(false),error=ref(''),q=ref(''),offset=ref(0),total=ref(0),target=ref(''),items=ref<NextPlanCandidate[]>([]),selected=ref<Record<string,NextPlanCandidate>>({})
const limit=20,count=computed(()=>Object.keys(selected.value).length)
let request=0
async function load(next=0){const current=++request;offset.value=next;loading.value=true;error.value='';try{const data=await api<{target_date:string;candidates:NextPlanCandidate[];total:number}>('/api/logs/next-plan-candidates',{method:'POST',body:JSON.stringify({date:props.date,project_id:props.projectId||'',assignee:props.assignee,q:q.value,limit,offset:next})});if(current===request){items.value=data.candidates;total.value=data.total;target.value=data.target_date}}catch(e){if(current===request)error.value=messageOf(e)}finally{if(current===request)loading.value=false}}
function show(){selected.value={};q.value='';open.value=true;void load()}
function toggle(item:NextPlanCandidate,checked:boolean){if(checked)selected.value[item.key]=item;else delete selected.value[item.key]}
function append(){emit('append',candidatePlanText(Object.values(selected.value)));open.value=false}
function kindName(kind:string){return ({tasks:'任务',task:'任务',issues:'问题',issue:'问题',records:'当天现场记录',record:'当天现场记录'} as Record<string,string>)[kind]||kind}
watch(()=>[props.date,props.projectId,props.assignee],()=>{request++;open.value=false;items.value=[];selected.value={}})
</script>
<template><button type="button" class="button subtle small" :disabled="disabled||!date||!assignee.trim()" @click="show"><ListPlus :size="15"/>{{buttonLabel||'选择明日计划'}}</button><Modal :open="open" :title="`${buttonLabel||'选择明日计划'}${target?' · '+target:''}`" wide @close="open=false"><div class="modal-body next-plan-picker"><p class="input-hint">按 {{assignee}} 的任务提供候选；当天现场记录仅供手动选择。勾选后追加到计划，已有手写内容会保留。</p><div class="next-plan-search"><div class="input-with-icon"><Search :size="15"/><input v-model="q" aria-label="搜索计划候选" placeholder="搜索候选" @keydown.enter.prevent="load()"/></div><button type="button" class="button subtle small" :disabled="loading" @click="load()">查询</button></div><p v-if="error" class="form-error" role="alert">{{error}}<button type="button" class="text-button" @click="load(offset)">重试</button></p><p v-else-if="loading" class="loading-message">正在读取候选…</p><p v-else-if="!items.length" class="empty-state compact">没有符合条件的候选，可以继续手动填写明日计划。</p><div v-else class="next-plan-list"><label v-for="item in items" :key="item.key" class="next-plan-option"><input type="checkbox" :checked="!!selected[item.key]" @change="toggle(item,($event.target as HTMLInputElement).checked)"/><span><strong>{{item.title}}</strong><small>{{kindName(item.kind)}} · {{item.project_id?projectName(item.project_id):'未归类'}} · {{item.reason}}</small><span v-if="item.content" class="candidate-content">{{item.content}}</span></span></label></div><Pager :offset="offset" :limit="limit" :total="total" @change="load"/><div class="modal-footer"><span class="muted">已选 {{count}} 项；仅追加，不修改候选来源。</span><button type="button" class="button primary" :disabled="!count||loading" @click="append">追加 {{count}} 项计划</button></div></div></Modal></template>
<style scoped>
.next-plan-search{display:flex;gap:10px;margin:15px 0}.next-plan-search .input-with-icon{flex:1}.next-plan-list{max-height:45vh;overflow:auto}.next-plan-option{display:flex;align-items:flex-start;gap:12px;border-bottom:1px solid var(--border);padding:14px 0;margin:0}.next-plan-option>input{width:16px;min-height:16px;flex-shrink:0;margin-top:5px}.next-plan-option>span{min-width:0}.next-plan-option strong{font-size:13px;font-weight:500;overflow-wrap:anywhere}.next-plan-option small{display:block;color:#788ba3;font-size:11px;margin:4px 0}.candidate-content{display:block;color:#63758e;white-space:pre-wrap;max-height:100px;overflow:auto;font-size:12px;font-weight:400}.next-plan-picker>.pager{padding-left:0;padding-right:0}.next-plan-picker .form-error .text-button{margin-left:12px}
</style>
