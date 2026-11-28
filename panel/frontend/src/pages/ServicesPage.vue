<template>
  <div class="services-page">
    <header class="page-header">
      <div class="page-header__left">
        <h1 class="page-title">服务</h1>
        <p class="page-subtitle">添加、查看、修改和删除接入。未完成状态只展示控制面返回的结果。</p>
      </div>
      <div class="page-header__right">
        <button type="button" class="btn btn-primary" @click="startCreate">添加服务</button>
      </div>
    </header>

    <div v-if="loadError && (services.length || wizard || selected)" class="services-page__alert" role="alert">
      <svg class="services-page__alert-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="9" />
        <path d="M12 8v4" />
        <path d="M12 16h.01" />
      </svg>
      <span class="services-page__alert-text">{{ loadError }}</span>
      <button type="button" class="btn btn-secondary btn-sm" :disabled="retryingLoad" @click="retryLoad">重试</button>
    </div>

    <div v-if="actionError && !wizard" class="services-page__alert" role="alert">
      <svg class="services-page__alert-icon" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <circle cx="12" cy="12" r="9" />
        <path d="M12 8v4" />
        <path d="M12 16h.01" />
      </svg>
      <span class="services-page__alert-text">{{ actionError }}</span>
    </div>

    <p v-if="copyNotice" class="services-page__notice" role="status">{{ copyNotice }}</p>

    <div v-if="deleting" class="service-delete" role="alertdialog" aria-labelledby="service-delete-title" data-testid="service-delete">
      <header class="service-delete__header">
        <div class="service-delete__title-row">
          <svg class="service-delete__icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
            <line x1="12" y1="9" x2="12" y2="13" />
            <line x1="12" y1="17" x2="12.01" y2="17" />
          </svg>
          <h2 id="service-delete-title" class="service-delete__title">删除服务</h2>
          <BaseBadge
            v-if="deleting.name || deleting.entry_url"
            class="service-delete__target-badge"
            tone="danger"
            size="sm"
          >
            {{ deleting.name || deleting.entry_url }}
          </BaseBadge>
        </div>
      </header>

      <div class="service-delete__body">
        <div class="service-delete__columns">
          <div class="service-delete__panel">
            <p class="service-delete__panel-title">将停止的入口</p>
            <ul class="service-delete__list">
              <li v-for="(entry, index) in deleteImpact.stopped" :key="`${entry.rule_id}-${index}`" class="service-delete__item">
                <code class="services-page__mono">{{ entry.url || '未命名入口' }}</code>
                <span class="service-delete__sep"> · </span>
                <span>{{ agentLabel(entry.agent_id) }}</span>
              </li>
              <li v-if="!deleteImpact.stopped.length" class="service-delete__item service-delete__item--muted">
                当前没有可停止的入口规则
              </li>
            </ul>
          </div>

          <div class="service-delete__panel">
            <p class="service-delete__panel-title">会保留的共享资源</p>
            <ul class="service-delete__list">
              <li v-for="(item, index) in deleteImpact.retained" :key="`${item.kind}-${index}`" class="service-delete__item">
                <BaseBadge tone="neutral" subtone="secondary" size="sm">{{ retainedKind(item.kind) }}</BaseBadge>
                <span v-if="item.id" class="services-page__mono"> {{ item.id }}</span>
                <span>：{{ item.reason }}</span>
              </li>
            </ul>
          </div>
        </div>

        <p class="service-delete__warning">
          节点尚未应用删除时，服务会留在列表并显示待生效。这不表示入口已经关闭。
        </p>

        <div class="service-delete__actions">
          <button type="button" class="btn btn-secondary" @click="deleting = null">返回</button>
          <button type="button" class="btn btn--danger" :disabled="submitting" @click="confirmDelete">确认删除</button>
        </div>
      </div>
    </div>

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
      <div class="service-detail__topbar">
        <button type="button" class="btn btn-secondary btn-sm" @click="closeDetail">返回列表</button>
        <div class="service-detail__top-actions">
          <button v-if="showRetry" type="button" class="btn btn-primary btn-sm" :disabled="submitting" @click="retrySelected">重试</button>
          <button v-if="selected.switch_phase === '退役旧入口'" type="button" class="btn btn-secondary btn-sm" :disabled="submitting" @click="retireSelected">退役旧入口</button>
          <button v-if="selected.switch_phase === '准备新入口' || selected.switch_phase === '等待新入口生效'" type="button" class="btn btn-secondary btn-sm" :disabled="submitting" @click="abandonSelected">放弃这次切换</button>
          <button type="button" class="btn btn-secondary btn-sm" @click="startEdit(selected)">修改</button>
          <button type="button" class="btn btn--danger-soft btn-sm" @click="startDelete(selected)">删除</button>
        </div>
      </div>

      <header class="service-detail__hero">
        <div class="service-detail__hero-head">
          <h2 class="service-detail__title">{{ selected.name || '未命名服务' }}</h2>
          <div class="service-detail__badges">
            <BaseBadge :tone="detailSummaryTone" size="md" dot data-testid="detail-summary">
              {{ selected.summary || '控制面没有返回摘要' }}
            </BaseBadge>
            <BaseBadge v-if="selected.switch_phase" tone="warning" size="md">
              {{ selected.switch_phase }}
            </BaseBadge>
          </div>
        </div>
        <div class="service-detail__endpoint">
          <span class="service-detail__endpoint-label">访问地址</span>
          <code class="service-detail__endpoint-url">{{ selected.entry_url || '—' }}</code>
        </div>
      </header>

      <dl class="service-detail__grid">
        <div class="service-detail__item">
          <dt>入口节点</dt>
          <dd>{{ agentLabel(selected.entry_agent_id) }}</dd>
        </div>
        <div class="service-detail__item">
          <dt>服务侧节点</dt>
          <dd>{{ agentLabel(selected.service_agent_id) }}</dd>
        </div>
        <div class="service-detail__item">
          <dt>路径</dt>
          <dd>{{ pathText(selected.path) }}</dd>
        </div>
        <div class="service-detail__item">
          <dt>切换阶段</dt>
          <dd>
            <BaseBadge v-if="selected.switch_phase" tone="warning" size="sm">{{ selected.switch_phase }}</BaseBadge>
            <span v-else>无</span>
          </dd>
        </div>
        <div class="service-detail__item">
          <dt>未完成环节</dt>
          <dd :class="{ 'service-detail__pending-text': detailSteps.length }">
            {{ detailSteps.length ? detailSteps.join('；') : '无' }}
          </dd>
        </div>
        <div class="service-detail__item">
          <dt>证书</dt>
          <dd class="service-detail__inline-meta">
            <BaseBadge :tone="detailCertificateTone" size="sm">{{ detailCertificate.label }}</BaseBadge><span v-if="detailCertificate.detail" class="service-detail__muted">：{{ detailCertificate.detail }}</span>
          </dd>
        </div>
        <div class="service-detail__item">
          <dt>流量</dt>
          <dd>
            <span class="services-page__mono">{{ detailTraffic.text }}</span><span v-if="detailTraffic.detail" class="service-detail__muted"> · {{ detailTraffic.detail }}</span>
          </dd>
        </div>
        <div v-if="selected.apply" class="service-detail__item">
          <dt>生效</dt>
          <dd class="service-detail__inline-meta">
            <BaseBadge v-if="selected.apply.pending" tone="warning" size="sm">待生效</BaseBadge>
            <span v-if="applyDetailText">{{ applyDetailText }}</span>
          </dd>
        </div>
      </dl>

      <section class="service-detail__section">
        <h3 class="service-detail__section-title">节点探测</h3>
        <p v-if="!detailProbes.length" class="service-detail__hint">
          控制面没有探测结果。节点在线或规则存在都不代表接入完成。
        </p>
        <ul v-else class="service-detail__probe-list">
          <li
            v-for="(probe, index) in detailProbes"
            :key="`${probe.agentId}-${index}`"
            class="service-detail__probe-row"
            data-testid="probe-row"
          >
            <div class="service-detail__probe-main">
              <span class="service-detail__probe-agent">{{ probe.agentId || '未知节点' }}</span>
              <BaseBadge v-if="probe.purpose" tone="neutral" subtone="secondary" size="sm" mono>{{ probe.purpose }}</BaseBadge>
              <BaseBadge :tone="probeTone(probe)" size="sm" mono>{{ probe.text }}</BaseBadge>
              <BaseBadge v-if="probe.verification" :tone="probe.verification === 'expired' ? 'warning' : 'neutral'" size="sm" mono>
                {{ probe.verification }}
              </BaseBadge>
            </div>
            <div v-if="probe.target || probe.checkedAt || probe.error" class="service-detail__probe-meta">
              <code v-if="probe.target" class="services-page__mono">{{ probe.target }}</code>
              <span v-if="probe.checkedAt" class="service-detail__muted">
                <template v-if="probe.target"> · </template>{{ probe.checkedAt }}
              </span>
              <span v-if="probe.error" class="service-detail__probe-error">
                <template v-if="probe.target || probe.checkedAt"> · </template>{{ probe.error }}
              </span>
            </div>
          </li>
        </ul>
      </section>

      <section class="service-detail__section" data-testid="client-confirmation">
        <div class="service-detail__section-head">
          <h3 class="service-detail__section-title">客户端访问</h3>
          <BaseBadge :tone="selected.client_confirmation?.confirmed ? 'success' : 'neutral'" size="sm" dot>
            {{ selected.client_confirmation?.confirmed ? '已由用户确认' : '未知' }}
          </BaseBadge>
        </div>
        <p v-if="selected.client_confirmation?.confirmed" class="service-detail__confirm-status">
          用户确认页面可打开
          <span v-if="selected.client_confirmation.confirmed_at" class="service-detail__muted">
            · {{ selected.client_confirmation.confirmed_at }}
          </span>
        </p>
        <p v-else class="service-detail__confirm-status">客户端访问：未知</p>
        <p class="service-detail__hint">用户确认与节点探测分开，不表示媒体播放通过。</p>
        <div class="services-page__actions">
          <a v-if="selectedHref" class="btn btn-secondary btn-sm" :href="selectedHref" target="_blank" rel="noopener">打开</a>
          <button type="button" class="btn btn-secondary btn-sm" @click="copyEntry(selected)">复制</button>
          <button v-if="!selected.client_confirmation?.confirmed" type="button" class="btn btn-primary btn-sm" :disabled="submitting" @click="confirmOpen">我确认页面可打开</button>
        </div>
      </section>

      <section v-if="selected.rules?.length" class="service-detail__section">
        <h3 class="service-detail__section-title">关联规则</h3>
        <ul class="service-detail__rule-list">
          <li v-for="rule in selected.rules" :key="`${rule.agent_id}:${rule.id}`" class="service-detail__rule-row">
            <div class="service-detail__rule-info">
              <code class="services-page__mono">{{ rule.frontend_url || selected.entry_url }}</code>
              <span v-if="ruleBackendLabels(rule).length" class="service-detail__muted">
                · {{ ruleBackendLabels(rule).join('，') }}
              </span>
            </div>
            <a class="btn btn-ghost btn-sm" :href="advancedRuleHref(rule)">高级规则</a>
          </li>
        </ul>
      </section>
    </section>

    <SkeletonList v-else-if="!wizard && loading" variant="cards" :count="3" />

    <ErrorState
      v-else-if="!wizard && loadError && !services.length"
      title="加载服务列表失败"
      :message="loadError"
      :retrying="retryingLoad"
      @retry="retryLoad"
    />

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
import BaseBadge from '../components/base/BaseBadge.vue'
import EmptyState from '../components/base/EmptyState.vue'
import ErrorState from '../components/base/ErrorState.vue'
import SkeletonList from '../components/base/SkeletonList.vue'
import { copyText } from '../utils/clipboard.js'
import { messageStore } from '../stores/messages.js'
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
const retryingLoad = computed(() => Boolean(servicesQuery.isFetching?.value))
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
const detailSummaryTone = computed(() => {
  const summary = String(selected.value?.summary || '').trim()
  if (summary === '已就绪') return 'success'
  if (summary === '失败') return 'danger'
  if (summary && (summary.includes('待') || summary.includes('中'))) return 'warning'
  return 'neutral'
})
const detailSteps = computed(() => incompleteSteps(selected.value))
const detailCertificate = computed(() => certificateText(selected.value?.certificate))
const detailCertificateTone = computed(() => {
  const state = String(selected.value?.certificate?.state || 'none')
  if (state === 'ready') return 'success'
  if (state === 'error') return 'danger'
  if (state === 'issuing') return 'primary'
  if (state === 'none') return 'neutral'
  return 'warning'
})
const detailTraffic = computed(() => serviceTrafficText(selected.value?.traffic))
const detailProbes = computed(() => (Array.isArray(selected.value?.probes) ? selected.value.probes : []).map((probe) => probeText(probe)))
const selectedHref = computed(() => safeHttpHref(selected.value?.entry_url))
const relayHostMissing = computed(() => errorRequestsRelayInput(selected.value?.last_error))
const showRetry = computed(() => {
  if (!selected.value || relayHostMissing.value) return false
  return selected.value.summary === '失败' || selected.value.summary === '待确认'
})
const deleteImpact = computed(() => deletionImpact(deleting.value || {}))
const applyDetailText = computed(() => {
  const apply = selected.value?.apply || {}
  const parts = []
  if (!apply.pending) parts.push('控制面没有把生效标为待处理')
  if (apply.old_entry_may_receive) parts.push('旧入口仍可能接收新请求')
  if (apply.new_entry_applied) parts.push('新入口已应用')
  if (apply.listener_applied) parts.push('监听器已生效')
  if (apply.certificate_ready) parts.push('证书已就绪')
  return parts.join('；')
})

