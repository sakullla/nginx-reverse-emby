<script setup>
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { fetchResources } from '../../api/access'
import { resourceGroupDisplayName } from '../../context/useAccessControl'

const RESOURCE_KIND_OPTIONS = [
  { id: '', label: '全部类型' },
  { id: 'agent', label: '节点' },
  { id: 'http_rule', label: 'HTTP 规则' },
  { id: 'l4_rule', label: 'L4 规则' },
  { id: 'relay_listener', label: 'Relay 监听器' },
  { id: 'certificate', label: '证书' },
  { id: 'egress_profile', label: '出口配置' }
]

const DEFAULT_GROUP_ID = 'default'

const props = defineProps({
  modelValue: { type: Object, default: null },
  resources: { type: [Array, Object], default: null },
  members: { type: [Array, Object], default: null },
  groups: { type: Array, default: () => [] },
  kind: { type: String, default: '' },
  query: { type: String, default: '' },
  writable: { type: Boolean, default: false },
  disabled: { type: Boolean, default: false },
  loading: { type: Boolean, default: false },
  error: { type: String, default: '' },
  busy: { type: [Boolean, String], default: false },
  targetGroupId: { type: String, default: '' },
  showCurrentMembers: { type: Boolean, default: true }
})

const emit = defineEmits([
  'update:modelValue',
  'update:kind',
  'update:query',
  'select',
  'search',
  'retry',
  'move',
  'unbind'
])

const kindFilter = ref(props.kind)
const searchQuery = ref(props.query)
const fetched = ref([])
const fetchError = ref('')
const fetchLoading = ref(false)
const searched = ref(false)
const selectedInternal = ref(null)
const selectedKeys = ref([])
const actionError = ref('')
const confirmDialog = ref(null)
const confirmDialogEl = ref(null)

const catalogLoading = computed(() => props.loading || fetchLoading.value)
const catalogError = computed(() => props.error || fetchError.value)
const isBusy = computed(() => Boolean(props.busy))
const canWrite = computed(() => props.writable && !props.disabled)
const visibleGroups = computed(() => (Array.isArray(props.groups) ? props.groups : []).filter((group) => group && group.id))
const targetGroup = computed(() => groupById(props.targetGroupId) || (props.targetGroupId ? { id: props.targetGroupId } : null))

const memberItems = computed(() => {
  if (!props.showCurrentMembers) return []
  if (props.members != null) return flattenResources(props.members)
  const target = String(props.targetGroupId || '').trim()
  if (!target) return []
  return fetched.value.filter((item) => groupIdOf(item) === target)
})

const searchResults = computed(() => {
  const source = props.resources != null ? flattenResources(props.resources) : fetched.value
  const kind = String(kindFilter.value || '').trim()
  const q = String(searchQuery.value || '').trim().toLowerCase()
  const target = String(props.targetGroupId || '').trim()
  return source.filter((item) => {
    if (target && groupIdOf(item) === target) return false
    if (kind && resourceKindOf(item) !== kind) return false
    if (!q) return true
    return resourceSearchText(item).includes(q)
  })
})

const selectedItems = computed(() => (
  selectedKeys.value
    .map((key) => searchResults.value.find((item) => resourceKey(item) === key))
    .filter(Boolean)
))
const selectedCount = computed(() => selectedItems.value.length)
const allDisplayedSelected = computed(() => (
  searchResults.value.length > 0
  && searchResults.value.every((item) => selectedKeys.value.includes(resourceKey(item)))
))

const selected = computed(() => {
  if (selectedItems.value.length) return selectedItems.value.at(-1)
  const current = props.modelValue || selectedInternal.value
  if (!current) return null
  return searchResults.value.find((item) => sameResource(item, current))
    || memberItems.value.find((item) => sameResource(item, current))
    || normalizeResource(current)
})

const confirmBusy = computed(() => {
  const kind = confirmDialog.value?.kind
  if (!kind) return false
  return props.busy === true || props.busy === kind
})

watch(() => props.kind, (value) => {
  if (value !== kindFilter.value) kindFilter.value = value
})

watch(() => props.query, (value) => {
  if (value !== searchQuery.value) searchQuery.value = value
})

watch(() => props.targetGroupId, () => {
  selectedKeys.value = []
  selectedInternal.value = null
  if (shouldAutoLoad()) loadCatalog()
})

watch(searchResults, () => {
  const valid = new Set(searchResults.value.map(resourceKey))
  const next = selectedKeys.value.filter((key) => valid.has(key))
  if (next.length !== selectedKeys.value.length) selectedKeys.value = next
})

onMounted(() => {
  if (shouldAutoLoad()) loadCatalog()
})

