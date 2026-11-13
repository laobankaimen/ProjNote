<script setup lang="ts">
import { computed } from 'vue'
const props=defineProps<{modelValue:string;disabled?:boolean}>(),emit=defineEmits<{'update:modelValue':[value:string]}>()
const parts=computed(()=>{const [date='',time='']=props.modelValue.split('T');const [hour='',minute='']=time.split(':');return {date,hour,minute}})
function update(field:'date'|'hour'|'minute',event:Event){const value=(event.target as HTMLInputElement|HTMLSelectElement).value,p={...parts.value,[field]:value};emit('update:modelValue',`${p.date}T${p.hour}:${p.minute}`)}
const hours=Array.from({length:24},(_,i)=>String(i).padStart(2,'0')),minutes=Array.from({length:60},(_,i)=>String(i).padStart(2,'0'))
</script>
<template><div class="local-datetime"><label>提醒日期<input :value="parts.date" type="date" required :disabled="disabled" @input="update('date',$event)"/></label><label>小时<select :value="parts.hour" required :disabled="disabled" @change="update('hour',$event)"><option value="" disabled>时</option><option v-for="hour in hours" :key="hour" :value="hour">{{hour}} 时</option></select></label><label>分钟<select :value="parts.minute" required :disabled="disabled" @change="update('minute',$event)"><option value="" disabled>分</option><option v-for="minute in minutes" :key="minute" :value="minute">{{minute}} 分</option></select></label></div></template>
<style scoped>.local-datetime{display:grid;grid-template-columns:minmax(0,2fr) minmax(80px,1fr) minmax(80px,1fr);gap:12px;margin-bottom:20px}.local-datetime label{margin:0}.local-datetime input,.local-datetime select{width:100%;min-width:0}@media(max-width:420px){.local-datetime{grid-template-columns:1fr 1fr}.local-datetime label:first-child{grid-column:1/-1}}</style>
