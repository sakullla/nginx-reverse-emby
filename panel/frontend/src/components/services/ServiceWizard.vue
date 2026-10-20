<template>
  <section class="service-wizard" data-testid="service-wizard">
    <header class="service-wizard__header">
      <h2>{{ service ? '修改服务' : '添加服务' }}</h2>
      <p>Emby、Jellyfin 和普通 Web 填写同一个后端 URL，这里不收集媒体账号。</p>
    </header>

    <form v-if="step === 'edit'" data-testid="service-form" @submit.prevent="goConfirm">
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
      <label class="service-wizard__select">
        <span>服务侧节点</span>
        <select
          id="service-agent"
          :value="draft.service_agent_id"
          :disabled="forwardingLocked"
          :aria-invalid="errors.service_agent_id ? 'true' : undefined"
          @change="updateField('service_agent_id', $event.target.value)"
        >
          <option value="">选择节点</option>
          <option v-if="missingOption(draft.service_agent_id)" :value="draft.service_agent_id">不存在的节点</option>
          <option v-for="agent in agents" :key="agent.id" :value="agent.id">{{ agent.name || agent.id }}</option>
        </select>
        <FieldError v-if="errors.service_agent_id">{{ errors.service_agent_id }}</FieldError>
      </label>
      <label class="service-wizard__select">
        <span>入口节点</span>
        <select
          id="entry-agent"
          :value="draft.entry_agent_id"
          :disabled="forwardingLocked"
          :aria-invalid="errors.entry_agent_id ? 'true' : undefined"
          @change="updateField('entry_agent_id', $event.target.value)"
        >
          <option value="">选择节点</option>
          <option v-if="missingOption(draft.entry_agent_id)" :value="draft.entry_agent_id">不存在的节点</option>
          <option v-for="agent in agents" :key="`entry-${agent.id}`" :value="agent.id">{{ agent.name || agent.id }}</option>
        </select>
        <FieldError v-if="errors.entry_agent_id">{{ errors.entry_agent_id }}</FieldError>
      </label>
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

      <div v-if="forwardingLocked" class="service-wizard__advanced" data-testid="advanced-backend">
        <p>简化转发编辑已禁用。{{ service.advanced_reason || '这条规则已是高级形态。' }}</p>
        <p>规则上的真实后端</p>
        <ul>
          <li v-for="(label, index) in realBackends" :key="`${label}-${index}`">{{ label }}</li>
        </ul>
        <p v-if="!realBackends.length">控制面没有返回规则后端。</p>
        <a v-for="link in advancedLinks" :key="link.href" :href="link.href">在高级规则页查看</a>
      </div>

      <div v-if="showRelay" class="service-wizard__relay">
        <p>控制面要求补充单跳监听器信息。已有共享监听器不会被修改，这里也不判断该走直连还是 Relay。</p>
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

      <p v-if="formError" class="service-wizard__error" role="alert">{{ formError }}</p>
      <div class="service-wizard__actions">
        <button type="button" class="btn btn-secondary" @click="$emit('cancel')">取消</button>
        <button type="submit" class="btn btn-primary">继续确认</button>
      </div>
    </form>

    <div v-else data-testid="service-confirm">
      <dl class="service-wizard__review">
        <div><dt>名称</dt><dd>{{ draft.name || '使用入口主机名' }}</dd></div>
        <div><dt>后端 URL</dt><dd>{{ draft.backend_url }}</dd></div>
        <div><dt>服务侧节点</dt><dd>{{ agentLabel(draft.service_agent_id) }}</dd></div>
        <div><dt>入口节点</dt><dd>{{ agentLabel(draft.entry_agent_id) }}</dd></div>
        <div><dt>入口地址</dt><dd>{{ draft.entry_url }}</dd></div>
        <div v-if="draft.relay_listen_port"><dt>已填写的监听端口</dt><dd>{{ draft.relay_listen_port }}</dd></div>
        <div v-if="draft.relay_public_host"><dt>已填写的拨号主机</dt><dd>{{ draft.relay_public_host }}</dd></div>
      </dl>
      <p>提交前不会创建或启用入口。路径由控制面在提交后返回，本页不事先判断直连或 Relay。</p>
      <div v-if="forwardingLocked" data-testid="advanced-confirm">
        <p>简化转发编辑已禁用。名称以外的转发改动请到高级规则页。</p>
        <a v-for="link in advancedLinks" :key="`confirm-${link.href}`" :href="link.href">在高级规则页查看</a>
      </div>
      <p v-if="formError" class="service-wizard__error" role="alert">{{ formError }}</p>
      <div class="service-wizard__actions">
        <button type="button" class="btn btn-secondary" data-testid="service-back" @click="step = 'edit'">返回</button>
        <button type="button" class="btn btn-primary" :disabled="submitting" @click="submit('update')">{{ service ? '保存修改' : '提交' }}</button>
        <button v-if="canCorrect" type="button" class="btn btn-secondary" :disabled="submitting" @click="submit('correct')">修正</button>
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
  gap: 0.75rem;
  margin-bottom: 1.25rem;
  padding: 1rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg, 12px);
  background: var(--color-bg-panel, var(--color-bg-surface));
}

.service-wizard__header h2 {
  margin: 0;
  font-size: 1.05rem;
}

.service-wizard__header p,
.service-wizard__advanced,
.service-wizard__relay {
  color: var(--color-text-secondary);
  font-size: 0.875rem;
}

.service-wizard form,
.service-wizard__review {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.service-wizard__select {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  font-size: 0.875rem;
}

.service-wizard__select select {
  min-height: 2.25rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md, 8px);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  padding: 0.35rem 0.5rem;
}

.service-wizard__review {
  margin: 0;
}

.service-wizard__review div {
  display: grid;
  grid-template-columns: 8rem minmax(0, 1fr);
  gap: 0.5rem;
}

.service-wizard__review dt {
  color: var(--color-text-muted);
}

.service-wizard__review dd {
  margin: 0;
  overflow-wrap: anywhere;
}

.service-wizard__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.5rem;
}

.service-wizard__error {
  color: var(--color-danger, #b42318);
}

.service-wizard__advanced ul {
  margin: 0.25rem 0;
  padding-left: 1.1rem;
}
</style>
