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
  targetGroupId: { type: String, default: '' }
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
  if (props.members != null) return flattenResources(props.members)
  const target = String(props.targetGroupId || '').trim()
  if (!target) return []
  return fetched.value.filter((item) => groupIdOf(item) === target)
})

const searchResults = computed(() => {
  const source = props.resources != null ? flattenResources(props.resources) : fetched.value
  const kind = String(kindFilter.value || '').trim()
  const q = String(searchQuery.value || '').trim().toLowerCase()
  return source.filter((item) => {
    if (kind && resourceKindOf(item) !== kind) return false
    if (!q) return true
    return resourceSearchText(item).includes(q)
  })
})

const selected = computed(() => {
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
  if (shouldAutoLoad()) loadCatalog()
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

function onKindChange(value) {
  kindFilter.value = value
  emit('update:kind', value)
}

function clearSearch() {
  if (!searchQuery.value) return
  searchQuery.value = ''
  emit('update:query', '')
}

function selectResource(item) {
  if (props.disabled || isBusy.value) return
  const next = normalizeResource(item)
  selectedInternal.value = next
  emit('update:modelValue', next)
  emit('select', next)
  actionError.value = ''
}

function requestMove() {
  if (!canWrite.value || isBusy.value) return
  if (!selected.value) {
    actionError.value = '请先选择要移动的资源。'
    return
  }
  const targetID = String(props.targetGroupId || '').trim()
  if (!targetID) {
    actionError.value = '请选择目标资源组。'
    return
  }
  if (targetID === groupIdOf(selected.value)) {
    actionError.value = '该资源已在当前资源组。'
    return
  }
  actionError.value = ''
  openConfirm({
    kind: 'move',
    title: '确认移动资源',
    confirmText: '确认移动',
    message: `将把「${resourceLabel(selected.value)}」从「${groupLabel(groupIdOf(selected.value))}」移动到「${groupLabel(targetID)}」。只改变所属资源组，不修改资源自身的业务配置。`,
    targetGroupId: targetID,
    resource: selected.value
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
    emit('move', {
      resource_kind: resourceKindOf(resource),
      resource_id: resourceIdOf(resource),
      resource_group_id: dialog.targetGroupId
    })
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
      <label>
        <span>资源类型</span>
        <select
          :value="kindFilter"
          data-test="resource-kind"
          :disabled="disabled"
          @change="onKindChange($event.target.value)"
        >
          <option v-for="option in RESOURCE_KIND_OPTIONS" :key="option.id || 'all'" :value="option.id">
            {{ option.label }}
          </option>
        </select>
      </label>
      <label>
        <span>搜索资源</span>
        <input
          v-model="searchQuery"
          data-test="resource-search"
          type="search"
          placeholder="按类型、名称或上下文搜索"
          :disabled="disabled"
          @keydown.esc.prevent="clearSearch"
        >
      </label>
      <button class="btn btn-secondary" type="submit" :disabled="disabled || catalogLoading">
        {{ catalogLoading ? '搜索中…' : '搜索' }}
      </button>
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
            class="btn btn-secondary"
            type="button"
            :data-test="unbindTestId(item)"
            :disabled="disabled || isBusy || groupIdOf(item) === DEFAULT_GROUP_ID"
            @click="requestUnbind(item)"
          >
            {{ busy === 'unbind' ? '解绑中…' : '解除绑定' }}
          </button>
        </li>
      </ul>
    </section>

    <div v-if="catalogLoading" class="resource-search-select__status">正在搜索资源…</div>
    <div v-else-if="catalogError" class="resource-search-select__status" role="alert">
      <p>{{ catalogError }}</p>
      <button class="btn btn-secondary" type="button" data-test="resource-retry" :disabled="disabled" @click="retrySearch">
        重试
      </button>
    </div>
    <p v-else-if="searched && !searchResults.length" class="resource-search-select__status">
      没有匹配的资源
    </p>
    <ul v-else-if="searchResults.length" class="resource-search-select__list" role="listbox" aria-label="可移动资源">
      <li v-for="item in searchResults" :key="`${resourceKindOf(item)}:${resourceIdOf(item)}`">
        <button
          type="button"
          role="option"
          class="resource-search-select__option"
          :class="{ 'resource-search-select__option--active': selected && sameResource(selected, item) }"
          :aria-selected="selected && sameResource(selected, item) ? 'true' : 'false'"
          :disabled="disabled || isBusy"
          :data-test="optionTestId(item)"
          @click="selectResource(item)"
        >
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

    <form v-if="canWrite" class="resource-search-select__move" data-test="move-form" @submit.prevent="requestMove">
      <p>
        把选中的资源移动到
        <strong>{{ targetGroup ? groupLabel(targetGroup) : '当前资源组' }}</strong>
        ，只改变所属组。
      </p>
      <button class="btn btn-secondary" type="submit" :disabled="disabled || isBusy || !selected">
        {{ busy === 'move' ? '移动中…' : '移动到当前组' }}
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
  display: grid;
  gap: var(--space-3);
}

.resource-search-select__search,
.resource-search-select__dialog-actions {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
  gap: var(--space-3);
  align-items: end;
}

.resource-search-select label {
  display: grid;
  gap: var(--space-2);
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.resource-search-select input,
.resource-search-select select {
  min-width: 0;
  padding: 0.65rem 0.75rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-canvas);
  color: var(--color-text-primary);
  font: inherit;
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
  display: grid;
  gap: var(--space-3);
  justify-items: start;
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
