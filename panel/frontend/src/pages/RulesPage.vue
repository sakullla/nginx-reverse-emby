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

    <!-- Search and tag filter toolbar -->
    <div v-if="selectedAgentId && rules.length" class="rules-page__toolbar">
      <input v-model="searchQuery" class="search-input" placeholder="搜索 URL / 标签 / #id=...">
      <div v-if="allTags.length" class="tag-filter">
        <button v-for="tag in allTags" :key="tag" class="tag-filter__btn" :class="{ 'tag-filter__btn--active': selectedTags.includes(tag), 'tag-filter__btn--system': isSystemTag(tag) }" @click="toggleTag(tag)">{{ tag }}</button>
      </div>
    </div>

    <!-- Rules card grid -->
    <div v-else-if="selectedAgentId && filteredRules.length" class="rule-grid">
      <div v-for="rule in filteredRules" :key="rule.id" class="rule-card">
        <div class="rule-card__header">
          <span class="rule-card__id">#{{ rule.id }}</span>
          <div class="rule-card__icon">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
              <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
            </svg>
          </div>
          <div class="rule-card__badges">
            <span class="proto-badge" :class="rule.frontend_url?.startsWith('https') ? 'proto-badge--https' : 'proto-badge--http'">
              {{ rule.frontend_url?.startsWith('https') ? 'HTTPS' : 'HTTP' }}
            </span>
            <span class="rule-card__status" :class="`rule-card__status--${getStatus(rule)}`">
              {{ getStatusLabel(rule) }}
            </span>
          </div>
        </div>
        <div class="rule-card__url">{{ rule.frontend_url }}</div>
        <div class="rule-card__backend">→ {{ rule.backend_url }}</div>
        <div class="rule-card__tags">
          <span v-for="tag in (rule.tags || [])" :key="tag" class="tag">{{ tag }}</span>
        </div>
        <div class="rule-card__actions">
          <button class="toggle toggle--sm" :class="{ 'toggle--on': rule.enabled }" @click="toggleRule(rule)">
            <span class="toggle__knob"></span>
          </button>
          <button class="btn btn-secondary btn-sm" @click="startEdit(rule)">编辑</button>
          <button class="btn btn-secondary btn-sm" @click="handleCopy(rule)" title="复制">复制</button>
          <button class="btn btn-danger btn-sm" @click="startDelete(rule)">删除</button>
        </div>
      </div>
    </div>

    <!-- Loading -->
    <div v-if="isLoading" class="rules-page__loading">
      <div class="spinner"></div>
    </div>

    <!-- Add/Edit Form Modal -->
    <Teleport to="body">
      <div v-if="showAddForm || editingRule" class="modal-overlay" @click.self="closeForm">
        <div class="modal modal--large">
          <div class="modal__header">{{ editingRule ? '编辑规则' : '添加规则' }}</div>
          <div class="modal__body">
            <RuleForm :initial-data="editingRule" :agent-id="agentId" @success="closeForm" />
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Copy Modal -->
    <Teleport to="body">
      <div v-if="showCopyModal" class="modal-overlay" @click.self="closeForm">
        <div class="modal modal--large">
          <div class="modal__header">复制规则</div>
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

// Search and filter
const searchQuery = ref('')
const selectedTags = ref([])
const SYSTEM_TAG_SET = new Set(['TCP', 'UDP', 'HTTP', 'HTTPS', 'RR', 'LC', 'RND', 'HASH'])
function isSystemTag(tag) { return SYSTEM_TAG_SET.has(tag) || /^:\d+$/.test(tag) }
function toggleTag(tag) {
  const i = selectedTags.value.indexOf(tag)
  if (i === -1) selectedTags.value.push(tag)
  else selectedTags.value.splice(i, 1)
}

