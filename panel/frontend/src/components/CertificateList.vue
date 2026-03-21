<template>
  <div class="cert-list">
    <div v-if="!ruleStore.certificates.length" class="cert-list__empty">
      <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
        <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
      </svg>
      <span>还没有统一证书</span>
      <button class="btn btn--primary btn--sm" @click="$emit('add')">添加第一个证书</button>
    </div>

    <div v-else-if="!ruleStore.filteredCertificates.length" class="cert-list__empty">
      <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
        <circle cx="11" cy="11" r="8"/>
        <line x1="21" y1="21" x2="16.65" y2="16.65"/>
      </svg>
      <span>未找到匹配的证书</span>
    </div>

    <div v-else class="cert-list__grid">
      <article
        v-for="cert in ruleStore.filteredCertificates"
        :key="cert.id"
        class="cert-card"
        :class="{ 'cert-card--disabled': cert.enabled === false }"
      >
        <!-- Accent bar -->
        <div class="cert-card__accent" :class="`cert-card__accent--${getEffectiveStatus(cert)}`"></div>

        <div class="cert-card__body">
          <!-- Header -->
          <div class="cert-card__header">
            <div class="cert-card__status">
              <span class="cert-card__id">#{{ cert.id }}</span>
              <span class="cert-card__status-dot" :class="`cert-card__status-dot--${getEffectiveStatus(cert)}`"></span>
              <span class="cert-card__status-text" :class="`cert-card__status-text--${getEffectiveStatus(cert)}`">
                {{ getStatusLabel(cert) }}
              </span>
            </div>
            <div class="cert-card__actions">
              <button
                class="cert-card__action cert-card__action--issue"
                title="签发/同步"
                :disabled="!canIssue(cert)"
                @click="issue(cert)"
              >
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <polyline points="20 6 9 17 4 12"/>
                </svg>
              </button>
              <button class="cert-card__action cert-card__action--edit" title="编辑" @click="edit(cert)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                </svg>
              </button>
              <button class="cert-card__action cert-card__action--delete" title="删除" @click="remove(cert)">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <polyline points="3 6 5 6 21 6"/>
                  <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
                </svg>
              </button>
            </div>
          </div>

          <!-- Domain row -->
          <div class="cert-card__domain-row">
            <div class="cert-card__domain-box">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
                <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
              </svg>
              <code>{{ cert.domain }}</code>
            </div>
            <div class="cert-card__badges">
              <span class="cert-card__badge cert-card__badge--scope">
                {{ cert.scope === 'ip' ? 'IP' : '域名' }}
              </span>
              <span
                class="cert-card__badge"
                :class="cert.issuer_mode === 'master_cf_dns' ? 'cert-card__badge--master' : 'cert-card__badge--local'"
              >
                {{ cert.issuer_mode === 'master_cf_dns' ? 'Master DNS' : '本地签发' }}
              </span>
            </div>
          </div>

          <!-- Error -->
          <div v-if="cert.last_error" class="cert-card__error">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="10"/>
              <line x1="15" y1="9" x2="9" y2="15"/>
              <line x1="9" y1="9" x2="15" y2="15"/>
            </svg>
            {{ cert.last_error }}
          </div>

          <!-- Last issued -->
          <div class="cert-card__meta">
            <span class="cert-card__meta-label">最近签发</span>
            <span class="cert-card__meta-value">{{ formatDate(cert.last_issue_at) }}</span>
          </div>
        </div>

        <!-- Footer: Tags -->
        <div v-if="cert.tags?.length" class="cert-card__footer">
          <span v-for="tag in cert.tags" :key="tag" class="cert-card__tag">{{ tag }}</span>
        </div>
      </article>
    </div>

    <!-- Edit Modal -->
    <BaseModal v-model="showEditModal" title="编辑统一证书" :subtitle="editingItem?.domain">
      <CertificateForm v-if="editingItem" :initial-data="editingItem" @success="showEditModal = false" />
    </BaseModal>

    <!-- Delete Modal -->
    <BaseModal v-model="showDeleteModal" title="确认删除" show-footer @confirm="confirmDelete">
      <p>确定要删除证书 <strong>{{ deletingItem?.domain }}</strong> 吗？</p>
    </BaseModal>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRuleStore } from '../stores/rules'
import BaseModal from './base/BaseModal.vue'
import CertificateForm from './CertificateForm.vue'

defineEmits(['add'])

const ruleStore = useRuleStore()
const editingItem = ref(null)
const deletingItem = ref(null)
const showEditModal = ref(false)
const showDeleteModal = ref(false)

function getEffectiveStatus(cert) {
  if (cert.enabled === false) return 'disabled'
  if (cert.status === 'error') return 'error'
  if (cert.status === 'active') return 'active'
  return 'pending'
}

function getStatusLabel(cert) {
  if (cert.enabled === false) return '已停用'
  if (cert.status === 'active') return '已生效'
  if (cert.status === 'error') return '签发失败'
  return '待签发'
}

function canIssue(cert) {
  return cert.enabled !== false
}

function formatDate(dateStr) {
  if (!dateStr) return '未签发'
  try {
    const d = new Date(dateStr)
    return d.toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' })
  } catch {
    return dateStr
  }
}

