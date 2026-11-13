<script setup lang="ts">
import { computed,ref,watch } from 'vue'
import { all,isProjectReadOnly,messageOf } from '../api'
import type { LanMember } from '../lan'
import { projectMembersRevision } from '../projectMembers'
import Modal from './Modal.vue'
import ProjectMembers from './ProjectMembers.vue'
const props=withDefaults(defineProps<{modelValue?:string|null;projectId?:string;stable?:boolean;legacyName?:string;disabled?:boolean;required?:boolean;manage?:boolean;placeholder?:string;label?:string}>(),{modelValue:'',projectId:'',manage:true,placeholder:'未指定'}),emit=defineEmits<{'update:modelValue':[value:string];chosen:[member:LanMember|undefined]}>()
const control=ref<HTMLSelectElement>()
const members=ref<LanMember[]>([]),loading=ref(false),error=ref(''),opened=ref(false),busy=ref(false),manager=ref<InstanceType<typeof ProjectMembers>>()
const options=computed(()=>members.value.filter(m=>m.enabled)),legacy=computed(()=>props.stable?!!props.legacyName&&!props.modelValue:!!props.modelValue&&!options.value.some(m=>m.name===props.modelValue)),selection=computed(()=>legacy.value?'__legacy__':props.modelValue||'')
let request=0
async function load(){const seq=++request;members.value=[];error.value='';if(!props.projectId)return;loading.value=true;try{const data=await all<LanMember>(`/api/projects/${encodeURIComponent(props.projectId)}/members`);if(seq===request)members.value=data}catch(e){if(seq===request)error.value=messageOf(e)}finally{if(seq===request)loading.value=false}}
function change(event:Event){const value=(event.target as HTMLSelectElement).value,member=options.value.find(m=>props.stable?m.id===value:m.name===value);emit('update:modelValue',value);emit('chosen',member)}
function close(){if(!busy.value&&(!manager.value||manager.value.canLeave())){opened.value=false;void load()}}
watch([()=>props.projectId,projectMembersRevision],load,{immediate:true})
defineExpose({focus:()=>control.value?.focus()})
</script>
<template><span class="project-person-select"><select ref="control" :value="selection" :aria-label="label" :disabled="disabled||loading||!projectId" :required="required" @change="change"><option value="">{{!projectId?'先保存或选择项目':loading?'正在读取成员…':placeholder}}</option><option v-if="legacy" value="__legacy__" disabled>{{legacyName||modelValue}}（原记录，未在当前可用成员中）</option><option v-else-if="stable&&modelValue&&!options.some(m=>m.id===modelValue)" :value="modelValue" disabled>{{legacyName||modelValue}}（原成员）</option><option v-for="member in options" :key="member.id" :value="stable?member.id:member.name">{{member.name}} · {{member.id.slice(0,8)}}</option></select><span v-if="error" class="form-error" role="alert">{{error}} <button type="button" class="text-button" @click="load">重试</button></span><button v-if="manage&&projectId&&!disabled" type="button" class="text-button person-manage" @click="opened=true">管理项目成员</button><Modal :open="opened" title="项目成员" @close="close"><div class="modal-body"><ProjectMembers ref="manager" :project-id="projectId" :readonly="isProjectReadOnly(projectId)" @changed="load" @busy="busy=$event"/><div class="modal-footer"><button type="button" class="button subtle" :disabled="busy" @click="close">完成</button></div></div></Modal></span></template>
<style scoped>.project-person-select{display:block;min-width:0}.project-person-select select{width:100%}.person-manage{margin-top:6px;font-size:11px}.project-person-select>.form-error{display:block;font-size:11px;margin-top:6px}</style>
