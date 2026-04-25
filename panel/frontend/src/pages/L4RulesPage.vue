<template>
  <div class="rules-page">
    <div class="rules-page__header">
      <div class="rules-page__header-left">
        <h1 class="rules-page__title">L4 规则</h1>
        <p class="rules-page__subtitle">
          <template v-if="agentId">
            {{ rules.length }} 条规则 · 启用 {{ enabledCount }} 条
          </template>
          <template v-else>
            请先选择一个节点
          </template>
        </p>
      </div>
      <div class="rules-page__header-right">
        <div class="search-wrapper" v-if="agentId && rules.length" @click="focusSearch">
          <svg class="search-icon-btn" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>
          <input ref="searchInputRef" v-model="searchQuery" name="l4-rule-search" class="search-input" placeholder="搜索协议 / 地址 / 端口 / 标签 / #id=...">
          <button v-if="searchQuery" class="clear-btn" @click.stop="searchQuery = ''">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </div>
        <button v-if="agentId" class="btn btn-primary" @click="showAddForm = true">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
          </svg>
          <span class="btn-text">添加 L4 规则</span>
        </button>
      </div>
    </div>

    <!-- No agent selected -->
    <div v-if="!agentId" class="rules-page__prompt">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/>
      </svg>
      <p>请选择一个节点来管理 L4 规则</p>
      <AgentPicker :agents="allAgents" @select="handleAgentSelect" />
      <p class="rules-page__prompt-hint">或前往节点管理页面添加新节点</p>
      <RouterLink to="/agents" class="btn btn-primary">加入节点</RouterLink>
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
    <div v-if="agentId && rules.length && !filteredRules.length" class="rules-page__prompt">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
      </svg>
      <p>没有匹配的 L4 规则</p>
    </div>

    <!-- Rule card grid -->
    <div v-if="agentId && filteredRules.length" class="rule-grid">
      <L4RuleItem v-for="rule in filteredRules" :key="rule.id" :rule="rule" :agent="selectedAgent" @edit="startEdit" @delete="startDelete" @copy="handleCopy" @toggle="toggleRule" @diagnose="openDiagnostic" />
    </div>

    <!-- Loading -->
    <div v-if="isLoading" class="rules-page__loading">
      <div class="spinner"></div>
    </div>

    <!-- Add/Edit Modal -->
    <BaseModal
      :model-value="showAddForm || !!editingRule"
      :title="editingRule ? '编辑 L4 规则' : '添加 L4 规则'"
      size="xl"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <L4RuleForm :initial-data="editingRule" :agent-id="agentId" @success="closeForm" />
    </BaseModal>

    <!-- Copy Modal -->
    <BaseModal
      :model-value="showCopyModal"
      title="复制 L4 规则"
      size="xl"
      :close-on-click-modal="false"
      @update:model-value="closeCopy"
    >
      <L4RuleForm v-if="copyingRule" :initial-data="copyingRule" :agent-id="agentId" @success="closeCopy" />
    </BaseModal>

    <!-- Delete Modal -->
    <DeleteConfirmDialog
      :show="!!deletingRule"
      title="确认删除规则"
      message="删除后该规则将立即失效，相关配置将无法恢复。"
      :name="deletingRule?.listen_host + ':' + deletingRule?.listen_port"
      confirm-text="确认删除"
      :loading="deleteL4Rule.isPending?.value"
      @confirm="confirmDelete"
      @cancel="deletingRule = null"
    />

    <RuleDiagnosticModal
      :model-value="showDiagnostic"
      :task="diagnosticTask"
      kind="l4_tcp"
      :rule-label="diagnosticRule?.name || `${diagnosticRule?.listen_host || ''}:${diagnosticRule?.listen_port || ''}`"
      :endpoint-label="diagnosticRule ? l4BackendAddresses(diagnosticRule).join(', ') : ''"
      :agent-label="selectedAgentLabel"
      @update:model-value="closeDiagnostic"
    />
  </div>
</template>

<script setup>
import { ref, computed, watchEffect } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAgent } from '../context/AgentContext'
import { useL4Rules, useCreateL4Rule, useUpdateL4Rule, useDeleteL4Rule } from '../hooks/useL4Rules'
import { useDiagnoseL4Rule, useDiagnosticTask } from '../hooks/useDiagnostics'
import { useAgents } from '../hooks/useAgents'
import L4RuleForm from '../components/L4RuleForm.vue'
import L4RuleItem from '../components/l4/L4RuleItem.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import BaseModal from '../components/base/BaseModal.vue'
import RuleDiagnosticModal from '../components/RuleDiagnosticModal.vue'
import AgentPicker from '../components/AgentPicker.vue'
import { messageStore } from '../stores/messages'

const route = useRoute()
const router = useRouter()
const { selectedAgentId } = useAgent()
const agentId = computed(() => route.query.agentId || selectedAgentId.value)

const { data: _rulesData, isLoading } = useL4Rules(agentId)

// Agents list for sync status derivation
const { data: agentsData } = useAgents()
const allAgents = computed(() => agentsData.value ?? [])
const selectedAgent = computed(() => agentsData.value?.find(a => a.id === agentId.value))

