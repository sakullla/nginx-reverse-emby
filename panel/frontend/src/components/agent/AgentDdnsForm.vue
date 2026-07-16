<template>
  <div class="agent-ddns-form">
    <p class="agent-ddns-form__lead" data-testid="agent-ddns-form-no-token-hint">
      为 NAT 节点解析动态域名；Cloudflare 凭证由主控统一保管，无需在此填写。
    </p>

    <label class="agent-ddns-form__field">
      <span class="agent-ddns-form__label">域名</span>
      <input
        :value="modelValue.domain"
        class="agent-ddns-form__input"
        type="text"
        placeholder="例如 edge.example.com"
        data-testid="agent-ddns-form-domain"
        @input="updateField('domain', $event.target.value)"
      >
    </label>

    <div
      v-for="family in families"
      :key="family.key"
      class="agent-ddns-form__family"
      :data-testid="`agent-ddns-form-family-${family.key}`"
    >
      <div class="agent-ddns-form__family-head">
        <label class="agent-ddns-form__check">
          <input
            :checked="modelValue[family.key].enabled"
            class="agent-ddns-form__checkbox"
            type="checkbox"
            :data-testid="`agent-ddns-form-${family.key}-enabled`"
            @change="updateFamily(family.key, 'enabled', $event.target.checked)"
          >
          <span class="agent-ddns-form__check-label">{{ family.label }} 提取</span>
        </label>
      </div>
      <div v-if="modelValue[family.key].enabled" class="agent-ddns-form__family-body">
        <label class="agent-ddns-form__field">
          <span class="agent-ddns-form__label">来源</span>
          <select
            :value="modelValue[family.key].source"
            class="agent-ddns-form__input"
            :data-testid="`agent-ddns-form-${family.key}-source`"
            @change="updateFamily(family.key, 'source', $event.target.value)"
          >
            <option value="public_api">公网探测</option>
            <option value="interface">本机网卡</option>
          </select>
        </label>
        <label v-if="modelValue[family.key].source === 'interface'" class="agent-ddns-form__field">
          <span class="agent-ddns-form__label">网卡名</span>
          <input
            :value="modelValue[family.key].interface"
            class="agent-ddns-form__input"
            type="text"
            placeholder="例如 eth0"
            :data-testid="`agent-ddns-form-${family.key}-interface`"
            @input="updateFamily(family.key, 'interface', $event.target.value)"
          >
        </label>
      </div>
    </div>

    <p v-if="domainMissing" class="agent-ddns-form__hint" data-testid="agent-ddns-form-domain-required">
      启用 IPv4 或 IPv6 时需填写域名
    </p>

    <div class="agent-ddns-form__footer">
      <button
        class="btn btn-primary agent-ddns-form__save"
        type="button"
        :disabled="saving || !canSave"
        data-testid="agent-ddns-form-save"
        @click="$emit('save')"
      >保存</button>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'

const props = defineProps({
  modelValue: { type: Object, required: true },
  saving: { type: Boolean, default: false }
})

const emit = defineEmits(['update:modelValue', 'save'])

const families = [
  { key: 'ipv4', label: 'IPv4' },
  { key: 'ipv6', label: 'IPv6' }
]

const anyEnabled = computed(() => !!(props.modelValue.ipv4?.enabled || props.modelValue.ipv6?.enabled))
const domainMissing = computed(() => anyEnabled.value && !String(props.modelValue.domain || '').trim())
const canSave = computed(() => !domainMissing.value)

function updateField(key, value) {
  emit('update:modelValue', { ...props.modelValue, [key]: value })
}

function updateFamily(family, key, value) {
  emit('update:modelValue', {
    ...props.modelValue,
    [family]: { ...props.modelValue[family], [key]: value }
  })
}
</script>

<style scoped>
.agent-ddns-form {
  display: flex;
  flex-direction: column;
  gap: 0.85rem;
}
.agent-ddns-form__lead {
  margin: 0;
  padding: 0.55rem 0.75rem;
  border: 1px dashed color-mix(in srgb, var(--color-border-default) 90%, transparent);
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--color-bg-subtle) 45%, var(--color-bg-surface));
  color: var(--color-text-muted);
  font-size: 0.75rem;
  line-height: 1.45;
}
.agent-ddns-form__field {
  display: flex;
  flex-direction: column;
  align-items: stretch;
  gap: 0.3rem;
  min-width: 0;
}
.agent-ddns-form__label {
  display: block;
  color: var(--color-text-secondary);
  font-size: 0.8125rem;
  font-weight: 550;
  line-height: 1.35;
}
.agent-ddns-form__input {
  display: block;
  box-sizing: border-box;
  width: 100%;
  min-width: 0;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  padding: 0.5rem 0.625rem;
  font-size: 0.875rem;
  line-height: 1.35;
  min-height: 2.25rem;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}
.agent-ddns-form__input:focus {
  outline: none;
  border-color: color-mix(in srgb, var(--color-primary, #3b82f6) 55%, var(--color-border-default));
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary, #3b82f6) 16%, transparent);
}
.agent-ddns-form__family {
  display: flex;
  flex-direction: column;
  gap: 0.55rem;
  padding: 0.7rem 0.8rem;
  border: 1px solid color-mix(in srgb, var(--color-border-default) 90%, transparent);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
}
.agent-ddns-form__family-head {
  display: flex;
  align-items: center;
}
.agent-ddns-form__check {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  min-height: 2.25rem;
}
.agent-ddns-form__checkbox {
  flex: 0 0 auto;
  width: 1rem;
  height: 1rem;
  margin: 0;
}
.agent-ddns-form__check-label {
  color: var(--color-text-primary);
  font-size: 0.875rem;
  font-weight: 600;
}
.agent-ddns-form__family-body {
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
  padding-top: 0.35rem;
  border-top: 1px solid color-mix(in srgb, var(--color-border-subtle) 85%, transparent);
}
.agent-ddns-form__hint {
  margin: 0;
  color: var(--color-warning, #d97706);
  font-size: 0.75rem;
  line-height: 1.4;
}
.agent-ddns-form__footer {
  display: flex;
  justify-content: flex-end;
  padding-top: 0.125rem;
}
.agent-ddns-form__save {
  min-width: 5.5rem;
}
</style>
