<template>
  <div class="rule-detail">
    <div class="rule-detail__header">
      <button class="btn btn-secondary" @click="router.back()">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polyline points="15 18 9 12 15 6"/>
        </svg>
        返回
      </button>
      <h1 class="rule-detail__title">{{ isNew ? '添加规则' : '编辑规则' }}</h1>
    </div>

    <div class="rule-detail__form">
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
      <div class="rule-detail__actions">
        <button class="btn btn-secondary" @click="router.back()">取消</button>
        <button class="btn btn-primary" @click="submit">{{ isNew ? '添加' : '保存' }}</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useAgent } from '../context/AgentContext'
import { useRules } from '../hooks/useRules'

const route = useRoute()
const router = useRouter()
const { selectedAgentId } = useAgent()
const { data: _rulesData } = useRules(selectedAgentId)
const rules = computed(() => _rulesData.value ?? [])

const isNew = computed(() => route.params.id === undefined || route.params.id === 'new')
const ruleId = computed(() => isNew.value ? null : Number(route.params.id))
const rule = computed(() => rules.value.find(r => r.id === ruleId.value))

const form = ref({ frontend_url: '', backend_url: '', tags: '', enabled: true })

onMounted(() => {
  if (rule.value) {
    form.value = {
      frontend_url: rule.value.frontend_url,
      backend_url: rule.value.backend_url,
      tags: (rule.value.tags || []).join(', '),
      enabled: rule.value.enabled
    }
  }
})

function submit() {
  // save rule
  router.back()
}
</script>

<style scoped>
.rule-detail { max-width: 600px; margin: 0 auto; }
.rule-detail__header { display: flex; align-items: center; gap: 1rem; margin-bottom: 1.5rem; }
.rule-detail__title { font-size: 1.25rem; font-weight: 700; margin: 0; color: var(--color-text-primary); }
.rule-detail__form { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); padding: 1.5rem; display: flex; flex-direction: column; gap: 1.25rem; }
.form-group { display: flex; flex-direction: column; gap: 0.375rem; }
.form-group label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-secondary); }
.form-group--check { flex-direction: row; align-items: center; }
.form-group--check label { display: flex; align-items: center; gap: 0.5rem; cursor: pointer; font-weight: normal; }
.input-base { width: 100%; padding: 0.625rem 0.875rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s; }
.input-base:focus { border-color: var(--color-primary); }
.rule-detail__actions { display: flex; justify-content: flex-end; gap: 0.75rem; padding-top: 0.5rem; }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
</style>
