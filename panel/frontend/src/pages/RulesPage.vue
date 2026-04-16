<template>
  <div class="rules-page">
    <div class="rules-page__header">
      <div class="rules-page__header-left">
        <h1 class="rules-page__title">HTTP 规则</h1>
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
          <input ref="searchInputRef" v-model="searchQuery" name="rule-search" class="search-input" placeholder="搜索 URL / 标签 / #id=...">
          <button v-if="searchQuery" class="clear-btn" @click.stop="searchQuery = ''">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
          </button>
        </div>
        <button v-if="agentId" class="btn btn-primary" @click="showAddForm = true">
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
            <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
          </svg>
          <span class="btn-text">添加规则</span>
        </button>
      </div>
    </div>

    <!-- No agent selected -->
    <div v-if="!agentId" class="rules-page__prompt">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
      </svg>
      <p>请从侧边栏选择一个节点</p>
      <p class="rules-page__prompt-hint">选择节点后即可管理其 HTTP 规则</p>
    </div>

    <!-- Agent selected, no rules -->
    <div v-else-if="agentId && !rules.length && !isLoading" class="rules-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
      </svg>
      <p>暂无规则</p>
      <button class="btn btn-primary" @click="showAddForm = true">添加第一条规则</button>
    </div>

    <!-- No search results -->
    <div v-else-if="agentId && rules.length && !filteredRules.length" class="rules-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
      </svg>
      <p>没有匹配的规则</p>
    </div>

    <!-- Rules card grid -->
    <div v-if="agentId && filteredRules.length" class="rule-grid">
      <div v-for="rule in filteredRules" :key="rule.id" class="rule-card" :class="{ 'rule-card--disabled': !rule.enabled }">
        <div class="rule-card__header">
          <div class="rule-card__badges">
            <span class="rule-card__id">#{{ rule.id }}</span>
            <span class="proto-badge" :class="rule.frontend_url?.startsWith('https') ? 'proto-badge--https' : 'proto-badge--http'">
              {{ rule.frontend_url?.startsWith('https') ? 'HTTPS' : 'HTTP' }}
            </span>
            <span class="rule-card__status" :class="`rule-card__status--${getStatus(rule)}`">
              {{ getStatusLabel(rule) }}
            </span>
          </div>
          <div class="rule-card__actions">
            <button class="rule-card__action rule-card__action--toggle" :title="rule.enabled ? '停用' : '启用'" @click="toggleRule(rule)">
              <svg v-if="rule.enabled" width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/></svg>
              <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><polygon points="5 3 19 12 5 21 5 3"/></svg>
            </button>
            <button class="rule-card__action" title="复制" @click="handleCopy(rule)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
            </button>
            <button class="rule-card__action" title="编辑" @click="startEdit(rule)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
            </button>
            <button class="rule-card__action rule-card__action--diagnose" title="诊断" @click="openDiagnostic(rule)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12h4l2-6 4 12 2-6h6"/></svg>
            </button>
            <button class="rule-card__action rule-card__action--delete" title="删除" @click="startDelete(rule)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
            </button>
          </div>
        </div>
        <div class="rule-card__mapping">
          <div class="rule-card__url-row">
            <span class="rule-card__url-icon">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>
            </span>
            <code class="rule-card__url">{{ rule.frontend_url }}</code>
          </div>
          <div class="rule-card__url-row">
            <span class="rule-card__url-icon">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M5 12h14"/><path d="M12 5l7 7-7 7"/></svg>
            </span>
            <code class="rule-card__backend" :title="httpBackendsTooltip(rule)">
              {{ formatHttpBackend(rule) }}
            </code>
          </div>
        </div>
        <div v-if="hasRequestHeaderIndicators(rule)" class="rule-card__meta-badges">
          <span v-if="hasUserAgent(rule)" class="rule-card__meta-badge">UA</span>
          <span v-if="hasCustomHeaders(rule)" class="rule-card__meta-badge">Headers</span>
          <span v-if="hasIpForwardDisabled(rule)" class="rule-card__meta-badge rule-card__meta-badge--warning">No IP Forward</span>
        </div>
        <div class="rule-card__tags">
          <span v-for="tag in (rule.tags || [])" :key="tag" class="tag">{{ tag }}</span>
        </div>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="isLoading" class="rules-page__loading">
      <div class="spinner"></div>
    </div>

    <!-- Add/Edit Form Modal -->
    <BaseModal
      :model-value="showAddForm || !!editingRule"
      :title="editingRule ? '编辑规则' : '添加规则'"
      size="lg"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <RuleForm :initial-data="editingRule" :agent-id="agentId" @success="closeForm" />
    </BaseModal>

    <!-- Copy Modal -->
    <BaseModal
      :model-value="showCopyModal"
      title="复制规则"
      size="lg"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <RuleForm v-if="copyingRule" :initial-data="copyingRule" :agent-id="agentId" @success="closeForm" />
    </BaseModal>

    <!-- Delete Modal -->
    <DeleteConfirmDialog
      :show="!!deletingRule"
      title="确认删除规则"
      message="删除后该规则将立即失效，相关配置将无法恢复。"
      :name="deletingRule?.frontend_url"
      confirm-text="确认删除"
      :loading="deleteRule.isPending?.value"
      @confirm="confirmDelete"
      @cancel="deletingRule = null"
    />

    <RuleDiagnosticModal
      :model-value="showDiagnostic"
      :task="diagnosticTask"
      kind="http"
      :rule-label="diagnosticRule?.frontend_url || ''"
      :endpoint-label="formatHttpBackend(diagnosticRule || {})"
      @update:model-value="closeDiagnostic"
    />
  </div>