function probeTone(probe) {
  if (probe.verification === 'expired') return 'warning'
  if (probe.error) return 'danger'
  if (probe.status === 'connected') return 'success'
  if (probe.status === 'http_status') return 'primary'
  if (probe.status === 'untested') return 'neutral'
  return 'warning'
}

function retryLoad() {
  return servicesQuery.refetch?.()
}

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
  const text = String(service?.entry_url || '').trim()
  if (!text) {
    copyNotice.value = '没有可复制的访问地址'
    messageStore.warning('没有可复制的访问地址')
    return
  }
  const result = await copyText(text)
  if (result.ok) {
    copyNotice.value = '已复制访问地址'
    messageStore.success('已复制访问地址')
    return
  }
  copyNotice.value = '复制失败'
  messageStore.error(result.error || '复制失败，请手动复制', '复制失败')
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
.services-page {
  min-width: 0;
}

.services-page__mono {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  overflow-wrap: anywhere;
  word-break: break-word;
}

.services-page__alert {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2-5);
  margin-bottom: var(--space-4);
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-lg);
  border: 1px solid color-mix(in srgb, var(--color-danger) 28%, transparent);
  background: var(--color-danger-50);
  color: var(--color-danger);
  font-size: var(--text-sm);
}

.services-page__alert-icon {
  flex-shrink: 0;
}

