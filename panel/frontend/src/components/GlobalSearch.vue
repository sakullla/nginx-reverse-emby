<template>
  <Teleport to="body">
    <div v-if="open" class="global-search-overlay" @click.self="close">
      <div class="global-search-panel">
        <div class="global-search-input-wrap">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="11" cy="11" r="8"/>
            <line x1="21" y1="21" x2="16.65" y2="16.65"/>
          </svg>
          <input
            ref="inputRef"
            v-model="query"
            type="text"
            class="global-search-input"
            placeholder="跨节点搜索规则..."
            @keydown.escape="close"
          >
          <button v-if="query" class="clear-btn" @click="query = ''">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
              <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
            </svg>
          </button>
        </div>

        <div class="global-search-body">
          <div v-if="isLoading" class="global-search-state">
            <div class="spinner"></div>
            <span>搜索中...</span>
          </div>
          <div v-else-if="!query" class="global-search-state">
            <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
              <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
            </svg>
            <p>输入关键字搜索所有节点的规则</p>
          </div>
          <div v-else-if="!results.length" class="global-search-state">
            <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
              <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
            </svg>
            <p>未找到匹配结果</p>
          </div>
          <div v-else class="global-search-results">
            <div v-for="group in results" :key="group.agentId" class="result-group">
              <div class="result-group__header" @click="navigateToResult(group.agentId)">
                <div class="result-group__dot" :class="group.online ? 'result-group__dot--online' : 'result-group__dot--offline'"></div>
                <span class="result-group__name">{{ group.agentName }}</span>
                <span class="result-group__count">{{ group.rules.length }} 条</span>
              </div>
              <div
                v-for="rule in group.rules"
                :key="rule.id"
                class="result-item"
                @click="navigateToRule(group.agentId, rule)"
              >
                <div class="result-item__status" :class="rule.enabled ? 'on' : 'off'"></div>
                <div class="result-item__info">
                  <div class="result-item__url">{{ rule.frontend_url }}</div>
                  <div class="result-item__backend">→ {{ rule.backend_url }}</div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup>
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAgents } from '../hooks/useAgents'
import * as api from '../api'

const props = defineProps({
  open: { type: Boolean, default: false }
})

const emit = defineEmits(['update:open', 'select'])

const router = useRouter()
const { data: agentsData } = useAgents()
const query = ref('')
const inputRef = ref(null)
const results = ref([])
const isLoading = ref(false)

watch(() => props.open, (val) => {
  if (val) {
    setTimeout(() => inputRef.value?.focus(), 50)
  }
})

watch(query, async (val) => {
  if (!val?.trim()) {
    results.value = []
    return
  }
  isLoading.value = true
  try {
    const agents = agentsData.value || []
    const searches = agents
      .filter(a => a.status !== 'offline')
      .map(agent =>
        api.fetchRules(agent.id)
          .then(rules => ({
            agentId: agent.id,
            agentName: agent.name,
            online: agent.status === 'online',
            rules: (rules || []).filter(r =>
              r.frontend_url?.includes(val) ||
              r.backend_url?.includes(val) ||
              (r.tags || []).some(tag => tag.includes(val))
            )
          }))
          .catch(() => null)
      )
    const groupResults = await Promise.all(searches)
    results.value = groupResults.filter(g => g && g.rules.length > 0)
  } finally {
    isLoading.value = false
  }
}, { immediate: true })

function close() {
  emit('update:open', false)
  query.value = ''
}

function navigateToResult(agentId) {
  router.push({ path: '/rules', query: { agentId } })
  close()
}

function navigateToRule(agentId, rule) {
  emit('select', { agentId, rule })
  close()
}

function handleKeydown(e) {
  if ((e.metaKey || e.ctrlKey) && e.key === 'k') {
    e.preventDefault()
    emit('update:open', true)
  }
}

onMounted(() => document.addEventListener('keydown', handleKeydown))
onUnmounted(() => document.removeEventListener('keydown', handleKeydown))
</script>

<style scoped>
.global-search-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.5); backdrop-filter: blur(4px); z-index: var(--z-modal); display: flex; align-items: flex-start; justify-content: center; padding-top: 8vh; }
.global-search-panel { width: min(640px, 92vw); max-height: 80vh; background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); box-shadow: var(--shadow-2xl); display: flex; flex-direction: column; overflow: hidden; }
.global-search-input-wrap { display: flex; align-items: center; gap: 0.75rem; padding: 1rem 1.25rem; border-bottom: 1px solid var(--color-border-subtle); }
.global-search-input-wrap svg { color: var(--color-text-muted); flex-shrink: 0; }
.global-search-input { flex: 1; border: none; background: transparent; font-size: 1rem; color: var(--color-text-primary); outline: none; font-family: inherit; }
.global-search-input::placeholder { color: var(--color-text-muted); }
.clear-btn { display: flex; align-items: center; justify-content: center; width: 20px; height: 20px; border: none; background: var(--color-bg-hover); border-radius: 50%; color: var(--color-text-secondary); cursor: pointer; }
.global-search-body { flex: 1; overflow-y: auto; padding: 1rem; }
.global-search-state { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem; padding: 3rem 1rem; color: var(--color-text-muted); font-size: 0.875rem; text-align: center; }
.global-search-results { display: flex; flex-direction: column; gap: 1rem; }
.result-group__header { display: flex; align-items: center; gap: 0.5rem; margin-bottom: 0.5rem; }
.result-group__dot { width: 8px; height: 8px; border-radius: 50%; }
.result-group__dot--online { background: var(--color-primary); }
.result-group__dot--offline { background: var(--color-text-muted); }
.result-group__name { font-size: 0.875rem; font-weight: 600; color: var(--color-text-primary); flex: 1; }
.result-group__count { font-size: 0.75rem; color: var(--color-text-tertiary); background: var(--color-bg-subtle); padding: 1px 6px; border-radius: var(--radius-full); }
.result-item { display: flex; align-items: center; gap: 0.75rem; padding: 0.75rem 1rem; background: var(--color-bg-subtle); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-lg); cursor: pointer; transition: all 0.15s; }
.result-item:hover { border-color: var(--color-primary); background: var(--color-primary-subtle); transform: translateX(2px); }
.result-item__status { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
.result-item__status.on { background: var(--color-primary); }
.result-item__status.off { background: var(--color-text-muted); }
.result-item__info { flex: 1; min-width: 0; }
.result-item__url { font-size: 0.875rem; font-weight: 500; color: var(--color-text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.result-item__backend { font-size: 0.75rem; color: var(--color-text-tertiary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.spinner { width: 20px; height: 20px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
