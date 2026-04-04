<template>
  <div class="rules-page">
    <div class="rules-page__header">
      <div>
        <h1 class="rules-page__title">L4 规则</h1>
        <p class="rules-page__subtitle">
          <template v-if="selectedAgentId">
            {{ rules.length }} 条规则 · 启用 {{ enabledCount }} 条
          </template>
          <template v-else>
            请先选择一个节点
          </template>
        </p>
      </div>
      <button v-if="selectedAgentId" class="btn btn-primary" @click="showAddForm = true">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
          <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
        </svg>
        添加 L4 规则
      </button>
    </div>

    <!-- No agent selected -->
    <div v-if="!selectedAgentId" class="rules-page__prompt">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/>
      </svg>
      <p>请从侧边栏选择一个节点</p>
    </div>

    <!-- Agent selected, no rules -->
    <div v-else-if="!rules.length && !isLoading" class="rules-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/>
      </svg>
      <p>暂无 L4 规则</p>
      <button class="btn btn-primary" @click="showAddForm = true">添加第一条规则</button>
    </div>

    <!-- No search results -->
    <div v-else-if="selectedAgentId && rules.length && !filteredRules.length" class="rules-page__prompt">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
      </svg>
      <p>没有匹配的 L4 规则</p>
    </div>

    <!-- Search toolbar -->
    <div v-if="selectedAgentId && rules.length" class="rules-page__toolbar">
      <input v-model="searchQuery" class="search-input" placeholder="搜索协议 / 地址 / 端口 / 标签 / #id=...">
    </div>

    <!-- Rule card grid -->
    <div v-if="selectedAgentId && filteredRules.length" class="rule-grid">
      <L4RuleItem v-for="rule in filteredRules" :key="rule.id" :rule="rule" @edit="startEdit" @delete="startDelete" @copy="handleCopy" @toggle="toggleRule" />
    </div>

    <!-- Loading -->
    <div v-if="isLoading" class="rules-page__loading">
      <div class="spinner"></div>
    </div>

    <!-- Add/Edit Modal -->
    <Teleport to="body">
      <div v-if="showAddForm || editingRule" class="modal-overlay" @click.self="closeForm">
        <div class="modal modal--large">
          <div class="modal__header">{{ editingRule ? '编辑 L4 规则' : '添加 L4 规则' }}</div>
          <div class="modal__body">
            <L4RuleForm :initial-data="editingRule" :agent-id="agentId" @success="closeForm" />
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Copy Modal -->
    <Teleport to="body">
      <div v-if="showCopyModal" class="modal-overlay" @click.self="closeCopy">
        <div class="modal modal--large">
          <div class="modal__header">复制 L4 规则</div>
          <div class="modal__body">
            <L4RuleForm v-if="copyingRule" :initial-data="copyingRule" :agent-id="agentId" @success="closeCopy" />
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Delete Modal -->
    <Teleport to="body">
      <div v-if="deletingRule" class="modal-overlay" @click.self="deletingRule = null">
        <div class="modal">
          <div class="modal__header">确认删除</div>
          <div class="modal__body">
            <p>确定删除规则 <strong>{{ deletingRule.listen_host }}:{{ deletingRule.listen_port }}</strong>？</p>
          </div>
          <div class="modal__footer">
            <button class="btn btn-secondary" @click="deletingRule = null">取消</button>
            <button class="btn btn-danger" @click="confirmDelete">删除</button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useRoute } from 'vue-router'
import { useAgent } from '../context/AgentContext'
import { useL4Rules, useCreateL4Rule, useUpdateL4Rule, useDeleteL4Rule } from '../hooks/useL4Rules'
import L4RuleForm from '../components/L4RuleForm.vue'
import L4RuleItem from '../components/l4/L4RuleItem.vue'

const route = useRoute()
const { selectedAgentId } = useAgent()
const agentId = computed(() => route.query.agentId || selectedAgentId.value)

const { data: _rulesData, isLoading } = useL4Rules(agentId)
const createL4Rule = useCreateL4Rule(agentId)
const updateL4Rule = useUpdateL4Rule(agentId)
const deleteL4Rule = useDeleteL4Rule(agentId)
const rules = computed(() => _rulesData.value ?? [])

// Search
const searchQuery = ref('')

const filteredRules = computed(() => {
  const raw = searchQuery.value.trim()
  if (!raw) return rules.value
  const idMatch = raw.match(/^#id=(\S+)$/)
  if (idMatch) return rules.value.filter(rule => String(rule.id) === idMatch[1])
  const q = raw.toLowerCase()
  return rules.value.filter(rule =>
    String(rule.protocol || '').toLowerCase().includes(q) ||
    String(rule.listen_host || '').toLowerCase().includes(q) ||
    String(rule.upstream_host || '').toLowerCase().includes(q) ||
    String(rule.listen_port || '').includes(q) ||
    (rule.tags || []).some(tag => String(tag).toLowerCase().includes(q))
  )
})

const enabledCount = computed(() => rules.value.filter(r => r.enabled).length)

// Modals
const showAddForm = ref(false)
const editingRule = ref(null)
const copyingRule = ref(null)
const showCopyModal = ref(false)
const deletingRule = ref(null)

function startEdit(rule) { editingRule.value = rule }
function handleCopy(rule) { const { id, ...rest } = rule; copyingRule.value = rest; showCopyModal.value = true }
function startDelete(rule) { deletingRule.value = rule }
function closeForm() { showAddForm.value = false; editingRule.value = null }
function closeCopy() { showCopyModal.value = false; copyingRule.value = null }
function toggleRule(rule) { updateL4Rule.mutate({ id: rule.id, enabled: !rule.enabled }) }
function confirmDelete() { if (deletingRule.value) deleteL4Rule.mutate(deletingRule.value.id); deletingRule.value = null }
</script>

<style scoped>
.rules-page { max-width: 1200px; margin: 0 auto; }
.rules-page__header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 2rem; gap: 1rem; }
.rules-page__title { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.rules-page__subtitle { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.rules-page__prompt, .rules-page__empty, .rules-page__loading { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }
/* Toolbar */
.rules-page__toolbar { margin-bottom: 1.5rem; }
.search-input { width: 100%; padding: 0.625rem 0.875rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s; box-sizing: border-box; }
.search-input:focus { border-color: var(--color-primary); }
.search-input::placeholder { color: var(--color-text-muted); }
/* Card grid */
.rule-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 1rem; }
/* Modals */
.modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.5); backdrop-filter: blur(4px); z-index: var(--z-modal); display: flex; align-items: center; justify-content: center; }
.modal { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); box-shadow: var(--shadow-xl); width: min(480px, 90vw); overflow: hidden; }
.modal--large { width: min(600px, 92vw); }
.modal__header { padding: 1rem 1.5rem; font-weight: 600; font-size: 1rem; border-bottom: 1px solid var(--color-border-subtle); }
.modal__body { padding: 1.5rem; display: flex; flex-direction: column; gap: 1.25rem; }
.modal__footer { padding: 1rem 1.5rem; display: flex; justify-content: flex-end; gap: 0.75rem; border-top: 1px solid var(--color-border-subtle); }
/* Buttons (still used by header + empty state + delete modal) */
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
/* Spinner */
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
