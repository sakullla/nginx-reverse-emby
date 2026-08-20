<script setup>
const DISPLAY_NAMES = {
  administrator: '管理员',
  operator: '运维',
  readonly: '只读'
}

const props = defineProps({
  modelValue: { type: Array, default: () => [] },
  roles: { type: Array, default: () => [] },
  disabled: { type: Boolean, default: false },
  error: { type: String, default: '' },
  dataTest: { type: String, default: 'roles' },
  testPrefix: { type: String, default: 'role' }
})

const emit = defineEmits(['update:modelValue'])

function selected(id) {
  return (props.modelValue || []).includes(id)
}

function toggle(id, enabled) {
  if (props.disabled) return
  const next = [...(props.modelValue || [])]
  const index = next.indexOf(id)
  if (enabled && index < 0) next.push(id)
  if (!enabled && index >= 0) next.splice(index, 1)
  emit('update:modelValue', next)
}

function label(role) {
  const id = String(role?.id || '').trim()
  const raw = String(role?.name || '').trim()
  const mapped = DISPLAY_NAMES[id.toLowerCase()]
  if (mapped && (!raw || raw.toLowerCase() === id.toLowerCase())) return mapped
  return raw || mapped || id || '未命名角色'
}

function purpose(role) {
  const hint = String(role?.description || '').trim()
  if (hint) return hint
  const id = String(role?.id || '').trim().toLowerCase()
  if (id === 'administrator') return '用户与系统'
  if (id === 'operator') return '流量与节点'
  if (id === 'readonly') return '仅查看配置'
  return '按该角色已授予的权限生效'
}

function hint() {
  const count = (props.modelValue || []).length
  if (count === 0) return '请点选至少一个角色。可多选，权限按并集生效。'
  if (count === 1) return '已选 1 个角色。可再点选其他角色叠加权限。'
  return `已选 ${count} 个角色，权限按并集生效。`
}
</script>

<template>
  <fieldset class="role-select" :data-test="dataTest">
    <legend>角色</legend>
    <p class="role-select__hint">{{ hint() }}</p>
    <div class="role-select__list">
      <label
        v-for="role in roles"
        :key="role.id"
        class="role-select__option"
        :class="{ 'role-select__option--on': selected(role.id) }"
      >
        <input
          class="role-select__input"
          type="checkbox"
          :data-test="`${testPrefix}-${role.id}`"
          :checked="selected(role.id)"
          :disabled="disabled"
          :aria-label="label(role)"
          @change="toggle(role.id, $event.target.checked)"
        >
        <span class="role-select__tick" aria-hidden="true">
          <svg viewBox="0 0 16 16" width="10" height="10" fill="none">
            <path d="M3.5 8.5l3 3 6-6" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
        </span>
        <strong class="role-select__name">{{ label(role) }}</strong>
        <small class="role-select__purpose">{{ purpose(role) }}</small>
      </label>
    </div>
    <p v-if="!roles.length" class="role-select__empty">当前没有可分配的角色</p>
    <p v-if="error" class="role-select__error">{{ error }}</p>
  </fieldset>
</template>

<style scoped>
.role-select {
  margin: 0;
  padding: 0;
  border: 0;
  display: grid;
  gap: 0.5rem;
}

.role-select legend {
  padding: 0;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.role-select__hint,
.role-select__empty,
.role-select__error {
  margin: 0;
  font-size: 0.75rem;
  line-height: 1.4;
}

.role-select__hint,
.role-select__empty {
  color: var(--color-text-muted);
}

.role-select__error {
  color: var(--color-danger);
}

.role-select__list {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 0.5rem;
}

@media (max-width: 340px) {
  .role-select__list {
    grid-template-columns: 1fr;
  }
}

.role-select__option {
  position: relative;
  display: grid;
  align-content: start;
  gap: 0.2rem;
  min-width: 0;
  min-height: 4.15rem;
  padding: 0.65rem 0.6rem 0.55rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  cursor: pointer;
  transition:
    border-color var(--duration-fast) var(--ease-default),
    background var(--duration-fast) var(--ease-default),
    box-shadow var(--duration-fast) var(--ease-default);
}

.role-select__option:hover {
  border-color: color-mix(in srgb, var(--color-primary) 42%, var(--color-border-default));
  background: color-mix(in srgb, var(--color-primary-subtle) 55%, var(--color-bg-surface));
}

.role-select__option--on {
  border-color: var(--color-primary);
  background: color-mix(in srgb, var(--color-primary) 12%, var(--color-bg-surface));
  box-shadow: inset 0 0 0 1px var(--color-primary);
}

.role-select__option:has(.role-select__input:focus-visible) {
  box-shadow: var(--shadow-focus);
}

.role-select__option:has(.role-select__input:disabled) {
  opacity: 0.6;
  cursor: not-allowed;
}

.role-select__input {
  appearance: none;
  -webkit-appearance: none;
  position: absolute;
  inset: 0;
  z-index: 1;
  width: 100%;
  height: 100%;
  margin: 0;
  opacity: 0;
  cursor: inherit;
}

.role-select__tick {
  position: absolute;
  top: 0.5rem;
  right: 0.5rem;
  display: grid;
  place-items: center;
  width: 1.05rem;
  height: 1.05rem;
  border-radius: 999px;
  background: var(--color-primary);
  color: #fff;
  opacity: 0;
  transform: scale(0.72);
  pointer-events: none;
  transition:
    opacity var(--duration-fast) var(--ease-default),
    transform var(--duration-fast) var(--ease-default);
}

.role-select__option--on .role-select__tick {
  opacity: 1;
  transform: scale(1);
}

.role-select__name {
  padding-right: 1.2rem;
  color: var(--color-text-primary);
  font-size: 0.875rem;
  font-weight: 650;
  line-height: 1.25;
}

.role-select__purpose {
  color: var(--color-text-muted);
  font-size: 0.72rem;
  line-height: 1.4;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.role-select__option--on .role-select__purpose {
  color: var(--color-text-secondary);
}
</style>