const filteredRules = computed(() => {
  let result = rules.value
  if (selectedTags.value.length > 0) {
    result = result.filter(rule => selectedTags.value.some(tag => (rule.tags || []).includes(tag)))
  }
  const raw = searchQuery.value.trim()
  if (!raw) return result
  const idMatch = raw.match(/^#id=(\S+)$/)
  if (idMatch) return result.filter(rule => String(rule.id) === idMatch[1])
  const q = raw.toLowerCase()
  return result.filter(rule =>
    String(rule.frontend_url || '').toLowerCase().includes(q) ||
    String(rule.backend_url || '').toLowerCase().includes(q) ||
    String(rule.name || '').toLowerCase().includes(q) ||
    (rule.tags || []).some(tag => String(tag).toLowerCase().includes(q))
  )
})

const allTags = computed(() => {
  const tagSet = new Set()
  rules.value.forEach(rule => (rule.tags || []).forEach(tag => tagSet.add(tag)))
  return [...tagSet].sort()
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
.rules-page__toolbar { display: flex; flex-direction: column; gap: 0.75rem; margin-bottom: 1.5rem; }
.search-input { width: 100%; padding: 0.625rem 0.875rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s; box-sizing: border-box; }
.search-input:focus { border-color: var(--color-primary); }
.search-input::placeholder { color: var(--color-text-muted); }
.tag-filter { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.tag-filter__btn { font-size: 0.7rem; padding: 2px 8px; background: var(--color-bg-subtle); border: 1px solid var(--color-border-default); border-radius: var(--radius-full); color: var(--color-text-secondary); cursor: pointer; transition: all 0.15s; }
.tag-filter__btn:hover { border-color: var(--color-border-strong); }
.tag-filter__btn--active { background: var(--color-primary-subtle); border-color: var(--color-primary); color: var(--color-primary); }
.tag-filter__btn--system { opacity: 0.6; font-family: var(--font-mono); }
/* Card grid */
.rule-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 1rem; }
/* Rule card */
.rule-card { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-xl); padding: 1.25rem; display: flex; flex-direction: column; gap: 0.75rem; }
.rule-card__header { display: flex; align-items: center; justify-content: space-between; }
.rule-card__id { font-size: 0.75rem; font-family: var(--font-mono); color: var(--color-text-tertiary); }
.rule-card__icon { color: var(--color-primary); }
.rule-card__badges { display: flex; align-items: center; gap: 0.5rem; }
.rule-card__status { font-size: 0.75rem; font-weight: 600; padding: 2px 8px; border-radius: var(--radius-full); }
.rule-card__status--active { background: var(--color-success-50); color: var(--color-success); }
.rule-card__status--disabled { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.rule-card__status--failed { background: var(--color-danger-50); color: var(--color-danger); }
.rule-card__url { font-family: var(--font-mono); font-size: 0.9375rem; font-weight: 600; color: var(--color-text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rule-card__backend { font-family: var(--font-mono); font-size: 0.8125rem; color: var(--color-text-secondary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rule-card__tags { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.rule-card__actions { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; margin-top: auto; }
/* Protocol badge */
.proto-badge { display: inline-block; font-size: 0.7rem; font-weight: 700; padding: 2px 6px; border-radius: var(--radius-sm); font-family: var(--font-mono); }
.proto-badge--http { background: var(--color-primary-subtle); color: var(--color-primary); }
.proto-badge--https { background: var(--color-success-50); color: var(--color-success); }
/* Tags */
.tag { font-size: 0.75rem; padding: 2px 8px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
/* Toggle */
.toggle { width: 40px; height: 22px; border-radius: 11px; border: none; background: var(--color-bg-subtle); cursor: pointer; position: relative; transition: background 0.2s; padding: 0; flex-shrink: 0; }
.toggle--on { background: var(--color-primary); }
.toggle--sm { width: 36px; height: 20px; border-radius: 10px; }
.toggle--sm .toggle__knob { width: 14px; height: 14px; }
.toggle--sm.toggle--on .toggle__knob { transform: translateX(16px); }
.toggle__knob { position: absolute; top: 3px; left: 3px; width: 16px; height: 16px; border-radius: 50%; background: white; transition: transform 0.2s; }
/* Modals - standardized spacing */
.modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.5); backdrop-filter: blur(4px); z-index: var(--z-modal); display: flex; align-items: center; justify-content: center; }
.modal { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); box-shadow: var(--shadow-xl); width: min(480px, 90vw); overflow: hidden; }
.modal--large { width: min(600px, 92vw); }
.modal__header { padding: 1rem 1.5rem; font-weight: 600; font-size: 1rem; border-bottom: 1px solid var(--color-border-subtle); }
.modal__body { padding: 1.5rem; display: flex; flex-direction: column; gap: 1.25rem; }
.modal__footer { padding: 1rem 1.5rem; display: flex; justify-content: flex-end; gap: 0.75rem; border-top: 1px solid var(--color-border-subtle); }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
.btn-sm { padding: 0.25rem 0.75rem; font-size: 0.8125rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
