<template>
  <div class="plugin-groups-page">
    <header class="plugin-groups-page__header">
      <div class="plugin-groups-page__header-left">
        <h1 class="plugin-groups-page__title">插件资源组</h1>
        <p class="plugin-groups-page__desc">
          插件声明的分组，用来打开插件自己的管理页。
          <template v-if="groups.length">共 {{ groups.length }} 个</template>
          <template v-if="query.trim()"> · 匹配 {{ filtered.length }} 个</template>
        </p>
      </div>
      <div v-if="groups.length" class="plugin-groups-page__header-right">
        <ViewToggle v-model:view="view" />
        <div class="search-field" @click="focusSearch">
          <svg class="search-field__icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
            <circle cx="11" cy="11" r="7" />
            <path d="M20 20l-3.5-3.5" />
          </svg>
          <input
            ref="searchInputRef"
            v-model="query"
            class="search-field__input"
            type="search"
            placeholder="搜索名称、引用、插件…"
            aria-label="搜索资源组"
            @keydown.esc.prevent="query = ''"
          >
          <button
            v-if="query.trim()"
            type="button"
            class="search-field__clear"
            aria-label="清空搜索"
            @click.stop="query = ''"
          >
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true">
              <line x1="18" y1="6" x2="6" y2="18" />
              <line x1="6" y1="6" x2="18" y2="18" />
            </svg>
          </button>
        </div>
      </div>
    </header>

    <div v-if="loading" class="plugin-groups-page__state">
      <div class="spinner"></div>
      <p>正在读取资源组…</p>
    </div>
    <template v-else>
      <EmptyState
        v-if="!groups.length"
        icon="🗂️"
        title="没有已声明的资源组"
        description="安装并启用带 resource.group 元数据的插件后，会在这里出现。"
      />
      <div v-else-if="!filtered.length" class="plugin-groups-page__state">
        <p>没有匹配的资源组</p>
        <button class="btn btn-secondary" type="button" @click="query = ''">清空搜索</button>
      </div>
      <div v-else-if="view === 'card'" class="plugin-groups-page__grid">
      <BaseListCard
        v-for="group in filtered"
        :key="group.id"
        class="resource-group-card"
        clickable
        @click="selected = group"
      >
        <template #header-left>
          <span class="resource-group-card__title" :title="group.label">{{ group.label }}</span>
          <BaseBadge :tone="group.status === 'registered' ? 'success' : 'neutral'" dot>
            {{ statusLabel(group.status) }}
          </BaseBadge>
        </template>
        <template #header-right>
          <BaseIconButton :title="copiedId === group.id ? '已复制' : '复制引用'" @click="copyRef(group)">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <rect x="9" y="9" width="13" height="13" rx="2" />
              <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
            </svg>
          </BaseIconButton>
        </template>
        <code class="resource-group-card__ref" :title="group.ref">{{ group.ref }}</code>
        <p v-if="group.description" class="resource-group-card__desc" :title="group.description">{{ group.description }}</p>
        <template #footer>
          <span class="resource-group-card__meta">{{ group.label || group.plugin_id }}</span>
          <a
            v-if="manageHref(group)"
            class="resource-group-card__link"
            :href="manageHref(group)"
            target="_blank"
            rel="noopener"
            @click.stop
          >打开管理页</a>
        </template>
      </BaseListCard>
    </div>
      <div v-else class="plugin-catalog-table-wrap" data-test="plugin-groups-table">
        <table class="plugin-catalog-table" aria-label="插件资源组">
          <thead>
            <tr>
              <th>资源组</th>
              <th class="plugin-catalog-table__col-status">状态</th>
              <th>引用</th>
              <th class="plugin-catalog-table__col-actions">操作</th>
            </tr>
          </thead>
          <tbody>
            <tr
              v-for="group in filtered"
              :key="group.id"
              @click="selected = group"
            >
              <td>
                <div class="plugin-catalog-table__name">
                  <strong :title="group.label">{{ group.label }}</strong>
                  <small v-if="group.description">{{ group.description }}</small>
                </div>
              </td>
              <td>
                <BaseBadge :tone="group.status === 'registered' ? 'success' : 'neutral'" dot>
                  {{ statusLabel(group.status) }}
                </BaseBadge>
              </td>
              <td>
                <code class="plugin-catalog-table__ref" :title="group.ref">{{ group.ref }}</code>
              </td>
              <td class="plugin-catalog-table__col-actions">
                <div class="plugin-catalog-table__actions" @click.stop>
                  <BaseIconButton :title="copiedId === group.id ? '已复制' : '复制引用'" @click="copyRef(group)">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <rect x="9" y="9" width="13" height="13" rx="2" />
                      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
                    </svg>
                  </BaseIconButton>
                  <a
                    v-if="manageHref(group)"
                    class="btn btn-ghost btn-sm"
                    :href="manageHref(group)"
                    target="_blank"
                    rel="noopener"
                  >打开管理页</a>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

    <BaseModal
      :model-value="Boolean(selected)"
      :title="selected?.label || '资源组'"
      :subtitle="selected?.description || ''"
      show-footer
      @update:model-value="onInspectChange"
    >
      <dl v-if="selected" class="resource-group-detail__list">
        <div>
          <dt>状态</dt>
          <dd>{{ statusLabel(selected.status) }}</dd>
        </div>
        <div>
          <dt>引用</dt>
          <dd><code>{{ selected.ref }}</code></dd>
        </div>
        <section class="resource-group-detail__auth">
          <h3>授权</h3>
          <p>这里是插件声明的分组，用于打开插件自己的管理页。</p>
        </section>
      </dl>
      <template #footer>
        <button class="btn btn-secondary" type="button" @click="selected = null">关闭</button>
        <a
          v-if="selected && manageHref(selected)"
          class="btn btn-primary"
          :href="manageHref(selected)"
          target="_blank"
          rel="noopener"
        >打开管理页</a>
      </template>
    </BaseModal>
    </template>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import BaseBadge from '../components/base/BaseBadge.vue'
