<script setup>
import { computed, onMounted, reactive, ref } from 'vue'
import {
  bindResource,
  createResourceGroup,
  fetchResourceGroupGrants,
  fetchResourceGroups,
  fetchRoles,
  fetchUsers,
  grantResourceGroup
} from '../../api/access'
import { resourceGroupDisplayName, useAccessControl } from '../../context/useAccessControl'
import EmptyState from '../../components/base/EmptyState.vue'

const resourceKindOptions = [
  { id: 'agent', label: '节点' },
  { id: 'http_rule', label: 'HTTP 规则' },
  { id: 'l4_rule', label: 'L4 规则' },
  { id: 'relay_listener', label: 'Relay 监听器' },
  { id: 'certificate', label: '证书' },
  { id: 'egress_profile', label: '出口配置' }
]

const { actor, can, refreshActor } = useAccessControl()
const loading = ref(true)
const error = ref('')
const actionError = ref('')
const actionBusy = ref('')
const groups = ref([])
const grants = ref([])
const users = ref([])
const roles = ref([])
const selectedID = ref('')
const createForm = reactive({ name: '', description: '' })
const grantForm = reactive({ subjectKind: 'user', subjectID: '' })
const bindForm = reactive({ resourceKind: 'agent', resourceID: '' })

const canRead = computed(() => can('resource.read') || can('*'))
const canCreate = computed(() => can('access.manage') || can('*'))
const canGrantOrBind = computed(() => can('system.admin') || can('*'))
const visibleGroups = computed(() => groups.value.filter((group) => group && group.id))
const selectedGroup = computed(() => visibleGroups.value.find((group) => group.id === selectedID.value) || null)
const selectedGrants = computed(() => grants.value.filter((grant) => grant.resource_group_id === selectedID.value))
const grantSubjects = computed(() => grantForm.subjectKind === 'role' ? roles.value : users.value)
const defaultGroupVisible = computed(() => visibleGroups.value.some((group) => group.id === 'default'))

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
  try {
    if (!actor.value) await refreshActor()
    if (!canRead.value) {
      groups.value = []
      grants.value = []
      users.value = []
      roles.value = []
      return
    }
    const [nextGroups, nextGrants, nextUsers, nextRoles] = await Promise.all([
      fetchResourceGroups(),
      canGrantOrBind.value ? fetchResourceGroupGrants().catch(() => []) : Promise.resolve([]),
      canCreate.value || canGrantOrBind.value ? fetchUsers().catch(() => []) : Promise.resolve([]),
      canCreate.value || canGrantOrBind.value ? fetchRoles().catch(() => []) : Promise.resolve([])
    ])
    groups.value = Array.isArray(nextGroups) ? nextGroups : []
    grants.value = Array.isArray(nextGrants) ? nextGrants : []
    users.value = Array.isArray(nextUsers) ? nextUsers : []
    roles.value = Array.isArray(nextRoles) ? nextRoles : []
    if (!visibleGroups.value.some((group) => group.id === selectedID.value)) {
      selectedID.value = pickSelectedGroupID(visibleGroups.value)
    }
    if (!grantForm.subjectID) grantForm.subjectID = grantSubjects.value[0]?.id || ''
  } catch (cause) {
    error.value = cause?.message || '读取资源组失败'
  } finally {
    loading.value = false
  }
}

function pickSelectedGroupID(list) {
  if (list.some((group) => group.id === 'default')) return 'default'
  return list[0]?.id || ''
}

function subjectLabel(grant) {
  if (grant.subject_kind === 'role') {
    const role = roles.value.find((item) => item.id === grant.subject_id)
    return role?.name || grant.subject_id
  }
  const user = users.value.find((item) => item.id === grant.subject_id)
  return user?.display_name || user?.username || grant.subject_id
}

function subjectOptionLabel(item) {
  return item.display_name || item.username || item.name || item.id
}

async function submitCreate() {
  if (!canCreate.value || actionBusy.value) return
  const name = createForm.name.trim()
  if (!name) {
    actionError.value = '请填写资源组名称。'
    return
  }
  actionBusy.value = 'create'
  actionError.value = ''
  try {
    const created = await createResourceGroup({
      name,
      description: createForm.description.trim()
    })
    createForm.name = ''
    createForm.description = ''
    await load()
    if (created?.id) selectedID.value = created.id
  } catch (cause) {
    actionError.value = cause?.message || '创建资源组失败'
  } finally {
    actionBusy.value = ''
  }
}

async function submitGrant() {
  if (!canGrantOrBind.value || !selectedGroup.value || actionBusy.value) return
  const subjectID = grantForm.subjectID.trim()
  if (!subjectID) {
    actionError.value = '请选择要授权的用户或角色。'
    return
  }
  actionBusy.value = 'grant'
  actionError.value = ''
  try {
    await grantResourceGroup({
      subject_kind: grantForm.subjectKind,
      subject_id: subjectID,
      resource_group_id: selectedGroup.value.id
    })
    await load()
  } catch (cause) {
    actionError.value = cause?.message || '授权失败'
  } finally {
    actionBusy.value = ''
  }
}

