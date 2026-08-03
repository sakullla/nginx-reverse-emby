<template>
  <div class="pki-page">
    <header class="pki-header">
      <div>
        <div class="pki-header__eyebrow">内部隧道安全域</div>
        <h1>内部 PKI</h1>
        <p>管理 relay mTLS 身份、CA 生命周期、撤销、审计和受保护迁移备份。</p>
      </div>
      <div class="pki-header__actions">
        <RouterLink class="btn btn-secondary" to="/certs">公网证书</RouterLink>
        <button class="btn btn-secondary" :disabled="loading" @click="loadAll">刷新</button>
        <button class="btn btn-primary" @click="openEnrollment">创建登记令牌</button>
      </div>
    </header>

    <div class="domain-boundary" role="note">
      <strong>与公网证书分域</strong>
      <span>这里的 CA 和端点证书只用于内部 relay TLS/TCP/QUIC；网站 ACME 与上传证书仍在“公网证书”中管理。</span>
    </div>

    <div v-if="pageError" class="notice notice--danger" role="alert">
      <span>{{ pageError }}</span>
      <button class="text-button" @click="loadAll">重试</button>
    </div>

    <div v-if="overview.recovery_blocker" class="notice notice--danger" role="alert">
      <strong>{{ overview.recovery_blocker.message }}</strong>
      <span>{{ overview.recovery_blocker.recovery_hint }}</span>
    </div>

    <section class="summary-grid" aria-label="内部 PKI 概览">
      <article class="summary-card">
        <span>PKI Domain</span>
        <strong class="mono">{{ overview.pki_domain_id || '尚未初始化' }}</strong>
      </article>
      <article class="summary-card">
        <span>Epoch / 安全修订</span>
        <strong>{{ overview.pki_epoch ?? '—' }} / {{ overview.security_revision ?? '—' }}</strong>
      </article>
      <article class="summary-card">
        <span>身份 / 证书</span>
        <strong>{{ overview.identity_count ?? identities.length }} / {{ overview.certificate_count ?? certificates.length }}</strong>
      </article>
      <article class="summary-card">
        <span>运行状态</span>
        <strong :class="statusClass(overview.runtime_status)">{{ overview.runtime_status || 'unknown' }}</strong>
      </article>
    </section>

    <section v-if="operations.length" class="panel" aria-label="内部 PKI 操作">
      <div class="section-heading">
        <div>
          <h2>操作进度</h2>
          <p>PKI operation 独立轮询；刷新页面后会按 operation ID 恢复，不提供 revision retry/rollback/dismiss。</p>
        </div>
      </div>
      <div class="operation-list">
        <article v-for="operation in operations" :key="operation.id" class="operation-row">
          <div>
            <strong>{{ operationLabel(operation.kind) }}</strong>
            <span class="mono">{{ operation.target_id || operation.id }}</span>
          </div>
          <div class="operation-row__state">
            <span class="status-pill" :class="statusClass(operation.state)">{{ operation.state }}</span>
            <span v-if="operation.phase">{{ operation.phase }}</span>
            <span v-if="operation.last_error" class="danger-text">{{ operation.last_error }}</span>
            <span v-if="operationErrors[operation.id]" class="danger-text">
              状态查询失败（{{ operationErrors[operation.id].status || 'network' }}）：{{ operationErrors[operation.id].message }}
            </span>
          </div>
          <div class="operation-row__actions">
            <button class="text-button" @click="refreshOperation(operation.id)">查询状态</button>
            <button v-if="operation.terminal || operationErrors[operation.id]?.status === 404" class="text-button text-button--muted" @click="forgetOperation(operation.id)">仅从本机列表移除</button>
          </div>
        </article>
      </div>
    </section>

    <section class="panel">
      <div class="section-heading">
        <div>
          <h2>告警与处置</h2>
          <p>告警事实由服务端派生；浏览器不重新计算安全级别。</p>
        </div>
        <div class="section-actions">
          <button class="btn btn-secondary" @click="openDomainAction('rotate-ca')">日常 CA 轮转</button>
          <button class="btn btn-danger" @click="openDomainAction('emergency-ca')">紧急 CA 轮转</button>
          <button class="btn btn-danger" @click="openDomainAction('activate')">迁移激活</button>
        </div>
      </div>
      <div v-if="alerts.length" class="alert-list">
        <article v-for="alert in alerts" :key="alertField(alert, 'id')" class="alert-row" :class="`alert-row--${String(alertField(alert, 'level')).toLowerCase()}`">
          <div>
            <strong>{{ alertField(alert, 'kind') || 'PKI alert' }}</strong>
            <span>{{ alertField(alert, 'object_type') }} · <span class="mono">{{ alertField(alert, 'object_id') }}</span></span>
          </div>
          <p>{{ alertField(alert, 'reason') }}</p>
          <time>{{ formatDate(alertField(alert, 'last_seen')) }}</time>
        </article>
      </div>
      <p v-else class="empty-state">当前没有内部 PKI 告警。</p>
    </section>

    <section class="panel">
      <div class="section-heading">
        <div>
          <h2>端点身份与证书</h2>
          <p>identity、owner、用途、链与 generation、有效期、轮转、撤销和最近错误均来自内部 PKI 资源。</p>
        </div>
      </div>
      <div class="table-wrap">
        <table class="data-table">
          <thead>
            <tr>
              <th>Identity / Owner</th>
              <th>Purpose / Chain</th>
              <th>Serial / Fingerprint</th>
              <th>有效期 / Next action</th>
              <th>Rotation / Revocation / Error</th>
              <th>处置</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in identityRows" :key="row.id">
              <td>
                <strong class="mono">{{ row.id }}</strong>
                <span>{{ row.owner }}</span>
              </td>
              <td>
                <strong>{{ row.purpose }}</strong>
                <span>CA generation {{ row.caGeneration }}</span>
              </td>
              <td>
                <span class="mono">{{ row.serial }}</span>
                <span class="mono fingerprint">{{ row.fingerprint }}</span>
              </td>
              <td>
                <span>{{ formatDate(row.notBefore) }} → {{ formatDate(row.notAfter) }}</span>
                <span>{{ row.nextAction }}</span>
              </td>
              <td>
                <span>{{ row.rotationPhase }}</span>
                <span :class="row.revoked ? 'danger-text' : ''">{{ row.revocation }}</span>
                <span v-if="row.latestError" class="danger-text">{{ row.latestError }}</span>
              </td>
              <td>
                <button class="text-button" @click="openIdentityAction('force-rotate', row)">强制换证</button>
                <button class="text-button text-button--danger" :disabled="row.revoked" @click="openIdentityAction('revoke', row)">撤销</button>
              </td>
            </tr>
            <tr v-if="!identityRows.length">
              <td colspan="6" class="empty-cell">暂无内部 PKI 身份。</td>
            </tr>
          </tbody>
        </table>
      </div>
    </section>

    <section class="panel split-panel">
      <div>
        <div class="section-heading">
          <div>
            <h2>CA generations</h2>
            <p>活动、退役中和历史根仅在内部信任域中使用。</p>
          </div>
        </div>
        <div class="compact-list">
          <article v-for="authority in authorities" :key="authority.id" class="compact-row">
            <div>
              <strong>Generation {{ authority.generation }}</strong>
              <span class="mono">{{ authority.fingerprint_sha256 || '—' }}</span>
            </div>
            <div>
              <span class="status-pill" :class="statusClass(authority.status)">{{ authority.status }}</span>
              <span>{{ formatDate(authority.not_after) }}</span>
            </div>
          </article>
          <p v-if="!authorities.length" class="empty-state">暂无 CA 记录。</p>
        </div>
      </div>

      <div>
        <div class="section-heading">
          <div>
            <h2>受保护备份</h2>
            <p>口令只驻留在本次表单与 request body；成功或失败后立即清空。口令丢失无法恢复。</p>
          </div>
        </div>
        <form class="backup-form" @submit.prevent="exportBackup">
          <label>
            导出口令
            <input v-model="exportPassphrase" data-test="export-passphrase" class="input" type="password" autocomplete="new-password" required>
          </label>
          <label>
            再次输入
            <input v-model="exportPassphraseConfirm" class="input" type="password" autocomplete="new-password" required>
          </label>
          <button class="btn btn-primary" :disabled="backupBusy">{{ backupBusy ? '处理中…' : '生成加密备份' }}</button>
          <button v-if="exportArchive" class="btn btn-secondary" type="button" @click="downloadArchive">下载后清除</button>
        </form>
        <form class="backup-form backup-form--import" @submit.prevent="importBackup">
          <label>
            加密备份文件
            <input ref="importFileInput" data-test="import-archive" class="input" type="file" accept=".nre-pki,.bin,application/octet-stream" required @change="selectImportFile">
          </label>
          <label>
            导入口令
            <input v-model="importPassphrase" data-test="import-passphrase" class="input" type="password" autocomplete="new-password" required>
          </label>
          <label>
            原因
            <input v-model="importReason" class="input" required placeholder="例如：计划迁移恢复">
          </label>
          <label>
            输入 IMPORT 明确确认
            <input v-model="importConfirmation" class="input" required autocomplete="off">
          </label>
          <button class="btn btn-danger" :disabled="backupBusy || importConfirmation !== 'IMPORT'">导入受保护备份</button>
        </form>
        <p v-if="backupMessage" :class="backupMessageKind === 'error' ? 'danger-text' : 'success-text'" role="status">{{ backupMessage }}</p>
      </div>
    </section>

    <section class="panel">
      <div class="section-heading">
        <div>
          <h2>安全审计</h2>
          <p>按服务端 canonical 字段查询签发、续签、轮转、撤销、拒绝、备份与恢复事件。</p>
        </div>
      </div>
      <form class="audit-filters" @submit.prevent="loadEvents">
        <input v-model="eventFilters.type" class="input" placeholder="事件类型">
        <input v-model="eventFilters.identity_id" class="input" placeholder="Identity ID">
        <input v-model="eventFilters.source" class="input" placeholder="来源">
        <input v-model="eventFilters.result" class="input" placeholder="结果">
        <button class="btn btn-secondary">查询</button>
      </form>
      <div class="compact-list">
        <article v-for="event in events" :key="event.id" class="compact-row audit-row">
          <div>
            <strong>{{ event.type }}</strong>
            <span>{{ event.object_type }} · <span class="mono">{{ event.object_id }}</span></span>
            <span v-if="event.reason">{{ event.reason }}</span>
          </div>
          <div>
            <span :class="event.result === 'failed' || event.result === 'rejected' ? 'danger-text' : ''">{{ event.result }}</span>
            <span>{{ event.source }}<template v-if="event.operator_id"> / {{ event.operator_id }}</template></span>
            <time>{{ formatDate(event.occurred_at) }}</time>
          </div>
        </article>
        <p v-if="!events.length" class="empty-state">当前筛选没有审计事件。</p>
      </div>
    </section>

    <div v-if="enrollmentOpen" class="modal-backdrop" data-test="enrollment-dialog" @click.self="closeEnrollment">
      <section class="modal-card" role="dialog" aria-modal="true" aria-labelledby="enrollment-title">
        <h2 id="enrollment-title">创建一次性登记令牌</h2>
        <template v-if="!enrollmentToken">
          <label>
            登记类型
            <select v-model="enrollmentScope" class="input">
              <option value="new_agent">新节点</option>
              <option value="bound_reenrollment">绑定现有节点</option>
            </select>
          </label>
          <label v-if="enrollmentScope === 'bound_reenrollment'">
            Agent ID
            <input v-model="enrollmentAgentID" class="input" autocomplete="off" required>
          </label>
          <p v-if="enrollmentError" class="danger-text">{{ enrollmentError }}</p>
          <div class="modal-actions">
            <button class="btn btn-secondary" @click="closeEnrollment">取消</button>
            <button class="btn btn-primary" :disabled="enrollmentBusy || (enrollmentScope === 'bound_reenrollment' && !enrollmentAgentID.trim())" @click="createEnrollment">生成令牌</button>
          </div>
        </template>
        <template v-else>
          <div class="one-time-secret" data-test="enrollment-secret">
            <strong>仅显示一次</strong>
            <code>{{ enrollmentToken.token }}</code>
            <span>有效期至 {{ formatDate(enrollmentToken.expires_at) }}。关闭后浏览器将清除此值。</span>
          </div>
          <div class="modal-actions">
            <button class="btn btn-secondary" @click="copyEnrollmentToken">复制</button>
            <button class="btn btn-primary" @click="closeEnrollment">我已保存并关闭</button>
          </div>
        </template>
      </section>
    </div>

    <div v-if="pendingAction" class="modal-backdrop" data-test="action-dialog" @click.self="closeAction">
      <form class="modal-card" role="dialog" aria-modal="true" @submit.prevent="submitAction">
        <h2>{{ pendingAction.label }}</h2>
        <p>对象：<strong class="mono">{{ pendingAction.targetLabel }}</strong></p>
        <label>
          操作原因
          <textarea v-model="actionReason" data-test="action-reason" class="input textarea" required></textarea>
        </label>
        <label>
          输入 <strong>{{ pendingAction.confirmText }}</strong> 明确确认
          <input v-model="actionConfirmation" data-test="action-confirmation" class="input" autocomplete="off" required>
        </label>
        <p v-if="actionError" class="danger-text">{{ actionError }}</p>
        <div class="modal-actions">
          <button class="btn btn-secondary" type="button" @click="closeAction">取消</button>
          <button class="btn btn-danger" :disabled="actionBusy || actionConfirmation !== pendingAction.confirmText">{{ actionBusy ? '提交中…' : '确认执行' }}</button>
        </div>
      </form>
    </div>
  </div>
