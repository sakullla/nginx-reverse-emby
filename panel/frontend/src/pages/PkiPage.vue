<template>
  <div class="pki-page">
    <CertificateCenterChrome
      domain="internal"
      title="内部 PKI"
      :subtitle="headerSubtitle"
    >
      <template #actions>
        <BaseButton variant="secondary" :disabled="loading" :loading="loading" @click="loadAll">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <polyline points="23 4 23 10 17 10"/>
            <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>
          </svg>
          刷新
        </BaseButton>
        <BaseButton variant="primary" @click="openEnrollment">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
            <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
          </svg>
          创建登记令牌
        </BaseButton>
      </template>
    </CertificateCenterChrome>

    <div v-if="pageError" class="notice notice--danger" role="alert">
      <div class="notice__body">
        <strong>内部 PKI 数据暂时不可用</strong>
        <span>{{ pageError }}</span>
      </div>
      <button class="text-button" @click="loadAll">重试</button>
    </div>

    <div v-if="overview.recovery_blocker" class="notice notice--danger" role="alert">
      <div class="notice__body">
        <strong>{{ overview.recovery_blocker.message }}</strong>
        <span>{{ overview.recovery_blocker.recovery_hint }}</span>
      </div>
    </div>

    <div
      v-if="overview.upgrade_state === 'migration_required'"
      data-test="automatic-activation-notice"
      class="notice notice--info"
      role="status"
    >
      <div class="notice__body">
        <strong>迁移激活等待中</strong>
        <span>系统会在端点证书与当前安全修订均确认就绪后自动激活，无需手工操作。</span>
      </div>
    </div>

    <PkiHealthOverview
      :overview="overview"
      :identity-count="identities.length"
      :certificate-count="certificates.length"
      :runtime-status-label="runtimeStatusLabel"
    />

    <PkiAttentionPanel
      :alerts="pagedAlerts"
      :alert-page="alertPage"
      :alert-total="sortedAlerts.length"
      :operations="pagedOperations"
      :operation-page="operationPage"
      :operation-total="sortedOperations.length"
      :operation-errors="operationErrors"
      :page-size="PKI_PAGE_SIZE"
      :alert-field="alertField"
      :alert-level-label="alertLevelLabel"
      :alert-kind-label="alertKindLabel"
      :operation-label="operationLabel"
      :operation-state-label="operationStateLabel"
      :format-date="formatDate"
      @rotate-ca="openDomainAction('rotate-ca')"
      @emergency-ca="openDomainAction('emergency-ca')"
      @refresh-operation="refreshOperation"
      @forget-operation="forgetOperation"
      @update:alert-page="alertPage = $event"
      @update:operation-page="operationPage = $event"
    />

    <PkiIdentityPanel
      :identities="pagedIdentityRows"
      :page="identityPage"
      :page-size="PKI_PAGE_SIZE"
      :total="identityRows.length"
      :purpose-label="purposeLabel"
      :format-date="formatDate"
      @force-rotate="openIdentityAction('force-rotate', $event)"
      @revoke="openIdentityAction('revoke', $event)"
      @update:page="identityPage = $event"
    />

    <PkiAuthorityPanel
      :authorities="recentAuthorities"
      :authority-status-label="authorityStatusLabel"
      :format-date="formatDate"
    />

    <PkiSection
      title="受保护备份"
      description="口令只驻留在本次表单与 request body；成功或失败后立即清空。口令丢失无法恢复。"
      eyebrow="灾难恢复"
      aria-label="受保护备份"
      collapsible
      storage-key="nre.pki.section.backup"
    >
      <PkiBackupPanel
        ref="backupPanelRef"
        :busy="backupBusy"
        :export-passphrase="exportPassphrase"
        :export-passphrase-confirm="exportPassphraseConfirm"
        :import-passphrase="importPassphrase"
        :import-reason="importReason"
        :import-confirmation="importConfirmation"
        :has-archive="Boolean(exportArchive)"
        :message="backupMessage"
        :message-kind="backupMessageKind"
        :hide-header="true"
        @export="exportBackup"
        @import="importBackup"
        @download="downloadArchive"
        @select-file="selectImportFile"
        @update:export-passphrase="exportPassphrase = $event"
        @update:export-passphrase-confirm="exportPassphraseConfirm = $event"
        @update:import-passphrase="importPassphrase = $event"
        @update:import-reason="importReason = $event"
        @update:import-confirmation="importConfirmation = $event"
      />
    </PkiSection>

    <PkiSection
      title="安全审计"
      description="查询签发、续签、轮转、撤销、拒绝、备份与恢复事件；最新优先，每页最多 5 条。"
      eyebrow="可追溯"
      aria-label="安全审计"
      collapsible
      storage-key="nre.pki.section.audit"
    >
      <PkiAuditPanel
        :events="pagedEvents"
        :filters="eventFilters"
        :page="eventPage"
        :page-size="PKI_PAGE_SIZE"
        :total="sortedEvents.length"
        :format-date="formatDate"
        :hide-header="true"
        @search="loadEvents"
        @update:page="eventPage = $event"
        @update:filters="Object.assign(eventFilters, $event)"
      />
    </PkiSection>

    <BaseModal
      :model-value="enrollmentOpen"
      title="创建一次性登记令牌"
      subtitle="令牌仅显示一次，关闭后浏览器会清除明文"
      data-test="enrollment-dialog"
      :close-on-click-modal="!enrollmentBusy"
      show-footer
      @update:model-value="onEnrollmentModalChange"
    >
      <template v-if="!enrollmentToken">
        <div class="pki-dialog-form">
          <label class="pki-field">
            <span class="pki-field__label">登记类型</span>
            <select v-model="enrollmentScope" class="pki-field__control">
              <option value="new_agent">新节点</option>
              <option value="bound_reenrollment">绑定现有节点</option>
            </select>
          </label>
          <label v-if="enrollmentScope === 'bound_reenrollment'" class="pki-field">
            <span class="pki-field__label">Agent ID</span>
            <input v-model="enrollmentAgentID" class="pki-field__control mono" autocomplete="off" required placeholder="现有稳定节点 ID">
          </label>
          <p v-if="enrollmentError" class="danger-text">{{ enrollmentError }}</p>
        </div>
      </template>
      <div v-else class="one-time-secret" data-test="enrollment-secret">
        <strong>仅显示一次</strong>
        <code>{{ enrollmentToken.token }}</code>
        <span>有效期至 {{ formatDate(enrollmentToken.expires_at) }}。关闭后浏览器将清除此值。</span>
      </div>
      <template #footer>
        <template v-if="!enrollmentToken">
          <button class="btn btn--secondary" type="button" :disabled="enrollmentBusy" @click="closeEnrollment">取消</button>
          <button
            class="btn btn--primary"
            type="button"
            :disabled="enrollmentBusy || (enrollmentScope === 'bound_reenrollment' && !enrollmentAgentID.trim())"
            @click="createEnrollment"
          >{{ enrollmentBusy ? '签发中…' : '生成令牌' }}</button>
        </template>
        <template v-else>
          <button class="btn btn--secondary" type="button" @click="copyEnrollmentToken">复制</button>
          <button class="btn btn--primary" type="button" @click="closeEnrollment">我已保存并关闭</button>
        </template>
      </template>
    </BaseModal>

    <BaseModal
      :model-value="Boolean(pendingAction)"
      :title="pendingAction?.label || '确认操作'"
      :subtitle="pendingAction ? `对象 · ${pendingAction.targetLabel}` : ''"
      data-test="action-dialog"
      :close-on-click-modal="!actionBusy"
      show-footer
      @update:model-value="onActionModalChange"
    >
      <form class="pki-dialog-form" @submit.prevent="submitAction">
        <div class="pki-target-chip">
          <span class="pki-target-chip__label">目标对象</span>
          <code class="mono">{{ pendingAction?.targetLabel }}</code>
        </div>
        <label class="pki-field">
          <span class="pki-field__label">操作原因</span>
          <textarea
            v-model="actionReason"
            data-test="action-reason"
            class="pki-field__control pki-field__control--textarea"
            :disabled="actionBusy"
            required
            placeholder="说明为何执行此高风险操作"
          ></textarea>
        </label>
        <label class="pki-field">
          <span class="pki-field__label">明确确认</span>
          <span class="pki-field__hint">请输入 <strong class="mono">{{ pendingAction?.confirmText }}</strong> 以继续</span>
          <input
            v-model="actionConfirmation"
            data-test="action-confirmation"
            class="pki-field__control mono"
            :disabled="actionBusy"
            autocomplete="off"
            required
            :placeholder="pendingAction?.confirmText || ''"
          >
        </label>
        <p v-if="actionError" class="danger-text">{{ actionError }}</p>
        </form>
      <template #footer>
        <button class="btn btn--secondary" type="button" :disabled="actionBusy" @click="closeAction">取消</button>
        <button
          class="btn btn--danger"
          type="button"
          :disabled="actionBusy || !pendingAction || actionConfirmation !== pendingAction.confirmText"
          @click="submitAction"
        >{{ actionBusy ? '提交中…' : '确认执行' }}</button>
      </template>
    </BaseModal>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref, watch } from 'vue'
