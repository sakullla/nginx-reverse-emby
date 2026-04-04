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

    <div v-else-if="!certificates.length" class="certs-page__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
        <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
      </svg>
      <p>暂无证书</p>
      <button class="btn btn-primary" @click="showAddForm = true">添加第一个证书</button>
    </div>

    <div v-else class="cert-grid">
      <div v-for="cert in certificates" :key="cert.id" class="cert-card">
        <div class="cert-card__header">
          <div class="cert-card__icon">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
              <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
            </svg>
          </div>
          <div class="cert-card__status" :class="`cert-card__status--${cert.status || 'inactive'}`">
            {{ getStatusLabel(cert) }}
          </div>
        </div>
        <div class="cert-card__domain">{{ cert.domain }}</div>
        <div class="cert-card__meta">
          <span class="cert-card__scope">{{ cert.scope === 'ip' ? 'IP 证书' : '域名证书' }}</span>
          <span class="cert-card__issuer">{{ getIssuerLabel(cert.issuer_mode) }}</span>
        </div>
        <div class="cert-card__tags">
          <span v-for="tag in (cert.tags || []).slice(0, 3)" :key="tag" class="tag">{{ tag }}</span>
        </div>
        <div class="cert-card__actions">
          <button v-if="cert.status === 'pending' || cert.status === 'error'" class="btn btn-primary btn-sm" @click="issueCert(cert)">签发</button>
          <button class="btn btn-secondary btn-sm" @click="startEdit(cert)">编辑</button>
          <button class="btn btn-danger btn-sm" @click="startDelete(cert)">删除</button>
        </div>
      </div>
    </div>

    <!-- Add/Edit Form Modal -->
    <Teleport to="body">
      <div v-if="showAddForm || editingCert" class="modal-overlay" @click.self="closeForm">
        <div class="modal">
          <div class="modal__header">{{ editingCert ? '编辑证书' : '添加证书' }}</div>
          <div class="modal__body">
            <div class="form-group">
              <label>域名 / IP</label>
              <input v-model="form.domain" class="input-base" placeholder="media.example.com">
            </div>
            <div class="form-group">
              <label>类型</label>
              <select v-model="form.scope" class="input-base">
                <option value="domain">域名证书</option>
                <option value="ip">IP 证书</option>
              </select>
            </div>
            <div class="form-group">
              <label>签发模式</label>
              <select v-model="form.issuer_mode" class="input-base">
                <option value="master_cf_dns">Master CF DNS</option>
                <option value="local_http01">本地 HTTP-01</option>
              </select>
            </div>
            <div class="form-group">
              <label>标签（逗号分隔）</label>
              <input v-model="form.tags" class="input-base" placeholder="media, streaming">
            </div>
          </div>
          <div class="modal__footer">
            <button class="btn btn-secondary" @click="closeForm">取消</button>
            <button class="btn btn-primary" @click="submitForm">{{ editingCert ? '保存' : '添加' }}</button>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- Delete Modal -->
    <Teleport to="body">
      <div v-if="deletingCert" class="modal-overlay" @click.self="deletingCert = null">
        <div class="modal">
          <div class="modal__header">确认删除</div>
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
import { useCertificates, useCreateCertificate, useUpdateCertificate, useDeleteCertificate, useIssueCertificate } from '../hooks/useCertificates'

const route = useRoute()
const { selectedAgentId } = useAgent()

const agentId = computed(() => route.query.agentId || selectedAgentId.value)

const { data: _certsData, isLoading } = useCertificates(agentId)
const createCertificate = useCreateCertificate(agentId)
const updateCertificate = useUpdateCertificate(agentId)
const deleteCertificate = useDeleteCertificate(agentId)
const issueCertificate = useIssueCertificate(agentId)
const certificates = computed(() => _certsData.value ?? [])
const showAddForm = ref(false)
const editingCert = ref(null)
const deletingCert = ref(null)
const form = ref({ domain: '', scope: 'domain', issuer_mode: 'master_cf_dns', tags: '' })

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
  form.value = { domain: cert.domain, scope: cert.scope, issuer_mode: cert.issuer_mode, tags: (cert.tags || []).join(', ') }
}

function startDelete(cert) {
  deletingCert.value = cert
}

function closeForm() {
  showAddForm.value = false
  editingCert.value = null
  form.value = { domain: '', scope: 'domain', issuer_mode: 'master_cf_dns', tags: '' }
}

function submitForm() {
  const payload = {
    domain: form.value.domain,
    scope: form.value.scope,
    issuer_mode: form.value.issuer_mode,
    tags: form.value.tags ? form.value.tags.split(',').map(t => t.trim()).filter(Boolean) : [],
    enabled: true
  }
  if (editingCert.value) {
    updateCertificate.mutate({ id: editingCert.value.id, ...payload })
  } else {
    createCertificate.mutate(payload)
  }
  closeForm()
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
.cert-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(280px, 1fr)); gap: 1rem; }
.cert-card { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-xl); padding: 1.25rem; display: flex; flex-direction: column; gap: 0.75rem; }
.cert-card__header { display: flex; align-items: center; justify-content: space-between; }
.cert-card__icon { color: var(--color-success); }
.cert-card__status { font-size: 0.75rem; font-weight: 600; padding: 2px 8px; border-radius: var(--radius-full); }
.cert-card__status--active { background: var(--color-success-50); color: var(--color-success); }
.cert-card__status--pending { background: var(--color-warning-50); color: var(--color-warning); }
.cert-card__status--error { background: var(--color-danger-50); color: var(--color-danger); }
.cert-card__status--inactive { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.cert-card__domain { font-size: 1rem; font-weight: 600; color: var(--color-text-primary); font-family: var(--font-mono); }
.cert-card__meta { display: flex; gap: 0.5rem; font-size: 0.75rem; color: var(--color-text-tertiary); }
.cert-card__scope { background: var(--color-bg-subtle); padding: 1px 6px; border-radius: var(--radius-sm); }
.cert-card__tags { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.cert-card__actions { display: flex; gap: 0.5rem; flex-wrap: wrap; margin-top: auto; }
.tag { font-size: 0.75rem; padding: 2px 8px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
.modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.5); backdrop-filter: blur(4px); z-index: var(--z-modal); display: flex; align-items: center; justify-content: center; }
.modal { background: var(--color-bg-surface); border: 1.5px solid var(--color-border-default); border-radius: var(--radius-2xl); box-shadow: var(--shadow-xl); width: min(480px, 90vw); overflow: hidden; }
.modal__header { padding: 1rem 1.5rem; font-weight: 600; font-size: 1rem; border-bottom: 1px solid var(--color-border-subtle); }
.modal__body { padding: 1.5rem; display: flex; flex-direction: column; gap: 1.25rem; }
.modal__footer { padding: 1rem 1.5rem; display: flex; justify-content: flex-end; gap: 0.75rem; border-top: 1px solid var(--color-border-subtle); }
.form-group { display: flex; flex-direction: column; gap: 0.5rem; }
.form-group label { font-size: 0.875rem; font-weight: 500; color: var(--color-text-secondary); }
.input-base { width: 100%; padding: 0.625rem 0.875rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s; box-sizing: border-box; }
.input-base:focus { border-color: var(--color-primary); }
select.input-base { appearance: auto; }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
.btn-sm { padding: 0.25rem 0.75rem; font-size: 0.8125rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