async function submitBind() {
  if (!canGrantOrBind.value || !selectedGroup.value || actionBusy.value) return
  const resourceID = bindForm.resourceID.trim()
  if (!resourceID) {
    actionError.value = '请填写要绑定的资源。'
    return
  }
  actionBusy.value = 'bind'
  actionError.value = ''
  try {
    await bindResource({
      resource_kind: bindForm.resourceKind,
      resource_id: resourceID,
      resource_group_id: selectedGroup.value.id
    })
    bindForm.resourceID = ''
    await load()
  } catch (cause) {
    actionError.value = cause?.message || '绑定资源失败'
  } finally {
    actionBusy.value = ''
  }
}
</script>

<template>
  <main class="resource-groups-page">
    <header class="page-header">
      <div class="page-header__left">
        <RouterLink to="/access" class="back-link">← 访问与安全</RouterLink>
        <h1 class="page-title">资源组</h1>
        <p class="page-subtitle">查看当前身份可见的资源组，创建新组，并对已有组授权或绑定资源。插件部署只从这些组里选择。</p>
      </div>
    </header>

    <div v-if="loading" class="resource-groups-page__loading">
      <div class="spinner"></div>
      <p>正在读取资源组…</p>
    </div>

    <EmptyState
      v-else-if="!canRead"
      title="无权查看资源组"
      description="当前身份没有 resource.read 权限。"
    />

    <div v-else-if="error" role="alert">
      <EmptyState title="读取失败" :description="error">
        <template #action>
          <button class="btn btn-secondary" type="button" @click="load">重试</button>
        </template>
      </EmptyState>
    </div>

    <template v-else>
      <p v-if="actionError" class="resource-alert" role="alert">{{ actionError }}</p>

      <section class="resource-workspace" aria-label="资源组">
        <aside class="resource-list">
          <div class="resource-list__heading">
            <strong>可见资源组</strong>
            <span>{{ visibleGroups.length }}</span>
          </div>
          <p v-if="!visibleGroups.length" class="resource-list__empty">当前身份没有可见资源组</p>
          <button
            v-for="group in visibleGroups"
            v-else
            :key="group.id"
            type="button"
            :class="['resource-list__item', { 'resource-list__item--active': selectedID === group.id }]"
            @click="selectedID = group.id"
          >
            <span>
              <strong>{{ resourceGroupDisplayName(group) }}</strong>
              <small>{{ group.builtin ? '内置组' : '自定义组' }}</small>
            </span>
          </button>
        </aside>

        <div v-if="selectedGroup" class="resource-detail">
          <div class="resource-detail__header">
            <div>
              <h2>{{ resourceGroupDisplayName(selectedGroup) }}</h2>
              <p>{{ selectedGroup.description || '暂无说明' }}</p>
            </div>
          </div>

          <dl class="resource-facts">
            <div>
              <dt>类型</dt>
              <dd>{{ selectedGroup.builtin ? '内置组' : '自定义组' }}</dd>
            </div>
            <div>
              <dt>默认组</dt>
              <dd>{{ selectedGroup.id === 'default' ? '是，插件部署可见时默认选中' : '否' }}</dd>
            </div>
          </dl>

          <p v-if="selectedGroup.id === 'default'" class="resource-notice">
            默认组始终可用作部署目标，不必手填内部 ID。
          </p>

          <section v-if="canGrantOrBind" class="resource-panel" aria-label="授权">
            <h3>授权</h3>
            <p>把用户或角色加入当前组后，他们才能看到该组下的插件实例。</p>
            <ul v-if="selectedGrants.length" class="resource-grants">
              <li v-for="grant in selectedGrants" :key="`${grant.subject_kind}:${grant.subject_id}`">
                {{ grant.subject_kind === 'role' ? '角色' : '用户' }} · {{ subjectLabel(grant) }}
              </li>
            </ul>
            <p v-else class="resource-empty">当前还没有额外授权记录。</p>
            <form class="resource-form" data-test="grant-form" @submit.prevent="submitGrant">
              <label>
                <span>主体类型</span>
                <select v-model="grantForm.subjectKind" data-test="grant-subject-kind" @change="grantForm.subjectID = grantSubjects[0]?.id || ''">
                  <option value="user">用户</option>
                  <option value="role">角色</option>
                </select>
              </label>
              <label>
                <span>{{ grantForm.subjectKind === 'role' ? '角色' : '用户' }}</span>
                <select v-model="grantForm.subjectID" data-test="grant-subject-id">
                  <option v-if="!grantSubjects.length" value="">暂无可选项</option>
                  <option v-for="item in grantSubjects" :key="item.id" :value="item.id">{{ subjectOptionLabel(item) }}</option>
                </select>
              </label>
              <button class="btn btn-secondary" type="submit" :disabled="actionBusy === 'grant' || !grantForm.subjectID">
                {{ actionBusy === 'grant' ? '授权中…' : '授权到当前组' }}
              </button>
            </form>
          </section>

          <section v-if="canGrantOrBind" class="resource-panel" aria-label="绑定资源">
            <h3>绑定资源</h3>
            <p>把已有节点、规则或证书放到当前组。插件部署不会在这里创建组。</p>
            <form class="resource-form" data-test="bind-form" @submit.prevent="submitBind">
              <label>
                <span>资源类型</span>
                <select v-model="bindForm.resourceKind" data-test="bind-resource-kind">
                  <option v-for="kind in resourceKindOptions" :key="kind.id" :value="kind.id">{{ kind.label }}</option>
                </select>
              </label>
              <label>
                <span>资源</span>
                <input v-model="bindForm.resourceID" data-test="bind-resource-id" type="text" placeholder="已有资源 ID，例如 edge-a">
              </label>
              <button class="btn btn-secondary" type="submit" :disabled="actionBusy === 'bind' || !bindForm.resourceID.trim()">
                {{ actionBusy === 'bind' ? '绑定中…' : '绑定到当前组' }}
              </button>
            </form>
          </section>
        </div>

        <div v-else class="resource-detail resource-detail--empty">
          <strong>当前没有可见资源组</strong>
          <p v-if="canCreate">创建一个组后，再把它授权给用户或绑定已有资源。</p>
          <p v-else>请联系管理员把你加入至少一个资源组。</p>
        </div>
      </section>

      <section v-if="canCreate" class="resource-create" aria-label="创建资源组">
        <h2>创建资源组</h2>
        <p>名称面向人看；系统会生成内部 ID。默认组 {{ defaultGroupVisible ? '已可见' : '不可见' }}，不必手填它的 ID。</p>
        <form class="resource-form" data-test="create-form" @submit.prevent="submitCreate">
          <label>
            <span>名称</span>
            <input v-model="createForm.name" data-test="create-group-name" type="text" placeholder="例如 团队组">
          </label>
          <label>
            <span>说明</span>
            <input v-model="createForm.description" data-test="create-group-description" type="text" placeholder="可选">
          </label>
          <button class="btn btn-primary" type="submit" :disabled="actionBusy === 'create' || !createForm.name.trim()">
            {{ actionBusy === 'create' ? '创建中…' : '创建资源组' }}
          </button>
        </form>
      </section>
    </template>
  </main>