import BaseButton from '../components/base/BaseButton.vue'
import BaseModal from '../components/base/BaseModal.vue'
import CertificateCenterChrome from '../components/certs/CertificateCenterChrome.vue'
import PkiAttentionPanel from '../components/pki/PkiAttentionPanel.vue'
import PkiAuditPanel from '../components/pki/PkiAuditPanel.vue'
import PkiAuthorityPanel from '../components/pki/PkiAuthorityPanel.vue'
import PkiBackupPanel from '../components/pki/PkiBackupPanel.vue'
import PkiHealthOverview from '../components/pki/PkiHealthOverview.vue'
import PkiIdentityPanel from '../components/pki/PkiIdentityPanel.vue'
import PkiSection from '../components/pki/PkiSection.vue'
import {
  PKI_CONFIRMATION_ACTION,
  createPkiEnrollmentToken,
  emergencyRotatePkiAuthority,
  exportProtectedPki,
  fetchPkiAlerts,
  fetchPkiAuthorities,
  fetchPkiCertificates,
  fetchPkiEvents,
  fetchPkiIdentities,
  fetchPkiOverview,
  forceRotatePkiIdentity,
  importProtectedPki,
  issuePkiConfirmation,
  protectedArchiveBlob,
  revokePkiIdentity,
  rotatePkiAuthority
} from '../api/pki'
import { recordPkiOperation, resetPkiOperationMemory, usePkiOperations } from '../hooks/usePkiOperations'

