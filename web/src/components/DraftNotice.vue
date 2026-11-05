<script setup lang="ts">
import ConflictNotice from './ConflictNotice.vue'
import type { Conflict, DraftValue } from '../drafts'
import { dateTimeLabel } from '../api'
defineProps<{value?:DraftValue;values?:DraftValue[];error?:string;recovery?:Conflict}>();defineEmits<{restore:[];discard:[];force:[];cancelRecovery:[];select:[storageKey:string]}>()
</script>
<template><div v-if="error" class="form-error" role="alert">{{error}}</div><div v-if="value" class="draft-notice" role="status"><div><strong>浏览器中保留了 {{values?.length||1}} 份草稿</strong><span>{{dateTimeLabel(value.saved_at)}} · {{value.own?'本标签修改':'待恢复副本'}} · 尚未保存到数据库</span><label v-if="values&&values.length>1" class="draft-picker">选择草稿副本<select :value="value.storage_key" aria-label="选择待恢复的草稿副本" @change="$emit('select',($event.target as HTMLSelectElement).value)"><option v-for="(item,index) in values" :key="item.storage_key" :value="item.storage_key">{{dateTimeLabel(item.saved_at)}} · {{item.own?'本标签':'副本 '+(index+1)}}</option></select></label></div><button type="button" class="text-button" @click="$emit('restore')">恢复草稿</button><button type="button" class="text-button muted" @click="$emit('discard')">丢弃此副本</button></div><ConflictNotice :value="recovery" recovery @keep="$emit('force')" @reload="$emit('cancelRecovery')"/></template>
