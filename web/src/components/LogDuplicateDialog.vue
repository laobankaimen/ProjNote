<script setup lang="ts">
import Modal from './Modal.vue'
defineProps<{open:boolean;existing?:{title?:string;log_date:string;content:string;version:number};fallbackTitle:string;modelValue:string;busy:boolean;error?:string}>()
defineEmits<{'update:modelValue':[value:string];overwrite:[];keep:[];saveNew:[]}>()
</script>
<template>
<Modal :open="open" title="该日期和范围已有日志" @close="!busy&&$emit('keep')">
  <div class="modal-body duplicate-log-body">
    <p>已保存「{{existing?.title||fallbackTitle}}」。请选择处理方式，当前编辑内容会保留。</p>
    <details v-if="existing"><summary>查看已有日志（版本 {{existing.version}}）</summary><pre>{{existing.content}}</pre></details>
    <p v-if="error" class="form-error" role="alert">{{error}}</p>
    <div class="duplicate-log-options">
      <button type="button" class="button subtle" :disabled="busy||!existing" @click="$emit('overwrite')">覆盖已有日志</button>
      <button type="button" class="button subtle" :disabled="busy" @click="$emit('keep')">保留已有，不覆盖</button>
    </div>
    <div class="duplicate-log-new"><label>新日志标题<input :value="modelValue" maxlength="200" :disabled="busy" placeholder="为新日志填写一个标题" @input="$emit('update:modelValue',($event.target as HTMLInputElement).value)"/></label><button type="button" class="button primary" :disabled="busy||!modelValue.trim()" @click="$emit('saveNew')">{{busy?'正在保存…':'另存为新日志'}}</button></div>
  </div>
</Modal>
</template>
<style scoped>
.duplicate-log-body>p{line-height:1.8;margin:0 0 16px}.duplicate-log-body details{font-size:13px;margin-bottom:20px}.duplicate-log-body summary{cursor:pointer;color:var(--muted)}.duplicate-log-body pre{max-height:220px;overflow:auto;white-space:pre-wrap;font:inherit;line-height:1.8;padding:12px;background:#f5f8fc;border:1px solid var(--border);border-radius:6px}.duplicate-log-options{display:flex;flex-wrap:wrap;gap:10px}.duplicate-log-new{border-top:1px solid var(--border);padding-top:18px;margin-top:20px;display:flex;gap:12px;align-items:flex-end}.duplicate-log-new label{flex:1;margin:0;min-width:0}.duplicate-log-new .button{flex:none}@media(max-width:600px){.duplicate-log-new{display:block}.duplicate-log-new .button{margin-top:12px;width:100%}}
</style>