const loading = ref(false)
const pageError = ref('')
const PKI_PAGE_SIZE = 5
const overview = ref({})
const authorities = ref([])
const identities = ref([])
const certificates = ref([])
const alerts = ref([])
const events = ref([])
const eventFilters = reactive({ type: '', identity_id: '', source: '', result: '' })
const operationPage = ref(1)
const alertPage = ref(1)
const identityPage = ref(1)
const eventPage = ref(1)
const backupPanelRef = ref(null)

const {
  operations,
  errors: operationErrors,
  track,
  refresh: refreshOperation,
  forget: forgetOperation
} = usePkiOperations()

const headerSubtitle = computed(() => {
  const domain = overview.value.pki_domain_id
  const status = overview.value.runtime_status
  if (!domain && !status) return '管理 relay mTLS 身份、CA 生命周期、撤销、审计和受保护迁移备份'
  const parts = []
  if (domain) parts.push(domain)
  if (status) parts.push(runtimeStatusLabel(status))
  return parts.join(' · ')
})

function field(value, snake, pascal) {
  return value?.[snake] ?? value?.[pascal] ?? ''
}

function alertField(alert, name) {
  const pascal = name.split('_').map(part => part.charAt(0).toUpperCase() + part.slice(1)).join('')
  return field(alert, name, pascal)
}

function timestamp(value) {
  const parsed = Date.parse(value || '')
  return Number.isFinite(parsed) ? parsed : 0
}

function compareText(left, right) {
  return String(left || '').localeCompare(String(right || ''))
}

function pageSlice(rows, page) {
  const start = (Math.max(1, page) - 1) * PKI_PAGE_SIZE
  return rows.slice(start, start + PKI_PAGE_SIZE)
}

function clampPage(page, total) {
  const lastPage = Math.max(1, Math.ceil(Math.max(0, total) / PKI_PAGE_SIZE))
  if (page.value > lastPage) page.value = lastPage
}

function operationStatusRank(status) {
  return ({ blocked: 0, failed: 0, running: 1, accepted: 1, cancelled: 2, succeeded: 3 })[String(status || '').toLowerCase()] ?? 2
}

function alertLevelRank(level) {
  return ({ failed_closed: 0, critical: 1, warning: 2 })[String(level || '').toLowerCase()] ?? 3
}

function authorityStatusRank(status) {
  return ({ active: 0, prepared: 1, retiring: 2, retired: 3, revoked: 4 })[String(status || '').toLowerCase()] ?? 5
}

function certificateStatusRank(identityState, certificateStatus, revoked) {
  if (revoked) return 4
  if (identityState === 'active' && certificateStatus === 'active') return 0
  if (['enrollment_required', 'pending'].includes(identityState) || ['pending', 'prepared'].includes(certificateStatus)) return 1
  if (['superseded', 'expired'].includes(certificateStatus)) return 2
  return 3
}

function formatDate(value) {
  if (!value) return '—'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return String(value)
  return parsed.toLocaleString('zh-CN', { hour12: false })
}

function runtimeStatusLabel(status) {
  return ({
    healthy: '健康',
    degraded: '降级',
    unavailable: '不可用',
    unknown: '未知'
  })[String(status || '').toLowerCase()] || status || '未知'
}

function operationStateLabel(state) {
  return ({
    accepted: '已受理',
    running: '执行中',
    blocked: '已阻断',
    succeeded: '已成功',
    failed: '已失败',
    cancelled: '已取消'
  })[String(state || '').toLowerCase()] || state || '—'
}

function alertLevelLabel(level) {
  return ({
    failed_closed: '失败关闭',
    critical: '严重',
    warning: '警告'
  })[String(level || '').toLowerCase()] || level || '告警'
}

function alertKindLabel(kind) {
  if (!kind) return 'PKI 告警'
  return String(kind).replace(/_/g, ' ')
}

function purposeLabel(purpose) {
  return ({
    client_auth: '客户端认证',
    server_auth: '服务端认证',
    both: '双向用途'
  })[String(purpose || '').toLowerCase()] || purpose || '—'
}

function authorityStatusLabel(status) {
  return ({
    active: '活动',
    prepared: '已准备',
    retiring: '退出中',
    retired: '已退出',
    revoked: '已撤销'
  })[String(status || '').toLowerCase()] || status || '—'
}