.services-page__alert-text {
  flex: 1 1 12rem;
  min-width: 0;
  overflow-wrap: anywhere;
}

.services-page__notice {
  margin: 0 0 var(--space-4);
  padding: var(--space-2-5) var(--space-3-5, 0.875rem);
  border-radius: var(--radius-md);
  border: 1px solid color-mix(in srgb, var(--color-primary) 20%, var(--color-border-default));
  background: var(--color-primary-50);
  color: var(--color-text-primary);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
}

.service-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 22rem), 1fr));
  gap: var(--space-4);
}

/* ----- Delete confirmation panel ----- */
.service-delete {
  margin-bottom: var(--space-5);
  border: 1px solid color-mix(in srgb, var(--color-danger) 32%, var(--color-border-default));
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-md);
  overflow: hidden;
  min-width: 0;
}

.service-delete__header {
  padding: var(--space-3-5, 0.875rem) var(--space-5);
  background: var(--color-danger-50);
  border-bottom: 1px solid color-mix(in srgb, var(--color-danger) 18%, transparent);
}

.service-delete__title-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
}

.service-delete__title-row .service-delete__target-badge {
  max-width: 100%;
  min-width: 0;
  height: auto;
  min-height: 1.25rem;
  padding-block: 0.15rem;
  white-space: normal;
  line-height: 1.35;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-delete__icon {
  color: var(--color-danger);
  flex-shrink: 0;
}

.service-delete__title {
  margin: 0;
  font-size: var(--text-base);
  font-weight: var(--font-semibold);
  color: var(--color-danger);
}

.service-delete__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: var(--space-4) var(--space-5);
}