</template>

<style scoped>
.resource-groups-page {
  max-width: 1180px;
  display: grid;
  gap: var(--space-6);
  margin: 0 auto;
}

.resource-groups-page__loading {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-3);
  padding: 4rem 2rem;
  color: var(--color-text-muted);
}

.back-link {
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  text-decoration: none;
}

.back-link:hover {
  color: var(--color-primary);
}

.resource-alert {
  color: var(--color-danger);
}

.resource-workspace {
  display: grid;
  grid-template-columns: minmax(14rem, 18rem) minmax(0, 1fr);
  gap: var(--space-5);
}

.resource-list,
.resource-detail,
.resource-create {
  display: grid;
  gap: var(--space-4);
  padding: var(--space-5);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
}

.resource-list__heading,
.resource-detail__header {
  display: flex;
  justify-content: space-between;
  gap: var(--space-3);
}

.resource-list__item {
  display: flex;
  width: 100%;
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: transparent;
  color: inherit;
  text-align: left;
  cursor: pointer;
}

.resource-list__item span {
  display: grid;
  gap: 2px;
}

.resource-list__item--active {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.resource-list__empty,
.resource-empty,
.resource-notice,
.resource-detail p,
.resource-create p {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.resource-detail h2,
.resource-create h2,
.resource-panel h3 {
  margin: 0;
}

.resource-facts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(10rem, 1fr));
  gap: var(--space-3);
  margin: 0;
}

.resource-facts dt {
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.resource-facts dd {
  margin: 0.25rem 0 0;
}

.resource-panel {
  display: grid;
  gap: var(--space-3);
  padding-top: var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
}

.resource-grants {
  margin: 0;
  padding-left: 1.25rem;
}

.resource-form {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(12rem, 1fr));
  gap: var(--space-3);
  align-items: end;
}

.resource-form label {
  display: grid;
  gap: var(--space-2);
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.resource-form input,
.resource-form select {
  min-width: 0;
  padding: 0.65rem 0.75rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-canvas);
  color: var(--color-text-primary);
  font: inherit;
}

@media (max-width: 800px) {
  .resource-workspace {
    grid-template-columns: 1fr;
  }
}
</style>
