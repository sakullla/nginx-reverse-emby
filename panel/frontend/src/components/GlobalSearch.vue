<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="global-search-overlay"
      role="dialog"
      aria-modal="true"
      aria-label="全局搜索"
      @click.self="close"
    >
      <div class="global-search-panel" @keydown="handlePanelKeydown">
        <div class="global-search-input-wrap">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="11" cy="11" r="8"/>
            <line x1="21" y1="21" x2="16.65" y2="16.65"/>
          </svg>
          <input
            ref="inputRef"
            v-model="query"
            name="global-search"
            type="text"
            class="global-search-input"
            placeholder="跨节点搜索规则 / 监听器 / 证书 / 节点..."
            role="combobox"
            aria-expanded="true"
            aria-controls="global-search-results"
            aria-autocomplete="list"
            :aria-activedescendant="activeIndex >= 0 ? `gs-item-${activeIndex}` : undefined"
          >
          <button v-if="query" type="button" class="clear-btn" aria-label="清除搜索" @click="query = ''">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
              <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
            </svg>
          </button>
        </div>

        <div class="global-search-body">
          <div v-if="hasFetchFailures" class="global-search-partial" role="status">
            部分节点请求失败，结果可能不完整
          </div>
          <div v-if="isLoading" class="global-search-state">
            <div class="spinner"></div>
            <span>搜索中...</span>
          </div>
          <div v-else-if="!query" class="global-search-state">
            <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
              <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
            </svg>
            <p>输入关键字搜索所有节点的规则、监听器、证书和节点</p>
          </div>
          <div v-else-if="!results.length" class="global-search-state">
            <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
              <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
            </svg>
            <p>未找到匹配结果</p>
          </div>
          <div v-else id="global-search-results" class="global-search-results" role="listbox">
            <div v-for="group in results" :key="group.agentId" class="result-group">
              <div class="result-group__header" @click="navigateToResult(group)">
                <div class="result-group__dot" :class="group.online ? 'result-group__dot--online' : 'result-group__dot--offline'"></div>
                <span class="result-group__name">{{ group.agentName }}</span>
                <span class="result-group__count">{{ group.items.length }} 条</span>
              </div>
              <div
                v-for="item in group.items"
                :key="`${item._type}-${item.id}`"
                :id="`gs-item-${flatIndexOf(group.agentId, item)}`"
                class="result-item"
                :class="{ 'result-item--active': flatIndexOf(group.agentId, item) === activeIndex }"
                role="option"
                :aria-selected="flatIndexOf(group.agentId, item) === activeIndex"
                @click="navigateToItem(group.agentId, item)"
                @mousemove="activeIndex = flatIndexOf(group.agentId, item)"
              >
                <div class="result-item__type-badge" :class="`result-item__type-badge--${item._type}`">
                  {{ typeLabel(item._type) }}
                </div>
                <div class="result-item__info">
                  <div class="result-item__url">
                    {{ item._type === 'agent'
                      ? item.name
                      : item.frontend_url || item.domain || item.name || `${item.listen_host || ''}:${item.listen_port}` || `#${item.id}` }}
                  </div>
                  <div v-if="item._type === 'agent'" class="result-item__backend">{{ getAgentEndpointLabel(item) }}</div>
                  <div v-else-if="item._type === 'rule'" class="result-item__backend">→ {{ formatHttpBackend(item) }}</div>
                  <div v-else-if="item._type === 'l4'" class="result-item__backend">{{ item.protocol?.toUpperCase() }} {{ item.listen_host || '*' }}:{{ item.listen_port }} → {{ formatL4Backend(item) }}</div>
                  <div v-else-if="item._type === 'cert'" class="result-item__backend">{{ getCertStatus(item) }}</div>
                  <div v-else-if="item._type === 'relay'" class="result-item__backend">{{ item.public_host || item.bind_hosts?.[0] || '' }}:{{ item.public_port || item.listen_port || '' }}</div>
                </div>
              </div>
            </div>
          </div>
        </div>

        <div class="global-search-footer">
          <span class="gs-hint"><kbd>↑</kbd><kbd>↓</kbd> 选择</span>
          <span class="gs-hint"><kbd>Enter</kbd> 打开</span>
          <span class="gs-hint"><kbd>Esc</kbd> 关闭</span>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<script setup>
