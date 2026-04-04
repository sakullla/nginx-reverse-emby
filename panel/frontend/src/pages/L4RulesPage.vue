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

    <div v-if="!selectedAgentId" class="rules-page__prompt">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/>
      </svg>
      <p>请从侧边栏选择一个节点</p>
    </div>

    <div v-else-if="!rules.length && !isLoading" class="rules-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/>
      </svg>
      <p>暂无 L4 规则</p>
      <button class="btn btn-primary" @click="showAddForm = true">添加第一条规则</button>
    </div>

    <div v-else-if="rules.length" class="rule-grid">
      <div v-for="rule in rules" :key="rule.id" class="rule-card">
        <div class="rule-card__header">
          <div class="rule-card__icon">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/>
            </svg>
          </div>
          <div class="rule-card__badges">
            <span class="proto-badge" :class="rule.protocol === 'udp' ? 'proto-badge--udp' : 'proto-badge--tcp'">
              {{ rule.protocol?.toUpperCase() }}
            </span>
            <span class="rule-card__status" :class="`rule-card__status--${getStatus(rule)}`">
              {{ getStatusLabel(rule) }}
            </span>
          </div>
        </div>
        <div class="rule-card__url">:{{ rule.listen_port }}</div>
        <div class="rule-card__backend">→ {{ rule.upstream_host }}:{{ rule.upstream_port }}</div>
        <div class="rule-card__tags">
          <span v-for="tag in (rule.tags || []).slice(0, 3)" :key="tag" class="tag">{{ tag }}</span>
        </div>
        <div class="rule-card__actions">
          <button class="toggle toggle--sm" :class="{ 'toggle--on': rule.enabled }" @click="toggleRule(rule)">
            <span class="toggle__knob"></span>
          </button>
          <button class="btn btn-secondary btn-sm" @click="startEdit(rule)">编辑</button>
          <button class="btn btn-danger btn-sm" @click="startDelete(rule)">删除</button>
        </div>
      </div>
    </div>

    <div v-if="isLoading" class="rules-page__loading">
      <div class="spinner"></div>
    </div>

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

    <!-- Add Form Modal -->
    <Teleport to="body">
      <div v-if="showAddForm || editingRule" class="modal-overlay" @click.self="closeForm">
        <div class="modal">
          <div class="modal__header">{{ editingRule ? '编辑 L4 规则' : '添加 L4 规则' }}</div>
          <div class="modal__body">
            <div class="form-group">
              <label>协议</label>
              <select v-model="form.protocol" class="input-base">
                <option value="tcp">TCP</option>
                <option value="udp">UDP</option>
              </select>
            </div>
            <div class="form-row">
              <div class="form-group">
                <label>监听地址</label>
                <input v-model="form.listen_host" class="input-base" placeholder="0.0.0.0">
              </div>
              <div class="form-group" style="width: 100px">
                <label>端口</label>
                <input v-model="form.listen_port" class="input-base" placeholder="25565">
              </div>
            </div>
            <div class="form-row">
              <div class="form-group">
                <label>后端地址</label>
                <input v-model="form.upstream_host" class="input-base" placeholder="192.168.1.100">
              </div>
              <div class="form-group" style="width: 100px">
                <label>端口</label>
                <input v-model="form.upstream_port" class="input-base" placeholder="25565">
              </div>
            </div>
            <div class="form-group">
              <label>标签</label>
              <input v-model="form.tags" class="input-base" placeholder="game, mc">
            </div>
          </div>
          <div class="modal__footer">
            <button class="btn btn-secondary" @click="closeForm">取消</button>
            <button class="btn btn-primary" @click="submitForm">{{ editingRule ? '保存' : '添加' }}</button>
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

const route = useRoute()
const { selectedAgentId } = useAgent()

// 优先从 URL query 获取，否则 fall back 到 AgentContext
const agentId = computed(() => route.query.agentId || selectedAgentId.value)

const { data: _rulesData, isLoading } = useL4Rules(agentId)
const createL4Rule = useCreateL4Rule(agentId)
const updateL4Rule = useUpdateL4Rule(agentId)
const deleteL4Rule = useDeleteL4Rule(agentId)
const rules = computed(() => _rulesData.value ?? [])
const showAddForm = ref(false)
const deletingRule = ref(null)
const editingRule = ref(null)
const form = ref({ protocol: 'tcp', listen_host: '0.0.0.0', listen_port: '', upstream_host: '', upstream_port: '', tags: '' })