</template>

<script setup>
import { ref, computed, watchEffect } from 'vue'
import { useRoute } from 'vue-router'
import { useAgent } from '../context/AgentContext'
import { useRules, useCreateRule, useUpdateRule, useDeleteRule } from '../hooks/useRules'
import { useDiagnoseRule, useDiagnosticTask } from '../hooks/useDiagnostics'
import { useAgents } from '../hooks/useAgents'
import { getRuleEffectiveStatus } from '../utils/syncStatus'
import RuleForm from '../components/RuleForm.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import BaseModal from '../components/base/BaseModal.vue'
import RuleDiagnosticModal from '../components/RuleDiagnosticModal.vue'
import { messageStore } from '../stores/messages'

const route = useRoute()
const { selectedAgentId } = useAgent()

// 优先从 URL query 获取，否则 fall back 到 AgentContext
const agentId = computed(() => route.query.agentId || selectedAgentId.value)

const { data: _rulesData, isLoading } = useRules(agentId)
const createRule = useCreateRule(agentId)
const updateRule = useUpdateRule(agentId)
const deleteRule = useDeleteRule(agentId)
const diagnoseRule = useDiagnoseRule(agentId)
const rules = computed(() => _rulesData.value ?? [])

// Agents list for sync status derivation
const { data: agentsData } = useAgents()
const selectedAgent = computed(() => agentsData.value?.find(a => a.id === agentId.value))

// Search
const searchQuery = ref('')
const searchInputRef = ref(null)
function focusSearch() { searchInputRef.value?.focus() }

function httpBackends(rule) {
  if (Array.isArray(rule?.backends) && rule.backends.length > 0) {
    return rule.backends
      .map((backend) => String(backend?.url || '').trim())
      .filter(Boolean)
  }
  return rule?.backend_url ? [String(rule.backend_url).trim()] : []
}

function formatHttpBackend(rule) {
  const backends = httpBackends(rule)
  if (backends.length === 0) return '-'
  if (backends.length === 1) return backends[0]
  return `${backends[0]} +${backends.length - 1}`
}

function httpBackendsTooltip(rule) {
  return httpBackends(rule).join('\n')
}

// Pre-fill search from global search navigation; reset when param is cleared
watchEffect(() => {
  searchQuery.value = route.query.search ?? ''
})

