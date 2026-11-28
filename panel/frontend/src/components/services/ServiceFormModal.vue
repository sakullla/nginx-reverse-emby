<template>
  <BaseModal
    :model-value="modelValue"
    :title="service ? '修改服务' : '添加服务'"
    subtitle="Emby、Jellyfin 和普通 Web 填写同一个后端 URL，不收集媒体账号。"
    size="lg"
    show-footer
    :close-on-click-modal="!submitting"
    data-test="service-form-modal"
    @update:model-value="handleModalUpdate"
  >
    <form
      :id="formId"
      class="service-form"
      data-testid="service-form"
      novalidate
      @submit.prevent="handleSubmit"
    >
      <div class="service-form__section">
        <BaseInput
          id="service-name"
          :model-value="draft.name"
          label="名称"
          placeholder="可留空，使用入口主机名"
          @update:model-value="updateField('name', $event)"
        />

        <BaseInput
          id="service-backend-url"
          :model-value="draft.backend_url"
          label="后端 URL"
          placeholder="http://127.0.0.1:8096"
          :disabled="forwardingLocked"
          :error="errors.backend_url"
          required
          @update:model-value="updateField('backend_url', $event)"
        />

        <div class="service-form__grid">
          <div class="service-form__field">
            <div class="service-form__label-row">
              <label for="service-agent" class="service-form__label">
                服务侧节点
                <span class="service-form__required" aria-hidden="true">*</span>
              </label>
            </div>
            <AgentSearchSelect
              id="service-agent"
              :model-value="draft.service_agent_id"
              :agents="agents"
              :show-all-option="false"
              placeholder="选择节点"
              missing-label="不存在的节点"
              :disabled="forwardingLocked"
              :invalid="Boolean(errors.service_agent_id)"
              :aria-describedby="errors.service_agent_id ? 'service-agent-error' : ''"
              @update:model-value="updateField('service_agent_id', $event)"
            />
            <FieldError v-if="errors.service_agent_id" id="service-agent-error">
              {{ errors.service_agent_id }}
            </FieldError>
          </div>

          <div class="service-form__field">
            <div class="service-form__label-row">
              <label for="entry-agent" class="service-form__label">
                入口节点
                <span class="service-form__required" aria-hidden="true">*</span>
              </label>
              <button
                v-if="!forwardingLocked"
                type="button"
                class="service-form__sync-btn"
                data-testid="sync-entry-agent"
                :disabled="!canSyncEntryAgent"
                @click="syncEntryToServiceAgent"
              >
                同服务侧节点
              </button>
            </div>
            <AgentSearchSelect
              id="entry-agent"
              :model-value="draft.entry_agent_id"
              :agents="agents"
              :show-all-option="false"
              placeholder="选择节点"
              missing-label="不存在的节点"
              :disabled="forwardingLocked"
              :invalid="Boolean(errors.entry_agent_id)"
              :aria-describedby="errors.entry_agent_id ? 'entry-agent-error' : ''"
              @update:model-value="updateField('entry_agent_id', $event)"
            />
            <FieldError v-if="errors.entry_agent_id" id="entry-agent-error">
              {{ errors.entry_agent_id }}
            </FieldError>
          </div>
        </div>

        <BaseInput
          id="service-entry-url"
          :model-value="draft.entry_url"
          label="入口地址"
          placeholder="https://media.example.com"
          :disabled="forwardingLocked"
          :error="errors.entry_url"
          required
          @update:model-value="updateField('entry_url', $event)"
        />
      </div>

      <div
        v-if="forwardingLocked"
        class="service-form__callout service-form__callout--warning"
        data-testid="advanced-backend"
      >
        <p class="service-form__callout-title">
          简化转发编辑已禁用。{{ service?.advanced_reason || '这条规则已是高级形态。' }}
        </p>
        <p class="service-form__callout-label">规则上的真实后端</p>
        <ul v-if="realBackends.length" class="service-form__backend-list">
          <li v-for="(label, index) in realBackends" :key="`${label}-${index}`">
            <code>{{ label }}</code>
          </li>
        </ul>
        <p v-else class="service-form__callout-text">控制面没有返回规则后端。</p>
        <div v-if="advancedLinks.length" class="service-form__callout-links">
          <a
            v-for="link in advancedLinks"
            :key="link.href"
            class="service-form__link"
            :href="link.href"
          >
            在高级规则页查看
          </a>
        </div>
      </div>

      <Transition name="service-form-reveal">
        <div
          v-if="showRelay"
          class="service-form__callout service-form__callout--info service-form__relay"
          data-testid="relay-input-section"
        >
          <p class="service-form__callout-text">
            控制面要求补充单跳监听器信息。已有共享监听器不会被修改，这里也不判断该走直连还是 Relay。
          </p>
          <div class="service-form__grid">
            <BaseInput
              id="relay-port"
              :model-value="String(draft.relay_listen_port ?? '')"
              label="服务侧监听端口"
              placeholder="例如 9443"
              :error="errors.relay_listen_port"
              @update:model-value="updateField('relay_listen_port', $event)"
            />
            <BaseInput
              id="relay-host"
              :model-value="draft.relay_public_host"
              label="入口可拨号的主机"
              placeholder="域名或公网 IP"
              :error="errors.relay_public_host"
              @update:model-value="updateField('relay_public_host', $event)"
            />
          </div>
        </div>
      </Transition>

      <p class="service-form__note">
        提交前不会创建或启用入口，路径由控制面在提交后返回，这里不事先判断直连或 Relay。
      </p>

      <div v-if="formError" class="service-form__error" role="alert" data-testid="service-form-error">
        <svg
          class="service-form__error-icon"
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="2"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <circle cx="12" cy="12" r="9" />
          <path d="M12 8v4" />
          <path d="M12 16h.01" />
        </svg>
        <span>{{ formError }}</span>
      </div>

      <button
        type="submit"
        class="service-form__hidden-submit"
        tabindex="-1"
        aria-hidden="true"
        :disabled="submitting"
      >
        {{ submitLabel }}
      </button>
    </form>

    <template #footer>
      <div class="service-form__footer">
        <button
          type="button"
          class="btn btn-secondary"
          :disabled="submitting"
          @click="$emit('cancel')"
        >
          取消
        </button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="submitting"
          data-testid="service-form-submit"
          @click="handleSubmit"
        >
          {{ submitLabel }}
        </button>
      </div>
    </template>
  </BaseModal>
