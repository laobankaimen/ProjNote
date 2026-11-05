<script setup lang="ts">
import { ref,watch } from 'vue'
import { api,messageOf,query } from '../api'
import { requirementStatuses,type RequirementSummary } from '../planning'
import type { Page } from '../types'
import Pager from './Pager.vue'
const props=defineProps<{taskId:string;revision:number;disabled?:boolean}>(),emit=defineEmits<{open:[id:string]}>(),items=ref<RequirementSummary[]>([]),total=ref(0),offset=ref(0),loading=ref(false),error=ref(''),limit=10
let request=0
async function load(){const seq=++request;loading.value=true;error.value='';try{const data=await api<Page<RequirementSummary>>(`/api/tasks/${encodeURIComponent(props.taskId)}/requirements?${query({limit,offset:offset.value})}`);if(seq===request){items.value=data.items;total.value=data.total}}catch(e){if(seq===request)error.value=messageOf(e)}finally{if(seq===request)loading.value=false}}
watch(()=>[props.taskId,props.revision],()=>{offset.value=0;void load()},{immediate:true})
</script>
<template><section class="task-requirements"><h3>关联需求 <span class="count">{{total}}</span></h3><p class="input-hint">仅列出直接关联且未作废的需求。任务完成后，仍需人工核对需求验收。</p><p v-if="loading" class="input-hint">正在读取关联需求…</p><p v-else-if="error" class="form-error" role="alert">{{error}} <button type="button" class="text-button" @click="load">重试</button></p><ul v-else-if="items.length"><li v-for="item in items" :key="item.id"><button type="button" class="text-button" :disabled="disabled" @click="emit('open',item.id)">{{item.code}} {{item.title}}</button><span>{{requirementStatuses[item.status]}} · 修订 {{item.revision}}</span></li></ul><p v-else class="input-hint">此任务尚未直接关联需求。</p><Pager v-if="total>limit" :offset="offset" :limit="limit" :total="total" @change="offset=$event;load()"/></section></template>
<style scoped>.task-requirements{border-top:1px solid var(--border);padding:18px 0;margin-top:18px}.task-requirements h3{font-size:13px;margin-bottom:7px}.task-requirements ul{list-style:none;padding:0;margin:12px 0 0}.task-requirements li{display:flex;gap:10px;align-items:baseline;justify-content:space-between;padding:9px 0;border-bottom:1px solid var(--border);font-size:11px}.task-requirements li .text-button{text-align:left;overflow-wrap:anywhere;font-size:12px}.task-requirements li>span{white-space:nowrap;color:var(--muted)}@media(max-width:600px){.task-requirements li{flex-direction:column;gap:4px}}
</style>