const filteredRules = computed(() => {
  const raw = searchQuery.value.trim()
  if (!raw) return rules.value
  const idMatch = raw.match(/^#id=(\S+)$/)
  if (idMatch) return rules.value.filter(rule => String(rule.id) === idMatch[1])
  const q = raw.toLowerCase()
  return rules.value.filter(rule =>
    String(rule.frontend_url || '').toLowerCase().includes(q) ||
    String(rule.backend_url || '').toLowerCase().includes(q) ||
    httpBackends(rule).some((backend) => backend.toLowerCase().includes(q)) ||
    String(rule.name || '').toLowerCase().includes(q) ||
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
const { data: diagnosticTaskData } = useDiagnosticTask(agentId, diagnosticTaskId)
const diagnosticTask = computed(() => diagnosticTaskData.value?.task || null)

function getStatus(rule) {
  return getRuleEffectiveStatus(rule, selectedAgent.value)
}

function getStatusLabel(rule) {
  const status = getStatus(rule)
  return { active: '生效中', pending: '待同步', failed: '同步失败', disabled: '已禁用' }[status] || '未知'
}

function hasUserAgent(rule) {
  return Boolean(String(rule?.user_agent || '').trim())
}

function hasCustomHeaders(rule) {
  return Array.isArray(rule?.custom_headers) && rule.custom_headers.length > 0
}

function hasIpForwardDisabled(rule) {
  return rule?.pass_proxy_headers === false
}

function hasRequestHeaderIndicators(rule) {
  return hasUserAgent(rule) || hasCustomHeaders(rule) || hasIpForwardDisabled(rule)
}

function toggleRule(rule) {
  updateRule.mutate({ id: rule.id, enabled: !rule.enabled })
}

function startEdit(rule) {
  editingRule.value = rule
}

function handleCopy(rule) {
  const { id, ...rest } = rule
  copyingRule.value = rest
  showCopyModal.value = true
}

function startDelete(rule) {
  deletingRule.value = rule
}

async function openDiagnostic(rule) {
  diagnosticRule.value = rule
  showDiagnostic.value = true
  try {
    const response = await diagnoseRule.mutateAsync(rule.id)
    diagnosticTaskId.value = response.task_id
  } catch (error) {
    closeDiagnostic()
    messageStore.error(error, '启动 HTTP 规则诊断失败')
  }
}

function closeForm() {
  showAddForm.value = false
  editingRule.value = null
  showCopyModal.value = false
  copyingRule.value = null
}

function closeDiagnostic() {
  showDiagnostic.value = false
  diagnosticRule.value = null
  diagnosticTaskId.value = ''
}

async function confirmDelete() {
  if (deletingRule.value) {
    await deleteRule.mutateAsync(deletingRule.value.id)
    deletingRule.value = null
  }
}

</script>

<style scoped>
.rules-page { max-width: 1200px; margin: 0 auto; }
.rules-page__header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 1.5rem; gap: 1rem; flex-wrap: wrap; }
.rules-page__header-left { flex: 1; min-width: 0; }
.rules-page__header-right { display: flex; align-items: center; gap: 0.75rem; flex-shrink: 0; }
.rules-page__title { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.rules-page__subtitle { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.rules-page__prompt, .rules-page__empty, .rules-page__loading { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }
.rules-page__prompt-hint { font-size: 0.875rem; color: var(--color-text-tertiary); }

/* Search wrapper in header */
.search-wrapper { display: flex; align-items: center; position: relative; }
.search-icon-btn { display: none; }
.search-input { flex: 1; min-width: 0; padding: 0.5rem 2rem 0.5rem 0.75rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s, width 0.2s; box-sizing: border-box; }
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
  .rules-page__header { gap: 0.5rem; }
  .btn-text { display: none; }
}

/* Card grid */
.rule-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 1rem; }

/* Rule card — L4 style */
.rule-card {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: 1.125rem 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 0.625rem;
  transition: opacity 0.15s;
}
.rule-card--disabled { opacity: 0.6; }
.rule-card__header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 0.125rem; }
.rule-card__badges { display: flex; align-items: center; gap: 0.5rem; }
.rule-card__id { font-size: 0.75rem; font-family: var(--font-mono); color: var(--color-text-tertiary); }
.rule-card__status { font-size: 0.75rem; font-weight: 600; padding: 2px 8px; border-radius: var(--radius-full); }
.rule-card__status--active { background: var(--color-success-50); color: var(--color-success); }
.rule-card__status--disabled { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.rule-card__status--failed { background: var(--color-danger-50); color: var(--color-danger); }

/* Actions */
.rule-card__actions { display: flex; gap: 0.25rem; }
.rule-card__action { display: flex; align-items: center; justify-content: center; width: 28px; height: 28px; border-radius: var(--radius-md); border: none; background: transparent; color: var(--color-text-tertiary); cursor: pointer; transition: all 0.15s; }
.rule-card__action:hover { background: var(--color-bg-hover); color: var(--color-text-primary); }
.rule-card__action--delete:hover { background: var(--color-danger-50); color: var(--color-danger); }
.rule-card__action--toggle:hover { background: var(--color-warning-50); color: var(--color-warning); }
.rule-card__action--diagnose:hover { background: rgba(56, 189, 248, 0.12); color: var(--color-primary); }

/* Inline mapping */
.rule-card__mapping { display: flex; flex-direction: column; gap: 0.375rem; }
.rule-card__url-row { display: flex; align-items: center; gap: 0.5rem; min-width: 0; }
.rule-card__url-icon { display: flex; align-items: center; justify-content: center; color: var(--color-text-tertiary); flex-shrink: 0; }
.rule-card__url, .rule-card__backend { font-family: var(--font-mono); font-size: 0.875rem; font-weight: 500; color: var(--color-text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; flex: 1; }

/* Tags */
.rule-card__tags { display: flex; gap: 0.375rem; flex-wrap: wrap; }
.rule-card__meta-badges { display: flex; gap: 0.375rem; flex-wrap: wrap; }
.rule-card__meta-badge { font-size: 0.7rem; padding: 2px 8px; background: var(--color-bg-subtle); color: var(--color-text-secondary); border: 1px solid var(--color-border-default); border-radius: var(--radius-full); font-weight: 600; }
.rule-card__meta-badge--warning { background: var(--color-warning-50); color: var(--color-warning); border-color: transparent; }

/* Protocol badge */
.proto-badge { display: inline-block; font-size: 0.7rem; font-weight: 700; padding: 2px 6px; border-radius: var(--radius-sm); font-family: var(--font-mono); }
.proto-badge--http { background: var(--color-primary-subtle); color: var(--color-primary); }
.proto-badge--https { background: var(--color-success-50); color: var(--color-success); }
.tag { font-size: 0.75rem; padding: 2px 8px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }

.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
.btn-sm { padding: 0.25rem 0.75rem; font-size: 0.8125rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
