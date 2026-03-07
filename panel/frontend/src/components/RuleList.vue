<template>
  <div class="rules-container">
    <div v-if="ruleStore.loading && !ruleStore.hasRules" class="loading-state">
      <div class="spinner"></div>
      <p>正在获取代理规则...</p>
    </div>

    <template v-else>
      <div class="list-controls" v-if="ruleStore.hasRules">
        <div class="search-box">
          <span class="search-icon">
            <svg viewBox="0 0 24 24"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
          </span>
          <input
            v-model="ruleStore.searchQuery"
            type="text"
            placeholder="搜索规则 ID、前端或后端 URL..."
            class="search-input"
          >
          <button v-if="ruleStore.searchQuery" @click="ruleStore.searchQuery = ''" class="clear-search">
            ×
          </button>
        </div>
      </div>

      <EmptyState
        v-if="!ruleStore.hasRules"
        icon="🎯"
        title="还没有代理规则"
        description="开始添加您的第一条反向代理规则，让流量管理变得简单高效！"
      >
        <template #action>
          <p class="empty-hint">
            点击上方的“添加规则”按钮开始
          </p>
        </template>
      </EmptyState>

      <EmptyState
        v-else-if="ruleStore.filteredRules.length === 0"
        icon="🔎"
        title="没有找到匹配的规则"
        :description="`未能找到包含 '${ruleStore.searchQuery}' 的规则。`"
      >
        <template #action>
          <button @click="ruleStore.searchQuery = ''" class="btn secondary small">
            重置搜索条件
          </button>
        </template>
      </EmptyState>

      <div v-else class="rules-grid">
        <RuleItem
          v-for="rule in ruleStore.filteredRules"
          :key="rule.id"
          :rule="rule"
        />
      </div>
    </template>
  </div>
</template>

<script setup>
import { useRuleStore } from '../stores/rules'
import RuleItem from './RuleItem.vue'
import EmptyState from './base/EmptyState.vue'

const ruleStore = useRuleStore()
</script>

<style scoped>
.rules-container {
  min-height: 200px;
}

.loading-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--spacing-4xl) 0;
  color: var(--color-text-muted);
}

.spinner {
  width: 40px;
  height: 40px;
  border: 3px solid var(--color-border);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
  margin-bottom: var(--spacing-md);
}

.list-controls {
  margin-bottom: var(--spacing-xl);
}

.search-box {
  position: relative;
  max-width: 480px;
}

.search-icon {
  position: absolute;
  left: var(--spacing-md);
  top: 50%;
  transform: translateY(-50%);
  width: 20px;
  height: 20px;
  color: var(--color-text-muted);
  pointer-events: none;
  transition: color var(--transition-base);
}

.search-box:focus-within .search-icon {
  color: var(--color-primary);
}

.search-icon svg {
  width: 100%;
  height: 100%;
  stroke: currentColor;
  stroke-width: 2.2;
  fill: none;
}

.search-input {
  width: 100%;
  height: 48px;
  padding: 0 var(--spacing-xl) 0 calc(var(--spacing-md) * 3);
  background: var(--color-bg-secondary);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-xl);
  font-size: var(--font-size-sm);
  transition: all var(--transition-base);
  box-shadow: var(--shadow-sm);
}

.search-input:focus {
  background: var(--color-bg-primary);
  border-color: var(--color-primary);
  box-shadow: 0 0 0 4px var(--color-primary-lighter);
  transform: translateY(-1px);
}

.clear-search {
  position: absolute;
  right: var(--spacing-sm);
  top: 50%;
  transform: translateY(-50%);
  background: var(--color-border-light);
  border: none;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-text-muted);
  cursor: pointer;
  font-size: 1.4rem;
  transition: all var(--transition-base);
}

.clear-search:hover {
  background: var(--color-danger-bg);
  color: var(--color-danger);
}

.empty-hint {
  color: var(--color-text-muted);
  font-size: var(--font-size-sm);
  margin-top: var(--spacing-md);
}

.rules-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: var(--spacing-xl);
}

@media (max-width: 768px) {
  .rules-grid {
    grid-template-columns: 1fr;
  }

  .search-box {
    max-width: 100%;
  }
}
</style>