function handleAgentSelect(agent) {
  router.replace({ query: { ...route.query, agentId: agent.id } })
}
const selectedAgentLabel = computed(() => String(selectedAgent.value?.name || agentId.value || '').trim())
const createL4Rule = useCreateL4Rule(agentId)
const updateL4Rule = useUpdateL4Rule(agentId)
const deleteL4Rule = useDeleteL4Rule(agentId)
const diagnoseL4Rule = useDiagnoseL4Rule(agentId)
const rules = computed(() => _rulesData.value ?? [])

// Search
const searchQuery = ref('')
const searchInputRef = ref(null)
function focusSearch() { searchInputRef.value?.focus() }

// Pre-fill search from global search navigation; reset when param is cleared
watchEffect(() => {
  searchQuery.value = route.query.search ?? ''
})

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

  if (rule?.upstream_host && rule?.upstream_port) {
    return [`${rule.upstream_host}:${rule.upstream_port}`]
  }

  return []
}

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
    l4BackendAddresses(rule).some((address) => address.toLowerCase().includes(q)) ||
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
const showDiagnostic = ref(false)
const diagnosticRule = ref(null)
const diagnosticTaskId = ref('')
const initialDiagnosticTask = ref(null)
const { data: diagnosticTaskData } = useDiagnosticTask(agentId, diagnosticTaskId)
const diagnosticTask = computed(() => diagnosticTaskData.value?.task || initialDiagnosticTask.value)

function startEdit(rule) { editingRule.value = rule }
function handleCopy(rule) { const { id, ...rest } = rule; copyingRule.value = rest; showCopyModal.value = true }
function startDelete(rule) { deletingRule.value = rule }
function closeForm() { showAddForm.value = false; editingRule.value = null }
function closeCopy() { showCopyModal.value = false; copyingRule.value = null }
function toggleRule(rule) { updateL4Rule.mutate({ id: rule.id, enabled: !rule.enabled }) }
function confirmDelete() { if (deletingRule.value) deleteL4Rule.mutate(deletingRule.value.id); deletingRule.value = null }
async function openDiagnostic(rule) {
  diagnosticRule.value = rule
  showDiagnostic.value = true
  try {
    const response = await diagnoseL4Rule.mutateAsync(rule.id)
    initialDiagnosticTask.value = response.task || null
    diagnosticTaskId.value = response.task_id
  } catch (error) {
    closeDiagnostic()
    messageStore.error(error, '启动 L4 规则诊断失败')
  }
}
function closeDiagnostic() { showDiagnostic.value = false; diagnosticRule.value = null; diagnosticTaskId.value = ''; initialDiagnosticTask.value = null }
</script>

<style scoped>
.rules-page { max-width: 1200px; margin: 0 auto; }
.rules-page__header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 1.5rem; gap: 1rem; flex-wrap: wrap; }
.rules-page__header-left { flex: 1; min-width: 0; }
.rules-page__header-right { display: flex; align-items: center; gap: 0.75rem; flex-shrink: 0; }
.rules-page__title { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.rules-page__subtitle { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.rules-page__prompt, .rules-page__empty, .rules-page__loading { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }
/* Toolbar */
.rules-page__toolbar { margin-bottom: 1.5rem; }
/* Card grid */
.rule-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 1rem; }
.search-wrapper { position: relative; display: flex; align-items: center; }
.search-icon-btn { display: none; }
.search-input { flex: 1; min-width: 0; padding: 0.625rem 2rem 0.625rem 0.875rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s, width 0.2s; box-sizing: border-box; }
.search-input:focus { border-color: var(--color-primary); width: 280px; }
.search-input::placeholder { color: var(--color-text-muted); }
.clear-btn { display: flex; align-items: center; justify-content: center; width: 18px; height: 18px; border: none; background: var(--color-bg-hover); border-radius: 50%; color: var(--color-text-secondary); cursor: pointer; flex-shrink: 0; padding: 0; position: absolute; right: 8px; z-index: 2; }

@media (max-width: 640px) {
  .search-wrapper {
    width: 36px;
    height: 36px;
    border-radius: var(--radius-lg);
    border: 1.5px solid var(--color-border-default);
    background: var(--color-bg-subtle);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    position: relative;
  }
  .search-icon-btn { display: flex; color: var(--color-text-secondary); }
  .search-input {
    position: absolute;
    left: 0;
    top: 0;
    width: 200px;
    height: 36px;
    opacity: 0;
    pointer-events: none;
    transition: opacity 0.2s, width 0.2s;
  }
  .search-wrapper:focus-within {
    width: 200px;
  }
  .search-wrapper:focus-within .search-input {
    opacity: 1;
    pointer-events: auto;
    border-color: var(--color-primary);
  }
  .search-wrapper:focus-within .clear-btn {
    opacity: 1;
    pointer-events: auto;
  }
  .clear-btn {
    opacity: 0;
    pointer-events: none;
    position: absolute;
    right: 8px;
    z-index: 2;
    transition: opacity 0.2s;
  }
  .btn-text { display: none; }
}
/* Buttons (still used by header + empty state + delete modal) */
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
/* Spinner */
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
