<script setup lang="ts">
import { computed } from 'vue'
const props=defineProps<{text:string;query:string}>()
const parts=computed(()=>{const q=props.query.trim();if(!q)return[{text:props.text,match:false}];const lower=props.text.toLocaleLowerCase(),needle=q.toLocaleLowerCase(),result:Array<{text:string;match:boolean}>=[];let start=0,index=lower.indexOf(needle);while(index>=0){if(index>start)result.push({text:props.text.slice(start,index),match:false});result.push({text:props.text.slice(index,index+q.length),match:true});start=index+q.length;index=lower.indexOf(needle,start)}if(start<props.text.length)result.push({text:props.text.slice(start),match:false});return result})
</script>
<template><template v-for="(part,index) in parts" :key="index"><mark v-if="part.match">{{part.text}}</mark><template v-else>{{part.text}}</template></template></template>
