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

    <!-- Rules table -->
    <div v-else-if="selectedAgentId && rules.length" class="rules-list">
      <table class="rules-table">
        <thead>
          <tr>
            <th style="width: 48px"></th>
            <th>前端地址</th>
            <th>后端地址</th>
            <th>标签</th>
            <th style="width: 80px">操作</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="rule in rules" :key="rule.id" class="rules-table__row">
            <td>
              <button
                class="toggle"
                :class="{ 'toggle--on': rule.enabled }"
                @click="toggleRule(rule)"
              >
                <span class="toggle__knob"></span>
              </button>
            </td>
            <td class="rules-table__url">{{ rule.frontend_url }}</td>
            <td class="rules-table__url rules-table__url--backend">{{ rule.backend_url }}</td>
            <td>
              <div class="rules-table__tags">
                <span v-for="tag in (rule.tags || [])" :key="tag" class="tag">{{ tag }}</span>
              </div>
            </td>
            <td>
              <div class="rules-table__actions">
                <button class="btn-icon" title="编辑" @click="startEdit(rule)">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                    <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                  </svg>
                </button>
                <button class="btn-icon btn-icon--danger" title="删除" @click="startDelete(rule)">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <polyline points="3 6 5 6 21 6"/>
                    <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
                  </svg>
                </button>
              </div>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <!-- Loading -->
    <div v-if="isLoading" class="rules-page__loading">
      <div class="spinner"></div>
    </div>

    <!-- Add/Edit Form Modal -->
    <Teleport to="body">
      <div v-if="showAddForm || editingRule" class="modal-overlay" @click.self="closeForm">
        <div class="modal">
          <div class="modal__header">{{ editingRule ? '编辑规则' : '添加规则' }}</div>
          <div class="modal__body">
            <div class="form-group">
              <label>前端地址</label>
              <input v-model="form.frontend_url" class="input-base" placeholder="https://example.com">
            </div>
            <div class="form-group">
              <label>后端地址</label>
              <input v-model="form.backend_url" class="input-base" placeholder="http://192.168.1.100:8096">
            </div>
            <div class="form-group">
              <label>标签（逗号分隔）</label>
              <input v-model="form.tags" class="input-base" placeholder="emby, media">
            </div>
            <div class="form-group form-group--check">
              <label>
                <input type="checkbox" v-model="form.enabled"> 启用规则
              </label>
            </div>
          </div>
          <div class="modal__footer">
            <button class="btn btn-secondary" @click="closeForm">取消</button>
            <button class="btn btn-primary" @click="submitForm">{{ editingRule ? '保存' : '添加' }}</button>
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
import { useAgent } from '../context/AgentContext'
import { useRules, useCreateRule, useUpdateRule, useDeleteRule } from '../hooks/useRules'

const { selectedAgentId } = useAgent()
const { data: _rulesData, isLoading, refetch } = useRules(selectedAgentId)
const createRule = useCreateRule(selectedAgentId)
const updateRule = useUpdateRule(selectedAgentId)
const deleteRule = useDeleteRule(selectedAgentId)
const rules = computed(() => _rulesData.value ?? [])
const showAddForm = ref(false)
const editingRule = ref(null)
const deletingRule = ref(null)
const form = ref({ frontend_url: '', backend_url: '', tags: '', enabled: true })

const enabledCount = computed(() => rules.value.filter(r => r.enabled).length)

function toggleRule(rule) {
  updateRule.mutate({ id: rule.id, enabled: !rule.enabled })
}

function startEdit(rule) {
  editingRule.value = rule
  form.value = { frontend_url: rule.frontend_url, backend_url: rule.backend_url, tags: (rule.tags || []).join(', '), enabled: rule.enabled }
}

function startDelete(rule) {
  deletingRule.value = rule
}

function closeForm() {
  showAddForm.value = false
  editingRule.value = null
  form.value = { frontend_url: '', backend_url: '', tags: '', enabled: true }
}