</template>

<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import { RouterLink } from 'vue-router'
import {
  PKI_CONFIRMATION_ACTION,
  activatePkiMigration,
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
import { usePkiOperations } from '../hooks/usePkiOperations'

const loading = ref(false)
const pageError = ref('')
const overview = ref({})
const authorities = ref([])
const identities = ref([])
const certificates = ref([])
const alerts = ref([])
const events = ref([])
const eventFilters = reactive({ type: '', identity_id: '', source: '', result: '' })

const {
  operations,
  errors: operationErrors,
  track,
  refresh: refreshOperation,
  forget: forgetOperation
} = usePkiOperations()

function field(value, snake, pascal) {
  return value?.[snake] ?? value?.[pascal] ?? ''
}

function alertField(alert, name) {
  const pascal = name.split('_').map(part => part.charAt(0).toUpperCase() + part.slice(1)).join('')
  return field(alert, name, pascal)
}

function formatDate(value) {
  if (!value) return '—'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return String(value)
  return parsed.toLocaleString('zh-CN', { hour12: false })
}

function statusClass(status) {
  const value = String(status || '').toLowerCase()
  if (['active', 'healthy', 'succeeded', 'success'].includes(value)) return 'status--success'
  if (['failed', 'revoked', 'critical', 'unavailable'].includes(value)) return 'status--danger'
  if (['blocked', 'warning', 'retiring', 'degraded'].includes(value)) return 'status--warning'
  return 'status--neutral'
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

const identityRows = computed(() => identities.value.map(identity => {
  const certificate = certificates.value.find(item => item.id === identity.current_certificate_id)
    || certificates.value.find(item => item.identity_id === identity.id && item.status === 'active')
    || certificates.value.find(item => item.identity_id === identity.id)
    || {}
  const ownerParts = [identity.kind, identity.agent_id, identity.listener_id].filter(Boolean)
  const revoked = identity.state === 'revoked' || certificate.status === 'revoked' || Boolean(identity.revoked_at || certificate.revoked_at)
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
    revocation: revoked ? `已撤销${identity.revoked_reason || certificate.revoked_reason ? `：${identity.revoked_reason || certificate.revoked_reason}` : ''}` : (identity.state || certificate.status || '—'),
    latestError: identity.latest_error || certificate.latest_error || identity.last_error || certificate.last_error || ''
  }
}))

async function loadEvents() {
  try {
    events.value = await fetchPkiEvents(eventFilters)
  } catch (error) {
    pageError.value = error?.message || '内部 PKI 审计查询失败'
  }
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
    pageError.value = error?.message || '内部 PKI 数据暂时不可用'
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
  enrollmentOpen.value = false
  enrollmentToken.value = null
  enrollmentAgentID.value = ''
  enrollmentError.value = ''
}

async function createEnrollment() {
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
        nonceAction: '',
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
      nonceAction: '',
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
    },
    activate: {
      kind,
      label: '迁移激活',
      targetID: 'domain',
      targetLabel: overview.value.pki_domain_id || 'domain',
      confirmText: 'ACTIVATE',
      nonceAction: PKI_CONFIRMATION_ACTION.activate,
      invoke: activatePkiMigration
    }
  }
  pendingAction.value = actions[kind]
}