import { ref, computed, watch, nextTick, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useAgents } from '../hooks/useAgents'
import { parseIdQuery } from '../hooks/useIdSearch'
import { getAgentEndpointLabel } from '../utils/agentHelpers.js'
import * as api from '../api'

// Result type: 'rule' | 'l4' | 'cert'
function makeResult(type, agentId, agentName, online, items) {
  return { type, agentId, agentName, online, items }
}

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
const searchDebounceTimer = ref(null)
const searchId = ref(0)
const activeIndex = ref(-1)
const hasFetchFailures = ref(false)

// Flattened [agentId, item] pairs in display order — the keyboard cursor moves
// through this list.
const flatEntries = computed(() =>
  results.value.flatMap(group => group.items.map(item => ({ agentId: group.agentId, item })))
)

function flatIndexOf(agentId, item) {
  return flatEntries.value.findIndex(e => e.agentId === agentId && e.item === item)
}

watch(flatEntries, (entries) => {
  activeIndex.value = entries.length ? 0 : -1
})

watch(activeIndex, async (index) => {
  if (index < 0) return
  await nextTick()
  document.getElementById(`gs-item-${index}`)?.scrollIntoView?.({ block: 'nearest' })
})

function handlePanelKeydown(e) {
  if (e.key === 'Escape') {
    close()
    return
  }
  const count = flatEntries.value.length
  if (!count) return
  if (e.key === 'ArrowDown') {
    e.preventDefault()
    activeIndex.value = (activeIndex.value + 1) % count
  } else if (e.key === 'ArrowUp') {
    e.preventDefault()
    activeIndex.value = (activeIndex.value - 1 + count) % count
  } else if (e.key === 'Enter') {
    const entry = flatEntries.value[activeIndex.value]
    if (entry) {
      e.preventDefault()
      navigateToItem(entry.agentId, entry.item)
    }
  }
}

function httpBackendUrls(rule) {
  if (Array.isArray(rule?.backends) && rule.backends.length > 0) {
    return rule.backends
      .map((backend) => String(backend?.url || '').trim())
      .filter(Boolean)
  }
  return []
}

function formatHttpBackend(rule) {
  const backends = httpBackendUrls(rule)
  if (backends.length === 0) return '-'
  if (backends.length === 1) return backends[0]
  return `${backends[0]} +${backends.length - 1}`
}

function l4BackendAddresses(rule) {
  if (Array.isArray(rule?.backends) && rule.backends.length > 0) {
    return rule.backends
      .map((backend) => {
        const host = String(backend?.host || '').trim()
        const port = Number(backend?.port)
        return host && Number.isInteger(port) && port > 0 ? `${host}:${port}` : ''
      })
      .filter(Boolean)
  }

  return []
}

function formatL4Backend(rule) {
  const backends = l4BackendAddresses(rule)
  if (backends.length === 0) return '-'
  if (backends.length === 1) return backends[0]
  return `${backends[0]} +${backends.length - 1}`
}

watch(() => props.open, (val) => {
  if (val) {
    setTimeout(() => inputRef.value?.focus(), 50)
  }
})

watch(query, (val) => {
  clearTimeout(searchDebounceTimer.value)
  if (!val?.trim()) {
    results.value = []
    return
  }
  searchDebounceTimer.value = setTimeout(() => {
    doSearch(val)
  }, 250)
})