const enabledCount = computed(() => rules.value.filter(r => r.enabled).length)

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
  updateL4Rule.mutate({ id: rule.id, enabled: !rule.enabled })
}

function startDelete(rule) {
  deletingRule.value = rule
}

function startEdit(rule) {
  editingRule.value = rule
  form.value = {
    protocol: rule.protocol,
    listen_host: rule.listen_host,
    listen_port: String(rule.listen_port),
    upstream_host: rule.upstream_host,
    upstream_port: String(rule.upstream_port),
    tags: (rule.tags || []).join(', ')
  }
  showAddForm.value = true
}

function confirmDelete() {
  if (deletingRule.value) {
    deleteL4Rule.mutate(deletingRule.value.id)
  }
  deletingRule.value = null
}

function closeForm() {
  showAddForm.value = false
  editingRule.value = null
}

function submitForm() {
  const payload = {
    protocol: form.value.protocol,
    listen_host: form.value.listen_host,
    listen_port: Number(form.value.listen_port),
    upstream_host: form.value.upstream_host,
    upstream_port: Number(form.value.upstream_port),
    tags: form.value.tags ? form.value.tags.split(',').map(t => t.trim()).filter(Boolean) : [],
    enabled: editingRule.value ? editingRule.value.enabled : true
  }
  if (editingRule.value) {
    updateL4Rule.mutate({ id: editingRule.value.id, ...payload })
  } else {
    createL4Rule.mutate(payload)
  }
  showAddForm.value = false
  editingRule.value = null
  form.value = { protocol: 'tcp', listen_host: '0.0.0.0', listen_port: '', upstream_host: '', upstream_port: '', tags: '' }
}
</script>

<style scoped>
.rules-page { max-width: 1200px; margin: 0 auto; }
.rules-page__header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 2rem; gap: 1rem; }
.rules-page__title { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.rules-page__subtitle { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.rules-page__prompt, .rules-page__empty, .rules-page__loading { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }
/* Card grid */
.rule-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 1rem; }
/* Rule card */
.rule-card { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-xl); padding: 1.25rem; display: flex; flex-direction: column; gap: 0.75rem; }
.rule-card__header { display: flex; align-items: center; justify-content: space-between; }
.rule-card__icon { color: var(--color-warning); }
.rule-card__badges { display: flex; align-items: center; gap: 0.5rem; }
.rule-card__status { font-size: 0.75rem; font-weight: 600; padding: 2px 8px; border-radius: var(--radius-full); }
.rule-card__status--active { background: var(--color-success-50); color: var(--color-success); }
.rule-card__status--disabled { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.rule-card__status--failed { background: var(--color-danger-50); color: var(--color-danger); }
.rule-card__url { font-family: var(--font-mono); font-size: 1.25rem; font-weight: 700; color: var(--color-text-primary); }
.rule-card__backend { font-family: var(--font-mono); font-size: 0.8125rem; color: var(--color-text-secondary); }
.rule-card__tags { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.rule-card__actions { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; margin-top: auto; }
/* Protocol badge */
.proto-badge { display: inline-block; font-size: 0.7rem; font-weight: 700; padding: 2px 6px; border-radius: var(--radius-sm); font-family: var(--font-mono); }
.proto-badge--tcp { background: var(--color-warning-50); color: var(--color-warning); }
.proto-badge--udp { background: #f3e8ff; color: #7c3aed; }
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
.modal__header { padding: 1rem 1.5rem; font-weight: 600; font-size: 1rem; border-bottom: 1px solid var(--color-border-subtle); }
.modal__body { padding: 1.5rem; display: flex; flex-direction: column; gap: 1.25rem; }
.modal__footer { padding: 1rem 1.5rem; display: flex; justify-content: flex-end; gap: 0.75rem; border-top: 1px solid var(--color-border-subtle); }
.form-group { display: flex; flex-direction: column; gap: 0.5rem; }
.form-group label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-secondary); }
.form-row { display: flex; gap: 0.75rem; }
.input-base { width: 100%; padding: 0.625rem 0.875rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s; box-sizing: border-box; }
.input-base:focus { border-color: var(--color-primary); }
select.input-base { appearance: auto; }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
.btn-sm { padding: 0.25rem 0.75rem; font-size: 0.8125rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