function shouldAutoLoad() {
  return props.members == null && !props.disabled
}

function flattenResources(value) {
  if (Array.isArray(value)) return value.filter(Boolean).map(normalizeResource).filter(Boolean)
  if (!value || typeof value !== 'object') return []
  return Object.entries(value).flatMap(([kind, items]) => (
    (Array.isArray(items) ? items : []).filter(Boolean).map((item) => normalizeResource({
      ...item,
      kind: resourceKindOf(item) || kind
    }))
  )).filter(Boolean)
}

function normalizeResource(item) {
  if (!item || typeof item !== 'object') return null
  const kind = resourceKindOf(item)
  const id = resourceIdOf(item)
  if (!kind || !id) return null
  return {
    ...item,
    kind,
    id,
    name: item.name || id,
    context: item.context || '',
    resource_group_id: groupIdOf(item)
  }
}

function resourceKindOf(item) {
  return String(item?.kind || item?.resource_kind || '').trim()
}

function resourceIdOf(item) {
  return String(item?.id || item?.resource_id || '').trim()
}

function groupIdOf(item) {
  return String(item?.resource_group_id || item?.resourceGroupId || DEFAULT_GROUP_ID).trim() || DEFAULT_GROUP_ID
}

function sameResource(left, right) {
  return resourceKindOf(left) === resourceKindOf(right) && resourceIdOf(left) === resourceIdOf(right)
}

function resourceKey(item) {
  return `${resourceKindOf(item)}:${resourceIdOf(item)}`
}

function resourceSearchText(item) {
  return [resourceIdOf(item), item?.name, item?.context, groupIdOf(item), kindLabel(resourceKindOf(item))]
    .map((part) => String(part || '').toLowerCase())
    .join(' ')
}

function kindLabel(kind) {
  return RESOURCE_KIND_OPTIONS.find((option) => option.id === kind)?.label || kind || '资源'
}

function groupById(id) {
  const groupID = String(id || '').trim()
  if (!groupID) return null
  return visibleGroups.value.find((group) => group.id === groupID) || { id: groupID, name: groupID === DEFAULT_GROUP_ID ? DEFAULT_GROUP_ID : groupID }
}

function groupLabel(idOrGroup) {
  const group = typeof idOrGroup === 'string' || idOrGroup == null ? groupById(idOrGroup) : idOrGroup
  if (!group) return '未分组'
  return resourceGroupDisplayName(group) || group.id
}

function resourceLabel(item) {
  return item?.name || resourceIdOf(item) || '未选择资源'
}

function optionTestId(item) {
  return `resource-option-${resourceIdOf(item)}`
}

function unbindTestId(item) {
  return `unbind-${resourceKindOf(item)}-${resourceIdOf(item)}`
}

function searchPayload() {
  return {
    kind: String(kindFilter.value || '').trim(),
    q: String(searchQuery.value || '').trim()
  }
}

async function loadCatalog() {
  const payload = searchPayload()
  emit('search', payload)
  emit('update:query', payload.q)
  fetchLoading.value = true
  fetchError.value = ''
  searched.value = true
  try {
    const items = await fetchResources(payload)
    fetched.value = flattenResources(items)
  } catch (cause) {
    fetchError.value = cause?.message || '读取资源失败'
    fetched.value = []
  } finally {
    fetchLoading.value = false
  }
}

function submitSearch() {
  if (props.disabled) return
  loadCatalog()
}

function retrySearch() {
  emit('retry', searchPayload())
  loadCatalog()
}

function humanCatalogError(message) {
  const raw = String(message || '').trim()
  if (/status code 5\d\d|network error|failed to fetch|502/i.test(raw)) {
    return '暂时连不上服务，请稍后重试。'
  }
  return raw || '读取资源失败'
}

function onKindChange(value) {
  kindFilter.value = value
  emit('update:kind', value)
  if (!props.disabled) loadCatalog()
}

function clearSearch() {
  if (!searchQuery.value) return
  searchQuery.value = ''
  emit('update:query', '')
}

function isChecked(item) {
  return selectedKeys.value.includes(resourceKey(item))
}

function toggleResource(item) {
  if (props.disabled || isBusy.value) return
  const next = normalizeResource(item)
  if (!next) return
  const key = resourceKey(next)
  selectedKeys.value = isChecked(next)
    ? selectedKeys.value.filter((itemKey) => itemKey !== key)
    : [...selectedKeys.value, key]
  selectedInternal.value = selectedItems.value.at(-1) || null
  emit('update:modelValue', selectedInternal.value)
  emit('select', selectedInternal.value)
  actionError.value = ''
}