function operationLabel(kind) {
  return ({
    revoke: '撤销身份',
    force_rotate: '端点强制换证',
    ca_rotate: '日常 CA 轮转',
    emergency_ca_rotate: '紧急 CA 轮转',
    protected_export: '受保护备份导出',
    protected_import: '受保护备份导入',
    activate: '迁移激活'
  })[kind] || kind || '内部 PKI 操作'
}

const sortedOperations = computed(() => [...operations.value].sort((left, right) => {
  const rank = operationStatusRank(left.state) - operationStatusRank(right.state)
  if (rank !== 0) return rank
  const time = timestamp(right.updated_at || right.created_at) - timestamp(left.updated_at || left.created_at)
  return time || compareText(left.id, right.id)
}))

const pagedOperations = computed(() => pageSlice(sortedOperations.value, operationPage.value))

const sortedAlerts = computed(() => [...alerts.value].sort((left, right) => {
  const rank = alertLevelRank(alertField(left, 'level')) - alertLevelRank(alertField(right, 'level'))
  if (rank !== 0) return rank
  const time = timestamp(alertField(right, 'last_seen')) - timestamp(alertField(left, 'last_seen'))
  return time || compareText(alertField(left, 'id'), alertField(right, 'id'))
}))

const pagedAlerts = computed(() => pageSlice(sortedAlerts.value, alertPage.value))

const recentAuthorities = computed(() => [...authorities.value]
  .sort((left, right) => {
    const rank = authorityStatusRank(left.status) - authorityStatusRank(right.status)
    if (rank !== 0) return rank
    const time = timestamp(right.not_before || right.not_after) - timestamp(left.not_before || left.not_after)
    if (time !== 0) return time
    const generation = Number(right.generation || 0) - Number(left.generation || 0)
    return generation || compareText(left.id, right.id)
  })
  .slice(0, PKI_PAGE_SIZE))

const sortedEvents = computed(() => [...events.value].sort((left, right) => {
  const time = timestamp(right.occurred_at) - timestamp(left.occurred_at)
  if (time !== 0) return time
  const resultRank = Number(!['failed', 'rejected'].includes(String(left.result || '').toLowerCase()))
    - Number(!['failed', 'rejected'].includes(String(right.result || '').toLowerCase()))
  return resultRank || compareText(left.id, right.id)
}))

const pagedEvents = computed(() => pageSlice(sortedEvents.value, eventPage.value))

const identityRows = computed(() => identities.value.map(identity => {
  const identityCertificates = certificates.value
    .filter(item => item.identity_id === identity.id)
    .sort((left, right) => {
      const activeRank = Number(right.status === 'active') - Number(left.status === 'active')
      if (activeRank !== 0) return activeRank
      const time = timestamp(right.not_before || right.revoked_at || right.not_after)
        - timestamp(left.not_before || left.revoked_at || left.not_after)
      return time || compareText(left.id, right.id)
    })
  const certificate = identityCertificates.find(item => item.id === identity.current_certificate_id)
    || identityCertificates[0]
    || {}
  const ownerParts = [identity.kind, identity.agent_id, identity.listener_id].filter(Boolean)
  const revoked = identity.state === 'revoked' || certificate.status === 'revoked' || Boolean(identity.revoked_at || certificate.revoked_at)
  const canRotate = !revoked && identity.state === 'active' && certificate.status === 'active'
  return {
    id: identity.id,
    owner: ownerParts.join(' · ') || '—',
    purpose: certificate.purpose || identity.purpose || identity.eku || '—',
    caGeneration: certificate.ca_generation ?? identity.ca_generation ?? '—',
    serial: certificate.serial_hex || '—',
    fingerprint: certificate.public_key_fingerprint_sha256 || certificate.fingerprint_sha256 || '—',
    notBefore: certificate.not_before,
    notAfter: certificate.not_after,
    nextAction: identity.next_action || certificate.next_action || identity.renew_due_at || '—',
    rotationPhase: identity.rotation_phase || certificate.rotation_phase || '—',
    revoked,
    canRotate,
    canRevoke: !revoked,
    statusRank: certificateStatusRank(identity.state, certificate.status, revoked),
    sortTimestamp: identity.revoked_at || certificate.revoked_at || certificate.not_before || certificate.not_after,
    revocation: revoked ? `已撤销${identity.revoked_reason || certificate.revoked_reason ? `：${identity.revoked_reason || certificate.revoked_reason}` : ''}` : (identity.state || certificate.status || '—'),
    latestError: identity.latest_error || certificate.latest_error || identity.last_error || certificate.last_error || ''
  }
}).sort((left, right) => {
  const rank = left.statusRank - right.statusRank
  if (rank !== 0) return rank
  const time = timestamp(right.sortTimestamp) - timestamp(left.sortTimestamp)
  return time || compareText(left.id, right.id)
}))

const pagedIdentityRows = computed(() => pageSlice(identityRows.value, identityPage.value))

watch(() => sortedOperations.value.length, total => clampPage(operationPage, total))
watch(() => sortedAlerts.value.length, total => clampPage(alertPage, total))
watch(() => identityRows.value.length, total => clampPage(identityPage, total))
watch(() => sortedEvents.value.length, total => clampPage(eventPage, total))

