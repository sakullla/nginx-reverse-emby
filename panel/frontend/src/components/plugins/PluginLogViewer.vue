<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { fetchPluginLogs } from '../../api/plugins'
import { sanitizePluginText } from '../../api/pluginSecurity'
import { formatPanelDateTime, panelTimeZone } from '../../utils/panelDateTime.js'
import BaseBadge from '../base/BaseBadge.vue'

const CONTROL_PLANE_AGENT_ID = 'control-plane'
const CONTROL_PLANE_LABEL = '控制面'

const props = defineProps({
  pluginId: { type: String, required: true },
  instanceId: { type: String, required: true },
  agents: { type: Array, default: () => [] }
})

const agentOptions = computed(() => {
  const options = []
  const seen = new Set()
  const push = (id, name) => {
    const key = String(id || '').trim()
    if (!key || seen.has(key)) return
    seen.add(key)
    options.push({ id: key, name: String(name || '').trim() })
  }
  for (const item of props.agents || []) {
    const record = agentRecord(item)
    push(record.id, record.name)
  }
  for (const entry of entries.value) {
    push(entry?.agent_id, '')
  }
  return options
})

const entries = ref([])
const agentID = ref('')
const loading = ref(false)
const error = ref('')
let generation = 0
let controller = null

onMounted(() => load(true))
watch(() => [props.pluginId, props.instanceId, agentID.value], () => load(true))
onBeforeUnmount(() => controller?.abort())

async function load(selectionChanged = false) {
  if (loading.value && !selectionChanged) return
  if (selectionChanged) controller?.abort()
  const requestGeneration = ++generation
  controller = new AbortController()
  loading.value = true
  error.value = ''
  const identity = { pluginID: props.pluginId, instanceID: props.instanceId, agentID: agentID.value }
  try {
    const page = await fetchPluginLogs(identity.pluginID, identity.instanceID, {
      agentID: identity.agentID,
      cursor: '',
      limit: 5,
      signal: controller.signal
    })
    if (requestGeneration !== generation) return
    entries.value = [...page.entries]
      .sort((left, right) => {
        const leftTime = Date.parse(left?.created_at || '') || 0
        const rightTime = Date.parse(right?.created_at || '') || 0
        return rightTime - leftTime
      })
      .slice(0, 5)
  } catch (cause) {
    if (requestGeneration === generation && cause?.name !== 'AbortError' && cause?.code !== 'ERR_CANCELED') {
      error.value = sanitizePluginText(cause?.message || '读取运行日志失败')
    }
  } finally {
    if (requestGeneration === generation) loading.value = false
  }
}

function levelTone(level) {
  const value = String(level || '').toLowerCase()
  if (['error', 'fatal', 'panic'].includes(value)) return 'danger'
  if (['warning', 'warn'].includes(value)) return 'warning'
  if (['debug', 'trace'].includes(value)) return 'neutral'
  return 'primary'
}

function agentRecord(value) {
  if (value && typeof value === 'object') {
    return { id: String(value.id || '').trim(), name: String(value.name || '').trim() }
  }
  return { id: String(value || '').trim(), name: '' }
}

function agentLabel(agentID) {
  const id = String(agentID || '').trim()
  if (id === CONTROL_PLANE_AGENT_ID) return CONTROL_PLANE_LABEL
  const match = agentOptions.value.find((item) => item.id === id)
  const name = String(match?.name || '').trim()
  return name || id
}

function formatStamp(value) {
  return formatPanelDateTime(value)
}

function displayMessage(value) {
  const text = String(value || '').replace(/^\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}(?:[.,]\d+)?\s+/, '').trim()
  return text || '无日志正文'
}
</script>

<template>
  <div class="plugin-log-viewer">
    <label class="plugin-log-viewer__filter">
      <span>Agent 过滤</span>
      <select v-model="agentID">
        <option value="">全部可见 Agent</option>
        <option v-for="agent in agentOptions" :key="agent.id" :value="agent.id">{{ agentLabel(agent.id) }}</option>
      </select>
    </label>

    <p v-if="error" class="plugin-log-viewer__error" role="alert">{{ error }}</p>
    <p v-else-if="loading && !entries.length" class="plugin-log-viewer__empty">正在读取运行日志…</p>
    <ol v-else-if="entries.length" class="plugin-log-list">
      <li v-for="(entry, index) in entries" :key="`${entry.created_at}-${index}`" :data-level="entry.level">
        <header>
          <BaseBadge :tone="levelTone(entry.level)" size="sm">{{ entry.level || 'info' }}</BaseBadge>
          <strong>{{ agentLabel(entry.agent_id) }}</strong>
          <time :datetime="entry.created_at" :title="entry.created_at" :data-timezone="panelTimeZone">{{ formatStamp(entry.created_at) }}</time>
        </header>
        <span>{{ displayMessage(entry.message) }}</span>
        <em v-if="entry.truncated">已截断</em>
      </li>
    </ol>
    <p v-else-if="!loading && !entries.length" class="plugin-log-viewer__empty">暂无宿主持久化运行日志。</p>
  </div>
</template>

<style scoped>
.plugin-log-viewer {
  display: grid;
  gap: var(--space-3);
  min-width: 0;
}

.plugin-log-viewer__filter {
  display: grid;
  gap: 0.35rem;
  max-width: 22rem;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.plugin-log-viewer__filter select {
  min-width: 0;
  width: 100%;
  padding: 0.55rem 0.7rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-canvas);
  color: var(--color-text-primary);
  font: inherit;
}

.plugin-log-viewer__error {
  margin: 0;
  padding: 0.65rem 0.8rem;
  border-radius: var(--radius-lg);
  background: var(--color-danger-50);
  color: var(--color-danger);
  font-size: var(--text-sm);
}

.plugin-log-viewer__empty {
  margin: 0;
  padding: 1.1rem 0.5rem;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
  text-align: center;
}

.plugin-log-list {
  display: grid;
  gap: 0.5rem;
  margin: 0;
  padding: 0;
  list-style: none;
}

.plugin-log-list li {
  display: grid;
  gap: 0.35rem;
  min-width: 0;
  padding: 0.7rem 0.8rem;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: color-mix(in srgb, var(--color-bg-subtle) 45%, var(--color-bg-surface));
}

.plugin-log-list li[data-level='error'],
.plugin-log-list li[data-level='fatal'] {
  border-color: color-mix(in srgb, var(--color-danger) 28%, var(--color-border-subtle));
}

.plugin-log-list li[data-level='warning'],
.plugin-log-list li[data-level='warn'] {
  border-color: color-mix(in srgb, var(--color-warning) 28%, var(--color-border-subtle));
}

.plugin-log-list header {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  min-width: 0;
}

.plugin-log-list strong {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.8125rem;
}

.plugin-log-list time {
  margin-left: auto;
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: 0.7rem;
  white-space: nowrap;
}

.plugin-log-list span {
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}

.plugin-log-list em {
  color: var(--color-warning);
  font-size: 0.75rem;
  font-style: normal;
}

@media (max-width: 42rem) {
  .plugin-log-viewer__filter {
    max-width: none;
  }

  .plugin-log-list header {
    flex-wrap: wrap;
  }

  .plugin-log-list time {
    margin-left: 0;
    width: 100%;
  }
}
</style>
