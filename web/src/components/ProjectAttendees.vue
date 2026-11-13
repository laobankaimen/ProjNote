<script setup lang="ts">
import { computed,ref,watch } from 'vue'
import { all,isProjectReadOnly,messageOf } from '../api'
import type { LanMember } from '../lan'
import { projectMembersRevision } from '../projectMembers'
import Modal from './Modal.vue'
import ProjectMembers from './ProjectMembers.vue'
const props=defineProps<{modelValue:string;projectId:string;disabled?:boolean}>(),emit=defineEmits<{'update:modelValue':[value:string]}>()
const members=ref<LanMember[]>([]),loading=ref(false),error=ref(''),opened=ref(false),busy=ref(false),manager=ref<InstanceType<typeof ProjectMembers>>()
const choices=computed(()=>members.value.filter(m=>m.enabled)),names=computed(()=>props.modelValue.split(/[、，,；;\n]+/).map(v=>v.trim()).filter(Boolean))
let request=0
async function load(){const seq=++request;members.value=[];error.value='';if(!props.projectId)return;loading.value=true;try{const data=await all<LanMember>(`/api/projects/${encodeURIComponent(props.projectId)}/members`);if(seq===request)members.value=data}catch(e){if(seq===request)error.value=messageOf(e)}finally{if(seq===request)loading.value=false}}
function personLabel(member:LanMember){return choices.value.filter(m=>m.name===member.name).length>1?`${member.name}（${member.id.slice(0,8)}）`:member.name}
function toggle(member:LanMember,event:Event){const label=personLabel(member),checked=(event.target as HTMLInputElement).checked;if(checked){if(!names.value.includes(label))emit('update:modelValue',props.modelValue?`${props.modelValue}、${label}`:label)}else emit('update:modelValue',names.value.filter(v=>v!==label).join('、'))}
function clear(){if(window.confirm('清空当前参会人，重新从项目成员选择？'))emit('update:modelValue','')}
function close(){if(!busy.value&&(!manager.value||manager.value.canLeave()))opened.value=false}
watch([()=>props.projectId,projectMembersRevision],load,{immediate:true})
</script>
<template><section class="project-attendees"><p v-if="modelValue" class="attendees-current" aria-label="当前参会人">{{modelValue}}</p><p v-if="!projectId" class="input-hint">先关联项目，再从项目成员中选择参会人；原参会人文字保留。</p><p v-if="error" class="form-error" role="alert">{{error}}</p><p v-if="loading" class="input-hint">正在读取项目成员…</p><div v-else-if="projectId" class="attendee-choices"><label v-for="member in choices" :key="member.id" class="checkbox-label"><input type="checkbox" :checked="names.includes(personLabel(member))" :disabled="disabled" @change="toggle(member,$event)"/>{{member.name}} <small>{{member.id.slice(0,8)}}</small></label><p v-if="!choices.length" class="input-hint">此项目尚无可选成员，请先新增。</p></div><div v-if="!disabled" class="row-actions"><button v-if="projectId" type="button" class="text-button" @click="opened=true">管理项目成员</button><button v-if="modelValue" type="button" class="text-button" @click="clear">清空并重新选择</button></div><Modal :open="opened" title="项目成员" @close="close"><div class="modal-body"><ProjectMembers ref="manager" :project-id="projectId" :readonly="isProjectReadOnly(projectId)" @busy="busy=$event"/><div class="modal-footer"><button type="button" class="button subtle" :disabled="busy" @click="close">完成</button></div></div></Modal></section></template>
<style scoped>.attendees-current{white-space:pre-wrap;overflow-wrap:anywhere;border:1px solid #dfe6f0;background:#f8faff;border-radius:4px;padding:10px 12px;margin:0 0 10px;font-size:12px;line-height:1.7}.attendee-choices{max-height:160px;overflow:auto;display:flex;flex-wrap:wrap;gap:8px 18px;margin:10px 0}.attendee-choices label{margin:0;font-size:12px}.attendee-choices small{color:#71849d;font-size:10px}.project-attendees>.row-actions{font-size:11px;flex-wrap:wrap}</style>
