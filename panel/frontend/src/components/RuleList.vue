<template>
  <div class="rule-list">
    <!-- Empty State - No Agent Selected -->
    <div v-if="!ruleStore.hasSelectedAgent" class="empty-state">
      <div class="empty-state__icon">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/>
          <circle cx="12" cy="7" r="4"/>
        </svg>
      </div>
      <div class="empty-state__title">选择节点</div>
      <div class="empty-state__description">
        在左侧选择一个 Agent 节点以查看和管理其代理规则
      </div>
    </div>

    <!-- Empty State - No Rules -->
    <div v-else-if="!ruleStore.hasRules" class="empty-state">
      <div class="empty-state__icon">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
          <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
        </svg>
      </div>
      <div class="empty-state__title">暂无规则</div>
      <button class="btn btn--primary btn--sm" @click="$emit('add')">添加第一条规则</button>
    </div>

    <!-- Empty State - No Search Results -->
    <div v-else-if="ruleStore.filteredRules.length === 0" class="empty-state">
      <div class="empty-state__icon">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="11" cy="11" r="8"/>
          <line x1="21" y1="21" x2="16.65" y2="16.65"/>
        </svg>
      </div>
      <div class="empty-state__title">无匹配结果</div>
      <div class="empty-state__description">
        没有找到与 "{{ ruleStore.searchQuery }}" 匹配的规则
      </div>
    </div>

    <!-- Rules Grid -->
    <div v-else class="rule-grid">
      <RuleItem
        v-for="rule in ruleStore.filteredRules"
        :key="rule.id"
        :rule="rule"
        :agent="ruleStore.selectedAgent"
        @edit="handleEdit"
        @delete="handleDelete"
        @copy="handleCopy"
      />
    </div>

    <!-- Edit Modal -->
    <Teleport to="body">
      <BaseModal
        v-if="editingRule"
        v-model="showEditModal"
        title="编辑代理规则"
      >
        <RuleForm :initial-data="editingRule" @success="showEditModal = false" />
      </BaseModal>
    </Teleport>

    <!-- Copy Modal -->
    <Teleport to="body">
      <BaseModal
        v-if="copyingRule"
        v-model="showCopyModal"
        title="复制代理规则"
      >
        <RuleForm :initial-data="copyingRule" @success="showCopyModal = false" />
      </BaseModal>
    </Teleport>

    <!-- Delete Modal -->
    <Teleport to="body">
      <BaseModal
        v-if="deletingRule"
        v-model="showDeleteModal"
        title="确认删除"
      >
        <div class="space-y-4">
          <p class="text-sm text-secondary">
            确定要删除这条代理规则吗？此操作无法撤销。
          </p>
          <div class="bg-subtle p-4 rounded-lg">
            <div class="text-sm font-mono text-primary">{{ deletingRule.frontend_url }}</div>
            <div class="text-xs text-tertiary mt-1">→ {{ deletingRule.backend_url }}</div>
          </div>
          <div class="flex justify-end gap-3">
            <button
              class="btn btn--secondary"
              @click="showDeleteModal = false"
            >
              取消
            </button>
            <button
              class="btn btn--danger"
              :disabled="isDeleting"
              @click="confirmDelete"
            >
              <span v-if="isDeleting" class="spinner spinner--sm mr-2"></span>
              {{ isDeleting ? '删除中...' : '确认删除' }}
            </button>
          </div>
        </div>
      </BaseModal>
    </Teleport>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRuleStore } from '../stores/rules'
import RuleItem from './RuleItem.vue'
import RuleForm from './RuleForm.vue'
import BaseModal from './base/BaseModal.vue'

defineEmits(['add'])

const ruleStore = useRuleStore()

const editingRule = ref(null)
const deletingRule = ref(null)
const showEditModal = ref(false)
const showDeleteModal = ref(false)
const isDeleting = ref(false)
const copyingRule = ref(null)
const showCopyModal = ref(false)

const handleEdit = (rule) => {
  editingRule.value = rule
  showEditModal.value = true
}

const handleCopy = (rule) => {
  // Strip id so RuleForm treats it as new rule
  const { id, ...copyData } = rule
  copyingRule.value = copyData
  showCopyModal.value = true
}

const handleDelete = (rule) => {
  deletingRule.value = rule
  showDeleteModal.value = true
}

const confirmDelete = async () => {
  if (!deletingRule.value) return
  isDeleting.value = true
  try {
    await ruleStore.removeRule(deletingRule.value.id)
    showDeleteModal.value = false
  } catch (err) {
    // Error handled by store
  } finally {
    isDeleting.value = false
  }
}
</script>

<style scoped>
.rule-list {
  min-height: 300px;
}

.rule-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 340px), 1fr));
  gap: var(--space-4);
}

/* 4K: 4 columns with larger cards */
@media (min-width: 2200px) {
  .rule-grid {
    grid-template-columns: repeat(4, 1fr);
    gap: var(--space-5);
  }
}

/* Large desktop: 3 columns */
@media (min-width: 1600px) and (max-width: 2199px) {
  .rule-grid {
    grid-template-columns: repeat(3, 1fr);
  }
}

/* Desktop: 2 columns */
@media (min-width: 768px) and (max-width: 1599px) {
  .rule-grid {
    grid-template-columns: repeat(2, 1fr);
  }
}

/* Mobile: 1 column */
@media (max-width: 767px) {
  .rule-grid {
    grid-template-columns: 1fr;
  }
}

/* Enhanced Empty State */
.empty-state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  padding: var(--space-16) var(--space-6);
  text-align: center;
}

.empty-state__icon {
  color: var(--color-primary);
  opacity: 0.4;
  margin-bottom: var(--space-5);
  animation: float 4s ease-in-out infinite;
}

.empty-state__title {
  font-size: var(--text-xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  margin-bottom: var(--space-2);
}

.empty-state__description {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  max-width: 360px;
  line-height: 1.6;
}

@keyframes float {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-8px); }
}

/* Utilities */
.space-y-4 > * + * { margin-top: var(--space-4); }
.flex { display: flex; }
.justify-end { justify-content: flex-end; }
.gap-3 { gap: var(--space-3); }
.mr-2 { margin-right: var(--space-2); }
.mt-1 { margin-top: var(--space-1); }
.bg-subtle { background: var(--color-bg-subtle); }
.p-4 { padding: var(--space-4); }
.rounded-lg { border-radius: var(--radius-lg); }
.text-sm { font-size: var(--text-sm); }
.text-xs { font-size: var(--text-xs); }
.font-mono { font-family: var(--font-mono); }
.text-primary { color: var(--color-text-primary); }
.text-secondary { color: var(--color-text-secondary); }
.text-tertiary { color: var(--color-text-tertiary); }
</style>
