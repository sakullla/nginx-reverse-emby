<template>
  <div class="services-page">
    <header class="page-header">
      <div class="page-header__left">
        <h1 class="page-title">服务</h1>
        <p>添加、查看、修改和删除接入。未完成状态只展示控制面返回的结果。</p>
      </div>
      <div class="page-header__right">
        <button type="button" class="btn btn-primary" @click="startCreate">添加服务</button>
      </div>
    </header>

    <p v-if="loadError" class="services-page__error" role="alert">{{ loadError }}</p>
    <p v-if="actionError && !wizard" class="services-page__error" role="alert">{{ actionError }}</p>

    <ServiceWizard
      v-if="wizard"
      :key="wizard.session || wizard.service?.id || 'new'"
      :service="wizard.service"
      :agents="agents"
      :submitting="submitting"
      :form-error="actionError"
      @cancel="closeWizard"
      @submit="submitWizard"
    />

    <section v-if="selected && !wizard" class="service-detail" data-testid="service-detail">
      <button type="button" class="btn btn-secondary" @click="closeDetail">返回列表</button>
      <h2>{{ selected.name || '未命名服务' }}</h2>
      <p data-testid="detail-summary">{{ selected.summary || '控制面没有返回摘要' }}</p>
      <dl>
        <div><dt>访问地址</dt><dd>{{ selected.entry_url || '—' }}</dd></div>
        <div><dt>入口节点</dt><dd>{{ agentLabel(selected.entry_agent_id) }}</dd></div>
        <div><dt>服务侧节点</dt><dd>{{ agentLabel(selected.service_agent_id) }}</dd></div>
        <div><dt>路径</dt><dd>{{ pathText(selected.path) }}</dd></div>
        <div><dt>切换阶段</dt><dd>{{ selected.switch_phase || '无' }}</dd></div>
        <div><dt>未完成环节</dt><dd>{{ detailSteps.length ? detailSteps.join('；') : '无' }}</dd></div>
        <div><dt>证书</dt><dd>{{ detailCertificate.label }}<template v-if="detailCertificate.detail">：{{ detailCertificate.detail }}</template></dd></div>
        <div><dt>流量</dt><dd>{{ detailTraffic.text }}<template v-if="detailTraffic.detail"> · {{ detailTraffic.detail }}</template></dd></div>
        <div v-if="selected.apply"><dt>生效</dt><dd>{{ applyText }}</dd></div>
      </dl>

      <section>
        <h3>节点探测</h3>
        <p v-if="!detailProbes.length">控制面没有探测结果。节点在线或规则存在都不代表接入完成。</p>
        <ul>
          <li v-for="(probe, index) in detailProbes" :key="`${probe.agentId}-${index}`" data-testid="probe-row">
            {{ probe.agentId || '未知节点' }}
            <template v-if="probe.purpose"> · {{ probe.purpose }}</template>
            · {{ probe.text }}
            <template v-if="probe.target"> · {{ probe.target }}</template>
            <template v-if="probe.checkedAt"> · {{ probe.checkedAt }}</template>
            <template v-if="probe.error"> · {{ probe.error }}</template>
          </li>
        </ul>
      </section>

      <section data-testid="client-confirmation">
        <h3>客户端访问</h3>
        <p v-if="selected.client_confirmation?.confirmed">
          用户确认页面可打开
          <template v-if="selected.client_confirmation.confirmed_at"> · {{ selected.client_confirmation.confirmed_at }}</template>
        </p>
        <p v-else>客户端访问：未知</p>
        <p>用户确认与节点探测分开，不表示媒体播放通过。</p>
        <div class="services-page__actions">
          <a v-if="selectedHref" class="btn btn-secondary" :href="selectedHref" target="_blank" rel="noopener">打开</a>
          <button type="button" class="btn btn-secondary" @click="copyEntry(selected)">复制</button>
          <button v-if="!selected.client_confirmation?.confirmed" type="button" class="btn btn-secondary" :disabled="submitting" @click="confirmOpen">我确认页面可打开</button>
        </div>
      </section>

      <section v-if="selected.rules?.length">
        <h3>关联规则</h3>
        <ul>
          <li v-for="rule in selected.rules" :key="`${rule.agent_id}:${rule.id}`">
            {{ rule.frontend_url || selected.entry_url }}
            <template v-if="ruleBackendLabels(rule).length"> · {{ ruleBackendLabels(rule).join('，') }}</template>
            <a :href="advancedRuleHref(rule)">高级规则</a>
          </li>
        </ul>
      </section>

      <div class="services-page__actions">
        <button v-if="showRetry" type="button" class="btn btn-secondary" :disabled="submitting" @click="retrySelected">重试</button>
        <button v-if="selected.switch_phase === '退役旧入口'" type="button" class="btn btn-secondary" :disabled="submitting" @click="retireSelected">退役旧入口</button>
        <button v-if="selected.switch_phase === '准备新入口' || selected.switch_phase === '等待新入口生效'" type="button" class="btn btn-secondary" :disabled="submitting" @click="abandonSelected">放弃这次切换</button>
        <button type="button" class="btn btn-secondary" @click="startEdit(selected)">修改</button>
        <button type="button" class="btn btn-secondary" @click="startDelete(selected)">删除</button>
      </div>
    </section>

    <SkeletonList v-else-if="!wizard && loading" variant="cards" :count="3" />

    <EmptyState v-else-if="!wizard && !services.length" title="还没有服务" description="添加后，进度会留在这个列表里。">
      <template #action>
        <button type="button" class="btn btn-primary" @click="startCreate">添加服务</button>
      </template>
    </EmptyState>

    <div v-else-if="!selected" class="service-grid">
      <ServiceCard
        v-for="service in services"
        :key="service.id"
        :service="service"
        :entry-agent-label="agentLabel(service.entry_agent_id)"
        @copy="copyEntry"
        @detail="openDetail"
        @edit="startEdit"
        @delete="startDelete"
      />
    </div>

    <p v-if="copyNotice" role="status">{{ copyNotice }}</p>

    <div v-if="deleting" class="service-delete" role="alertdialog" aria-labelledby="service-delete-title" data-testid="service-delete">
      <h2 id="service-delete-title">删除服务</h2>
      <p>将停止的入口</p>
      <ul>
        <li v-for="(entry, index) in deleteImpact.stopped" :key="`${entry.rule_id}-${index}`">
          {{ entry.url || '未命名入口' }} · {{ agentLabel(entry.agent_id) }}
        </li>
        <li v-if="!deleteImpact.stopped.length">当前没有可停止的入口规则</li>
      </ul>
      <p>会保留的共享资源</p>
      <ul>
        <li v-for="(item, index) in deleteImpact.retained" :key="`${item.kind}-${index}`">
          {{ retainedKind(item.kind) }}<template v-if="item.id"> {{ item.id }}</template>：{{ item.reason }}
        </li>
      </ul>
      <p>节点尚未应用删除时，服务会留在列表并显示待生效。这不表示入口已经关闭。</p>
      <div class="services-page__actions">
        <button type="button" class="btn btn-secondary" @click="deleting = null">返回</button>
        <button type="button" class="btn btn-primary" :disabled="submitting" @click="confirmDelete">确认删除</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery, useQueryClient } from '@tanstack/vue-query'
