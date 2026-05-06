<template>
  <div class="quick-agent-select">
    <div v-if="!agents.length" class="quick-agent-select__empty">
      暂无可用节点
    </div>
    <div v-else class="quick-agent-select__chips">
      <button
        v-for="agent in visibleAgents"
        :key="agent.id"
        class="quick-agent-select__chip"
        :class="{ 'quick-agent-select__chip--active': agent.id === agentId }"
        :title="agent.name"
        @click="select(agent)"
      >
        <span
          class="quick-agent-select__status-dot"
          :class="`quick-agent-select__status-dot--${getAgentStatus(agent)}`"
        />
        <span class="quick-agent-select__chip-name">{{ agent.name }}</span>
      </button>

      <div
        v-if="hiddenAgents.length"
        class="quick-agent-select__more"
        ref="moreRef"
      >
        <button
          class="quick-agent-select__chip quick-agent-select__chip--more"
          @click="moreOpen = !moreOpen"
        >
          +{{ hiddenAgents.length }} 更多
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="6 9 12 15 18 9"/>
          </svg>
        </button>
        <div v-if="moreOpen" class="quick-agent-select__dropdown">
          <div class="quick-agent-select__dropdown-search">
            <input
              v-model="moreSearch"
              class="quick-agent-select__dropdown-input"
              placeholder="搜索节点..."
            />
          </div>
          <div class="quick-agent-select__dropdown-list">
            <button
              v-for="agent in filteredHiddenAgents"
              :key="agent.id"
              class="quick-agent-select__dropdown-item"
              :class="{ active: agent.id === agentId }"
              @click="select(agent)"
            >
              <span
                class="quick-agent-select__status-dot"
                :class="`quick-agent-select__status-dot--${getAgentStatus(agent)}`"
              />
              <span class="quick-agent-select__dropdown-name">{{ agent.name }}</span>
            </button>
            <div v-if="!filteredHiddenAgents.length" class="quick-agent-select__dropdown-empty">
              没有匹配的节点
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { getAgentStatus } from '../utils/agentHelpers.js'

const props = defineProps({
  agentId: { type: String, default: null },
  agents: { type: Array, required: true }
})

const emit = defineEmits(['update:agentId'])

const MAX_VISIBLE = 5

const moreOpen = ref(false)
const moreSearch = ref('')
const moreRef = ref(null)

const sortedAgents = computed(() => {
  // Base sort: name order (fixed)
  const list = [...props.agents].sort((a, b) => a.name.localeCompare(b.name))

  // If selected agent is in hidden region, swap it into the last visible slot
  if (props.agentId) {
    const selectedIdx = list.findIndex(a => a.id === props.agentId)
    if (selectedIdx >= MAX_VISIBLE) {
      const selected = list[selectedIdx]
      // Remove from current position
      list.splice(selectedIdx, 1)
      // Insert at last visible position (index MAX_VISIBLE - 1)
      list.splice(MAX_VISIBLE - 1, 0, selected)
    }
  }

  return list
})

const visibleAgents = computed(() => sortedAgents.value.slice(0, MAX_VISIBLE))
const hiddenAgents = computed(() => sortedAgents.value.slice(MAX_VISIBLE))

const filteredHiddenAgents = computed(() => {
  const q = moreSearch.value.trim().toLowerCase()
  if (!q) return hiddenAgents.value
  return hiddenAgents.value.filter(a =>
    a.name.toLowerCase().includes(q)
  )
})

function select(agent) {
  emit('update:agentId', agent.id)
  moreOpen.value = false
  moreSearch.value = ''
}

function handleClickOutside(e) {
  if (moreRef.value && !moreRef.value.contains(e.target)) {
    moreOpen.value = false
    moreSearch.value = ''
  }
}

onMounted(() => document.addEventListener('mousedown', handleClickOutside))
onUnmounted(() => document.removeEventListener('mousedown', handleClickOutside))
</script>

<style scoped>
.quick-agent-select {
  margin-bottom: 1.25rem;
}

.quick-agent-select__empty {
  font-size: 0.875rem;
  color: var(--color-text-muted);
  padding: 0.5rem 0;
}

.quick-agent-select__chips {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.quick-agent-select__chip {
  display: inline-flex;
  align-items: center;
  gap: 0.375rem;
  padding: 0.375rem 0.875rem;
  border-radius: var(--radius-full);
  border: 1px solid var(--color-border);
  background: var(--color-surface-elevated);
  color: var(--color-text-primary);
  font-size: 0.8125rem;
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  font-family: inherit;
  white-space: nowrap;
  max-width: 160px;
}

.quick-agent-select__chip:hover {
  border-color: var(--color-primary);
  background: var(--color-bg-hover);
}

.quick-agent-select__chip--active {
  background: var(--color-primary);
  color: #fff;
  border-color: var(--color-primary);
}

.quick-agent-select__chip--active:hover {
  background: var(--color-primary-hover);
  border-color: var(--color-primary-hover);
}

.quick-agent-select__chip--more {
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  border-color: var(--color-border);
  padding-right: 0.625rem;
}

.quick-agent-select__chip--more:hover {
  background: var(--color-bg-hover);
}

.quick-agent-select__chip-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.quick-agent-select__status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.quick-agent-select__status-dot--online {
  background: var(--color-success);
}

.quick-agent-select__status-dot--offline {
  background: var(--color-text-muted);
}

.quick-agent-select__status-dot--failed {
  background: var(--color-danger);
}

.quick-agent-select__status-dot--pending {
  background: var(--color-warning);
}

.quick-agent-select__more {
  position: relative;
}

.quick-agent-select__dropdown {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  width: 220px;
  background: var(--color-bg-surface-raised);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  z-index: var(--z-dropdown);
  animation: scaleIn 0.15s var(--ease-bounce) both;
  overflow: hidden;
}

.quick-agent-select__dropdown-search {
  padding: 0.5rem;
  border-bottom: 1px solid var(--color-border-subtle);
}

.quick-agent-select__dropdown-input {
  width: 100%;
  padding: 0.375rem 0.625rem;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  font-size: 0.8rem;
  color: var(--color-text-primary);
  outline: none;
  font-family: inherit;
  box-sizing: border-box;
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.quick-agent-select__dropdown-input:focus {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.quick-agent-select__dropdown-list {
  max-height: 240px;
  overflow-y: auto;
  padding: 0.25rem;
  scrollbar-width: thin;
}

.quick-agent-select__dropdown-list::-webkit-scrollbar { width: 6px; }
.quick-agent-select__dropdown-list::-webkit-scrollbar-track { background: transparent; }
.quick-agent-select__dropdown-list::-webkit-scrollbar-thumb { background: var(--color-border-default); border-radius: 3px; }

.quick-agent-select__dropdown-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  width: 100%;
  padding: 0.5rem 0.625rem;
  border: none;
  background: transparent;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default);
  font-family: inherit;
  text-align: left;
}

.quick-agent-select__dropdown-item:hover {
  background: var(--color-bg-hover);
}

.quick-agent-select__dropdown-item.active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.quick-agent-select__dropdown-name {
  font-size: 0.8125rem;
  color: var(--color-text-primary);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.quick-agent-select__dropdown-empty {
  padding: 1rem;
  text-align: center;
  font-size: 0.8125rem;
  color: var(--color-text-muted);
}

@media (max-width: 768px) {
  .quick-agent-select__chip {
    padding: 0.3125rem 0.625rem;
    font-size: 0.75rem;
    max-width: 120px;
  }
}
</style>
