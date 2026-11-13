<script setup lang="ts">
import { projectHref,projectViewLabels,type ProjectView } from '../projectRoutes'
defineProps<{active:ProjectView|'plans';projectId?:string}>()
</script>
<template><nav v-if="projectId" class="worklog-mode task-tabs" aria-label="项目功能"><a v-for="(label,view) in projectViewLabels" :key="view" :href="projectHref(projectId,view)" :class="{selected:active===view||(active==='plans'&&view==='plan-versions')}" :aria-current="active===view||(active==='plans'&&view==='plan-versions')?'page':undefined">{{label}}</a></nav></template>
<style scoped>.task-tabs{flex-wrap:wrap}.task-tabs a{white-space:nowrap}@media(max-width:500px){.task-tabs a{padding:10px 12px}}</style>