async function loadEvents() {
  eventPage.value = 1
  try {
    events.value = await fetchPkiEvents(eventFilters)
  } catch (error) {
    pageError.value = error?.message || '内部 PKI 审计查询失败'
  }
}

function applyMockData() {
  overview.value = {
    pki_domain_id: 'pki-domain-demo-7f3a',
    pki_epoch: 4,
    security_revision: 11,
    upgrade_state: 'tunnel_mtls_only',
    runtime_status: 'healthy',
    identity_count: 3,
    certificate_count: 3
  }
  authorities.value = [
    {
      id: 'ca-3',
      generation: 3,
      status: 'active',
      fingerprint_sha256: 'a1b2c3d4e5f60718293a4b5c6d7e8f9012345678abcd',
      not_before: '2026-07-01T00:00:00Z',
      not_after: '2036-07-01T00:00:00Z'
    },
    {
      id: 'ca-2',
      generation: 2,
      status: 'retiring',
      fingerprint_sha256: 'f0e1d2c3b4a5968778695a4b3c2d1e0f9988776655',
      not_before: '2026-06-01T00:00:00Z',
      not_after: '2036-06-01T00:00:00Z'
    },
    {
      id: 'ca-1',
      generation: 1,
      status: 'retired',
      fingerprint_sha256: '11223344556677889900aabbccddeeff00112233',
      not_before: '2026-01-01T00:00:00Z',
      not_after: '2036-01-01T00:00:00Z'
    }
  ]
  identities.value = [
    {
      id: 'identity-agent-local',
      kind: 'agent',
      agent_id: 'local',
      state: 'active',
      current_certificate_id: 'cert-agent-local',
      rotation_phase: 'idle'
    },
    {
      id: 'identity-agent-edge-1',
      kind: 'agent',
      agent_id: 'edge-1',
      state: 'active',
      current_certificate_id: 'cert-agent-edge-1',
      rotation_phase: 'renewing'
    },
    {
      id: 'identity-listener-1',
      kind: 'listener',
      listener_id: 'listener-1',
      state: 'active',
      current_certificate_id: 'cert-listener-1',
      rotation_phase: 'idle'
    },
    {
      id: 'identity-agent-offline',
      kind: 'agent',
      agent_id: 'offline-2',
      state: 'enrollment_required',
      current_certificate_id: null,
      rotation_phase: '—'
    },
    {
      id: 'identity-revoked-old',
      kind: 'agent',
      agent_id: 'old-node',
      state: 'revoked',
      current_certificate_id: 'cert-revoked-old',
      revoked_at: '2026-08-01T10:00:00Z',
      revoked_reason: 'compromised key material'
    }
  ]
  certificates.value = [
    {
      id: 'cert-agent-local',
      identity_id: 'identity-agent-local',
      purpose: 'client_auth',
      ca_generation: 3,
      serial_hex: '0a1b2c',
      public_key_fingerprint_sha256: 'pk-local-aa11bb22cc33dd44ee55',
      status: 'active',
      not_before: '2026-08-01T00:00:00Z',
      not_after: '2026-10-30T00:00:00Z',
      next_action: 'renew at one-third lifetime'
    },
    {
      id: 'cert-agent-edge-1',
      identity_id: 'identity-agent-edge-1',
      purpose: 'client_auth',
      ca_generation: 3,
      serial_hex: '11aa22',
      public_key_fingerprint_sha256: 'pk-edge-77889900aabbccddeeff',
      status: 'active',
      not_before: '2026-07-20T00:00:00Z',
      not_after: '2026-10-18T00:00:00Z',
      next_action: 'force rotate pending',
      rotation_phase: 'renewing'
    },
    {
      id: 'cert-listener-1',
      identity_id: 'identity-listener-1',
      purpose: 'server_auth',
      ca_generation: 3,
      serial_hex: 'ff0011',
      public_key_fingerprint_sha256: 'pk-listener-0011223344556677',
      status: 'active',
      not_before: '2026-08-02T00:00:00Z',
      not_after: '2026-11-01T00:00:00Z',
      next_action: 'renew at one-third lifetime'
    },
    {
      id: 'cert-revoked-old',
      identity_id: 'identity-revoked-old',
      purpose: 'client_auth',
      ca_generation: 2,
      serial_hex: 'dead01',
      public_key_fingerprint_sha256: 'pk-revoked-deadbeefcafe',
      status: 'revoked',
      revoked_at: '2026-08-01T10:00:00Z',
      revoked_reason: 'compromised key material',
      not_before: '2026-05-01T00:00:00Z',
      not_after: '2026-08-01T00:00:00Z'
    }
  ]
  alerts.value = [
    {
      id: 'alert-1',
      kind: 'endpoint_renewal_delayed',
      object_type: 'identity',
      object_id: 'identity-agent-edge-1',
      level: 'warning',
      reason: '端点续签超过预期窗口，仍在重试。',
      last_seen: '2026-08-05T12:10:00Z'
    },
    {
      id: 'alert-2',
      kind: 'enrollment_required',
      object_type: 'identity',
      object_id: 'identity-agent-offline',
      level: 'critical',
      reason: '节点缺少有效 tunnel 凭据，相关 relay 路径不可用。',
      last_seen: '2026-08-05T11:40:00Z'
    },
    {
      id: 'alert-3',
      kind: 'ca_overlap_window',
      object_type: 'authority',
      object_id: 'ca-2',
      level: 'warning',
      reason: '旧 CA generation 处于退出窗口，请确认在线节点已收敛。',
      last_seen: '2026-08-05T09:00:00Z'
    }
  ]
  events.value = [
    {
      id: 'event-1',
      type: 'force_rotate',
      object_type: 'identity',
      object_id: 'identity-agent-edge-1',
      result: 'success',
      source: 'panel',
      operator_id: 'admin',
      reason: 'manual renew for edge path',
      occurred_at: '2026-08-05T12:05:00Z'
    },
    {
      id: 'event-2',
      type: 'revoke',
      object_type: 'identity',
      object_id: 'identity-revoked-old',
      result: 'success',
      source: 'panel',
      operator_id: 'admin',
      reason: 'compromised key material',
      occurred_at: '2026-08-01T10:00:00Z'
    },
    {
      id: 'event-3',
      type: 'ca_rotate',
      object_type: 'authority',
      object_id: 'ca-3',
      result: 'success',
      source: 'scheduler',
      reason: 'scheduled authority maintenance',
      occurred_at: '2026-07-01T00:05:00Z'
    },
    {
      id: 'event-4',
      type: 'enrollment',
      object_type: 'identity',
      object_id: 'identity-agent-local',
      result: 'success',
      source: 'control-plane',
      reason: 'embedded local agent auto enrollment',
      occurred_at: '2026-07-01T00:10:00Z'
    },
    {
      id: 'event-5',
      type: 'protected_export',
      object_type: 'backup',
      object_id: 'backup-2026-08-03',
      result: 'success',
      source: 'panel',
      operator_id: 'admin',
      occurred_at: '2026-08-03T18:20:00Z'
    },
    {
      id: 'event-6',
      type: 'handshake_reject',
      object_type: 'identity',
      object_id: 'identity-revoked-old',
      result: 'rejected',
      source: 'relay',
      reason: 'revoked certificate presented',
      occurred_at: '2026-08-02T08:12:00Z'
    }
  ]
  resetPkiOperationMemory()
  ;[
    {
      id: 'op-force-1',
      kind: 'force_rotate',
      state: 'running',
      phase: 'awaiting_agent_ack',
      target_id: 'identity-agent-edge-1',
      updated_at: '2026-08-05T12:08:00Z',
      created_at: '2026-08-05T12:05:00Z',
      terminal: false
    },
    {
      id: 'op-export-1',
      kind: 'protected_export',
      state: 'succeeded',
      target_id: 'domain',
      updated_at: '2026-08-03T18:20:30Z',
      created_at: '2026-08-03T18:20:00Z',
      terminal: true
    },
    {
      id: 'op-ca-1',
      kind: 'ca_rotate',
      state: 'succeeded',
      target_id: 'domain',
      updated_at: '2026-07-01T00:08:00Z',
      created_at: '2026-07-01T00:05:00Z',
      terminal: true
    }
  ].forEach(recordPkiOperation)
}