</template>

<script setup>
import { computed, reactive, ref, useId, watch } from 'vue'
import BaseModal from '../base/BaseModal.vue'
import BaseInput from '../base/BaseInput.vue'
import FieldError from '../base/FieldError.vue'
import AgentSearchSelect from '../common/AgentSearchSelect.vue'
import {
  advancedRuleHref,
  draftFromService,
  errorRequestsRelayInput,
  ruleBackendLabels,
  servicePayload,
  validateServiceDraft,
} from '../../utils/accessService.js'

const props = defineProps({
  modelValue: { type: Boolean, default: false },
  sessionKey: { type: String, default: '' },
  service: { type: Object, default: null },
  agents: { type: Array, default: () => [] },
  submitting: { type: Boolean, default: false },
  formError: { type: String, default: '' },
})

const emit = defineEmits(['cancel', 'submit'])

const formId = `service-form-${useId()}`
const draft = reactive(draftFromService(props.service))
const errors = reactive({})
const relayForced = ref(false)

function clearErrors() {
  Object.keys(errors).forEach((key) => delete errors[key])
}

function relayInputRequested() {
  return errorRequestsRelayInput(props.formError) || errorRequestsRelayInput(props.service?.last_error)
}

function revealRelayInput() {
  if (relayInputRequested()) {
    relayForced.value = true
  }
}

function resetFromProps() {
  Object.assign(draft, draftFromService(props.service))
  clearErrors()
  relayForced.value = false
  revealRelayInput()
}

watch(
  [() => props.modelValue, () => props.sessionKey],
  ([open]) => {
    if (open) {
      resetFromProps()
    }
  },
  { immediate: true },
)

watch([() => props.formError, () => props.service?.last_error], revealRelayInput)

const forwardingLocked = computed(() => props.service?.advanced === true)
const canSyncEntryAgent = computed(() => (
  !forwardingLocked.value
  && Boolean(draft.service_agent_id)
  && String(draft.entry_agent_id || '') !== String(draft.service_agent_id)
))
const showRelay = computed(() => (
  relayForced.value
  || String(draft.relay_listen_port ?? '').trim() !== ''
  || String(draft.relay_public_host || '').trim() !== ''
))
const realBackends = computed(() => (
  (Array.isArray(props.service?.rules) ? props.service.rules : []).flatMap((rule) => ruleBackendLabels(rule))
))
const advancedLinks = computed(() => (
  (Array.isArray(props.service?.rules) ? props.service.rules : []).map((rule) => ({
    href: advancedRuleHref(rule),
  }))
))
const submitLabel = computed(() => {
  if (props.submitting) return props.service ? '保存中...' : '提交中...'
  return props.service ? '保存修改' : '添加服务'
})