.service-delete__columns {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
}

.service-delete__panel {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3-5, 0.875rem);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
  min-width: 0;
}

.service-delete__panel-title {
  margin: 0;
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-secondary);
}

.service-delete__list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-1-5);
}

.service-delete__item {
  font-size: var(--text-xs);
  color: var(--color-text-primary);
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.service-delete__item--muted {
  color: var(--color-text-tertiary);
}

.service-delete__sep {
  color: var(--color-text-muted);
}

.service-delete__warning {
  margin: 0;
  padding: var(--space-3) var(--space-3-5, 0.875rem);
  border-radius: var(--radius-md);
  border: 1px solid color-mix(in srgb, var(--color-warning) 28%, transparent);
  background: var(--color-warning-50);
  color: var(--color-text-primary);
  font-size: var(--text-xs);
  line-height: 1.55;
}

.service-delete__actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--space-2);
  padding-top: var(--space-2);
  border-top: 1px solid var(--color-border-subtle);
}

/* ----- Service detail panel ----- */
.service-detail {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  margin-bottom: var(--space-5);
  padding: var(--space-5);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-xs);
  min-width: 0;
}

.service-detail__topbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding-bottom: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
}

.service-detail__top-actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1-5);
}

.service-detail__hero {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
}

.service-detail__hero-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.service-detail__title {
  margin: 0;
  font-size: var(--text-xl);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  line-height: 1.25;
  overflow-wrap: anywhere;
}

