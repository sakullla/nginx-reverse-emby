<template>
  <section class="service-wizard" data-testid="service-wizard">
    <header class="service-wizard__header">
      <div class="service-wizard__header-top">
        <div class="service-wizard__heading">
          <h2 class="service-wizard__title">{{ service ? '修改服务' : '添加服务' }}</h2>
          <p class="service-wizard__subtitle">Emby、Jellyfin 和普通 Web 填写同一个后端 URL，这里不收集媒体账号。</p>
        </div>
        <ol class="service-wizard__steps" aria-label="向导步骤">
          <li
            class="service-wizard__step"
            :class="{ 'service-wizard__step--active': step === 'edit', 'service-wizard__step--done': step === 'confirm' }"
            :aria-current="step === 'edit' ? 'step' : undefined"
          >
            <span class="service-wizard__step-num">1</span>
            <span>填写接入</span>
          </li>
          <li
            class="service-wizard__step"
            :class="{ 'service-wizard__step--active': step === 'confirm' }"
            :aria-current="step === 'confirm' ? 'step' : undefined"
          >
            <span class="service-wizard__step-num">2</span>
            <span>确认提交</span>
          </li>
        </ol>
      </div>
    </header>

    <form v-if="step === 'edit'" class="service-wizard__form" data-testid="service-form" @submit.prevent="goConfirm">
      <div class="service-wizard__section">
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
        <div class="service-wizard__grid">
          <div class="service-wizard__field">
            <label for="service-agent" class="service-wizard__label">
              服务侧节点
              <span class="service-wizard__required" aria-hidden="true">*</span>
            </label>
            <div class="service-wizard__select-wrap" :class="{ 'is-disabled': forwardingLocked }">
              <select
                id="service-agent"
                class="input-base service-wizard__select"
                :class="{ 'input-error': Boolean(errors.service_agent_id) }"
                :value="draft.service_agent_id"
                :disabled="forwardingLocked"
                :aria-invalid="errors.service_agent_id ? 'true' : undefined"
                :aria-describedby="errors.service_agent_id ? 'service-agent-error' : undefined"
                @change="updateField('service_agent_id', $event.target.value)"
              >
                <option value="">选择节点</option>
                <option v-if="missingOption(draft.service_agent_id)" :value="draft.service_agent_id">不存在的节点</option>
                <option v-for="agent in agents" :key="agent.id" :value="agent.id">{{ agent.name || agent.id }}</option>
              </select>
              <svg class="service-wizard__select-chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <polyline points="6 9 12 15 18 9" />
              </svg>
            </div>
            <FieldError v-if="errors.service_agent_id" id="service-agent-error">{{ errors.service_agent_id }}</FieldError>
          </div>

          <div class="service-wizard__field">
            <label for="entry-agent" class="service-wizard__label">
              入口节点
              <span class="service-wizard__required" aria-hidden="true">*</span>
            </label>
            <div class="service-wizard__select-wrap" :class="{ 'is-disabled': forwardingLocked }">
              <select
                id="entry-agent"
                class="input-base service-wizard__select"
                :class="{ 'input-error': Boolean(errors.entry_agent_id) }"
                :value="draft.entry_agent_id"
                :disabled="forwardingLocked"
                :aria-invalid="errors.entry_agent_id ? 'true' : undefined"
                :aria-describedby="errors.entry_agent_id ? 'entry-agent-error' : undefined"
                @change="updateField('entry_agent_id', $event.target.value)"
              >
                <option value="">选择节点</option>
                <option v-if="missingOption(draft.entry_agent_id)" :value="draft.entry_agent_id">不存在的节点</option>
                <option v-for="agent in agents" :key="`entry-${agent.id}`" :value="agent.id">{{ agent.name || agent.id }}</option>
              </select>
              <svg class="service-wizard__select-chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                <polyline points="6 9 12 15 18 9" />
              </svg>
            </div>
            <FieldError v-if="errors.entry_agent_id" id="entry-agent-error">{{ errors.entry_agent_id }}</FieldError>
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

      <div v-if="forwardingLocked" class="service-wizard__callout service-wizard__callout--warning" data-testid="advanced-backend">
        <p class="service-wizard__callout-title">简化转发编辑已禁用。{{ service.advanced_reason || '这条规则已是高级形态。' }}</p>
        <p class="service-wizard__callout-label">规则上的真实后端</p>
        <ul v-if="realBackends.length" class="service-wizard__backend-list">
          <li v-for="(label, index) in realBackends" :key="`${label}-${index}`">
            <code>{{ label }}</code>
          </li>
        </ul>
        <p v-else class="service-wizard__callout-text">控制面没有返回规则后端。</p>
        <div v-if="advancedLinks.length" class="service-wizard__callout-links">
          <a v-for="link in advancedLinks" :key="link.href" class="service-wizard__link" :href="link.href">在高级规则页查看</a>
        </div>
      </div>

      <div v-if="showRelay" class="service-wizard__callout service-wizard__callout--info service-wizard__relay">
        <p class="service-wizard__callout-text">控制面要求补充单跳监听器信息。已有共享监听器不会被修改，这里也不判断该走直连还是 Relay。</p>
        <div class="service-wizard__grid">
          <BaseInput
            id="relay-port"
            :model-value="String(draft.relay_listen_port ?? '')"
            label="服务侧监听端口"
            :error="errors.relay_listen_port"
            @update:model-value="updateField('relay_listen_port', $event)"
          />
          <BaseInput
            id="relay-host"
            :model-value="draft.relay_public_host"
            label="入口可拨号的主机"
            :error="errors.relay_public_host"
            @update:model-value="updateField('relay_public_host', $event)"
          />
        </div>
      </div>

      <div v-if="formError" class="service-wizard__error" role="alert">
        <svg class="service-wizard__error-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="9" />
          <path d="M12 8v4" />
          <path d="M12 16h.01" />
        </svg>
        <span>{{ formError }}</span>
      </div>

      <div class="service-wizard__actions">
        <button type="button" class="btn btn-secondary" @click="$emit('cancel')">取消</button>
        <button type="submit" class="btn btn-primary">继续确认</button>
      </div>
    </form>

    <div v-else class="service-wizard__confirm" data-testid="service-confirm">
      <dl class="service-wizard__review">
        <div class="service-wizard__review-row">
          <dt>名称</dt>
          <dd>{{ draft.name || '使用入口主机名' }}</dd>
        </div>
        <div class="service-wizard__review-row">
          <dt>后端 URL</dt>
          <dd class="service-wizard__mono">{{ draft.backend_url }}</dd>
        </div>
        <div class="service-wizard__review-row">
          <dt>服务侧节点</dt>
          <dd>{{ agentLabel(draft.service_agent_id) }}</dd>
        </div>
        <div class="service-wizard__review-row">
          <dt>入口节点</dt>
          <dd>{{ agentLabel(draft.entry_agent_id) }}</dd>
        </div>
        <div class="service-wizard__review-row">
          <dt>入口地址</dt>
          <dd class="service-wizard__mono">{{ draft.entry_url }}</dd>
        </div>
        <div v-if="draft.relay_listen_port" class="service-wizard__review-row">
          <dt>已填写的监听端口</dt>
          <dd class="service-wizard__mono">{{ draft.relay_listen_port }}</dd>
        </div>
        <div v-if="draft.relay_public_host" class="service-wizard__review-row">
          <dt>已填写的拨号主机</dt>
          <dd class="service-wizard__mono">{{ draft.relay_public_host }}</dd>
        </div>
      </dl>

      <p class="service-wizard__note">
        提交前不会创建或启用入口。路径由控制面在提交后返回，本页不事先判断直连或 Relay。
      </p>

      <div v-if="forwardingLocked" class="service-wizard__callout service-wizard__callout--warning" data-testid="advanced-confirm">
        <p class="service-wizard__callout-text">简化转发编辑已禁用。名称以外的转发改动请到高级规则页。</p>
        <div v-if="advancedLinks.length" class="service-wizard__callout-links">
          <a v-for="link in advancedLinks" :key="`confirm-${link.href}`" class="service-wizard__link" :href="link.href">在高级规则页查看</a>
        </div>
      </div>

      <div v-if="formError" class="service-wizard__error" role="alert">
        <svg class="service-wizard__error-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <circle cx="12" cy="12" r="9" />
          <path d="M12 8v4" />
          <path d="M12 16h.01" />
        </svg>
        <span>{{ formError }}</span>
      </div>

      <div class="service-wizard__actions">
        <button type="button" class="btn btn-secondary" data-testid="service-back" @click="step = 'edit'">返回</button>
        <button v-if="canCorrect" type="button" class="btn btn-secondary" :disabled="submitting" @click="submit('correct')">修正</button>
        <button type="button" class="btn btn-primary" :disabled="submitting" @click="submit('update')">{{ service ? '保存修改' : '提交' }}</button>
      </div>
    </div>
  </section>
