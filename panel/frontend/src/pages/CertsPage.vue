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
      <p>请选择一个节点来管理证书</p>
      <AgentPicker :agents="allAgents" @select="handleAgentSelect" />
      <p class="certs-page__prompt-hint">或前往节点管理页面添加新节点</p>
      <RouterLink to="/agents" class="btn btn-primary">加入节点</RouterLink>
    </div>

    <div v-else-if='isLoading' class='certs-page__loading'>
      <div class='spinner'></div>
    </div>

    <div v-else-if='certificates.length && filteredCerts.length' class='cert-grid'>
      <CertCard
        v-for='cert in filteredCerts'
        :key='cert.id'
        :cert='cert'
        @edit='startEdit'
        @delete='startDelete'
        @issue='issueCert'
      />
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
import { useRoute, useRouter } from 'vue-router'
import { useAgent } from '../context/AgentContext'
import { useAgents } from '../hooks/useAgents'
import { useCertificates, useDeleteCertificate, useIssueCertificate } from '../hooks/useCertificates'
import CertificateForm from '../components/CertificateForm.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import BaseModal from '../components/base/BaseModal.vue'
import AgentPicker from '../components/AgentPicker.vue'
import CertCard from '../components/certs/CertCard.vue'
import {
  isSystemRelayCA
} from '../utils/certificateTemplates'

const route = useRoute()
const router = useRouter()
const { selectedAgentId } = useAgent()
const { data: agentsData } = useAgents()
const allAgents = computed(() => agentsData.value ?? [])

const agentId = computed(() => route.query.agentId || selectedAgentId.value)

const { data: certsData, isLoading } = useCertificates(agentId)

function handleAgentSelect(agent) {
  router.replace({ query: { ...route.query, agentId: agent.id } })
}
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
</style>
