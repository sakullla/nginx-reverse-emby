<template>
  <div class="rules-container">
    <div v-if="ruleStore.loading && !ruleStore.hasRules" class="loading-state">
      <div class="spinner"></div>
      <p>正在获取代理规则...</p>
    </div>

    <template v-else>
      <!-- 标签筛选行 -->
      <div class="filter-row" v-if="ruleStore.hasRules && (ruleStore.selectedTags.length > 0 || ruleStore.searchQuery)">
        <div class="active-filters" v-if="ruleStore.selectedTags.length > 0">
          <div class="active-filter" v-for="tag in ruleStore.selectedTags" :key="tag">
            <span class="filter-label">
              <svg viewBox="0 0 24 24"><path d="M20.59 13.41l-7.17 7.17a2 2 0 0 1-2.83 0L2 12V2h10l8.59 8.59a2 2 0 0 1 0 2.82z"/><line x1="7" y1="7" x2="7.01" y2="7"/></svg>
              {{ tag }}
            </span>
            <button @click="removeTag(tag)" class="clear-filter" title="移除此标签">
              <svg viewBox="0 0 24 24"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
            </button>
          </div>
          <button v-if="ruleStore.selectedTags.length > 1" @click="ruleStore.selectedTags = []" class="clear-all-btn" title="清除所有筛选">
            清除全部
          </button>
        </div>

        <div class="results-count" v-if="ruleStore.searchQuery || ruleStore.selectedTags.length > 0">
          找到 {{ ruleStore.filteredRules.length }} 条结果
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
            点击上方的"添加规则"按钮开始
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

      <div v-else :class="['rules-layout', `view-${ruleStore.viewMode}`]">
        <RuleItem
          v-for="rule in ruleStore.filteredRules"
          :key="rule.id"
          :rule="rule"
          :view-mode="ruleStore.viewMode"
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

const removeTag = (tag) => {
  const index = ruleStore.selectedTags.indexOf(tag)
  if (index > -1) {
    ruleStore.selectedTags.splice(index, 1)
  }
}
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

@keyframes spin {
  to { transform: rotate(360deg); }
}

.filter-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--spacing-md);
  flex-wrap: wrap;
  margin-bottom: var(--spacing-lg);
  padding: var(--spacing-sm) 0;
}

.active-filters {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.active-filter {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 38px;
  padding: 0 8px 0 12px;
  background: linear-gradient(135deg, var(--color-primary-bg) 0%, var(--color-primary-lighter) 100%);
  border: 2px solid var(--color-primary-light);
  border-radius: var(--radius-lg);
  box-shadow: 0 2px 8px rgba(37, 99, 235, 0.15);
  position: relative;
}

.filter-label {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--color-primary);
}

.filter-label svg {
  width: 12px;
  height: 12px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
}

.clear-filter {
  background: rgba(37, 99, 235, 0.15);
  border: none;
  width: 20px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--color-primary);
  cursor: pointer;
  transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  flex-shrink: 0;
  border-radius: 4px;
  padding: 0;
}

.clear-filter svg {
  width: 12px;
  height: 12px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
}

.clear-filter:hover {
  background: var(--color-primary);
  color: white;
}

.clear-filter:active {
  transform: scale(0.9);
}

.clear-all-btn {
  height: 38px;
  padding: 0 14px;
  background: var(--color-bg-secondary);
  border: 2px solid var(--color-border);
  border-radius: var(--radius-lg);
  font-size: 0.8rem;
  font-weight: 600;
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  white-space: nowrap;
}

.clear-all-btn:hover {
  background: var(--color-danger-bg);
  border-color: var(--color-danger);
  color: var(--color-danger);
}

.clear-all-btn:active {
  transform: scale(0.95);
}

.results-count {
  font-size: 0.85rem;
  color: var(--color-text-muted);
  white-space: nowrap;
}

.rules-layout.view-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(350px, 100%), 1fr));
  gap: var(--spacing-xl);
}

.rules-layout.view-list {
  display: flex;
  flex-direction: column;
  gap: var(--spacing-md);
}

@media (max-width: 768px) {
  .rules-layout.view-grid {
    grid-template-columns: 1fr;
    gap: var(--spacing-md);
  }

  .filter-row {
    flex-direction: column;
    align-items: flex-start;
    gap: var(--spacing-sm);
  }

  .active-filters {
    width: 100%;
  }

  .results-count {
    width: 100%;
    text-align: left;
  }

  .active-filter {
    flex: 0 0 auto;
  }
}
</style>