async function doSearch(val) {
  const currentSearchId = ++searchId.value
  isLoading.value = true
  hasFetchFailures.value = false
  try {
    const agents = agentsData.value || []
    if (!agents.length) {
      if (currentSearchId === searchId.value) results.value = []
      return
    }
    const agentIds = agents.map(a => a.id)
    const settled = await Promise.all([
      api.fetchAllAgentsRules(agentIds).then((value) => ({ ok: true, value }), () => ({ ok: false })),
      api.fetchAllAgentsL4Rules(agentIds).then((value) => ({ ok: true, value }), () => ({ ok: false })),
      api.fetchAllAgentsCertificates(agentIds).then((value) => ({ ok: true, value }), () => ({ ok: false })),
      api.fetchAllAgentsRelayListeners(agentIds).then((value) => ({ ok: true, value }), () => ({ ok: false }))
    ])
    if (currentSearchId !== searchId.value) return
    hasFetchFailures.value = settled.some((entry) => !entry.ok)
    const [rulesResults, l4Results, certsResults, relayResults] = settled.map((entry) => (entry.ok ? entry.value : []))

    const rulesByAgent = Object.fromEntries(rulesResults.map(r => [r.agentId, r.rules || []]))
    const l4ByAgent = Object.fromEntries(l4Results.map(r => [r.agentId, r.l4Rules || []]))
    const certsByAgent = Object.fromEntries(certsResults.map(r => [r.agentId, r.certificates || []]))
    const relayByAgent = Object.fromEntries(relayResults.map(r => [r.agentId, r.listeners || []]))

    const q = val.toLowerCase()
    const groupResults = []

    // #id= exact match branch: search across all agents by record id
    const idQuery = parseIdQuery(val)
    if (idQuery) {
      const targetId = idQuery.id
      for (const agent of agents) {
        const rules = rulesByAgent[agent.id] || []
        const l4Rules = l4ByAgent[agent.id] || []
        const certs = certsByAgent[agent.id] || []
        const matchedRules = rules.filter(r => String(r.id) === targetId)
          .map(r => ({ ...r, _type: 'rule' }))
        const matchedL4 = l4Rules.filter(r => String(r.id) === targetId)
          .map(r => ({ ...r, _type: 'l4' }))
        const matchedCerts = certs.filter(c => String(c.id) === targetId)
          .map(c => ({ ...c, _type: 'cert' }))

        const items = [...matchedRules, ...matchedL4, ...matchedCerts]
        if (items.length) {
          groupResults.push(makeResult(null, agent.id, agent.name, agent.status === 'online', items))
        }
      }
      if (currentSearchId === searchId.value) results.value = groupResults
      return
    }

    // Agent results
    const matchedAgents = agents.filter(a =>
      String(a.name || '').toLowerCase().includes(q) ||
      String(a.agent_url || '').toLowerCase().includes(q) ||
      String(a.ddns_domain || '').toLowerCase().includes(q) ||
      String(a.last_seen_ip || '').toLowerCase().includes(q) ||
      (a.tags || []).some(tag => String(tag).toLowerCase().includes(q))
    )
    if (matchedAgents.length) {
      groupResults.push(makeResult('agent', null, '节点', null, matchedAgents.map(a => ({ ...a, _type: 'agent' }))))
    }

    for (const agent of agents) {
      const rules = rulesByAgent[agent.id] || []
      const l4Rules = l4ByAgent[agent.id] || []
      const certs = certsByAgent[agent.id] || []
      const relays = relayByAgent[agent.id] || []

      const matchedRules = rules.filter(r =>
        r.frontend_url?.toLowerCase().includes(q) ||
        httpBackendUrls(r).some((backend) => backend.toLowerCase().includes(q)) ||
        (r.tags || []).some(tag => tag.toLowerCase().includes(q))
      )
      const matchedL4 = l4Rules.filter(r =>
        String(r.protocol || '').toLowerCase().includes(q) ||
        String(r.listen_host || '').toLowerCase().includes(q) ||
        l4BackendAddresses(r).some((backend) => backend.toLowerCase().includes(q)) ||
        String(r.listen_port || '').includes(q) ||
        (r.tags || []).some(tag => tag.toLowerCase().includes(q))
      )
      const matchedCerts = certs.filter(c =>
        c.domain?.toLowerCase().includes(q) ||
        (c.tags || []).some(tag => tag.toLowerCase().includes(q))
      )
      const matchedRelays = relays.filter(r =>
        String(r.name || '').toLowerCase().includes(q) ||
        String(r.public_host || '').toLowerCase().includes(q) ||
        (r.bind_hosts || []).some(h => String(h).toLowerCase().includes(q)) ||
        String(r.listen_port || '').includes(q) ||
        (r.tags || []).some(tag => tag.toLowerCase().includes(q))
      )
      const items = [
        ...matchedRules.map(r => ({ ...r, _type: 'rule' })),
        ...matchedL4.map(r => ({ ...r, _type: 'l4' })),
        ...matchedCerts.map(c => ({ ...c, _type: 'cert' })),
        ...matchedRelays.map(r => ({ ...r, _type: 'relay' }))
      ]
      if (items.length) {
        groupResults.push(makeResult(null, agent.id, agent.name, agent.status === 'online', items))
      }
    }
    if (currentSearchId === searchId.value) results.value = groupResults
  } finally {
    if (currentSearchId === searchId.value) isLoading.value = false
  }
}