function toggleAllResults() {
  if (props.disabled || isBusy.value || !searchResults.value.length) return
  if (allDisplayedSelected.value) {
    selectedKeys.value = []
    selectedInternal.value = null
    emit('update:modelValue', null)
    emit('select', null)
    return
  }
  selectedKeys.value = searchResults.value.map(resourceKey)
  selectedInternal.value = searchResults.value.at(-1)
  emit('update:modelValue', selectedInternal.value)
  emit('select', selectedInternal.value)
  actionError.value = ''
}

function requestMove() {
  if (!canWrite.value || isBusy.value) return
  const items = selectedItems.value.length ? selectedItems.value : (selected.value ? [selected.value] : [])
  if (!items.length) {
    actionError.value = '请先选择要移动的资源。'
    return
  }
  const targetID = String(props.targetGroupId || '').trim()
  if (!targetID) {
    actionError.value = '请选择目标资源组。'
    return
  }
  const movable = items.filter((item) => groupIdOf(item) !== targetID)
  if (!movable.length) {
    actionError.value = '所选资源已在当前资源组。'
    return
  }
  actionError.value = ''
  const message = movable.length === 1
    ? `将把「${resourceLabel(movable[0])}」从「${groupLabel(groupIdOf(movable[0]))}」移动到「${groupLabel(targetID)}」。只改变所属资源组，不修改资源自身的业务配置。`
    : `将把 ${movable.length} 项资源移动到「${groupLabel(targetID)}」。只改变所属资源组，不修改资源自身的业务配置。`
  openConfirm({
    kind: 'move',
    title: '确认移动资源',
    confirmText: '确认移动',
    message,
    targetGroupId: targetID,
    resource: movable[0],
    resources: movable
  })
}

function requestUnbind(item) {
  if (!canWrite.value || isBusy.value) return
  const resource = normalizeResource(item)
  if (!resource) return
  if (groupIdOf(resource) === DEFAULT_GROUP_ID) {
    actionError.value = '该资源已在默认组，无需解绑。'
    return
  }
  actionError.value = ''
  openConfirm({
    kind: 'unbind',
    title: '确认解绑资源',
    confirmText: '确认解绑',
    message: `将解除「${resourceLabel(resource)}」在「${groupLabel(groupIdOf(resource))}」的显式绑定。解绑后该资源会出现在默认组，业务配置保持不变。`,
    resource
  })
}

function openConfirm(dialog) {
  confirmDialog.value = dialog
  nextTick(() => confirmDialogEl.value?.focus())
}

function cancelConfirm() {
  if (confirmBusy.value) return
  confirmDialog.value = null
}

function acceptConfirm() {
  const dialog = confirmDialog.value
  if (!dialog || confirmBusy.value) return
  const resource = dialog.resource
  if (dialog.kind === 'move') {
    const resources = Array.isArray(dialog.resources) && dialog.resources.length ? dialog.resources : [resource]
    emit('move', {
      resource_kind: resourceKindOf(resource),
      resource_id: resourceIdOf(resource),
      resource_group_id: dialog.targetGroupId,
      resources: resources.map((item) => ({
        resource_kind: resourceKindOf(item),
        resource_id: resourceIdOf(item)
      }))
    })
    selectedKeys.value = []
    selectedInternal.value = null
  } else {
    emit('unbind', {
      resource_kind: resourceKindOf(resource),
      resource_id: resourceIdOf(resource)
    })
  }
  if (!confirmBusy.value) confirmDialog.value = null
}
</script>