function edit(cert) {
  editingItem.value = cert
  showEditModal.value = true
}

function remove(cert) {
  deletingItem.value = cert
  showDeleteModal.value = true
}

async function confirmDelete() {
  if (!deletingItem.value) return
  await ruleStore.removeCertificate(deletingItem.value.id)
  showDeleteModal.value = false
}

async function issue(cert) {
  await ruleStore.syncCertificate(cert.id)
}
</script>

<style scoped>
.cert-list {
  min-height: 200px;
}

.cert-list__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: var(--space-3);
}

.cert-list__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-4);
  padding: var(--space-12) var(--space-6);
  color: var(--color-text-muted);
  text-align: center;
}

.cert-list__empty svg {
  opacity: 0.5;
}

.cert-list__empty span {
  font-size: var(--text-sm);
}

/* Card */
.cert-card {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  overflow: hidden;
  transition: border-color var(--duration-normal) var(--ease-default),
              box-shadow var(--duration-normal) var(--ease-default);
  backdrop-filter: blur(12px);
}

.cert-card:hover {
  border-color: var(--color-border-strong);
  box-shadow: var(--shadow-md);
}

.cert-card--disabled {
  opacity: 0.65;
}

/* Accent bar */
.cert-card__accent {
  height: 3px;
}

.cert-card__accent--active {
  background: var(--gradient-primary);
}

.cert-card__accent--pending {
  background: var(--color-warning);
}

.cert-card__accent--error {
  background: var(--color-danger);
}

.cert-card__accent--disabled {
  background: var(--color-border-default);
}

/* Body */
.cert-card__body {
  padding: var(--space-4);
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

/* Header */
.cert-card__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
}

.cert-card__status {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.cert-card__id {
  font-size: 10px;
  font-weight: var(--font-semibold);
  color: var(--color-text-muted);
  font-family: var(--font-mono);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-sm);
  padding: 1px 5px;
  flex-shrink: 0;
}

.cert-card__status-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.cert-card__status-dot--active {
  background: var(--color-success);
  box-shadow: 0 0 0 3px var(--color-success-50);
  animation: pulse 2s ease-in-out infinite;
}

.cert-card__status-dot--pending {
  background: var(--color-warning);
  box-shadow: 0 0 0 3px var(--color-warning-50);
}

.cert-card__status-dot--error {
  background: var(--color-danger);
  box-shadow: 0 0 0 3px var(--color-danger-50);
}

.cert-card__status-dot--disabled {
  background: var(--color-text-muted);
}

.cert-card__status-text {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
}

.cert-card__status-text--active { color: var(--color-success); }
.cert-card__status-text--pending { color: var(--color-warning); }
.cert-card__status-text--error { color: var(--color-danger); }
.cert-card__status-text--disabled { color: var(--color-text-muted); }

/* Actions */
.cert-card__actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  opacity: 0;
  transition: opacity var(--duration-fast) var(--ease-default);
}

.cert-card:hover .cert-card__actions {
  opacity: 1;
}

.cert-card__action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  border: none;
  background: transparent;
  color: var(--color-text-muted);
}

.cert-card__action:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}

.cert-card__action:not(:disabled):hover {
  background: var(--color-bg-hover);
}

.cert-card__action--issue:not(:disabled):hover {
  color: var(--color-success);
  background: var(--color-success-50);
}

.cert-card__action--edit:hover {
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.cert-card__action--delete:hover {
  color: var(--color-danger);
  background: var(--color-danger-50);
}

/* Domain row */
.cert-card__domain-row {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.cert-card__domain-box {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  flex: 1;
  min-width: 0;
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-subtle);
  border-radius: var(--radius-lg);
  border: 1px solid var(--color-border-subtle);
}

.cert-card__domain-box svg {
  color: var(--color-text-muted);
  flex-shrink: 0;
}

.cert-card__domain-box code {
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.cert-card__badges {
  display: flex;
  gap: var(--space-1);
  flex-shrink: 0;
}

.cert-card__badge {
  font-size: 9px;
  font-weight: var(--font-bold);
  padding: 2px 6px;
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
  white-space: nowrap;
}

.cert-card__badge--scope {
  background: var(--color-bg-subtle);
  color: var(--color-text-secondary);
  border: 1px solid var(--color-border-default);
}

.cert-card__badge--master {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.cert-card__badge--local {
  background: var(--color-warning-50);
  color: var(--color-warning);
}

/* Error */
.cert-card__error {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  font-size: var(--text-xs);
  color: var(--color-danger);
  padding: var(--space-2) var(--space-3);
  background: var(--color-danger-50);
  border-radius: var(--radius-md);
}

/* Meta */
.cert-card__meta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.cert-card__meta-label {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.cert-card__meta-value {
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  font-family: var(--font-mono);
}

/* Footer: Tags */
.cert-card__footer {
  display: flex;
  gap: var(--space-2);
  flex-wrap: wrap;
  padding: var(--space-3) var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
}

.cert-card__tag {
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  padding: var(--space-1) var(--space-2-5);
  background: var(--color-bg-surface);
  color: var(--color-primary);
  border-radius: var(--radius-full);
  border: 1px solid var(--color-border-default);
}

@keyframes pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.6; }
}
</style>
