<template>
  <div class="settings-egress">
    <header class="task-header">
      <div class="task-header__text">
        <h2 class="task-header__title">网络出口</h2>
        <p class="task-header__desc">管理出口 Profile，供 HTTP / L4 规则引用</p>
      </div>
      <div class="task-header__actions">
        <div v-if="!isLoading" class="task-header__stats" aria-label="出口统计">
          <div class="task-header__stat">
            <span class="task-header__stat-value">{{ profiles.length }}</span>
            <span class="task-header__stat-label">出口</span>
          </div>
          <div class="task-header__stat-divider" aria-hidden="true"></div>
          <div class="task-header__stat">
            <span class="task-header__stat-value">{{ enabledCount }}</span>
            <span class="task-header__stat-label">启用</span>
          </div>
        </div>
        <button type="button" class="btn btn--primary settings-egress__create-btn" @click="startCreate">
          <svg class="settings-egress__create-icon" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
            <path d="M12 5v14M5 12h14" stroke-linecap="round" />
          </svg>
          新建出口
        </button>
      </div>
    </header>

    <OperationStatusList />

    <div v-if="isLoading" class="settings-egress__state">
      <div class="spinner"></div>
      <span>加载中…</span>
    </div>

    <div v-else-if="!profiles.length" class="settings-egress__state settings-egress__state--empty">
      <p class="settings-egress__empty-title">暂无网络出口</p>
      <p class="settings-egress__hint">创建后可在 HTTP / L4 规则中引用</p>
      <button type="button" class="btn btn--primary" @click="startCreate">创建第一个出口</button>
    </div>

    <div v-else class="egress-list">
      <article
        v-for="profile in profiles"
        :key="profile.id"
        class="egress-card"
        :class="{ 'egress-card--disabled': profile.enabled === false }"
        data-testid="egress-card"
      >
        <div
          class="egress-card__accent"
          :class="profile.enabled === false ? 'egress-card__accent--off' : 'egress-card__accent--on'"
          aria-hidden="true"
        />

        <div class="egress-card__body">
          <div class="egress-card__top">
            <div class="egress-card__identity">
              <div class="egress-card__title-row">
                <h3 class="egress-card__name">{{ profile.name || `出口 #${profile.id}` }}</h3>
                <span
                  class="egress-card__status"
                  :class="profile.enabled === false ? 'egress-card__status--off' : 'egress-card__status--on'"
                >
                  <i class="egress-card__status-dot" />
                  {{ profile.enabled === false ? '停用' : '启用' }}
                </span>
              </div>

              <div class="egress-card__meta">
                <span class="egress-card__type">{{ typeLabel(profile.type) }}</span>
                <span class="egress-card__sep" aria-hidden="true">·</span>
                <span class="egress-card__id">#{{ profile.id }}</span>
                <template v-if="profile.revision != null">
                  <span class="egress-card__sep" aria-hidden="true">·</span>
                  <span class="egress-card__rev">修订 {{ profile.revision }}</span>
                </template>
              </div>
            </div>

            <div class="egress-card__actions">
              <button type="button" class="btn btn--secondary btn--sm egress-card__btn" @click="startEdit(profile)">
                编辑
              </button>
              <button type="button" class="btn btn--danger-soft btn--sm egress-card__btn" @click="deletingProfile = profile">
                删除
              </button>
            </div>
          </div>

          <p class="egress-card__desc" :class="{ 'egress-card__desc--empty': !profile.description }">
            {{ profile.description || '暂无说明' }}
          </p>
        </div>
      </article>
    </div>

    <BaseModal
      v-model="showForm"
      :title="editingProfile ? '编辑网络出口' : '新建网络出口'"
      size="lg"
      :close-on-click-modal="false"
    >
      <EgressProfileForm
        :initial-data="editingProfile"
        :is-loading="isSaving"
        @submit="handleSubmit"
      />
    </BaseModal>

    <DeleteConfirmDialog
      :show="!!deletingProfile"
      title="确认删除网络出口"
      message="如果该出口已被 HTTP 或 L4 规则引用，删除会被后端阻止。"
      :name="deletingProfile?.name"
      confirm-text="确认删除"
      :loading="deleteEgressProfile.isPending?.value"
      @confirm="confirmDelete"
      @cancel="deletingProfile = null"
    />
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import {
  useEgressProfiles,
  useCreateEgressProfile,
  useUpdateEgressProfile,
  useDeleteEgressProfile
} from '../../hooks/useEgressProfiles'
import BaseModal from '../base/BaseModal.vue'
import DeleteConfirmDialog from '../DeleteConfirmDialog.vue'
import EgressProfileForm from '../egress/EgressProfileForm.vue'
import OperationStatusList from '../operations/OperationStatusList.vue'

