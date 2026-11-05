<script setup lang="ts">
import { ref } from 'vue'
import type { ExportFormat } from '../exports'
defineProps<{busy?:boolean;disabled?:boolean;label?:string}>()
const emit=defineEmits<{export:[format:ExportFormat]}>(),format=ref<ExportFormat>('docx')
</script>
<template><div class="format-export"><select v-model="format" :aria-label="label||'导出格式'" :disabled="busy||disabled"><option value="docx">Word</option><option value="xlsx">Excel</option><option value="md">Markdown</option><option value="json">JSON</option><option value="html">打印 / PDF</option></select><button type="button" class="button subtle small" :disabled="busy||disabled" @click="emit('export',format)">{{busy?'正在创建…':'导出比较结果'}}</button></div></template>
<style scoped>.format-export{display:flex;align-items:center;gap:8px;flex-wrap:wrap}.format-export select{min-width:110px;width:auto;font-size:12px;min-height:32px}</style>
