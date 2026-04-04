<template>
  <div class="certs-page">
    <div class="certs-page__header">
      <div>
        <h1 class="certs-page__title">统一证书</h1>
        <p class="certs-page__subtitle">
          <template v-if="selectedAgentId">
            {{ certificates.length }} 项证书 · {{ activeCount }} 生效中
          </template>
          <template v-else>
            请先选择一个节点
          </template>
        </p>
      </div>
      <button v-if="selectedAgentId" class="btn btn-primary" @click="showAddForm = true">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
          <line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>
        </svg>
        添加证书
      </button>
    </div>

    <div v-if="!selectedAgentId" class="certs-page__prompt">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
        <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
      </svg>
      <p>请从侧边栏选择一个节点</p>
    </div>

    <div v-else-if="isLoading" class="certs-page__loading">
      <div class="spinner"></div>
    </div>

    <template v-else-if="certificates.length">
      <!-- Search toolbar -->
      <div class="certs-page__toolbar">
        <input v-model="searchQuery" class="search-input" placeholder="搜索域名 / 标签 / #id=...">
      </div>

      <!-- No search results -->
      <div v-if="!filteredCerts.length" class="certs-page__empty">
        <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
        </svg>
        <p>没有匹配的证书</p>
      </div>

      <!-- Cert grid -->
      <div v-else class="cert-grid">
        <div v-for="cert in filteredCerts" :key="cert.id" class="cert-card">
          <div class="cert-card__header">
            <div class="cert-card__icon">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
                <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
              </svg>
            </div>
            <span class="cert-card__id">#{{ cert.id }}</span>
            <div class="cert-card__status" :class="`cert-card__status--${cert.status || 'inactive'}`">
              {{ getStatusLabel(cert) }}
            </div>
          </div>
          <div class="cert-card__domain">{{ cert.domain }}</div>
          <div class="cert-card__meta">
            <span class="cert-card__scope">{{ cert.scope === 'ip' ? 'IP 证书' : '域名证书' }}</span>
            <span class="cert-card__issuer">{{ getIssuerLabel(cert.issuer_mode) }}</span>
            <span v-if="cert.last_issue_at" class="cert-card__date">{{ formatDate(cert.last_issue_at) }}</span>
          </div>
          <p v-if="cert.last_error" class="cert-card__error">{{ cert.last_error }}</p>
          <div class="cert-card__tags">
            <span v-for="tag in (cert.tags || [])" :key="tag" class="tag">{{ tag }}</span>
          </div>
          <div class="cert-card__actions">
            <button v-if="cert.status === 'pending' || cert.status === 'error'" class="btn btn-primary btn-sm" @click="issueCert(cert)">签发</button>
            <button class="btn btn-secondary btn-sm" @click="startEdit(cert)">编辑</button>
            <button class="btn btn-danger btn-sm" @click="startDelete(cert)">删除</button>
          </div>
        </div>
      </div>
    </template>

    <!-- Empty state (no certificates at all) -->
    <div v-else class="certs-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
        <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
      </svg>
      <p>暂无证书</p>
      <button class="btn btn-primary" @click="showAddForm = true">添加第一个证书</button>
    </div>

    <!-- Add/Edit Form Modal -->
    <Teleport to="body">
      <div v-if="showAddForm || editingCert" class="modal-overlay" @click.self="closeForm">
        <div class="modal modal--large">
          <div class="modal__header">
            <span>{{ editingCert ? '编辑证书' : '添加证书' }}</span>
            <button class="modal__close" @click="closeForm">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18"/>
                <line x1="6" y1="6" x2="18" y2="18"/>
              </svg>
            </button>
          </div>
          <div class="modal__body">
            <CertificateForm :initial-data="editingCert" :agent-id="agentId" @success="closeForm" />
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Delete Modal -->
    <Teleport to="body">
      <div v-if="deletingCert" class="modal-overlay" @click.self="deletingCert = null">
        <div class="modal">
          <div class="modal__header">
            <span>确认删除</span>
            <button class="modal__close" @click="deletingCert = null">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="18" y1="6" x2="6" y2="18"/>
                <line x1="6" y1="6" x2="18" y2="18"/>
              </svg>
            </button>
          </div>
          <div class="modal__body">
            <p>确定删除证书 <strong>{{ deletingCert.domain }}</strong>？</p>
          </div>
          <div class="modal__footer">
            <button class="btn btn-secondary" @click="deletingCert = null">取消</button>
            <button class="btn btn-danger" @click="confirmDelete">删除</button>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { ref, computed } from 'vue'
import { useRoute } from 'vue-router'
import { useAgent } from '../context/AgentContext'
import { useCertificates, useDeleteCertificate, useIssueCertificate } from '../hooks/useCertificates'
import CertificateForm from '../components/CertificateForm.vue'

const route = useRoute()
const { selectedAgentId } = useAgent()

const agentId = computed(() => route.query.agentId || selectedAgentId.value)

const { data: _certsData, isLoading } = useCertificates(agentId)
const deleteCertificate = useDeleteCertificate(agentId)
const issueCertificate = useIssueCertificate(agentId)
const certificates = computed(() => _certsData.value ?? [])
const showAddForm = ref(false)
const editingCert = ref(null)
const deletingCert = ref(null)

// Search
const searchQuery = ref('')

