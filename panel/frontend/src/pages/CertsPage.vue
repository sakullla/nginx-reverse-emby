<template>
  <div class='certs-page'>
    <div class='certs-page__header'>
      <div class='certs-page__header-left'>
        <h1 class='certs-page__title'>证书管理</h1>
        <p class='certs-page__subtitle'>
          <template v-if='agentId'>
            {{ certificates.length }} 项证书 · {{ activeCount }} 生效中 · 模板优先创建
          </template>
          <template v-else>
            请先选择一个节点
          </template>
        </p>
      </div>
      <div class='certs-page__header-right'>
        <div v-if='agentId && certificates.length' class='search-wrapper' @click='focusSearch'>
          <svg class='search-icon-btn' width='16' height='16' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
            <circle cx='11' cy='11' r='8' />
            <line x1='21' y1='21' x2='16.65' y2='16.65' />
          </svg>
          <input ref='searchInputRef' v-model='searchQuery' name='certificate-search' class='search-input' placeholder='搜索域名 / 标签 / #id=...'>
          <button v-if='searchQuery' class='clear-btn' @click.stop='searchQuery = ""'>
            <svg width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2.5'>
              <line x1='18' y1='6' x2='6' y2='18' />
              <line x1='6' y1='6' x2='18' y2='18' />
            </svg>
          </button>
        </div>
        <button v-if='agentId' class='btn btn-primary' @click='showAddForm = true'>
          <svg width='16' height='16' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2.5'>
            <line x1='12' y1='5' x2='12' y2='19' />
            <line x1='5' y1='12' x2='19' y2='12' />
          </svg>
          <span class='btn-text'>新建证书</span>
        </button>
      </div>
    </div>

    <div v-if='!agentId' class='certs-page__prompt'>
      <svg width='48' height='48' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='1.5'>
        <rect x='3' y='11' width='18' height='11' rx='2' ry='2' />
        <path d='M7 11V7a5 5 0 0 1 10 0v4' />
      </svg>
      <p>请从侧边栏选择一个节点</p>
    </div>

    <div v-else-if='isLoading' class='certs-page__loading'>
      <div class='spinner'></div>
    </div>

    <div v-else-if='certificates.length && filteredCerts.length' class='cert-grid'>
      <div v-for='cert in filteredCerts' :key='cert.id' class='cert-card' :class="{ 'cert-card--disabled': !cert.enabled }">
        <div class='cert-card__header'>
          <div class='cert-card__badges'>
            <span class='cert-card__id'>#{{ cert.id }}</span>
            <span class='cert-card__scope'>{{ cert.scope === 'ip' ? 'IP' : '域名' }}</span>
            <span class='cert-card__status' :class='`cert-card__status--${cert.status || "inactive"}`'>
              {{ getStatusLabel(cert) }}
            </span>
          </div>
          <div class='cert-card__actions'>
            <button
              v-if='cert.status === "pending" || cert.status === "error"'
              class='cert-card__action cert-card__action--issue'
              title='签发'
              @click='issueCert(cert)'
            >
              <svg width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
                <path d='M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z' />
              </svg>
            </button>
            <button class='cert-card__action' title='编辑' @click='startEdit(cert)'>
              <svg width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
                <path d='M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7' />
                <path d='M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z' />
              </svg>
            </button>
            <button
              v-if='!isSystemRelayCA(cert)'
              class='cert-card__action cert-card__action--delete'
              title='删除'
              @click='startDelete(cert)'
            >
              <svg width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
                <polyline points='3 6 5 6 21 6' />
                <path d='M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2' />
              </svg>
            </button>
          </div>
        </div>

        <div class='cert-card__domain'>
          <span class='cert-card__url-icon'>
            <svg width='14' height='14' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='2'>
              <path d='M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71'/>
              <path d='M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71'/>
            </svg>
          </span>
          <code class='cert-card__addr'>{{ cert.domain }}</code>
        </div>

        <div class='cert-card__meta'>
          <span class='cert-card__meta-tag'>{{ getCertificateUsageLabel(cert.usage) }}</span>
          <span class='cert-card__meta-tag'>{{ getCertificateIssuerLabel(cert) }}</span>
          <span v-if='cert.last_issue_at' class='cert-card__date'>{{ formatDate(cert.last_issue_at) }}</span>
        </div>

        <p v-if='cert.last_error' class='cert-card__error'>{{ cert.last_error }}</p>

        <div class='cert-card__tags'>
          <span v-if='isSystemRelayCA(cert)' class='tag tag--info'>系统 Relay CA</span>
          <span v-if='cert.self_signed' class='tag tag--warn'>自签</span>
          <span v-for='tag in cert.tags || []' :key='tag' class='tag'>{{ tag }}</span>
        </div>
      </div>
    </div>

    <div v-else-if='certificates.length && !filteredCerts.length && !isIdExactMatch' class='certs-page__empty'>
      <svg width='48' height='48' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='1.5'>
        <circle cx='11' cy='11' r='8' />
        <line x1='21' y1='21' x2='16.65' y2='16.65' />
      </svg>
      <p>没有匹配的证书</p>
    </div>

    <div v-else class='certs-page__empty'>
      <svg width='48' height='48' viewBox='0 0 24 24' fill='none' stroke='currentColor' stroke-width='1.5'>
        <rect x='3' y='11' width='18' height='11' rx='2' ry='2' />
        <path d='M7 11V7a5 5 0 0 1 10 0v4' />
      </svg>
      <p>暂无证书</p>
      <button class='btn btn-primary' @click='showAddForm = true'>从模板创建第一个证书</button>
    </div>

    <BaseModal
      :model-value="showAddForm || !!editingCert"
      :title="editingCert ? '编辑证书' : '新建证书'"
      size="xl"
      :close-on-click-modal="false"
      @update:model-value="closeForm"
    >
      <CertificateForm :initial-data="editingCert" :agent-id="agentId" @success="closeForm" />
    </BaseModal>

    <DeleteConfirmDialog
      :show='!!deletingCert'
      title='确认删除证书'
      message='删除后该证书将立即失效，相关配置将无法恢复。'
      :name='deletingCert?.domain'
      confirm-text='确认删除'
      :loading='deleteCertificate.isPending?.value'
      @confirm='confirmDelete'
      @cancel='deletingCert = null'
    />
  </div>