function close() {
  emit('update:open', false)
  query.value = ''
}

function navigateToResult(group) {
  // Route by group type: agent groups go to the agent list, per-agent groups
  // keep the rules page pre-fill behavior.
  if (group.type === 'agent') {
    router.push('/agents')
  } else {
    router.push({ path: '/rules', query: { agentId: group.agentId, search: query.value } })
  }
  close()
}

function navigateToItem(agentId, item) {
  close()
  if (item._type === 'agent') {
    router.push(`/agents/${item.id}`)
  } else if (item._type === 'rule') {
    router.push({ path: '/rules', query: { agentId, search: `#id=${item.id}` } })
  } else if (item._type === 'l4') {
    router.push({ path: '/l4', query: { agentId, search: `#id=${item.id}` } })
  } else if (item._type === 'cert') {
    router.push({ path: '/certs', query: { agentId, search: `#id=${item.id}` } })
  } else if (item._type === 'relay') {
    router.push({ path: '/relay-listeners', query: { agentId } })
  }
}

function typeLabel(type) {
  return type === 'rule' ? 'HTTP' : type === 'l4' ? 'L4' : type === 'cert' ? '证书' : type === 'relay' ? 'Relay' : '节点'
}

function getCertStatus(cert) {
  return cert.status === 'active' ? '生效中' : cert.status === 'pending' ? '待签发' : '未激活'
}

function isEditableTarget(el) {
  return !!el?.closest?.('input, textarea, select, [contenteditable="true"], [role="textbox"]')
}

function handleKeydown(e) {
  if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === 'k') {
    e.preventDefault()
    if (props.open) close()
    else emit('update:open', true)
    return
  }
  if (!props.open && e.key === '/' && !isEditableTarget(e.target)) {
    e.preventDefault()
    emit('update:open', true)
  }
}

onMounted(() => document.addEventListener('keydown', handleKeydown))
onUnmounted(() => document.removeEventListener('keydown', handleKeydown))
</script>

