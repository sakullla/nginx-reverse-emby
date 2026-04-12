<template>
  <div class="rule-table">
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
            <button class="toggle" :class="{ 'toggle--on': rule.enabled }" @click="$emit('toggle', rule)">
              <span class="toggle__knob"></span>
            </button>
          </td>
          <td class="rules-table__url">{{ rule.frontend_url }}</td>
          <td class="rules-table__url rules-table__url--backend">
            {{ formatBackend(rule) }}
          </td>
          <td>
            <div class="rules-table__tags">
              <span v-for="tag in (rule.tags || [])" :key="tag" class="tag">{{ tag }}</span>
            </div>
          </td>
          <td>
            <div class="rules-table__actions">
              <button class="btn-icon" title="编辑" @click="$emit('edit', rule)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                </svg>
              </button>
              <button class="btn-icon btn-icon--danger" title="删除" @click="$emit('delete', rule)">
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
</template>

<script setup>
function httpBackends(rule) {
  if (Array.isArray(rule?.backends) && rule.backends.length > 0) {
    return rule.backends
      .map((backend) => String(backend?.url || '').trim())
      .filter(Boolean)
  }
  return rule?.backend_url ? [String(rule.backend_url).trim()] : []
}

function formatBackend(rule) {
  const backends = httpBackends(rule)
  if (backends.length === 0) return '-'
  if (backends.length === 1) return backends[0]
  return `${backends[0]} +${backends.length - 1}`
}

defineProps({
  rules: { type: Array, default: () => [] }
})
defineEmits(['toggle', 'edit', 'delete'])
</script>

<style scoped>
.rule-table { overflow-x: auto; }
.rules-table { width: 100%; border-collapse: collapse; }
.rules-table th { text-align: left; padding: 0.75rem 1rem; font-size: 0.75rem; font-weight: 600; color: var(--color-text-tertiary); border-bottom: 1px solid var(--color-border-default); }
.rules-table__row { border-bottom: 1px solid var(--color-border-subtle); }
.rules-table__row:hover { background: var(--color-bg-hover); }
.rules-table td { padding: 0.875rem 1rem; vertical-align: middle; }
.rules-table__url { font-family: var(--font-mono); font-size: 0.8125rem; color: var(--color-text-primary); }
.rules-table__url--backend { color: var(--color-text-secondary); }
.rules-table__tags { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.rules-table__actions { display: flex; gap: 0.25rem; }
.rules-table__actions .btn-icon { display: flex; align-items: center; justify-content: center; width: 28px; height: 28px; border-radius: var(--radius-md); border: none; background: transparent; color: var(--color-text-tertiary); cursor: pointer; transition: all 0.15s; }
.rules-table__actions .btn-icon:hover { background: var(--color-bg-hover); color: var(--color-primary); }
.rules-table__actions .btn-icon--danger:hover { background: var(--color-danger-50); color: var(--color-danger); }
.toggle { width: 40px; height: 22px; border-radius: 11px; border: none; background: var(--color-bg-subtle); cursor: pointer; position: relative; transition: background 0.2s; padding: 0; }
.toggle--on { background: var(--color-primary); }
.toggle__knob { position: absolute; top: 3px; left: 3px; width: 16px; height: 16px; border-radius: 50%; background: white; transition: transform 0.2s; }
.toggle--on .toggle__knob { transform: translateX(18px); }
.tag { font-size: 0.75rem; padding: 2px 8px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
</style>
