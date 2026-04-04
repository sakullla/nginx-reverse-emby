<template>
  <div class="rules-page">
    <div class="rules-page__header">
      <div>
        <h1 class="rules-page__title">HTTP 规则</h1>
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
        添加规则
      </button>
    </div>

    <!-- No agent selected -->
    <div v-if="!selectedAgentId" class="rules-page__prompt">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
      </svg>
      <p>请从侧边栏选择一个节点</p>
      <p class="rules-page__prompt-hint">选择节点后即可管理其 HTTP 规则</p>
    </div>

    <!-- Agent selected, no rules -->
    <div v-else-if="selectedAgentId && !rules.length && !isLoading" class="rules-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
      </svg>
      <p>暂无规则</p>
      <button class="btn btn-primary" @click="showAddForm = true">添加第一条规则</button>
    </div>

    <!-- No search results -->
    <div v-else-if="selectedAgentId && rules.length && !filteredRules.length" class="rules-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
      </svg>
      <p>没有匹配的规则</p>
    </div>

    <!-- Search toolbar -->
    <div v-if="selectedAgentId && rules.length" class="rules-page__toolbar">
      <input v-model="searchQuery" class="search-input" placeholder="搜索 URL / 标签 / #id=...">
    </div>

    <!-- Rules card grid -->
    <div v-if="selectedAgentId && filteredRules.length" class="rule-grid">
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
            <button class="rule-card__action rule-card__action--delete" title="删除" @click="startDelete(rule)">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
            </button>
          </div>
        </div>
        <div class="rule-card__mapping">
          <code class="rule-card__url">{{ rule.frontend_url }}</code>
          <span class="rule-card__arrow">→</span>
          <code class="rule-card__backend">{{ rule.backend_url }}</code>
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
    <Teleport to="body">
      <div v-if="showAddForm || editingRule" class="modal-overlay">
        <div class="modal modal--large">
          <div class="modal__header">
            <span>{{ editingRule ? '编辑规则' : '添加规则' }}</span>
            <button class="modal__close" @click="closeForm">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18"/>
                <line x1="6" y1="6" x2="18" y2="18"/>
              </svg>
            </button>
          </div>
          <div class="modal__body">
            <RuleForm :initial-data="editingRule" :agent-id="agentId" @success="closeForm" />
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Copy Modal -->
    <Teleport to="body">
      <div v-if="showCopyModal" class="modal-overlay">
        <div class="modal modal--large">
          <div class="modal__header">
            <span>复制规则</span>
            <button class="modal__close" @click="closeForm">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18"/>
                <line x1="6" y1="6" x2="18" y2="18"/>
              </svg>
            </button>
          </div>
          <div class="modal__body">
            <RuleForm v-if="copyingRule" :initial-data="copyingRule" :agent-id="agentId" @success="closeForm" />
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
            <p>确定删除规则 <strong>{{ deletingRule.frontend_url }}</strong>？</p>
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
import { useRules, useCreateRule, useUpdateRule, useDeleteRule } from '../hooks/useRules'
import RuleForm from '../components/RuleForm.vue'

const route = useRoute()
const { selectedAgentId } = useAgent()

// 优先从 URL query 获取，否则 fall back 到 AgentContext
const agentId = computed(() => route.query.agentId || selectedAgentId.value)

const { data: _rulesData, isLoading, refetch } = useRules(agentId)
const createRule = useCreateRule(agentId)
const updateRule = useUpdateRule(agentId)
const deleteRule = useDeleteRule(agentId)
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
    String(rule.frontend_url || '').toLowerCase().includes(q) ||
    String(rule.backend_url || '').toLowerCase().includes(q) ||
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

function getStatus(rule) {
  if (!rule.enabled) return 'disabled'
  if (rule.last_apply_status === 'failed') return 'failed'
  return 'active'
}

