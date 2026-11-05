<script setup lang="ts">
import { nextTick, onMounted, onUnmounted, ref, useId, watch } from 'vue'
import { X } from 'lucide-vue-next'
const props=defineProps<{open:boolean;title:string;wide?:boolean}>()
const emit=defineEmits<{close:[]}>()
const titleId=useId()
const dialog=ref<HTMLDialogElement>()
let previous: HTMLElement | null = null
function sync() { if (!dialog.value) return; if (props.open && !dialog.value.open) { previous=document.activeElement as HTMLElement; dialog.value.showModal(); nextTick(()=>{ const el=dialog.value?.querySelector<HTMLElement>('[autofocus]')||dialog.value?.querySelector<HTMLElement>('input:not([type="hidden"]),textarea,select'); el?.focus() }) } else if (!props.open && dialog.value.open) { dialog.value.close(); previous?.focus() } }
watch(()=>props.open,sync)
onMounted(sync)
onUnmounted(()=>{ if(dialog.value?.open)dialog.value.close() })
</script>
<template><dialog ref="dialog" class="modal" :class="{wide}" :aria-labelledby="titleId" @cancel.prevent="emit('close')" @click="($event.target===dialog) && emit('close')"><div class="modal-heading"><h2 :id="titleId">{{title}}</h2><button class="icon-button" aria-label="关闭对话框" @click="emit('close')"><X :size="20"/></button></div><slot/></dialog></template>
