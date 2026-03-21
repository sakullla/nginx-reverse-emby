<template>
  <div class="rule-list">
    <div v-if="!ruleStore.hasSelectedAgent" class="rule-list__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
        <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
      </svg>
      <span>请选择左侧节点管理 L4 规则</span>
    </div>
    <div v-else-if="!ruleStore.hasL4Rules" class="rule-list__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="12" cy="12" r="10"/>
        <line x1="12" y1="8" x2="12" y2="16"/>
        <line x1="8" y1="12" x2="16" y2="12"/>
      </svg>
      <span>当前节点还没有 L4 规则</span>
      <button class="btn btn--primary btn--sm" @click="$emit('add')">添加第一条规则</button>
    </div>
    <template v-else-if="ruleStore.filteredL4Rules.length === 0">
      <div class="rule-list__empty">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="11" cy="11" r="8"/>
          <line x1="21" y1="21" x2="16.65" y2="16.65"/>
        </svg>
        <span>没有匹配的 L4 规则</span>
      </div>
    </template>
    <div v-else class="rule-list__grid">
      <L4RuleItem
        v-for="rule in ruleStore.filteredL4Rules"
        :key="rule.id"
        :rule="rule"
        @edit="handleEdit"
        @delete="handleDelete"
      />
    </div>

    <BaseModal v-model="showEditModal" title="编辑 L4 规则" :subtitle="editingRule?.name">
      <L4RuleForm v-if="editingRule" :initial-data="editingRule" @success="showEditModal = false" />
    </BaseModal>

    <BaseModal v-model="showDeleteModal" title="确认删除" show-footer @confirm="confirmDelete">
      <p>确定要删除规则 <strong>{{ deletingRule?.name }}</strong> 吗？</p>
    </BaseModal>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRuleStore } from '../stores/rules'
import BaseModal from './base/BaseModal.vue'
import L4RuleForm from './L4RuleForm.vue'
import L4RuleItem from './L4RuleItem.vue'

defineEmits(['add'])

const ruleStore = useRuleStore()
const editingRule = ref(null)
const deletingRule = ref(null)
const showEditModal = ref(false)
const showDeleteModal = ref(false)

function handleEdit(rule) {
  editingRule.value = rule
  showEditModal.value = true
}

function handleDelete(rule) {
  deletingRule.value = rule
  showDeleteModal.value = true
}

async function confirmDelete() {
  if (!deletingRule.value) return
  await ruleStore.removeL4Rule(deletingRule.value.id)
  showDeleteModal.value = false
}
</script>

<style scoped>
.rule-list {
  min-height: 200px;
}

.rule-list__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 340px), 1fr));
  gap: var(--space-4);
}

/* 4K: 4 columns with larger cards */
@media (min-width: 2200px) {
  .rule-list__grid {
    grid-template-columns: repeat(4, 1fr);
    gap: var(--space-5);
  }
}

/* Large desktop: 3 columns */
@media (min-width: 1600px) and (max-width: 2199px) {
  .rule-list__grid {
    grid-template-columns: repeat(3, 1fr);
  }
}

/* Desktop: 2 columns */
@media (min-width: 768px) and (max-width: 1599px) {
  .rule-list__grid {
    grid-template-columns: repeat(2, 1fr);
  }
}

/* Mobile: 1 column */
@media (max-width: 767px) {
  .rule-list__grid {
    grid-template-columns: 1fr;
  }
}

.rule-list__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-4);
  padding: var(--space-12) var(--space-6);
  color: var(--color-text-muted);
  text-align: center;
}

.rule-list__empty svg {
  opacity: 0.5;
  animation: float 4s ease-in-out infinite;
}

.rule-list__empty span {
  font-size: var(--text-sm);
}

@keyframes float {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-6px); }
}
</style>