<style scoped>
.global-search-overlay { position: fixed; inset: 0; background: var(--color-overlay); backdrop-filter: blur(4px); z-index: var(--z-modal); display: flex; align-items: flex-start; justify-content: center; padding-top: 8vh; }
.global-search-panel { width: min(640px, 92vw); max-height: 80vh; max-height: 80dvh; background: var(--color-bg-surface); border: var(--border-width-thin) solid var(--color-border-default); border-radius: var(--radius-2xl); box-shadow: var(--shadow-2xl); display: flex; flex-direction: column; overflow: hidden; }
.global-search-input-wrap { display: flex; align-items: center; gap: var(--space-3); padding: var(--space-4) var(--space-5); border-bottom: var(--border-width-thin) solid var(--color-border-subtle); }
.global-search-input-wrap svg { color: var(--color-text-muted); flex-shrink: 0; }
.global-search-input { flex: 1; border: none; background: transparent; font-size: var(--text-base); color: var(--color-text-primary); outline: none; font-family: inherit; }
.global-search-input::placeholder { color: var(--color-text-muted); }
.clear-btn { display: flex; align-items: center; justify-content: center; width: 20px; height: 20px; border: none; background: var(--color-bg-hover); border-radius: 50%; color: var(--color-text-secondary); cursor: pointer; }
.global-search-body { flex: 1; overflow-y: auto; padding: var(--space-4); }
.global-search-partial { display: flex; align-items: center; gap: var(--space-1-5); margin-bottom: var(--space-3); padding: var(--space-2) var(--space-3); border: var(--border-width-thin) solid var(--color-warning-subtle); border-radius: var(--radius-lg); background: var(--color-warning-subtle); color: var(--color-warning); font-size: var(--text-xs); }
.global-search-state { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: var(--space-3); padding: var(--space-12) var(--space-4); color: var(--color-text-muted); font-size: var(--text-sm); text-align: center; }
.global-search-results { display: flex; flex-direction: column; gap: var(--space-4); }
.result-group__header { display: flex; align-items: center; gap: var(--space-2); margin-bottom: var(--space-2); }
.result-group__dot { width: 8px; height: 8px; border-radius: 50%; }
.result-group__dot--online { background: var(--color-primary); }
.result-group__dot--offline { background: var(--color-text-muted); }
.result-group__name { font-size: var(--text-sm); font-weight: var(--font-semibold); color: var(--color-text-primary); flex: 1; }
.result-group__count { font-size: var(--text-xs); color: var(--color-text-tertiary); background: var(--color-bg-subtle); padding: var(--space-0-5) var(--space-1-5); border-radius: var(--radius-full); }
.result-item { display: flex; align-items: center; gap: var(--space-3); padding: var(--space-3) var(--space-4); background: var(--color-bg-subtle); border: var(--border-width-thin) solid var(--color-border-subtle); border-radius: var(--radius-lg); cursor: pointer; transition: all var(--duration-fast) var(--ease-default); }
.result-item:hover,
.result-item--active { border-color: var(--color-primary); background: var(--color-primary-subtle); }
.result-item:hover { transform: translateX(2px); }
.result-item__status { width: 8px; height: 8px; border-radius: 50%; flex-shrink: 0; }
.result-item__status.on { background: var(--color-primary); }
.result-item__status.off { background: var(--color-text-muted); }
.result-item__type-badge { font-size: var(--text-xs); font-weight: var(--font-bold); padding: var(--space-0-5) var(--space-1); border-radius: var(--radius-full); flex-shrink: 0; text-transform: uppercase; letter-spacing: 0.03em; }
.result-item__type-badge--rule { background: var(--color-primary-subtle); color: var(--color-primary); }
.result-item__type-badge--l4 { background: var(--color-accent-subtle); color: var(--color-accent); }
.result-item__type-badge--cert { background: var(--color-success-subtle); color: var(--color-success); }
.result-item__type-badge--agent { background: var(--color-badge-agent-bg); color: var(--color-badge-agent-ink); }
.result-item__type-badge--relay { background: var(--color-warning-subtle); color: var(--color-warning); }
.result-item__info { flex: 1; min-width: 0; }
.result-item__url { font-size: var(--text-sm); font-weight: var(--font-medium); color: var(--color-text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.result-item__backend { font-size: var(--text-xs); color: var(--color-text-tertiary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.global-search-footer { display: flex; align-items: center; gap: var(--space-4); padding: var(--space-2) var(--space-5); border-top: var(--border-width-thin) solid var(--color-border-subtle); color: var(--color-text-secondary); font-size: var(--text-xs); }
.gs-hint { display: inline-flex; align-items: center; gap: var(--space-1); }
.gs-hint kbd { display: inline-flex; align-items: center; justify-content: center; min-width: var(--space-5); padding: var(--space-0-5) var(--space-1-5); border: var(--border-width-thin) solid var(--color-border-default); border-bottom-width: var(--focus-outline-width); border-radius: var(--radius-sm); background: var(--color-bg-subtle); font-family: inherit; font-size: var(--text-xs); color: var(--color-text-secondary); }
@media (max-width: 640px) { .global-search-footer { display: none; } }
.spinner { width: 20px; height: 20px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