import BaseIconButton from '../components/base/BaseIconButton.vue'
import BaseListCard from '../components/base/BaseListCard.vue'
import BaseModal from '../components/base/BaseModal.vue'
import EmptyState from '../components/base/EmptyState.vue'
import ViewToggle from '../components/common/ViewToggle.vue'
import { useViewToggle } from '../composables/useViewToggle'
import { usePluginResourceGroups } from '../hooks/usePluginResourceGroups'

const { groups, loading, error } = usePluginResourceGroups()
const { view } = useViewToggle('plugin-resource-groups')
const query = ref('')
const selected = ref(null)
const copiedId = ref('')
const searchInputRef = ref(null)

const filtered = computed(() => {
  const needle = query.value.trim().toLowerCase()
  if (!needle) return groups.value
  return groups.value.filter((group) => {
    return [group.label, group.ref, group.plugin_id, group.description]
      .join(' ')
      .toLowerCase()
      .includes(needle)
  })
})

function statusLabel(status) {
  return status === 'registered' ? '已注册' : (status || '未知')
}

function manageHref(group) {
  const href = String(group?.ui_href || '').trim()
  if (href) return href
  const routeID = String(group?.ui_route_id || group?.plugin_id || '').trim()
  if (!routeID) return ''
  return `/panel-api/plugins/${encodeURIComponent(routeID)}/`
}

function focusSearch() {
  searchInputRef.value?.focus?.()
}

function onInspectChange(open) {
  if (!open) selected.value = null
}

async function copyRef(group) {
  try {
    await navigator.clipboard.writeText(group.ref)
    copiedId.value = group.id
    setTimeout(() => {
      if (copiedId.value === group.id) copiedId.value = ''
    }, 1500)
  } catch {
    copiedId.value = ''
  }
}
</script>

<style scoped>
.plugin-groups-page {
  max-width: 1180px;
  margin: 0 auto;
  display: grid;
  gap: 0.85rem;
}

.plugin-groups-page__header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: 0.75rem 1rem;
  flex-wrap: wrap;
}

.plugin-groups-page__header-left {
  flex: 1;
  min-width: 0;
}

.plugin-groups-page__header-right {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 0.5rem;
  flex: 1 1 16rem;
  min-width: 0;
}

.plugin-groups-page__header-right .search-field {
  flex: 1 1 12rem;
  min-width: 0;
  max-width: 22rem;
}

.plugin-groups-page__title {
  font-size: 1.3125rem;
  font-weight: 700;
  margin: 0 0 0.15rem;
  color: var(--color-text-primary);
  letter-spacing: -0.02em;
  line-height: 1.25;
}

.plugin-groups-page__desc {
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
  margin: 0;
  line-height: 1.35;
}

.plugin-groups-page__state {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 0.75rem;
  padding: 3.25rem 1.5rem;
  color: var(--color-text-muted);
  text-align: center;
}

.plugin-groups-page__state--error {
  color: var(--color-danger);
}

.plugin-groups-page__grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(17rem, 1fr));
  gap: 0.75rem;
  padding: 4px 4px 12px;
  margin: -4px -4px -4px;
}

.resource-group-card :deep(.base-list-card__header-left) {
  flex-wrap: nowrap;
  min-width: 0;
  flex: 1;
}

.resource-group-card--active {
  border-color: var(--color-primary);
}

.resource-group-card__title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.9375rem;
  font-weight: 700;
  color: var(--color-text-primary);
}

.resource-group-card__ref {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.75rem;
  color: var(--color-text-secondary);
}

.resource-group-card__desc {
  margin: 0;
  min-height: 2.3em;
  font-size: 0.8125rem;
  color: var(--color-text-muted);
  line-height: 1.45;
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
  overflow-wrap: anywhere;
}

.resource-group-card__meta {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.75rem;
  color: var(--color-text-tertiary);
}

.resource-group-card__link {
  margin-left: auto;
  color: var(--color-primary);
  font-size: 0.75rem;
  text-decoration: none;
  white-space: nowrap;
}

.resource-group-card :deep(.base-list-card__footer) {
  gap: 0.5rem;
  padding-top: 0.45rem;
  border-top: 1px solid var(--color-border-subtle);
}

.resource-group-detail {
  min-width: 0;
  padding: var(--space-5);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
}

.resource-group-detail__header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--space-3);
  margin-bottom: var(--space-4);
}

.resource-group-detail__header h2 {
  margin: 0;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 1.0625rem;
}

.resource-group-detail__close {
  flex-shrink: 0;
  border: 0;
  background: transparent;
  cursor: pointer;
  color: var(--color-text-secondary);
}

.resource-group-detail__list {
  display: grid;
  gap: var(--space-3);
  margin: 0 0 var(--space-5);
}

.resource-group-detail__list dt {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
}

.resource-group-detail__list dd {
  margin: var(--space-1) 0 0;
  overflow-wrap: anywhere;
}

.resource-group-detail__auth h3 {
  margin: 0 0 var(--space-2);
  font-size: var(--text-sm);
}

.resource-group-detail__auth p {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
}

@media (max-width: 800px) {
  .plugin-groups-page__header {
    flex-direction: column;
    align-items: stretch;
  }

  .plugin-groups-page__header-right,
  .plugin-groups-page__header-right .search-field {
    max-width: none;
    width: 100%;
  }
}
</style>