<template>
  <div class="resource-search-select" data-test="resource-search-select">
    <form class="resource-search-select__search" data-test="resource-search-form" @submit.prevent="submitSearch">
      <div class="resource-search-select__kinds" role="tablist" aria-label="资源类型">
        <button
          v-for="option in RESOURCE_KIND_OPTIONS"
          :key="option.id || 'all'"
          type="button"
          class="resource-search-select__kind"
          :class="{ 'resource-search-select__kind--active': kindFilter === option.id }"
          :disabled="disabled"
          @click="onKindChange(option.id)"
        >
          {{ option.label }}
        </button>
      </div>
      <select
        class="resource-search-select__kind-native"
        :value="kindFilter"
        data-test="resource-kind"
        :disabled="disabled"
        tabindex="-1"
        aria-hidden="true"
        @change="onKindChange($event.target.value)"
      >
        <option v-for="option in RESOURCE_KIND_OPTIONS" :key="option.id || 'all'" :value="option.id">
          {{ option.label }}
        </option>
      </select>
      <div class="search-field">
        <svg class="search-field__icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
          <circle cx="11" cy="11" r="7" />
          <path d="M20 20l-3.5-3.5" />
        </svg>
        <input
          v-model="searchQuery"
          data-test="resource-search"
          type="search"
          placeholder="按名称或上下文搜索"
          aria-label="搜索资源"
          :disabled="disabled"
          @keydown.esc.prevent="clearSearch"
        >
        <button class="resource-search-select__sr-submit" type="submit" tabindex="-1">搜索</button>
      </div>
    </form>

    <p v-if="actionError" class="resource-search-select__alert" role="alert">{{ actionError }}</p>

    <section v-if="memberItems.length" class="resource-search-select__members" aria-label="当前组成员">
      <ul>
        <li v-for="item in memberItems" :key="`${resourceKindOf(item)}:${resourceIdOf(item)}`">
          <span>
            <strong>{{ resourceLabel(item) }}</strong>
            <small>
              {{ kindLabel(resourceKindOf(item)) }}
              <template v-if="item.context"> · {{ item.context }}</template>
              · {{ groupLabel(groupIdOf(item)) }}
            </small>
          </span>
          <button
            v-if="canWrite"
            class="resource-search-select__icon-btn"
            type="button"
            :data-test="unbindTestId(item)"
            :disabled="disabled || isBusy || groupIdOf(item) === DEFAULT_GROUP_ID"
            title="解绑回默认组"
            aria-label="解绑回默认组"
            @click="requestUnbind(item)"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
              <path d="M18 6L6 18M6 6l12 12" />
            </svg>
          </button>
        </li>
      </ul>
    </section>

    <div v-if="catalogLoading" class="resource-search-select__status">正在搜索资源…</div>
    <div v-else-if="catalogError" class="resource-search-select__status" role="alert">
      <p>
        {{ humanCatalogError(catalogError) }}
        <button class="resource-search-select__retry" type="button" data-test="resource-retry" :disabled="disabled" @click="retrySearch">
          重试
        </button>
      </p>
    </div>
    <p v-else-if="searched && !searchResults.length" class="resource-search-select__status">
      没有可移入的资源
    </p>
    <template v-else-if="searchResults.length">
      <div class="resource-search-select__toolbar">
        <button
          type="button"
          class="resource-search-select__select-all"
          data-test="resource-select-all"
          :disabled="disabled || isBusy"
          @click="toggleAllResults"
        >
          {{ allDisplayedSelected ? '取消全选' : '全选当前列表' }}
        </button>
        <span>{{ searchResults.length }} 项<template v-if="selectedCount"> · 已选 {{ selectedCount }}</template></span>
      </div>
      <ul class="resource-search-select__list" role="listbox" aria-multiselectable="true" aria-label="可移动资源">
        <li v-for="item in searchResults" :key="`${resourceKindOf(item)}:${resourceIdOf(item)}`">
          <button
            type="button"
            role="option"
            class="resource-search-select__option"
            :class="{ 'resource-search-select__option--active': isChecked(item) }"
            :aria-selected="isChecked(item) ? 'true' : 'false'"
            :disabled="disabled || isBusy"
            :data-test="optionTestId(item)"
            @click="toggleResource(item)"
          >
            <span class="resource-search-select__check" :class="{ 'resource-search-select__check--on': isChecked(item) }" aria-hidden="true"></span>
            <span>
              <strong>{{ resourceLabel(item) }}</strong>
              <small>
                {{ kindLabel(resourceKindOf(item)) }}
                <template v-if="item.context"> · {{ item.context }}</template>
                · {{ groupLabel(groupIdOf(item)) }}
              </small>
            </span>
          </button>
        </li>
      </ul>
    </template>

    <form v-if="canWrite && selectedCount" class="resource-search-select__move" data-test="move-form" @submit.prevent="requestMove">
      <button class="btn btn-primary" type="submit" :disabled="disabled || isBusy">
        {{ selectedCount > 1
          ? `移入 ${targetGroup ? groupLabel(targetGroup) : '当前资源组'}（${selectedCount}）`
          : `移入 ${targetGroup ? groupLabel(targetGroup) : '当前资源组'}` }}
      </button>
    </form>

    <div
      v-if="confirmDialog"
      ref="confirmDialogEl"
      class="resource-search-select__overlay"
      data-test="confirm-dialog"
      role="dialog"
      aria-modal="true"
      aria-labelledby="resource-confirm-title"
      tabindex="-1"
      @keydown.escape.prevent="cancelConfirm"
      @click.self="cancelConfirm"
    >
      <div class="resource-search-select__dialog">
        <h3 id="resource-confirm-title">{{ confirmDialog.title }}</h3>
        <p>{{ confirmDialog.message }}</p>
        <div class="resource-search-select__dialog-actions">
          <button class="btn btn-secondary" type="button" data-test="confirm-cancel" :disabled="confirmBusy" @click="cancelConfirm">
            取消
          </button>
          <button class="btn btn-primary" type="button" data-test="confirm-accept" :disabled="confirmBusy" @click="acceptConfirm">
            {{ confirmBusy ? '提交中…' : confirmDialog.confirmText }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.resource-search-select,
.resource-search-select__members,
.resource-search-select__move {
  position: relative;
  display: grid;
  gap: var(--space-3);
}

.resource-search-select__search {
  position: relative;
  display: grid;
  gap: var(--space-3);
}

.resource-search-select__kinds {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.35rem;
}

.resource-search-select__kind {
  padding: 0.3rem 0.7rem;
  border: 1px solid var(--color-border-default);
  border-radius: 999px;
  background: var(--color-bg-canvas);
  color: var(--color-text-secondary);
  font: inherit;
  font-size: 0.8125rem;
  line-height: 1.2;
  cursor: pointer;
}

.resource-search-select .search-field {
  width: 100%;
}

.resource-search-select__kind--active {
  border-color: var(--color-primary);
  background: var(--color-primary);
  color: #fff;
}

.resource-search-select__kind-native,
.resource-search-select__sr-submit {
  position: absolute;
  width: 1px;
  height: 1px;
  padding: 0;
  margin: -1px;
  overflow: hidden;
  clip: rect(0, 0, 0, 0);
  white-space: nowrap;
  border: 0;
  pointer-events: none;
}

.resource-search-select__toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  color: var(--color-text-muted);
  font-size: 0.75rem;
}