function submitForm() {
  const payload = {
    frontend_url: form.value.frontend_url,
    backend_url: form.value.backend_url,
    tags: form.value.tags ? form.value.tags.split(',').map(t => t.trim()).filter(Boolean) : [],
    enabled: form.value.enabled
  }
  if (editingRule.value) {
    updateRule.mutate({ id: editingRule.value.id, ...payload })
  } else {
    createRule.mutate(payload)
  }
  closeForm()
}

function confirmDelete() {
  if (deletingRule.value) {
    deleteRule.mutate(deletingRule.value.id)
  }
  deletingRule.value = null
}
</script>

<style scoped>
.rules-page { max-width: 1000px; margin: 0 auto; }
.rules-page__header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 1.5rem; gap: 1rem; }
.rules-page__title { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.rules-page__subtitle { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.rules-page__prompt, .rules-page__empty, .rules-page__loading { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }
.rules-page__prompt-hint { font-size: 0.875rem; color: var(--color-text-tertiary); }
.rules-list { overflow-x: auto; }
.rules-table { width: 100%; border-collapse: collapse; }
.rules-table th { text-align: left; padding: 0.75rem 1rem; font-size: 0.75rem; font-weight: 600; color: var(--color-text-tertiary); border-bottom: 1px solid var(--color-border-default); }
.rules-table__row { border-bottom: 1px solid var(--color-border-subtle); }
.rules-table__row:hover { background: var(--color-bg-hover); }
.rules-table td { padding: 0.875rem 1rem; vertical-align: middle; }
.rules-table__url { font-family: var(--font-mono); font-size: 0.8125rem; color: var(--color-text-primary); }
.rules-table__url--backend { color: var(--color-text-secondary); }
.rules-table__tags { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.rules-table__actions { display: flex; gap: 0.25rem; opacity: 0; transition: opacity 0.15s; }
.rules-table__row:hover .rules-table__actions { opacity: 1; }
.rules-table__actions .btn-icon { display: flex; align-items: center; justify-content: center; width: 28px; height: 28px; border-radius: var(--radius-md); border: none; background: transparent; color: var(--color-text-tertiary); cursor: pointer; transition: all 0.15s; }
.rules-table__actions .btn-icon:hover { background: var(--color-bg-hover); color: var(--color-primary); }
.rules-table__actions .btn-icon--danger:hover { background: var(--color-danger-50); color: var(--color-danger); }
/* Toggle */
.toggle { width: 40px; height: 22px; border-radius: 11px; border: none; background: var(--color-bg-subtle); cursor: pointer; position: relative; transition: background 0.2s; padding: 0; }
.toggle--on { background: var(--color-primary); }
.toggle__knob { position: absolute; top: 3px; left: 3px; width: 16px; height: 16px; border-radius: 50%; background: white; transition: transform 0.2s; }
.toggle--on .toggle__knob { transform: translateX(18px); }
/* Tags */
.tag { font-size: 0.75rem; padding: 2px 8px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
/* Modals */
.modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.5); backdrop-filter: blur(4px); z-index: var(--z-modal); display: flex; align-items: center; justify-content: center; }
.modal { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); box-shadow: var(--shadow-xl); width: min(480px, 90vw); overflow: hidden; }
.modal__header { padding: 1rem 1.25rem; font-weight: 600; font-size: 1rem; border-bottom: 1px solid var(--color-border-subtle); }
.modal__body { padding: 1.25rem; display: flex; flex-direction: column; gap: 1rem; }
.modal__footer { padding: 1rem 1.25rem; display: flex; justify-content: flex-end; gap: 0.75rem; border-top: 1px solid var(--color-border-subtle); }
.form-group { display: flex; flex-direction: column; gap: 0.375rem; }
.form-group label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-secondary); }
.form-group--check { flex-direction: row; align-items: center; }
.form-group--check label { display: flex; align-items: center; gap: 0.5rem; cursor: pointer; font-weight: normal; }
.input-base { width: 100%; padding: 0.5rem 0.75rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s; }
.input-base:focus { border-color: var(--color-primary); }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