import { useAgents } from '../hooks/useAgents'
import {
  abandonAccessService,
  confirmAccessServiceClient,
  correctAccessService,
  createAccessService,
  deleteAccessService,
  fetchAccessServices,
  retireAccessService,
  retryAccessService,
  updateAccessService,
} from '../api'
import ServiceCard from '../components/services/ServiceCard.vue'
import ServiceWizard from '../components/services/ServiceWizard.vue'
import EmptyState from '../components/base/EmptyState.vue'
import SkeletonList from '../components/base/SkeletonList.vue'
import {
  accessErrorMessage,
  accessErrorService,
  advancedRuleHref,
  certificateText,
  deletionImpact,
  errorRequestsRelayInput,
  forwardingChanged,
  incompleteSteps,
  simplifiedForwardingChanged,
  newIdempotencyKey,
  pathText,
  probeText,
  reuseIdempotencyKey,
  ruleBackendLabels,
  safeHttpHref,
  serviceTrafficText,
} from '../utils/accessService.js'

const route = useRoute()
const router = useRouter()
const queryClient = useQueryClient()
const { data: agentsData } = useAgents()
const agents = computed(() => (Array.isArray(agentsData.value) ? agentsData.value : []))

const servicesQuery = useQuery({
  queryKey: ['access-services'],
  queryFn: () => fetchAccessServices(),
})

