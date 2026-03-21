<template>
  <div class="mobile-selector">
    <button
      class="mobile-selector__trigger"
      @click="isOpen = !isOpen"
      :disabled="!agents.length"
    >
      <div class="mobile-selector__current">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
          <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
        </svg>
        <span>{{ selectedAgent?.name || '选择节点' }}</span>
      </div>
      <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <polyline points="6 9 12 15 18 9"/>
      </svg>
    </button>

    <Teleport to="body">
      <div v-if="isOpen" class="mobile-selector__overlay" @click="isOpen = false">
        <div class="mobile-selector__menu" @click.stop>
          <div class="mobile-selector__header">
            <h3>选择 Agent 节点</h3>
            <button @click="isOpen = false" class="btn btn--icon btn--ghost">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18"/>
                <line x1="6" y1="6" x2="18" y2="18"/>
              </svg>
            </button>
          </div>
          <div class="mobile-selector__list">
            <div
              v-for="agent in agents"
              :key="agent.id"
              class="mobile-selector__item"
              :class="{ 'mobile-selector__item--active': selectedAgentId === agent.id }"
              @click="selectAgent(agent.id)"
            >
              <div class="mobile-selector__item-content">
                <div class="mobile-selector__item-name">{{ agent.name }}</div>
                <div class="mobile-selector__item-url">{{ agent.agent_url || '本机节点' }}</div>
              </div>
              <span
                class="badge"
                :class="agent.status === 'online' ? 'badge--success' : 'badge--danger'"
              >
                {{ agent.status === 'online' ? '在线' : '离线' }}
              </span>
            </div>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'

const props = defineProps({
  agents: { type: Array, required: true },
  selectedAgentId: { type: String, default: '' }
})

const emit = defineEmits(['select'])

const isOpen = ref(false)

const selectedAgent = computed(() =>
  props.agents.find(a => a.id === props.selectedAgentId)
)

const selectAgent = (id) => {
  emit('select', id)
  isOpen.value = false
}
</script>

<style scoped>
.mobile-selector {
  display: none;
}

.mobile-selector__trigger {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
}

.mobile-selector__trigger:hover:not(:disabled) {
  border-color: var(--color-border-strong);
}

.mobile-selector__trigger:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.mobile-selector__current {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.mobile-selector__overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  z-index: 1000;
  display: flex;
  align-items: flex-end;
  animation: fadeIn 0.2s ease;
}

@keyframes fadeIn {
  from { opacity: 0; }
  to { opacity: 1; }
}

.mobile-selector__menu {
  width: 100%;
  max-height: 70vh;
  background: var(--color-bg-surface);
  border-radius: var(--radius-xl) var(--radius-xl) 0 0;
  display: flex;
  flex-direction: column;
  animation: slideUp 0.3s ease;
  box-shadow: 0 -4px 20px rgba(0, 0, 0, 0.15);
}

@keyframes slideUp {
  from { transform: translateY(100%); }
  to { transform: translateY(0); }
}

.mobile-selector__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-4);
  border-bottom: 1px solid var(--color-border-default);
  background: var(--color-bg-subtle);
}

.mobile-selector__header h3 {
  font-size: var(--text-base);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  margin: 0;
}

.mobile-selector__list {
  overflow-y: auto;
  padding: var(--space-2);
}

.mobile-selector__item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: var(--space-3);
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default);
  margin: var(--space-1) 0;
}

.mobile-selector__item:hover {
  background: var(--color-bg-hover);
}

.mobile-selector__item--active {
  background: var(--color-primary-subtle);
  border: 1px solid var(--color-primary);
}

.mobile-selector__item-content {
  flex: 1;
  min-width: 0;
}

.mobile-selector__item-name {
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
}

.mobile-selector__item-url {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  margin-top: var(--space-0-5);
  font-family: var(--font-mono);
}

@media (max-width: 768px) {
  .mobile-selector {
    display: block;
  }
}
</style>