const filteredCerts = computed(() => {
  const raw = searchQuery.value.trim()
  if (!raw) return certificates.value
  const idMatch = raw.match(/^#id=(\S+)$/)
  if (idMatch) return certificates.value.filter(c => String(c.id) === idMatch[1])
  const q = raw.toLowerCase()
  return certificates.value.filter(c =>
    c.domain.toLowerCase().includes(q) ||
    (c.tags || []).some(tag => String(tag).toLowerCase().includes(q))
  )
})

function formatDate(dateStr) {
  if (!dateStr) return ''
  try { return new Date(dateStr).toLocaleString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) }
  catch { return dateStr }
}

const activeCount = computed(() => certificates.value.filter(c => c.enabled && c.status === 'active').length)

function getStatusLabel(cert) {
  if (!cert.enabled) return '已禁用'
  if (cert.status === 'active') return '生效中'
  if (cert.status === 'pending') return '待签发'
  if (cert.status === 'error') return '签发失败'
  return '未知'
}

function getIssuerLabel(mode) {
  if (mode === 'master_cf_dns') return 'Master DNS'
  if (mode === 'local_http01') return '本地 HTTP-01'
  return mode
}

function issueCert(cert) {
  issueCertificate.mutate(cert.id)
}

function startEdit(cert) {
  editingCert.value = cert
}

function startDelete(cert) {
  deletingCert.value = cert
}

function closeForm() {
  showAddForm.value = false
  editingCert.value = null
}

function confirmDelete() {
  if (deletingCert.value) {
    deleteCertificate.mutate(deletingCert.value.id)
  }
  deletingCert.value = null
}
</script>

<style scoped>
.certs-page { max-width: 1200px; margin: 0 auto; }
.certs-page__header { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 1.5rem; gap: 1rem; }
.certs-page__title { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.certs-page__subtitle { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.certs-page__loading, .certs-page__empty { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }
.certs-page__toolbar { margin-bottom: 1.5rem; }
.search-input { width: 100%; padding: 0.625rem 0.875rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s; box-sizing: border-box; }
.search-input:focus { border-color: var(--color-primary); }
.search-input::placeholder { color: var(--color-text-muted); }
.cert-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 1rem; }
.cert-card { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-xl); padding: 1.25rem; display: flex; flex-direction: column; gap: 0.75rem; }
.cert-card__header { display: flex; align-items: center; justify-content: space-between; }
.cert-card__icon { color: var(--color-success); }
.cert-card__id { font-size: 0.75rem; font-family: var(--font-mono); color: var(--color-text-tertiary); }
.cert-card__status { font-size: 0.75rem; font-weight: 600; padding: 2px 8px; border-radius: var(--radius-full); }
.cert-card__status--active { background: var(--color-success-50); color: var(--color-success); }
.cert-card__status--pending { background: var(--color-warning-50); color: var(--color-warning); }
.cert-card__status--error { background: var(--color-danger-50); color: var(--color-danger); }
.cert-card__status--inactive { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.cert-card__domain { font-size: 1rem; font-weight: 600; color: var(--color-text-primary); font-family: var(--font-mono); }
.cert-card__meta { display: flex; gap: 0.5rem; font-size: 0.75rem; color: var(--color-text-tertiary); }
.cert-card__scope { background: var(--color-bg-subtle); padding: 1px 6px; border-radius: var(--radius-sm); }
.cert-card__error { font-size: 0.75rem; color: var(--color-danger); background: var(--color-danger-50); padding: 0.25rem 0.5rem; border-radius: var(--radius-sm); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cert-card__date { font-size: 0.75rem; color: var(--color-text-tertiary); }
.cert-card__tags { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.cert-card__actions { display: flex; gap: 0.5rem; flex-wrap: wrap; margin-top: auto; }
.tag { font-size: 0.75rem; padding: 2px 8px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
.modal-overlay { position: fixed; inset: 0; background: rgba(37,23,54,0.4); backdrop-filter: blur(8px); z-index: var(--z-modal); display: flex; align-items: center; justify-content: center; padding: var(--space-4); }
.modal { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-3xl); box-shadow: var(--shadow-2xl); width: min(480px, 90vw); max-height: calc(100vh - var(--space-8)); display: flex; flex-direction: column; overflow: hidden; }
.modal--large { width: min(600px, 92vw); }
.modal__header { display: flex; align-items: center; justify-content: space-between; gap: var(--space-4); padding: var(--space-5) var(--space-6); border-bottom: 1px solid var(--color-border-subtle); flex-shrink: 0; background: var(--gradient-soft); font-weight: 600; font-size: var(--text-lg); color: var(--color-text-primary); }
.modal__body { padding: var(--space-6); overflow-y: auto; flex: 1; display: flex; flex-direction: column; gap: var(--space-5); }
.modal__footer { padding: var(--space-4) var(--space-6); display: flex; justify-content: flex-end; gap: var(--space-3); border-top: 1px solid var(--color-border-subtle); flex-shrink: 0; }
.modal__close { display: flex; align-items: center; justify-content: center; width: 36px; height: 36px; border-radius: var(--radius-full); color: var(--color-text-tertiary); transition: all var(--duration-normal) var(--ease-bounce); flex-shrink: 0; border: none; background: transparent; cursor: pointer; }
.modal__close:hover { background: var(--color-danger-50); color: var(--color-danger); transform: rotate(90deg); }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
.btn-sm { padding: 0.25rem 0.75rem; font-size: 0.8125rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