function getStatusLabel(rule) {
  if (!rule.enabled) return '已禁用'
  if (rule.last_apply_status === 'failed') return '同步失败'
  return '生效中'
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

function closeForm() {
  showAddForm.value = false
  editingRule.value = null
  showCopyModal.value = false
  copyingRule.value = null
}

function confirmDelete() {
  if (deletingRule.value) {
    deleteRule.mutate(deletingRule.value.id)
  }
  deletingRule.value = null
}
</script>

<style scoped>
.rules-page { max-width: 1200px; margin: 0 auto; }
.rules-page__header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 2rem; gap: 1rem; }
.rules-page__title { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.rules-page__subtitle { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.rules-page__prompt, .rules-page__empty, .rules-page__loading { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }
.rules-page__prompt-hint { font-size: 0.875rem; color: var(--color-text-tertiary); }

/* Toolbar */
.rules-page__toolbar { margin-bottom: 1.5rem; }
.search-input { width: 100%; padding: 0.625rem 0.875rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s; box-sizing: border-box; }
.search-input:focus { border-color: var(--color-primary); }
.search-input::placeholder { color: var(--color-text-muted); }

/* Card grid */
.rule-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 1rem; }

/* Rule card — L4 style */
.rule-card {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  transition: opacity 0.15s;
}
.rule-card--disabled { opacity: 0.6; }
.rule-card__header { display: flex; align-items: center; justify-content: space-between; }
.rule-card__badges { display: flex; align-items: center; gap: 0.5rem; }
.rule-card__id { font-size: 0.75rem; font-family: var(--font-mono); color: var(--color-text-tertiary); }
.rule-card__status { font-size: 0.75rem; font-weight: 600; padding: 2px 8px; border-radius: var(--radius-full); }
.rule-card__status--active { background: var(--color-success-50); color: var(--color-success); }
.rule-card__status--disabled { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.rule-card__status--failed { background: var(--color-danger-50); color: var(--color-danger); }

/* Actions — hidden until hover */
.rule-card__actions { display: flex; gap: 0.25rem; opacity: 0; transition: opacity 0.15s; }
.rule-card:hover .rule-card__actions { opacity: 1; }
.rule-card__action { display: flex; align-items: center; justify-content: center; width: 28px; height: 28px; border-radius: var(--radius-md); border: none; background: transparent; color: var(--color-text-tertiary); cursor: pointer; transition: all 0.15s; }
.rule-card__action:hover { background: var(--color-bg-hover); color: var(--color-text-primary); }
.rule-card__action--delete:hover { background: var(--color-danger-50); color: var(--color-danger); }
.rule-card__action--toggle:hover { background: var(--color-warning-50); color: var(--color-warning); }

/* Inline mapping */
.rule-card__mapping { display: flex; align-items: center; gap: 0.75rem; flex-wrap: wrap; }
.rule-card__url, .rule-card__backend { font-family: var(--font-mono); font-size: 0.9375rem; font-weight: 600; color: var(--color-text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rule-card__arrow { color: var(--color-text-muted); font-size: 0.875rem; flex-shrink: 0; }

/* Tags */
.rule-card__tags { display: flex; gap: 0.25rem; flex-wrap: wrap; }

/* Protocol badge */
.proto-badge { display: inline-block; font-size: 0.7rem; font-weight: 700; padding: 2px 6px; border-radius: var(--radius-sm); font-family: var(--font-mono); }
.proto-badge--http { background: var(--color-primary-subtle); color: var(--color-primary); }
.proto-badge--https { background: var(--color-success-50); color: var(--color-success); }
.tag { font-size: 0.75rem; padding: 2px 8px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }

/* Modals — same as L4 style */
.modal-overlay { position: fixed; inset: 0; background: rgba(37,23,54,0.4); backdrop-filter: blur(8px); z-index: var(--z-modal); display: flex; align-items: center; justify-content: center; padding: var(--space-4); }
.modal { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-3xl); box-shadow: var(--shadow-2xl); width: min(480px, 90vw); max-height: calc(100vh - var(--space-8)); display: flex; flex-direction: column; overflow: hidden; }
.modal--large { width: min(600px, 92vw); }
.modal__header { display: flex; align-items: center; justify-content: space-between; gap: var(--space-4); padding: var(--space-5) var(--space-6); border-bottom: 1px solid var(--color-border-subtle); flex-shrink: 0; background: var(--gradient-soft); font-weight: 600; font-size: var(--text-lg); color: var(--color-text-primary); }
.modal__body { padding: var(--space-6); overflow-y: auto; flex: 1; display: flex; flex-direction: column; gap: var(--space-5); }
.modal__footer { padding: var(--space-4) var(--space-6); display: flex; justify-content: flex-end; gap: var(--space-3); border-top: 1px solid var(--color-border-subtle); flex-shrink: 0; }
.modal__close { display: flex; align-items: center; justify-content: center; width: 36px; height: 36px; border-radius: var(--radius-full); color: var(--color-text-tertiary); transition: all var(--duration-normal) var(--ease-bounce); flex-shrink: 0; border: none; background: transparent; cursor: pointer; }
.modal__close:hover { background: var(--color-danger-50); color: var(--color-danger); transform: rotate(90deg); }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
.btn-sm { padding: 0.25rem 0.75rem; font-size: 0.8125rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
