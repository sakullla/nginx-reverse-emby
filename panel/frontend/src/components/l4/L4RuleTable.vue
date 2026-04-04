<template>
  <div class="rule-table">
    <table class="rules-table">
      <thead>
        <tr>
          <th style="width: 48px"></th>
          <th>协议</th>
          <th>监听</th>
          <th>后端</th>
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
          <td><span class="proto-badge">{{ rule.protocol?.toUpperCase() }}</span></td>
          <td class="rules-table__url">{{ rule.listen_host }}:{{ rule.listen_port }}</td>
          <td class="rules-table__url rules-table__url--backend">{{ rule.upstream_host }}:{{ rule.upstream_port }}</td>
          <td>
            <div class="rules-table__tags">
              <span v-for="tag in (rule.tags || [])" :key="tag" class="tag">{{ tag }}</span>
            </div>
          </td>
          <td>
            <div class="rules-table__actions">
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
defineProps({
  rules: { type: Array, default: () => [] }
})
defineEmits(['toggle', 'delete'])
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
.rules-table__actions { display: flex; gap: 0.25rem; opacity: 0; transition: opacity 0.15s; }
.rules-table__row:hover .rules-table__actions { opacity: 1; }
.rules-table__actions .btn-icon { display: flex; align-items: center; justify-content: center; width: 28px; height: 28px; border-radius: var(--radius-md); border: none; background: transparent; color: var(--color-text-tertiary); cursor: pointer; transition: all 0.15s; }
.rules-table__actions .btn-icon--danger:hover { background: var(--color-danger-50); color: var(--color-danger); }
.proto-badge { display: inline-block; font-size: 0.75rem; font-weight: 700; padding: 2px 6px; background: var(--color-warning-50); color: var(--color-warning); border-radius: var(--radius-sm); font-family: var(--font-mono); }
.toggle { width: 40px; height: 22px; border-radius: 11px; border: none; background: var(--color-bg-subtle); cursor: pointer; position: relative; transition: background 0.2s; padding: 0; }
.toggle--on { background: var(--color-primary); }
.toggle__knob { position: absolute; top: 3px; left: 3px; width: 16px; height: 16px; border-radius: 50%; background: white; transition: transform 0.2s; }
.toggle--on .toggle__knob { transform: translateX(18px); }
.tag { font-size: 0.75rem; padding: 2px 8px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
</style>