async function loadAll() {
  loading.value = true
  pageError.value = ''
  try {
    const [nextOverview, nextAuthorities, nextIdentities, nextCertificates, nextAlerts, nextEvents] = await Promise.all([
      fetchPkiOverview(),
      fetchPkiAuthorities(),
      fetchPkiIdentities(),
      fetchPkiCertificates(),
      fetchPkiAlerts(),
      fetchPkiEvents(eventFilters)
    ])
    overview.value = nextOverview
    authorities.value = nextAuthorities
    identities.value = nextIdentities
    certificates.value = nextCertificates
    alerts.value = nextAlerts
    events.value = nextEvents
  } catch (error) {
    // API unavailable in local UI preview: fall back to rich mock data so layout can be reviewed.
    applyMockData()
    pageError.value = `${error?.message || '内部 PKI 数据暂时不可用'}（已加载预览 mock 数据）`
  } finally {
    loading.value = false
  }
}

const enrollmentOpen = ref(false)
const enrollmentScope = ref('new_agent')
const enrollmentAgentID = ref('')
const enrollmentToken = ref(null)
const enrollmentBusy = ref(false)
const enrollmentError = ref('')

function openEnrollment() {
  enrollmentOpen.value = true
  enrollmentError.value = ''
}

function closeEnrollment() {
  if (enrollmentBusy.value) return
  enrollmentOpen.value = false
  enrollmentToken.value = null
  enrollmentAgentID.value = ''
  enrollmentError.value = ''
}

function onEnrollmentModalChange(open) {
  if (open) {
    enrollmentOpen.value = true
    return
  }
  closeEnrollment()
  // Keep open if busy (closeEnrollment is a no-op while busy).
  if (enrollmentBusy.value) enrollmentOpen.value = true
}

