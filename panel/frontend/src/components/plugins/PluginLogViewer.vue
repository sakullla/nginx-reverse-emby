<script setup>
import { onMounted, ref, watch } from 'vue'
import { fetchPluginLogs } from '../../api/plugins'
import { sanitizePluginText } from '../../api/pluginSecurity'
const props = defineProps({ pluginId: { type: String, required: true }, instanceId: { type: String, required: true }, agents: { type: Array, default: () => [] } })
const entries = ref([]); const nextCursor = ref(''); const agentID = ref(''); const loading = ref(false); const error = ref('')
onMounted(() => load(false)); watch(() => props.instanceId, () => load(false)); watch(agentID, () => load(false))
async function load(more) {
  if (loading.value) return
  loading.value = true; error.value = ''
  try {
    const page = await fetchPluginLogs(props.pluginId, props.instanceId, { agentID: agentID.value, cursor: more ? nextCursor.value : '', limit: 50 })
    entries.value = more ? entries.value.concat(page.entries) : page.entries
    nextCursor.value = page.next_cursor
  } catch (cause) { error.value = sanitizePluginText(cause?.message || '读取运行日志失败') } finally { loading.value = false }
}
</script>

<template>
  <div class="plugin-log-viewer">
    <label>Agent 过滤<select v-model="agentID"><option value="">全部可见 Agent</option><option v-for="agent in agents" :key="agent" :value="agent">{{ agent }}</option></select></label>
    <p v-if="error" role="alert">{{ error }}</p>
    <ol v-else class="plugin-log-list"><li v-for="(entry, index) in entries" :key="`${entry.created_at}-${index}`" :data-level="entry.level"><time>{{ entry.created_at }}</time><strong>{{ entry.agent_id }}</strong><span>{{ entry.message }}</span><em v-if="entry.truncated">已截断</em></li></ol>
    <p v-if="!loading && !entries.length">暂无宿主持久化运行日志。</p>
    <button v-if="nextCursor" class="btn btn-secondary" type="button" :disabled="loading" @click="load(true)">{{ loading ? '读取中…' : '加载更早日志' }}</button>
  </div>
</template>

<style scoped>
.plugin-log-viewer { display: grid; gap: var(--space-3); }.plugin-log-viewer label { display: flex; gap: var(--space-2); align-items: center; }.plugin-log-viewer select { padding: .5rem; }.plugin-log-list { display: grid; gap: var(--space-2); margin: 0; padding: 0; list-style: none; }.plugin-log-list li { min-width: 0; display: grid; grid-template-columns: minmax(10rem, auto) minmax(6rem, auto) minmax(0, 1fr) auto; gap: var(--space-2); padding: var(--space-2); background: var(--color-bg-subtle); }.plugin-log-list span { overflow-wrap: anywhere; }.plugin-log-list em { color: var(--color-warning); }@media (max-width: 42rem) { .plugin-log-list li { grid-template-columns: minmax(0, 1fr); } }
</style>