const TYPE_LABELS = {
  direct: '直连',
  socks: 'SOCKS',
  socks5: 'SOCKS5',
  http: 'HTTP 代理',
  https: 'HTTPS 代理'
}

const { data: profilesData, isLoading } = useEgressProfiles()
const createEgressProfile = useCreateEgressProfile()
const updateEgressProfile = useUpdateEgressProfile()
const deleteEgressProfile = useDeleteEgressProfile()

const profiles = computed(() => profilesData.value ?? [])
const enabledCount = computed(() => profiles.value.filter((profile) => profile.enabled !== false).length)
const isSaving = computed(() => createEgressProfile.isPending.value || updateEgressProfile.isPending.value)

const showForm = ref(false)
const editingProfile = ref(null)
const deletingProfile = ref(null)

function typeLabel(type) {
  const key = String(type || '').toLowerCase()
  return TYPE_LABELS[key] || type || '未知类型'
}

function startCreate() {
  editingProfile.value = null
  showForm.value = true
}

function startEdit(profile) {
  editingProfile.value = profile
  showForm.value = true
}

function closeForm() {
  showForm.value = false
  editingProfile.value = null
}

async function handleSubmit(payload) {
  try {
    if (editingProfile.value) {
      await updateEgressProfile.mutateAsync({ id: editingProfile.value.id, ...payload })
    } else {
      await createEgressProfile.mutateAsync(payload)
    }
    closeForm()
  } catch {
    // Error surfaced by mutation hook.
  }
}

function confirmDelete() {
  if (!deletingProfile.value) return
  deleteEgressProfile.mutate(deletingProfile.value.id, {
    onSuccess: () => {
      deletingProfile.value = null
    }
  })
}
</script>

<style scoped>
.settings-egress {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.task-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
  flex-wrap: wrap;
}

.task-header__text {
  min-width: 0;
}

.task-header__title {
  margin: 0 0 0.3rem;
  font-size: var(--text-lg);
  font-weight: 700;
  letter-spacing: -0.01em;
  color: var(--color-text-primary);
  line-height: 1.25;
}

.task-header__desc {
  margin: 0;
  font-size: var(--text-sm);
  line-height: 1.5;
  color: var(--color-text-tertiary);
}

.task-header__actions {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  flex-wrap: wrap;
}

.settings-egress__create-btn {
  min-height: 2.25rem;
  padding-inline: 0.95rem;
  border-radius: var(--radius-full);
  box-shadow: 0 1px 2px color-mix(in srgb, var(--color-primary) 18%, transparent);
}

.settings-egress__create-btn:hover:not(:disabled) {
  box-shadow: 0 4px 12px color-mix(in srgb, var(--color-primary) 22%, transparent);
}

.settings-egress__create-icon {
  flex-shrink: 0;
}

.task-header__stats {
  display: inline-flex;
  align-items: stretch;
  gap: 0;
  padding: 0.4rem 0.7rem;
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  box-shadow: var(--shadow-xs);
}

.task-header__stat {
  display: flex;
  flex-direction: column;
  align-items: center;
  min-width: 2.5rem;
  padding: 0 0.55rem;
}

.task-header__stat-value {
  font-size: 1.05rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
  line-height: 1.15;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
}

.task-header__stat-label {
  margin-top: 0.1rem;
  font-size: 0.6875rem;
  font-weight: 500;
  letter-spacing: 0.02em;
  color: var(--color-text-tertiary);
}

.task-header__stat-divider {
  width: 1px;
  margin: 0.15rem 0;
  background: var(--color-border-subtle);
}

.settings-egress__state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-10) var(--space-4);
  text-align: center;
  border: 1px dashed var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
  color: var(--color-text-muted);
}