</template>

<script setup>
import { computed, ref, watchEffect } from 'vue'
import { useRoute } from 'vue-router'
import { useAgent } from '../context/AgentContext'
import { useCertificates, useDeleteCertificate, useIssueCertificate } from '../hooks/useCertificates'
import CertificateForm from '../components/CertificateForm.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import BaseModal from '../components/base/BaseModal.vue'
import {
  getCertificateSourceLabel,
  getCertificateUsageLabel,
  isSystemManagedRelayListenerCertificate,
  isSystemRelayCA
} from '../utils/certificateTemplates'

const route = useRoute()
const { selectedAgentId } = useAgent()

const agentId = computed(() => route.query.agentId || selectedAgentId.value)

const { data: certsData, isLoading } = useCertificates(agentId)
const deleteCertificate = useDeleteCertificate(agentId)
const issueCertificate = useIssueCertificate(agentId)
const certificates = computed(() => certsData.value ?? [])
const showAddForm = ref(false)
const editingCert = ref(null)
const deletingCert = ref(null)

const searchQuery = ref('')
const searchInputRef = ref(null)
function focusSearch() {
  searchInputRef.value?.focus()
}

watchEffect(() => {
  searchQuery.value = route.query.search ?? ''
})

const isIdExactMatch = computed(() => {
  const raw = searchQuery.value.trim()
  if (!raw) return false
  const idMatch = raw.match(/^#id=(\S+)$/)
  if (!idMatch) return false
  return certificates.value.some((cert) => String(cert.id) === idMatch[1])
})

const filteredCerts = computed(() => {
  const raw = searchQuery.value.trim()
  if (!raw) return certificates.value
  const idMatch = raw.match(/^#id=(\S+)$/)
  if (idMatch) return certificates.value.filter((cert) => String(cert.id) === idMatch[1])
  const query = raw.toLowerCase()
  return certificates.value.filter((cert) =>
    cert.domain.toLowerCase().includes(query) ||
    (cert.tags || []).some((tag) => String(tag).toLowerCase().includes(query))
  )
})

const activeCount = computed(() => certificates.value.filter((cert) => cert.enabled && cert.status === 'active').length)

function formatDate(dateStr) {
  if (!dateStr) return ''
  try {
    return new Date(dateStr).toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit'
    })
  } catch {
    return dateStr
  }
}

function getStatusLabel(cert) {
  if (!cert.enabled) return '已禁用'
  if (cert.status === 'active') return '生效中'
  if (cert.status === 'pending') return '待签发'
  if (cert.status === 'error') return '签发失败'
  return '未知'
}

function getCertificateIssuerLabel(cert) {
  if (isSystemManagedRelayListenerCertificate(cert)) {
    return '系统自动签发'
  }
  return getCertificateSourceLabel(cert?.certificate_type)
}

function issueCert(cert) {
  issueCertificate.mutate(cert.id)
}

function startEdit(cert) {
  editingCert.value = cert
}