async function createEnrollment() {
  if (enrollmentBusy.value) return
  enrollmentBusy.value = true
  enrollmentError.value = ''
  try {
    enrollmentToken.value = await createPkiEnrollmentToken({
      scope: enrollmentScope.value,
      boundAgentId: enrollmentScope.value === 'bound_reenrollment' ? enrollmentAgentID.value : ''
    })
  } catch (error) {
    enrollmentError.value = error?.message || '登记令牌创建失败'
  } finally {
    enrollmentBusy.value = false
  }
}

async function copyEnrollmentToken() {
  if (!enrollmentToken.value?.token) return
  try {
    await navigator.clipboard.writeText(enrollmentToken.value.token)
  } catch {
    enrollmentError.value = '浏览器未允许复制，请手动保存令牌'
  }
}

const pendingAction = ref(null)
const actionReason = ref('')
const actionConfirmation = ref('')
const actionBusy = ref(false)
const actionError = ref('')

function openIdentityAction(kind, identity) {
  pendingAction.value = kind === 'revoke'
    ? {
        kind,
        label: '撤销内部 PKI 身份',
        targetID: identity.id,
        targetLabel: identity.id,
        confirmText: identity.id,
        nonceAction: PKI_CONFIRMATION_ACTION.revoke,
        invoke: options => revokePkiIdentity(identity.id, options)
      }
    : {
        kind,
        label: '强制端点换证',
        targetID: identity.id,
        targetLabel: identity.id,
        confirmText: identity.id,
        nonceAction: PKI_CONFIRMATION_ACTION.forceRotate,
        invoke: options => forceRotatePkiIdentity(identity.id, options)
      }
}

function openDomainAction(kind) {
  const actions = {
    'rotate-ca': {
      kind,
      label: '日常 CA 轮转',
      targetID: 'domain',
      targetLabel: overview.value.pki_domain_id || 'domain',
      confirmText: 'ROTATE CA',
      nonceAction: PKI_CONFIRMATION_ACTION.rotateCA,
      invoke: rotatePkiAuthority
    },
    'emergency-ca': {
      kind,
      label: '紧急 CA 轮转',
      targetID: 'domain',
      targetLabel: overview.value.pki_domain_id || 'domain',
      confirmText: 'EMERGENCY ROTATE',
      nonceAction: PKI_CONFIRMATION_ACTION.emergencyRotateCA,
      invoke: emergencyRotatePkiAuthority
    }
  }
  pendingAction.value = actions[kind]
}

function resetAction() {
  pendingAction.value = null
  actionReason.value = ''
  actionConfirmation.value = ''
  actionError.value = ''
}

function closeAction() {
  if (actionBusy.value) return
  resetAction()
}

function onActionModalChange(open) {
  if (open) return
  closeAction()
}

async function submitAction() {
  const action = pendingAction.value
  const reason = actionReason.value.trim()
  const confirmationText = actionConfirmation.value
  if (actionBusy.value || !action || confirmationText !== action.confirmText || !reason) return
  actionBusy.value = true
  actionError.value = ''
  try {
    let confirmationNonce = ''
    if (action.nonceAction) {
      const confirmation = await issuePkiConfirmation(action.nonceAction, action.targetID)
      confirmationNonce = confirmation?.nonce || ''
      if (!confirmationNonce) throw new Error('服务端未返回有效 confirmation nonce')
    }
    const operation = await action.invoke({ reason, confirmationNonce })
    track(operation)
    operationPage.value = 1
    resetAction()
    await loadAll()
  } catch (error) {
    actionError.value = error?.message || '内部 PKI 操作提交失败；请刷新状态后再决定是否重试'
  } finally {
    actionBusy.value = false
  }
}

const backupBusy = ref(false)
const backupMessage = ref('')
const backupMessageKind = ref('success')
const exportPassphrase = ref('')
const exportPassphraseConfirm = ref('')
const exportArchive = ref(null)
const importPassphrase = ref('')
const importReason = ref('')
const importConfirmation = ref('')
let importFile = null

function setBackupMessage(message, kind = 'success') {
  backupMessage.value = message
  backupMessageKind.value = kind
}

async function exportBackup() {
  if (!exportPassphrase.value || exportPassphrase.value !== exportPassphraseConfirm.value) {
    setBackupMessage('两次输入的导出口令不一致', 'error')
    return
  }
  const passphrase = exportPassphrase.value
  exportPassphrase.value = ''
  exportPassphraseConfirm.value = ''
  backupBusy.value = true
  setBackupMessage('')
  try {
    const operation = await exportProtectedPki(passphrase)
    track(operation)
    operationPage.value = 1
    exportArchive.value = protectedArchiveBlob(operation)
    setBackupMessage(exportArchive.value ? '加密备份已生成，请下载并安全保存口令' : '导出操作已受理，可在操作进度中恢复查询')
  } catch {
    setBackupMessage('受保护备份导出失败；口令已从本页清除', 'error')
  } finally {
    backupBusy.value = false
  }
}

