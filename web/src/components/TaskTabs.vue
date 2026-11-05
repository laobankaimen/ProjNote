<script setup lang="ts">
defineProps<{active:'tasks'|'plans'|'requirements'}>()
</script>
<template><nav class="worklog-mode task-tabs" aria-label="任务管理模式"><a href="#/tasks" :class="{selected:active==='tasks'}" :aria-current="active==='tasks'?'page':undefined">任务列表</a><a href="#/plan-versions" :class="{selected:active==='plans'}" :aria-current="active==='plans'?'page':undefined">计划版本</a><a href="#/requirements" :class="{selected:active==='requirements'}" :aria-current="active==='requirements'?'page':undefined">需求管理</a></nav></template>
<style scoped>.task-tabs{flex-wrap:wrap}.task-tabs a{white-space:nowrap}@media(max-width:500px){.task-tabs a{padding:10px 12px}}</style>