function startDelete(cert) {
  if (isSystemRelayCA(cert)) {
    return
  }
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
.certs-page__header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 1.5rem; gap: 1rem; flex-wrap: wrap; }
.certs-page__header-left { flex: 1; min-width: 0; }
.certs-page__header-right { display: flex; align-items: center; gap: 0.75rem; flex-shrink: 0; }
.certs-page__title { font-size: 1.5rem; font-weight: 700; margin: 0 0 0.25rem; color: var(--color-text-primary); }
.certs-page__subtitle { font-size: 0.875rem; color: var(--color-text-tertiary); margin: 0; }
.certs-page__loading, .certs-page__empty, .certs-page__prompt { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 0.75rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }
.cert-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 1rem; }
.search-wrapper { position: relative; display: flex; align-items: center; }
.search-icon-btn { display: none; }
.search-input { flex: 1; min-width: 0; padding: 0.625rem 2rem 0.625rem 0.875rem; border-radius: var(--radius-lg); border: 1.5px solid var(--color-border-default); background: var(--color-bg-subtle); font-size: 0.875rem; color: var(--color-text-primary); outline: none; font-family: inherit; transition: border-color 0.15s, width 0.2s; box-sizing: border-box; }
.search-input:focus { border-color: var(--color-primary); width: 280px; }
.search-input::placeholder { color: var(--color-text-muted); }
.clear-btn { display: flex; align-items: center; justify-content: center; width: 18px; height: 18px; border: none; background: var(--color-bg-hover); border-radius: 50%; color: var(--color-text-secondary); cursor: pointer; flex-shrink: 0; padding: 0; position: absolute; right: 8px; z-index: 2; }

@media (max-width: 640px) {
  .search-wrapper {
    width: 36px;
    height: 36px;
    border-radius: var(--radius-lg);
    border: 1.5px solid var(--color-border-default);
    background: var(--color-bg-subtle);
    cursor: pointer;
    display: flex;
    align-items: center;
    justify-content: center;
    position: relative;
  }
  .search-icon-btn { display: flex; color: var(--color-text-secondary); }
  .search-input {
    position: absolute;
    left: 0;
    top: 0;
    width: 200px;
    height: 36px;
    opacity: 0;
    pointer-events: none;
    transition: opacity 0.2s, width 0.2s;
  }
  .search-wrapper:focus-within {
    width: 200px;
  }
  .search-wrapper:focus-within .search-input {
    opacity: 1;
    pointer-events: auto;
    border-color: var(--color-primary);
  }
  .search-wrapper:focus-within .clear-btn {
    opacity: 1;
    pointer-events: auto;
  }
  .clear-btn {
    opacity: 0;
    pointer-events: none;
    position: absolute;
    right: 8px;
    z-index: 2;
    transition: opacity 0.2s;
  }
  .btn-text { display: none; }
}

.cert-card {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  transition: opacity 0.15s;
}
.cert-card--disabled { opacity: 0.6; }
.cert-card__header { display: flex; align-items: center; justify-content: space-between; }
.cert-card__badges { display: flex; align-items: center; gap: 0.5rem; }
.cert-card__id { font-size: 0.75rem; font-family: var(--font-mono); color: var(--color-text-tertiary); }
.cert-card__scope { display: inline-block; font-size: 0.7rem; font-weight: 700; padding: 2px 6px; border-radius: var(--radius-sm); font-family: var(--font-mono); background: var(--color-bg-subtle); color: var(--color-text-secondary); }
.cert-card__domain { display: flex; align-items: center; gap: 0.5rem; min-width: 0; }
.cert-card__url-icon { display: flex; align-items: center; justify-content: center; color: var(--color-text-tertiary); flex-shrink: 0; }
.cert-card__addr { font-family: var(--font-mono); font-size: 0.875rem; font-weight: 500; color: var(--color-text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cert-card__meta { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
.cert-card__meta-tag { font-size: 0.7rem; padding: 1px 6px; background: var(--color-bg-subtle); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-sm); color: var(--color-text-secondary); font-family: var(--font-mono); }
.cert-card__date { font-size: 0.75rem; color: var(--color-text-muted); margin-left: auto; }
.cert-card__error { font-size: 0.75rem; color: var(--color-danger); background: var(--color-danger-50); padding: 0.25rem 0.5rem; border-radius: var(--radius-sm); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.cert-card__tags { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.tag { font-size: 0.75rem; padding: 2px 8px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
.tag--info { background: var(--color-primary-subtle); color: var(--color-primary); }
.tag--warn { background: var(--color-warning-50); color: var(--color-warning); }

.cert-card__status { font-size: 0.75rem; font-weight: 600; padding: 2px 8px; border-radius: var(--radius-full); }
.cert-card__status--active { background: var(--color-success-50); color: var(--color-success); }
.cert-card__status--pending { background: var(--color-warning-50); color: var(--color-warning); }
.cert-card__status--error { background: var(--color-danger-50); color: var(--color-danger); }
.cert-card__status--inactive { background: var(--color-bg-subtle); color: var(--color-text-muted); }

.cert-card__actions { display: flex; gap: 0.25rem; }
.cert-card__action { display: flex; align-items: center; justify-content: center; width: 28px; height: 28px; border-radius: var(--radius-md); border: none; background: transparent; color: var(--color-text-tertiary); cursor: pointer; transition: all 0.15s; }
.cert-card__action:hover { background: var(--color-bg-hover); color: var(--color-text-primary); }
.cert-card__action--delete:hover { background: var(--color-danger-50); color: var(--color-danger); }
.cert-card__action--issue:hover { background: var(--color-success-50); color: var(--color-success); }

.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
.btn-danger { background: var(--color-danger); color: white; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