function closeAction() {
  pendingAction.value = null
  actionReason.value = ''
  actionConfirmation.value = ''
  actionError.value = ''
}

async function submitAction() {
  const action = pendingAction.value
  if (!action || actionConfirmation.value !== action.confirmText || !actionReason.value.trim()) return
  actionBusy.value = true
  actionError.value = ''
  try {
    let confirmationNonce = ''
    if (action.nonceAction) {
      const confirmation = await issuePkiConfirmation(action.nonceAction, action.targetID)
      confirmationNonce = confirmation?.nonce || ''
      if (!confirmationNonce) throw new Error('服务端未返回有效 confirmation nonce')
    }
    const operation = await action.invoke({ reason: actionReason.value.trim(), confirmationNonce })
    track(operation)
    closeAction()
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
const importFileInput = ref(null)
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

async function importBackup() {
  if (!importFile || !importPassphrase.value || !importReason.value.trim() || importConfirmation.value !== 'IMPORT') return
  const passphrase = importPassphrase.value
  importPassphrase.value = ''
  importConfirmation.value = ''
  backupBusy.value = true
  setBackupMessage('')
  try {
    const operation = await importProtectedPki({
      archive: importFile,
      passphrase,
      reason: importReason.value.trim()
    })
    track(operation)
    importReason.value = ''
    importFile = null
    if (importFileInput.value) importFileInput.value.value = ''
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
.pki-page { display: flex; flex-direction: column; gap: var(--space-5); padding-bottom: var(--space-8); }
.pki-header { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--space-5); flex-wrap: wrap; }
.pki-header h1 { margin: 0.1rem 0 0.35rem; font-size: 1.75rem; color: var(--color-text-primary); }
.pki-header p, .section-heading p { margin: 0; color: var(--color-text-tertiary); font-size: var(--text-sm); line-height: 1.5; }
.pki-header__eyebrow { color: var(--color-primary); font-size: var(--text-xs); font-weight: 700; letter-spacing: 0.08em; text-transform: uppercase; }
.pki-header__actions, .section-actions, .modal-actions { display: flex; gap: var(--space-2); flex-wrap: wrap; }
.domain-boundary, .notice { display: flex; gap: var(--space-3); align-items: flex-start; padding: var(--space-3) var(--space-4); border-radius: var(--radius-lg); border: 1px solid var(--color-border-default); background: var(--color-bg-subtle); color: var(--color-text-secondary); font-size: var(--text-sm); }
.domain-boundary strong { color: var(--color-primary); white-space: nowrap; }
.notice--danger { border-color: color-mix(in srgb, var(--color-danger) 38%, var(--color-border-default)); background: color-mix(in srgb, var(--color-danger) 8%, var(--color-bg-surface)); color: var(--color-danger); }
.summary-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--space-3); }
.summary-card, .panel { border: 1px solid var(--color-border-subtle); border-radius: var(--radius-xl); background: var(--color-bg-surface); box-shadow: var(--shadow-xs); }
.summary-card { display: flex; flex-direction: column; gap: var(--space-2); padding: var(--space-4); min-width: 0; }
.summary-card span { color: var(--color-text-tertiary); font-size: var(--text-xs); }
.summary-card strong { color: var(--color-text-primary); font-size: var(--text-base); overflow-wrap: anywhere; }
.panel { padding: var(--space-5); }
.section-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: var(--space-4); margin-bottom: var(--space-4); flex-wrap: wrap; }
.section-heading h2 { margin: 0 0 0.25rem; color: var(--color-text-primary); font-size: var(--text-lg); }
.operation-list, .compact-list, .alert-list { display: flex; flex-direction: column; gap: var(--space-2); }
.operation-row, .compact-row, .alert-row { display: grid; align-items: center; gap: var(--space-3); padding: var(--space-3); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-lg); }
.operation-row { grid-template-columns: minmax(150px, 0.8fr) minmax(220px, 1.6fr) auto; }
.operation-row > div, .compact-row > div, .alert-row > div { display: flex; flex-direction: column; gap: 0.2rem; min-width: 0; }
.operation-row span, .compact-row span, .alert-row span, .alert-row p, .alert-row time { color: var(--color-text-secondary); font-size: var(--text-xs); margin: 0; }
.operation-row__state { align-items: flex-start; }
.operation-row__actions { align-items: flex-end; }
.status-pill { display: inline-flex; width: fit-content; padding: 0.15rem 0.5rem; border-radius: var(--radius-full); background: var(--color-bg-subtle); font-weight: 600; }
.status--success { color: var(--color-success) !important; }
.status--danger { color: var(--color-danger) !important; }
.status--warning { color: var(--color-warning) !important; }
.status--neutral { color: var(--color-text-secondary) !important; }
.alert-row { grid-template-columns: minmax(150px, 0.8fr) 2fr auto; }
.alert-row--critical { border-color: color-mix(in srgb, var(--color-danger) 40%, var(--color-border-default)); }
.alert-row--warning { border-color: color-mix(in srgb, var(--color-warning) 40%, var(--color-border-default)); }
.table-wrap { overflow-x: auto; }
.data-table { width: 100%; border-collapse: collapse; min-width: 1100px; }
.data-table th { text-align: left; color: var(--color-text-tertiary); font-size: var(--text-xs); font-weight: 600; border-bottom: 1px solid var(--color-border-default); padding: var(--space-2) var(--space-3); }
.data-table td { vertical-align: top; border-bottom: 1px solid var(--color-border-subtle); padding: var(--space-3); color: var(--color-text-secondary); font-size: var(--text-xs); }
.data-table td > span, .data-table td > strong { display: block; margin-bottom: 0.25rem; }
.data-table tbody tr:last-child td { border-bottom: 0; }
.fingerprint { max-width: 180px; overflow: hidden; text-overflow: ellipsis; }
.empty-cell, .empty-state { text-align: center; color: var(--color-text-tertiary); font-size: var(--text-sm); padding: var(--space-5); margin: 0; }
.split-panel { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); gap: var(--space-6); }
.compact-row { grid-template-columns: minmax(0, 1fr) auto; }
.audit-row { grid-template-columns: minmax(0, 1fr) minmax(180px, auto); }
.audit-row > div:last-child { align-items: flex-end; }
.backup-form { display: grid; grid-template-columns: 1fr 1fr; gap: var(--space-3); padding-bottom: var(--space-4); }
.backup-form--import { border-top: 1px solid var(--color-border-subtle); padding-top: var(--space-4); }
.backup-form label, .modal-card label { display: flex; flex-direction: column; gap: var(--space-1); color: var(--color-text-secondary); font-size: var(--text-xs); }
.audit-filters { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)) auto; gap: var(--space-2); margin-bottom: var(--space-4); }
.input { width: 100%; box-sizing: border-box; border: 1px solid var(--color-border-default); border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); padding: 0.55rem 0.7rem; font: inherit; }
.textarea { min-height: 88px; resize: vertical; }
.btn { display: inline-flex; align-items: center; justify-content: center; border: 1px solid transparent; border-radius: var(--radius-md); padding: 0.55rem 0.8rem; font: inherit; font-size: var(--text-sm); font-weight: 600; cursor: pointer; text-decoration: none; }
.btn:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-primary { color: white; background: var(--color-primary); }
.btn-secondary { color: var(--color-text-primary); background: var(--color-bg-surface); border-color: var(--color-border-default); }
.btn-danger { color: white; background: var(--color-danger); }
.text-button { border: 0; padding: 0; background: transparent; color: var(--color-primary); cursor: pointer; font: inherit; font-size: var(--text-xs); }
.text-button--danger, .danger-text { color: var(--color-danger) !important; }
.text-button--muted { color: var(--color-text-tertiary); }
.success-text { color: var(--color-success); }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace; }
.modal-backdrop { position: fixed; inset: 0; z-index: var(--z-modal, 1100); display: grid; place-items: center; padding: var(--space-4); background: rgba(0, 0, 0, 0.55); }
.modal-card { width: min(520px, 100%); box-sizing: border-box; display: flex; flex-direction: column; gap: var(--space-4); padding: var(--space-5); border-radius: var(--radius-xl); background: var(--color-bg-surface); box-shadow: var(--shadow-xl); }
.modal-card h2, .modal-card p { margin: 0; }
.modal-actions { justify-content: flex-end; }
.one-time-secret { display: flex; flex-direction: column; gap: var(--space-2); padding: var(--space-4); border: 1px solid var(--color-warning); border-radius: var(--radius-lg); background: color-mix(in srgb, var(--color-warning) 8%, var(--color-bg-surface)); }
.one-time-secret code { overflow-wrap: anywhere; user-select: all; color: var(--color-text-primary); }
.one-time-secret span { color: var(--color-text-secondary); font-size: var(--text-xs); }
@media (max-width: 1000px) { .summary-grid { grid-template-columns: repeat(2, 1fr); } .split-panel { grid-template-columns: 1fr; } .audit-filters { grid-template-columns: repeat(2, 1fr); } }
@media (max-width: 680px) { .summary-grid { grid-template-columns: 1fr; } .operation-row, .alert-row { grid-template-columns: 1fr; } .operation-row__actions, .audit-row > div:last-child { align-items: flex-start; } .backup-form, .audit-filters { grid-template-columns: 1fr; } .domain-boundary { flex-direction: column; } }
</style>