</template>

<script setup>
import { computed, reactive, ref, watch } from 'vue'
import BaseInput from '../base/BaseInput.vue'
import FieldError from '../base/FieldError.vue'
import {
  advancedRuleHref,
  draftFromService,
  errorRequestsRelayInput,
  isNameOnlyChange,
  ruleBackendLabels,
  servicePayload,
  validateServiceDraft,
} from '../../utils/accessService.js'

const props = defineProps({
  service: { type: Object, default: null },
  agents: { type: Array, default: () => [] },
  submitting: { type: Boolean, default: false },
  formError: { type: String, default: '' },
})

const emit = defineEmits(['cancel', 'submit'])

const draft = reactive(draftFromService(props.service))
const errors = reactive({})
const step = ref('edit')
const relayForced = ref(false)

function relayInputRequested() {
  return errorRequestsRelayInput(props.formError) || errorRequestsRelayInput(props.service?.last_error)
}

function revealRelayInput() {
  if (!relayInputRequested()) return
  relayForced.value = true
  step.value = 'edit'
}

watch([() => props.formError, () => props.service?.last_error], revealRelayInput)
revealRelayInput()

const forwardingLocked = computed(() => props.service?.advanced === true)
const showRelay = computed(() => relayForced.value || String(draft.relay_listen_port || '').trim() !== '' || String(draft.relay_public_host || '').trim() !== '')
const realBackends = computed(() => (Array.isArray(props.service?.rules) ? props.service.rules : []).flatMap((rule) => ruleBackendLabels(rule)))
const advancedLinks = computed(() => (Array.isArray(props.service?.rules) ? props.service.rules : []).map((rule) => ({
  href: advancedRuleHref(rule),
})))
const canCorrect = computed(() => {
  if (!props.service || props.service.advanced === true) return false
  if (isNameOnlyChange(props.service, draft)) return false
  const summary = String(props.service.summary || '')
  return summary === '失败' || summary === '待确认' || Boolean(props.service.last_error)
})

