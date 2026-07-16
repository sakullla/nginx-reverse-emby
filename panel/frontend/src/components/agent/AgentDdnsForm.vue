<template>
  <div class="agent-ddns-form">
    <!-- 当前状态:打开弹窗即可对照解析结果;未配置时收起为一行空态 -->
    <div class="agent-ddns-form__status" data-testid="agent-ddns-form-status">
      <template v-if="hasStatusInfo">
        <div class="agent-ddns-form__status-head">
          <span class="agent-ddns-form__status-dot" :data-tone="statusBadge.tone" aria-hidden="true" />
          <span class="agent-ddns-form__status-badge" data-testid="agent-ddns-form-status-badge">{{ statusBadge.label }}</span>
          <span class="agent-ddns-form__status-domain" data-testid="agent-ddns-form-status-domain">{{ activeDomain || '—' }}</span>
          <span v-if="lastSuccessText !== '—'" class="agent-ddns-form__status-updated" data-testid="agent-ddns-form-status-updated">最近更新 {{ lastSuccessText }}</span>
        </div>
        <div v-if="status?.last_resolved_ipv4 || status?.last_resolved_ipv6" class="agent-ddns-form__status-ips">
          <span v-if="status?.last_resolved_ipv4" class="agent-ddns-form__status-ip" data-testid="agent-ddns-form-status-ipv4">IPv4 {{ status.last_resolved_ipv4 }}</span>
          <span v-if="status?.last_resolved_ipv6" class="agent-ddns-form__status-ip" data-testid="agent-ddns-form-status-ipv6">IPv6 {{ status.last_resolved_ipv6 }}</span>
        </div>
        <p v-if="status?.last_error" class="agent-ddns-form__status-error" data-testid="agent-ddns-form-status-error">{{ status.last_error }}</p>
      </template>
      <p v-else class="agent-ddns-form__status-empty" data-testid="agent-ddns-form-status-empty">尚未配置 DDNS，开启下方开关并填写域名后开始解析</p>
    </div>

    <!-- 总开关:关闭即停止解析更新,子配置保留、重开即恢复 -->
    <label class="agent-ddns-form__switch">
      <input
        :checked="modelValue.enabled"
        class="agent-ddns-form__checkbox"
        type="checkbox"
        data-testid="agent-ddns-form-enabled"
        @change="updateField('enabled', $event.target.checked)"
      >
      <span class="agent-ddns-form__switch-label">启用 DDNS</span>
      <span class="agent-ddns-form__switch-hint">关闭后停止解析更新，子配置保留</span>
    </label>

    <fieldset class="agent-ddns-form__config" :disabled="!modelValue.enabled" data-testid="agent-ddns-form-config">
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
        <span v-if="domainMissing" class="agent-ddns-form__hint" data-testid="agent-ddns-form-domain-required">
          启用 IPv4 或 IPv6 时需填写域名
        </span>
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
    </fieldset>

    <div class="agent-ddns-form__footer">
      <button
        class="btn btn-primary agent-ddns-form__save"
        type="button"
        :disabled="saving || !canSave"
        data-testid="agent-ddns-form-save"
        @click="$emit('save')"
      >{{ saving ? '保存中…' : '保存' }}</button>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { ddnsStatusBadge } from '../../constants/agentDetailLabels'

const props = defineProps({
  modelValue: { type: Object, required: true },
  saving: { type: Boolean, default: false },
  // status is the agent's ddns_status runtime payload (ok | error | disabled |
  // idle + last resolved IPs / last success / last error). activeDomain is the
  // currently dispatched ddns_domain — the form's editable domain may diverge
  // while the user is editing.
  status: { type: Object, default: null },
  activeDomain: { type: String, default: '' }
})

const emit = defineEmits(['update:modelValue', 'save'])

const families = [
  { key: 'ipv4', label: 'IPv4' },
  { key: 'ipv6', label: 'IPv6' }
]

const anyEnabled = computed(() => !!(props.modelValue.ipv4?.enabled || props.modelValue.ipv6?.enabled))
// The domain requirement applies only while the master switch is on; a
// switched-off config saves verbatim so the sub-config survives for re-enable.
const domainMissing = computed(() => !!props.modelValue.enabled && anyEnabled.value && !String(props.modelValue.domain || '').trim())
const canSave = computed(() => !domainMissing.value)

const statusBadge = computed(() => ddnsStatusBadge(props.status?.status))
const hasStatusInfo = computed(() => !!(String(props.activeDomain || '').trim() || String(props.status?.status || '').trim()))
const lastSuccessText = computed(() => {
  const unix = Number(props.status?.last_success_at_unix)
  if (!unix) return '—'
  return new Date(unix * 1000).toLocaleString()
})

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
.agent-ddns-form__status {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  padding: 0.55rem 0.75rem;
  border: 1px solid color-mix(in srgb, var(--color-border-default) 90%, transparent);
  border-radius: var(--radius-lg);
  background: color-mix(in srgb, var(--color-bg-subtle) 45%, var(--color-bg-surface));
}
.agent-ddns-form__status-head {
  display: flex;
  align-items: baseline;
  gap: 0.5rem;
  min-width: 0;
}
.agent-ddns-form__status-badge {
  color: var(--color-text-primary);
  font-size: 0.8125rem;
  font-weight: 600;
  line-height: 1.4;
}
.agent-ddns-form__status-domain {
  flex: 1;
  min-width: 0;
  color: var(--color-text-secondary);
  font-family: var(--font-mono);
  font-size: 0.8125rem;
  line-height: 1.4;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.agent-ddns-form__status-updated {
  flex: 0 0 auto;
  color: var(--color-text-muted);
  font-size: 0.75rem;
  line-height: 1.4;
}
.agent-ddns-form__status-ips {
  display: flex;
  align-items: baseline;
  gap: 0.75rem;
  flex-wrap: wrap;
}
.agent-ddns-form__status-ip {
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  font-size: 0.75rem;
  line-height: 1.4;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.agent-ddns-form__status-error {
  margin: 0;
  color: var(--color-danger, #dc2626);
  font-size: 0.75rem;
  line-height: 1.4;
  word-break: break-all;
}
.agent-ddns-form__status-empty {
  margin: 0;
  color: var(--color-text-muted);
  font-size: 0.75rem;
  line-height: 1.45;
}
.agent-ddns-form__status-dot {
  display: inline-block;
  width: 0.5rem;
  height: 0.5rem;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--color-text-muted);
  transform: translateY(-0.0625rem);
}
.agent-ddns-form__status-dot[data-tone='success'] { background: var(--color-success); }
.agent-ddns-form__status-dot[data-tone='danger'] { background: var(--color-danger); }
.agent-ddns-form__status-dot[data-tone='warning'] { background: var(--color-warning); }
.agent-ddns-form__status-dot[data-tone='neutral'] { background: var(--color-text-muted); }
.agent-ddns-form__switch {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  min-height: 2.25rem;
}
.agent-ddns-form__switch-label {
  color: var(--color-text-primary);
  font-size: 0.875rem;
  font-weight: 600;
}
.agent-ddns-form__switch-hint {
  color: var(--color-text-muted);
  font-size: 0.75rem;
  line-height: 1.4;
}
.agent-ddns-form__config {
  display: flex;
  flex-direction: column;
  gap: 0.85rem;
  min-width: 0;
  margin: 0;
  padding: 0;
  border: none;
}
.agent-ddns-form__config:disabled {
  opacity: 0.55;
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