.resource-search-select__select-all {
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--color-primary);
  font: inherit;
  font-size: 0.75rem;
  cursor: pointer;
}

.resource-search-select__select-all:disabled {
  color: var(--color-text-muted);
  cursor: not-allowed;
}

.resource-search-select__icon-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 2.25rem;
  height: 2.25rem;
  flex: 0 0 auto;
  padding: 0;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-canvas);
  color: var(--color-text-secondary);
  cursor: pointer;
}

.resource-search-select__icon-btn:hover:not(:disabled) {
  border-color: var(--color-primary);
  color: var(--color-primary);
}

.resource-search-select__icon-btn:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.resource-search-select__dialog-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-3);
}

.resource-search-select input[type="search"] {
  min-width: 0;
  width: 100%;
  padding: 0.45rem 0;
  border: 0;
  background: transparent;
  color: var(--color-text-primary);
  font: inherit;
  outline: none;
}

.resource-search-select input[type="search"]::-webkit-search-cancel-button {
  appearance: none;
}

.resource-search-select__alert {
  margin: 0;
  color: var(--color-danger);
}

.resource-search-select__status,
.resource-search-select__members small,
.resource-search-select__option small,
.resource-search-select__move p,
.resource-search-select__dialog p {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.resource-search-select__status {
  display: block;
}

.resource-search-select__retry {
  margin-left: 0.35rem;
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--color-primary);
  font: inherit;
  cursor: pointer;
}

.resource-search-select__members ul,
.resource-search-select__list {
  display: grid;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.resource-search-select__list {
  max-height: 16rem;
  overflow: auto;
}

.resource-search-select__check {
  width: 1rem;
  height: 1rem;
  flex-shrink: 0;
  border: 1px solid var(--color-border-default);
  border-radius: 0.25rem;
  background: var(--color-bg-canvas);
}

.resource-search-select__check--on {
  border-color: var(--color-primary);
  background: var(--color-primary);
  background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 16 16' fill='none' stroke='white' stroke-width='2.4' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpath d='M3.5 8.5l3 3 6-6'/%3E%3C/svg%3E");
  background-size: 0.75rem;
  background-position: center;
  background-repeat: no-repeat;
}

.resource-search-select__members li,
.resource-search-select__option {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--space-3);
  width: 100%;
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: transparent;
  color: inherit;
  text-align: left;
}

.resource-search-select__option {
  cursor: pointer;
}

.resource-search-select__members li span,
.resource-search-select__option span {
  display: grid;
  gap: 2px;
}

.resource-search-select__option--active {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.resource-search-select__move .btn {
  width: 100%;
}

.resource-search-select__overlay {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-4);
  background: rgba(15, 23, 42, 0.4);
  z-index: var(--z-modal, 40);
}

.resource-search-select__dialog {
  display: grid;
  gap: var(--space-4);
  width: min(28rem, 100%);
  padding: var(--space-5);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
}

.resource-search-select__dialog h3 {
  margin: 0;
}
</style>