.settings-egress__empty-title {
  margin: 0;
  font-size: var(--text-sm);
  font-weight: 600;
  color: var(--color-text-secondary);
}

.settings-egress__hint {
  margin: 0 0 var(--space-2);
  color: var(--color-text-tertiary);
  font-size: var(--text-xs);
  line-height: 1.45;
}

.egress-list {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}

.egress-card {
  position: relative;
  display: flex;
  overflow: hidden;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-xs);
  transition:
    border-color var(--duration-fast) var(--ease-default),
    box-shadow var(--duration-fast) var(--ease-default),
    transform var(--duration-fast) var(--ease-default);
}

.egress-card:hover {
  border-color: color-mix(in srgb, var(--color-primary) 28%, var(--color-border-default));
  box-shadow: var(--shadow-sm);
  transform: translateY(-1px);
}

.egress-card--disabled {
  opacity: 0.88;
}

.egress-card__accent {
  width: 3px;
  flex-shrink: 0;
}

.egress-card__accent--on {
  background: var(--color-success, #34d399);
}

.egress-card__accent--off {
  background: var(--color-border-strong, #cbd5e1);
}

.egress-card__body {
  flex: 1;
  min-width: 0;
  padding: 0.95rem 1rem 0.95rem 0.95rem;
  display: flex;
  flex-direction: column;
  gap: 0.55rem;
}

.egress-card__top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
}

.egress-card__identity {
  min-width: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

.egress-card__title-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}

.egress-card__name {
  margin: 0;
  font-size: 0.98rem;
  font-weight: 650;
  letter-spacing: -0.015em;
  line-height: 1.3;
  color: var(--color-text-primary);
}

.egress-card__status {
  display: inline-flex;
  align-items: center;
  gap: 0.28rem;
  padding: 0.16rem 0.5rem;
  border-radius: var(--radius-full);
  font-size: 0.6875rem;
  font-weight: 650;
  letter-spacing: 0.01em;
  line-height: 1.2;
}

.egress-card__status-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
  flex-shrink: 0;
}

.egress-card__status--on {
  color: var(--color-success, #059669);
  background: color-mix(in srgb, var(--color-success, #34d399) 14%, transparent);
}

.egress-card__status--off {
  color: var(--color-text-tertiary);
  background: var(--color-bg-subtle);
}

.egress-card__meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 0.2rem 0.35rem;
  min-width: 0;
  font-size: 0.75rem;
  line-height: 1.35;
  color: var(--color-text-tertiary);
}

.egress-card__type {
  display: inline-flex;
  align-items: center;
  padding: 0.12rem 0.45rem;
  border-radius: var(--radius-sm);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  color: var(--color-text-secondary);
  font-size: 0.6875rem;
  font-weight: 650;
  letter-spacing: 0.02em;
}

.egress-card__sep {
  color: var(--color-border-strong, #cbd5e1);
  user-select: none;
}

.egress-card__id,
.egress-card__rev {
  font-variant-numeric: tabular-nums;
  font-weight: 500;
}

.egress-card__desc {
  margin: 0;
  max-width: 46rem;
  font-size: 0.8125rem;
  line-height: 1.55;
  color: var(--color-text-secondary);
  word-break: break-word;
}

.egress-card__desc--empty {
  color: var(--color-text-muted);
  font-style: italic;
}

.egress-card__actions {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  flex-shrink: 0;
  padding-top: 0.05rem;
}

.egress-card__btn {
  min-width: 3.5rem;
  min-height: 1.9rem;
  font-weight: 600;
  letter-spacing: 0.01em;
}

.egress-card:hover .egress-card__btn.btn--secondary {
  background: var(--color-bg-canvas);
  border-color: var(--color-border-default);
}

.spinner {
  width: 28px;
  height: 28px;
  border: 2px solid var(--color-border-subtle);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
  margin-bottom: 0.25rem;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

@media (max-width: 640px) {
  .egress-card__top {
    flex-direction: column;
  }

  .egress-card__actions {
    width: 100%;
  }

  .egress-card__actions .btn {
    flex: 1;
  }

  .task-header__stats {
    width: 100%;
    justify-content: center;
  }
}
</style>