function updateField(key, value) {
  draft[key] = value
  delete errors[key]
}

function syncEntryToServiceAgent() {
  if (!canSyncEntryAgent.value) return
  updateField('entry_agent_id', draft.service_agent_id)
}

function handleModalUpdate(nextOpen) {
  if (!nextOpen && !props.submitting) {
    emit('cancel')
  }
}

function handleSubmit() {
  if (props.submitting) return
  clearErrors()
  if (!forwardingLocked.value) {
    const nextErrors = validateServiceDraft(draft, props.agents)
    Object.assign(errors, nextErrors)
    if (Object.keys(nextErrors).length) return
  }
  emit('submit', { payload: servicePayload(draft) })
}
</script>

<style scoped>
.service-form,
.service-form__section {
  display: flex;
  flex-direction: column;
  gap: var(--space-3-5, 0.875rem);
  min-width: 0;
}

.service-form__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3-5, 0.875rem);
}

.service-form__field {
  display: flex;
  flex-direction: column;
  gap: var(--space-1-5);
  min-width: 0;
}

.service-form__label-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  min-height: 1.25rem;
}

.service-form__label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
}

.service-form__required {
  color: var(--color-danger);
}

.service-form__sync-btn {
  padding: 0;
  border: none;
  background: transparent;
  color: var(--color-primary);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  font-family: inherit;
  cursor: pointer;
  white-space: nowrap;
  transition: color var(--duration-fast) var(--ease-default), opacity var(--duration-fast) var(--ease-default);
}

.service-form__sync-btn:hover:not(:disabled) {
  text-decoration: underline;
}

.service-form__sync-btn:focus-visible {
  outline: none;
  border-radius: var(--radius-sm);
  box-shadow: var(--shadow-focus);
}

.service-form__sync-btn:disabled {
  color: var(--color-text-muted);
  opacity: 0.55;
  cursor: not-allowed;
}

.service-form__callout {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3-5, 0.875rem) var(--space-4);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-subtle);
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  min-width: 0;
}

.service-form__callout--warning {
  border-color: color-mix(in srgb, var(--color-warning) 28%, var(--color-border-default));
  background: var(--color-warning-50);
}

.service-form__callout--info {
  border-color: color-mix(in srgb, var(--color-primary) 24%, var(--color-border-default));
  background: var(--color-primary-50);
}

.service-form__callout-title {
  margin: 0;
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  overflow-wrap: anywhere;
}

.service-form__callout-label {
  margin: 0;
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  color: var(--color-text-tertiary);
}

.service-form__callout-text {
  margin: 0;
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.service-form__backend-list {
  margin: 0;
  padding-left: 1.15rem;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.service-form__backend-list code {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--color-text-primary);
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-form__callout-links {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.service-form__link {
  color: var(--color-primary);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  text-decoration: none;
}

.service-form__link:hover {
  text-decoration: underline;
}

.service-form__note {
  margin: 0;
  padding: var(--space-2-5) var(--space-3-5, 0.875rem);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
  color: var(--color-text-tertiary);
  font-size: var(--text-xs);
  line-height: 1.55;
}

.service-form__error {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-2-5) var(--space-3);
  border-radius: var(--radius-md);
  border: 1px solid color-mix(in srgb, var(--color-danger) 28%, transparent);
  background: var(--color-danger-50);
  color: var(--color-danger);
  font-size: var(--text-sm);
  line-height: 1.45;
  overflow-wrap: anywhere;
}

.service-form__error-icon {
  flex-shrink: 0;
  margin-top: 2px;
}

.service-form__hidden-submit {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
}

.service-form__footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-2);
  width: 100%;
}

.service-form-reveal-enter-active,
.service-form-reveal-leave-active {
  transition: opacity var(--duration-normal) var(--ease-default),
              transform var(--duration-normal) var(--ease-default);
}

.service-form-reveal-enter-from,
.service-form-reveal-leave-to {
  opacity: 0;
  transform: translateY(-4px);
}

@media (max-width: 640px) {
  .service-form__grid {
    grid-template-columns: 1fr;
  }

  .service-form__footer {
    justify-content: stretch;
  }

  .service-form__footer > .btn {
    flex: 1 1 auto;
  }
}

@media (prefers-reduced-motion: reduce) {
  .service-form-reveal-enter-active,
  .service-form-reveal-leave-active {
    transition: none;
  }
}
</style>