function downloadArchive() {
  if (!exportArchive.value) return
  const url = URL.createObjectURL(exportArchive.value)
  const link = document.createElement('a')
  link.href = url
  link.download = `internal-pki-${new Date().toISOString().slice(0, 10)}.nre-pki`
  document.body.appendChild(link)
  link.click()
  link.remove()
  URL.revokeObjectURL(url)
  exportArchive.value = null
  setBackupMessage('备份下载已触发，浏览器内的 archive 已清除')
}

function selectImportFile(event) {
  importFile = event.target.files?.[0] || null
}

function clearImportFile() {
  importFile = null
  backupPanelRef.value?.clearFile?.()
}

async function importBackup() {
  if (!importFile || !importPassphrase.value || !importReason.value.trim() || importConfirmation.value !== 'IMPORT') return
  const archive = importFile
  const passphrase = importPassphrase.value
  importPassphrase.value = ''
  importConfirmation.value = ''
  clearImportFile()
  backupBusy.value = true
  setBackupMessage('')
  try {
    const operation = await importProtectedPki({
      archive,
      passphrase,
      reason: importReason.value.trim()
    })
    track(operation)
    operationPage.value = 1
    importReason.value = ''
    setBackupMessage('受保护备份导入已完成或受理，请核对 operation 与 PKI domain/epoch')
    await loadAll()
  } catch {
    setBackupMessage('受保护备份导入失败；口令已从本页清除，canonical 状态不会由浏览器假定改变', 'error')
  } finally {
    backupBusy.value = false
  }
}

onMounted(loadAll)
</script>

<style scoped>
.pki-page {
  max-width: 1200px;
  margin: 0 auto;
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
  padding-bottom: var(--space-8);
  animation: fadeIn var(--duration-normal) var(--ease-default) both;
}

.notice {
  display: flex;
  gap: var(--space-3);
  align-items: flex-start;
  justify-content: space-between;
  padding: var(--space-3) var(--space-4);
  border-radius: var(--radius-xl);
  border: 1px solid var(--color-border-default);
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.notice__body {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  min-width: 0;
}

.notice--danger {
  border-color: color-mix(in srgb, var(--color-danger) 38%, var(--color-border-default));
  background: color-mix(in srgb, var(--color-danger) 8%, var(--color-bg-surface));
  color: var(--color-danger);
}

.notice--info {
  border-color: color-mix(in srgb, var(--color-primary) 30%, var(--color-border-default));
  background: color-mix(in srgb, var(--color-primary) 7%, var(--color-bg-surface));
}

.text-button {
  border: 0;
  padding: 0;
  background: transparent;
  color: var(--color-primary);
  cursor: pointer;
  font: inherit;
  font-size: var(--text-xs);
  flex-shrink: 0;
}

.pki-dialog-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.pki-dialog-form__hidden-submit {
  position: absolute;
  width: 1px;
  height: 1px;
  opacity: 0;
  pointer-events: none;
}

.pki-target-chip {
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
  padding: 0.7rem 0.85rem;
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-subtle);
  background: color-mix(in srgb, var(--color-bg-subtle) 70%, var(--color-bg-surface));
}

.pki-target-chip__label {
  color: var(--color-text-tertiary);
  font-size: 0.7rem;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.pki-target-chip code {
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}

.pki-field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  min-width: 0;
}

.pki-field__label {
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  font-weight: 600;
}

.pki-field__hint {
  color: var(--color-text-tertiary);
  font-size: var(--text-xs);
  line-height: 1.4;
}

.pki-field__control {
  width: 100%;
  box-sizing: border-box;
  min-height: 2.5rem;
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  padding: 0.55rem 0.75rem;
  font: inherit;
  font-size: var(--text-sm);
  transition: border-color var(--duration-fast) var(--ease-default),
              box-shadow var(--duration-fast) var(--ease-default);
}

.pki-field__control:focus {
  outline: none;
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.pki-field__control--textarea {
  min-height: 96px;
  resize: vertical;
  line-height: 1.45;
}

.pki-field__control:disabled {
  opacity: 0.65;
  cursor: not-allowed;
}

.one-time-secret {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-4);
  border: 1px solid color-mix(in srgb, var(--color-warning) 55%, var(--color-border-default));
  border-radius: var(--radius-lg);
  background: color-mix(in srgb, var(--color-warning) 8%, var(--color-bg-surface));
}

.one-time-secret code {
  overflow-wrap: anywhere;
  user-select: all;
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  padding: 0.55rem 0.7rem;
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
}

.one-time-secret span {
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
}

.mono {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}

.danger-text {
  color: var(--color-danger) !important;
  margin: 0;
  font-size: var(--text-sm);
}

/* Align with certs/rules/dashboard wide-screen steps */
@media (min-width: 1920px) {
  .pki-page { max-width: 1600px; }
}

@media (min-width: 2560px) {
  .pki-page { max-width: 2000px; }
}

@media (max-width: 640px) {
  .pki-page {
    gap: var(--space-3);
    padding-bottom: var(--space-6);
  }

  .notice {
    flex-direction: column;
    align-items: stretch;
    gap: var(--space-2);
  }

  .text-button {
    align-self: flex-end;
    min-height: 2rem;
  }
}
</style>
