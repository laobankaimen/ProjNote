<script setup lang="ts">
const props=defineProps<{offset:number;limit:number;total:number}>()
const emit=defineEmits<{change:[offset:number]}>()
</script>
<template><div class="pager" v-if="total>0"><span>共 {{total}} 条 <span class="muted">/ {{offset+1}}–{{Math.min(offset+limit,total)}}</span></span><div><button class="button subtle small" :disabled="offset===0" @click="emit('change',Math.max(0,offset-limit))">上一页</button><button class="button subtle small" :disabled="offset+limit>=total" @click="emit('change',offset+limit)">下一页</button></div></div></template>