function missingOption(id) {
  if (!id) return false
  return !props.agents.some((agent) => String(agent.id) === String(id))
}

function agentLabel(id) {
  const agent = props.agents.find((item) => String(item.id) === String(id))
  return agent?.name || id || '—'
}

function updateField(key, value) {
  draft[key] = value
  delete errors[key]
}

function goConfirm() {
  const nextErrors = validateServiceDraft(draft, props.agents)
  Object.keys(errors).forEach((key) => delete errors[key])
  Object.assign(errors, nextErrors)
  if (Object.keys(nextErrors).length) return
  step.value = 'confirm'
}

function submit(intent) {
  if (props.service?.advanced === true && intent === 'correct') return
  emit('submit', { payload: servicePayload(draft), intent: props.service ? intent : 'create' })
}
</script>

<style scoped>
.service-wizard {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  margin-bottom: var(--space-5);
  padding: var(--space-5);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-sm);
  min-width: 0;
}

.service-wizard__header {
  padding-bottom: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
}

.service-wizard__header-top {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}

.service-wizard__heading {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.service-wizard__title {
  margin: 0;
  font-size: var(--text-lg);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  line-height: 1.3;
}

.service-wizard__subtitle {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  line-height: 1.5;
}

.service-wizard__steps {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  list-style: none;
  margin: 0;
  padding: 0;
}

.service-wizard__step {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1-5);
  padding: var(--space-1) var(--space-2-5);
  border-radius: var(--radius-full);
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-subtle);
  color: var(--color-text-tertiary);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
}

