<template>
  <div class="egress-page">
    <div class="egress-page__header">
      <div>
        <h1 class="egress-page__title">Egress Profiles</h1>
        <p class="egress-page__subtitle">{{ profiles.length }} 个 Profile · {{ enabledCount }} 个启用</p>
      </div>
      <button class="btn btn--primary" @click="startCreate">
        <span>+</span>
        新建 Profile
      </button>
    </div>

    <div v-if="isLoading" class="egress-page__empty">
      <div class="spinner"></div>
    </div>

    <div v-else-if="!profiles.length" class="egress-page__empty">
      <p>暂无 Egress Profile</p>
      <button class="btn btn--primary" @click="startCreate">创建第一个 Profile</button>
    </div>

    <div v-else class="profile-table-wrap">
      <table class="profile-table">
        <thead>
          <tr>
            <th>Name</th>
            <th>Type</th>
            <th>Status</th>
            <th>Description</th>
            <th>Revision</th>
            <th></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="profile in profiles" :key="profile.id">
            <td>
              <div class="profile-name">{{ profile.name || profile.id }}</div>
              <div class="profile-id">#{{ profile.id }}</div>
            </td>
            <td>
              <BaseBadge tone="neutral" shape="square">{{ profile.type }}</BaseBadge>
            </td>
            <td>
              <BaseBadge :tone="profile.enabled === false ? 'neutral' : 'success'" dot>
                {{ profile.enabled === false ? '停用' : '启用' }}
              </BaseBadge>
            </td>
            <td class="description-cell">{{ profile.description || '-' }}</td>
            <td>{{ profile.revision ?? 0 }}</td>
            <td class="actions-cell">
              <button class="btn btn--secondary btn--sm" @click="startEdit(profile)">编辑</button>
              <button class="btn btn--danger btn--sm" @click="deletingProfile = profile">删除</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>

    <BaseModal
      v-model="showForm"
      :title="editingProfile ? '编辑 Egress Profile' : '新建 Egress Profile'"
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
      title="确认删除 Egress Profile"
      message="如果该 Profile 已被 HTTP 或 L4 规则引用，删除会被后端阻止。"
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
} from '../hooks/useEgressProfiles'
import BaseModal from '../components/base/BaseModal.vue'
import BaseBadge from '../components/base/BaseBadge.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'
import EgressProfileForm from '../components/egress/EgressProfileForm.vue'

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
  } catch (error) {
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
.egress-page {
  max-width: 1200px;
  margin: 0 auto;
}

.egress-page__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-4);
  margin-bottom: var(--space-5);
  flex-wrap: wrap;
}

.egress-page__title {
  margin: 0 0 var(--space-1);
  font-size: 1.5rem;
  color: var(--color-text-primary);
}

.egress-page__subtitle {
  margin: 0;
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
}

.egress-page__empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: 4rem 2rem;
  color: var(--color-text-muted);
  text-align: center;
}

.profile-table-wrap {
  overflow-x: auto;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
}

.profile-table {
  width: 100%;
  border-collapse: collapse;
  font-size: var(--text-sm);
}

.profile-table th,
.profile-table td {
  padding: var(--space-3);
  border-bottom: 1px solid var(--color-border-subtle);
  text-align: left;
  vertical-align: middle;
}

.profile-table th {
  color: var(--color-text-muted);
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  background: var(--color-bg-subtle);
}

.profile-table tr:last-child td {
  border-bottom: none;
}

.profile-name {
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
}

.profile-id {
  margin-top: 2px;
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.description-cell {
  max-width: 320px;
  color: var(--color-text-secondary);
}

.actions-cell {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-2);
  white-space: nowrap;
}

.btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  border: none;
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-4);
  font-size: var(--text-sm);
  cursor: pointer;
  font-family: inherit;
}

.btn--sm {
  padding: 4px 10px;
  font-size: var(--text-xs);
}

.btn--primary {
  background: var(--color-primary);
  color: white;
}

.btn--secondary {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  color: var(--color-text-primary);
}

.btn--danger {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.spinner {
  width: 40px;
  height: 40px;
  border: 3px solid var(--color-border-subtle);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to {
    transform: rotate(360deg);
  }
}

@media (max-width: 640px) {
  .profile-table th:nth-child(4),
  .profile-table td:nth-child(4),
  .profile-table th:nth-child(5),
  .profile-table td:nth-child(5) {
    display: none;
  }
}
</style>