const services = computed(() => (Array.isArray(servicesQuery.data.value) ? servicesQuery.data.value : []))
const loading = computed(() => servicesQuery.isLoading.value)
const loadError = computed(() => (servicesQuery.error.value ? accessErrorMessage(servicesQuery.error.value) : ''))

const wizard = ref(null)
const persistedId = ref('')
const deleting = ref(null)
const submitting = ref(false)
const actionError = ref('')
const copyNotice = ref('')
const idempotency = ref({})
let wizardSerial = 0

const selectedId = computed(() => String(route.query.id || ''))
const selected = computed(() => services.value.find((service) => service.id === selectedId.value) || null)
const detailSteps = computed(() => incompleteSteps(selected.value))
const detailCertificate = computed(() => certificateText(selected.value?.certificate))
const detailTraffic = computed(() => serviceTrafficText(selected.value?.traffic))
const detailProbes = computed(() => (Array.isArray(selected.value?.probes) ? selected.value.probes : []).map((probe) => probeText(probe)))
const selectedHref = computed(() => safeHttpHref(selected.value?.entry_url))
const relayHostMissing = computed(() => errorRequestsRelayInput(selected.value?.last_error))
const showRetry = computed(() => {
  if (!selected.value || relayHostMissing.value) return false
  return selected.value.summary === '失败' || selected.value.summary === '待确认'
})
const deleteImpact = computed(() => deletionImpact(deleting.value || {}))
const applyText = computed(() => {
  const apply = selected.value?.apply || {}
  const parts = []
  parts.push(apply.pending ? '待生效' : '控制面没有把生效标为待处理')
  if (apply.old_entry_may_receive) parts.push('旧入口仍可能接收新请求')
  if (apply.new_entry_applied) parts.push('新入口已应用')
  if (apply.listener_applied) parts.push('监听器已生效')
  if (apply.certificate_ready) parts.push('证书已就绪')
  return parts.join('；')
})

function agentLabel(id) {
  const agent = agents.value.find((item) => String(item.id) === String(id))
  return agent?.name || id || '—'
}

function retainedKind(kind) {
  if (kind === 'certificate') return '证书'
  if (kind === 'relay_listener') return '监听器'
  if (kind === 'agent') return '节点'
  return kind || '资源'
}

function rememberService(service) {
  if (!service?.id) return
  queryClient.setQueryData(['access-services'], (current) => {
    const list = Array.isArray(current) ? current.slice() : []
    const index = list.findIndex((item) => item.id === service.id)
    if (index >= 0) list.splice(index, 1, service)
    else list.unshift(service)
    return list
  })
}

function keyFor(operation, body) {
  const next = reuseIdempotencyKey(idempotency.value, operation, body)
  idempotency.value = next
  return next[operation].key
}

function dropKey(operation) {
  const next = { ...idempotency.value }
  delete next[operation]
  idempotency.value = next
}

function startCreate() {
  actionError.value = ''
  persistedId.value = ''
  deleting.value = null
  wizardSerial += 1
  wizard.value = { service: null, session: `create-${wizardSerial}` }
  if (route.query.id) router.replace({ query: {} })
}

function startEdit(service) {
  actionError.value = ''
  persistedId.value = service.id
  deleting.value = null
  wizardSerial += 1
  wizard.value = { service, session: `edit-${service.id}-${wizardSerial}` }
}

function holdRelayFollowUp(saved) {
  if (!errorRequestsRelayInput(saved?.last_error)) return false
  actionError.value = String(saved.last_error)
  persistedId.value = saved.id
  wizard.value = { session: wizard.value?.session, service: saved }
  return true
}

function closeWizard() {
  wizard.value = null
  actionError.value = ''
}

function openDetail(service) {
  wizard.value = null
  router.replace({ query: { id: service.id } })
}

function closeDetail() {
  router.replace({ query: {} })
}

function startDelete(service) {
  actionError.value = ''
  deleting.value = service
}

async function copyEntry(service) {
  const text = String(service?.entry_url || '')
  try {
    if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(text)
    copyNotice.value = text ? '已复制访问地址' : '没有可复制的访问地址'
  } catch {
    copyNotice.value = '复制失败'
  }
}

