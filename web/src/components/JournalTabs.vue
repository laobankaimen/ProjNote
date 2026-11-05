<script setup lang="ts">
defineProps<{active:'personal'|'project'|'meetings'|'exports'}>()
</script>
<template><nav class="worklog-mode" aria-label="工作日志模式"><a href="#/worklogs" :class="{selected:active==='personal'}" :aria-current="active==='personal'?'page':undefined">个人日志</a><a href="#/project-journal" :class="{selected:active==='project'}" :aria-current="active==='project'?'page':undefined">项目日志</a><a href="#/meeting-minutes" :class="{selected:active==='meetings'}" :aria-current="active==='meetings'?'page':undefined">会议纪要</a><a href="#/exports" :class="{selected:active==='exports'}" :aria-current="active==='exports'?'page':undefined">导出中心</a></nav></template>

<style scoped>.worklog-mode{flex-wrap:wrap}.worklog-mode>a{white-space:nowrap}@media(max-width:500px){.worklog-mode>a{padding:10px 12px}}</style>