.service-detail__badges {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1-5);
}

.service-detail__endpoint {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-3) var(--space-3-5, 0.875rem);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
  min-width: 0;
}

.service-detail__endpoint-label {
  font-size: var(--text-2xs);
  font-weight: var(--font-medium);
  color: var(--color-text-muted);
}

.service-detail__endpoint-url {
  font-family: var(--font-mono);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-detail__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--space-3);
  margin: 0;
}

.service-detail__item {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-3);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
  min-width: 0;
}

.service-detail__item dt {
  font-size: var(--text-2xs);
  font-weight: var(--font-medium);
  color: var(--color-text-muted);
}

.service-detail__item dd {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  line-height: 1.45;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.service-detail__pending-text {
  color: var(--color-warning);
}

.service-detail__inline-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1-5);
}

.service-detail__muted {
  color: var(--color-text-tertiary);
}

.service-detail__section {
  display: flex;
  flex-direction: column;
  gap: var(--space-2-5);
  padding: var(--space-4);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
  min-width: 0;
}

.service-detail__section-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.service-detail__section-title {
  margin: 0;
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
}

.service-detail__hint {
  margin: 0;
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  line-height: 1.5;
}

.service-detail__confirm-status {
  margin: 0;
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
}

.service-detail__probe-list,
.service-detail__rule-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.service-detail__probe-row {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding: var(--space-2-5) var(--space-3);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  min-width: 0;
}

.service-detail__probe-main {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1-5);
}

.service-detail__probe-agent {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
}

.service-detail__probe-meta {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1);
  font-size: var(--text-xs);
  overflow-wrap: anywhere;
}

.service-detail__probe-error {
  color: var(--color-danger);
}

.service-detail__rule-row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding: var(--space-2-5) var(--space-3);
  border-radius: var(--radius-md);
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-surface);
  min-width: 0;
}

.service-detail__rule-info {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1);
  min-width: 0;
  font-size: var(--text-xs);
  overflow-wrap: anywhere;
}

.services-page__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

@media (max-width: 768px) {
  .service-delete__columns,
  .service-detail__grid {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 640px) {
  .service-detail,
  .service-delete__body {
    padding: var(--space-4);
  }

  .service-delete__header {
    padding: var(--space-3) var(--space-4);
  }

  .service-detail__section {
    padding: var(--space-3);
  }

  .service-delete__actions {
    justify-content: stretch;
  }

  .service-delete__actions > .btn {
    flex: 1 1 auto;
  }
}
</style>