async function runMutation(work) {
  submitting.value = true
  actionError.value = ''
  try {
    const service = await work()
    rememberService(service)
    return service
  } catch (error) {
    const saved = accessErrorService(error)
    if (saved?.id) {
      rememberService(saved)
      persistedId.value = saved.id
    }
    actionError.value = accessErrorMessage(error)
    if (errorRequestsRelayInput(actionError.value)) {
      wizard.value = wizard.value || { service: saved }
    }
    return null
  } finally {
    submitting.value = false
  }
}

async function submitWizard({ payload, intent }) {
  const current = wizard.value?.service
  if (current?.advanced === true && (intent === 'correct' || simplifiedForwardingChanged(current, payload))) {
    actionError.value = current.advanced_reason
      ? `简化转发编辑已禁用。${current.advanced_reason}`
      : '简化转发编辑已禁用。请到高级规则页修改。'
    return
  }
  const serviceId = current?.id || persistedId.value
  if (!serviceId) {
    const operation = 'create'
    const key = keyFor(operation, payload)
    const created = await runMutation(() => createAccessService(payload, key))
    if (!created) return
    dropKey(operation)
    if (holdRelayFollowUp(created)) return
    wizard.value = null
    router.replace({ query: { id: created.id } })
    return
  }
  const nameOnly = current && !forwardingChanged(current, payload)
  const supplyingRelay = Boolean(errorRequestsRelayInput(current?.last_error)
    && (payload.relay_public_host || payload.relay_listen_port))
  const useCorrect = current?.advanced !== true && ((intent === 'correct' && !nameOnly) || supplyingRelay)
  const operation = `${useCorrect ? 'correct' : 'update'}:${serviceId}`
  const key = keyFor(operation, payload)
  const request = useCorrect
    ? correctAccessService(serviceId, payload, key)
    : updateAccessService(serviceId, payload, key)
  const saved = await runMutation(() => request)
  if (!saved) return
  dropKey(operation)
  if (holdRelayFollowUp(saved)) return
  wizard.value = null
  router.replace({ query: { id: saved.id } })
}

async function confirmDelete() {
  const service = deleting.value
  if (!service?.id) return
  const operation = `delete:${service.id}`
  const key = keyFor(operation, {})
  const saved = await runMutation(() => deleteAccessService(service.id, key))
  if (!saved) return
  dropKey(operation)
  deleting.value = null
}

async function confirmOpen() {
  if (!selected.value?.id) return
  const body = { confirmed: true }
  const operation = `confirm:${selected.value.id}`
  const key = keyFor(operation, body)
  const saved = await runMutation(() => confirmAccessServiceClient(selected.value.id, body, key))
  if (saved) dropKey(operation)
}

async function retrySelected() {
  if (!selected.value?.id) return
  if (errorRequestsRelayInput(selected.value.last_error)) {
    startEdit(selected.value)
    return
  }
  const operation = `retry:${selected.value.id}`
  const key = idempotency.value[operation]?.key || newIdempotencyKey()
  idempotency.value = { ...idempotency.value, [operation]: { key, body: '{}' } }
  const saved = await runMutation(() => retryAccessService(selected.value.id, key))
  if (saved) dropKey(operation)
}

async function retireSelected() {
  if (!selected.value?.id) return
  const operation = `retire:${selected.value.id}`
  const key = keyFor(operation, {})
  const saved = await runMutation(() => retireAccessService(selected.value.id, key))
  if (saved) dropKey(operation)
}

async function abandonSelected() {
  if (!selected.value?.id) return
  const operation = `abandon:${selected.value.id}`
  const key = keyFor(operation, {})
  const saved = await runMutation(() => abandonAccessService(selected.value.id, key))
  if (saved) dropKey(operation)
}
</script>

<style scoped>
.services-page__error {
  color: var(--color-danger, #b42318);
}

.service-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 22rem), 1fr));
  gap: 1rem;
}

.service-detail,
.service-delete {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  margin-bottom: 1rem;
  padding: 1rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg, 12px);
  background: var(--color-bg-panel, var(--color-bg-surface));
}

.service-detail dl,
.service-detail ul,
.service-delete ul {
  margin: 0;
}

.service-detail dl {
  display: grid;
  gap: 0.4rem;
}

.service-detail dl div {
  display: grid;
  grid-template-columns: 6.5rem minmax(0, 1fr);
  gap: 0.5rem;
}

.service-detail dd {
  margin: 0;
  overflow-wrap: anywhere;
}

.service-detail dt {
  color: var(--color-text-muted);
}

.services-page__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.45rem;
}
</style>