.service-wizard__step--active {
  border-color: color-mix(in srgb, var(--color-primary) 30%, transparent);
  background: var(--color-primary-50);
  color: var(--color-primary);
}

.service-wizard__step--done {
  border-color: color-mix(in srgb, var(--color-success) 24%, transparent);
  background: var(--color-success-50);
  color: var(--color-success);
}

.service-wizard__step-num {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 1.125rem;
  height: 1.125rem;
  border-radius: var(--radius-full);
  background: color-mix(in srgb, currentColor 14%, transparent);
  font-size: var(--text-2xs);
  font-weight: var(--font-semibold);
}

.service-wizard__form,
.service-wizard__confirm,
.service-wizard__section {
  display: flex;
  flex-direction: column;
  gap: var(--space-3-5, 0.875rem);
  min-width: 0;
}

.service-wizard__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3-5, 0.875rem);
}

.service-wizard__field {
  display: flex;
  flex-direction: column;
  gap: var(--space-1-5);
  min-width: 0;
}

.service-wizard__label {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
}

.service-wizard__required {
  color: var(--color-danger);
}

.service-wizard__select-wrap {
  position: relative;
  display: flex;
  align-items: center;
  min-width: 0;
}

.service-wizard__select {
  appearance: none;
  -webkit-appearance: none;
  width: 100%;
  max-width: 100%;
  min-width: 0;
  padding-right: 2.25rem;
  box-shadow: var(--shadow-xs);
  cursor: pointer;
}

.service-wizard__select:disabled {
  opacity: 0.48;
  cursor: not-allowed;
  background: var(--color-bg-subtle);
}

.service-wizard__select-chevron {
  position: absolute;
  right: var(--space-3);
  color: var(--color-text-muted);
  pointer-events: none;
}

.service-wizard__select-wrap.is-disabled .service-wizard__select-chevron {
  opacity: 0.48;
}

.service-wizard__callout {
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

.service-wizard__callout--warning {
  border-color: color-mix(in srgb, var(--color-warning) 28%, var(--color-border-default));
  background: var(--color-warning-50);
}

.service-wizard__callout--info {
  border-color: color-mix(in srgb, var(--color-primary) 24%, var(--color-border-default));
  background: var(--color-primary-50);
}

.service-wizard__callout-title {
  margin: 0;
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
}

.service-wizard__callout-label {
  margin: 0;
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  color: var(--color-text-tertiary);
}

.service-wizard__callout-text {
  margin: 0;
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.service-wizard__backend-list {
  margin: 0;
  padding-left: 1.15rem;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.service-wizard__backend-list code {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--color-text-primary);
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-wizard__callout-links {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.service-wizard__link {
  color: var(--color-primary);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  text-decoration: none;
}

.service-wizard__link:hover {
  text-decoration: underline;
}

.service-wizard__review {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding: var(--space-4);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
}

.service-wizard__review-row {
  display: grid;
  grid-template-columns: 8.5rem minmax(0, 1fr);
  align-items: baseline;
  gap: var(--space-2);
  font-size: var(--text-sm);
}

.service-wizard__review-row dt {
  color: var(--color-text-muted);
  font-weight: var(--font-medium);
}

.service-wizard__review-row dd {
  margin: 0;
  min-width: 0;
  color: var(--color-text-primary);
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-wizard__mono {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
}

.service-wizard__note {
  margin: 0;
  padding: var(--space-3) var(--space-3-5, 0.875rem);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  line-height: 1.55;
}

.service-wizard__error {
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

.service-wizard__error-icon {
  flex-shrink: 0;
  margin-top: 2px;
}

.service-wizard__actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-2);
  padding-top: var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
}

@media (max-width: 720px) {
  .service-wizard__grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .service-wizard {
    padding: var(--space-4);
  }

  .service-wizard__review {
    padding: var(--space-3);
  }

  .service-wizard__review-row {
    grid-template-columns: 1fr;
    gap: var(--space-0-5);
  }

  .service-wizard__actions {
    justify-content: stretch;
  }

  .service-wizard__actions > .btn {
    flex: 1 1 auto;
  }
}
</style>
