<script setup>
import { computed, reactive, ref, watch } from 'vue'
import * as hostApi from '../../api'
import { configurePlugin, enablePlugin, publishPlugin } from '../../api/plugins'
import { sanitizePluginText, stripReadOnlyConfigValues } from '../../api/pluginSecurity'
import { pickDefaultResourceGroupID, resourceGroupDisplayName } from '../../context/useAccessControl'
import BaseModal from '../base/BaseModal.vue'
import PluginDeclarativeUI from './PluginDeclarativeUI.vue'
import { getAgentStatus, getAgentStatusLabel } from '../../utils/agentHelpers'
import { messageStore } from '../../stores/messages'

const props = defineProps({
  modelValue: { type: Boolean, required: true },
  pluginId: { type: String, required: true },
  instances: { type: Array, default: () => [] },
  agents: { type: Array, default: () => [] },
  resourceGroups: { type: Array, default: () => [] },
  configSchema: { type: Object, default: null },
  document: { type: Object, required: true },
  formEmpty: { type: Boolean, default: false },
  desiredLifecycle: { type: String, default: '' },
  currentLifecycle: { type: String, default: '' },
  declaresHTTPBackend: { type: Boolean, required: false },
  packageDetail: { type: Object, default: null },
  instance: { type: Object, default: null },
  publishedEntry: { type: Object, default: null },
  intent: { type: String, default: '' },
  canSubmit: { type: Boolean, default: true },
  httpRules: { type: Array, default: null }
})
const emit = defineEmits(['update:modelValue', 'deployed'])

const busy = ref(false)
const httpRulesLoading = ref(false)
const loadedHttpRules = ref([])
const agentQuery = ref('')
const agentStatusFilter = ref('')
const deployment = reactive({ resourceGroupID: '', targets: [], host: '', https: true })
const agentStatusOptions = [
  { value: '', label: '全部' },
  { value: 'online', label: '在线' },
  { value: 'offline', label: '离线' },
  { value: 'failed', label: '失败' }
]

const hasHTTPBackend = computed(() => {
  if (props.declaresHTTPBackend === true) return true
  if (props.declaresHTTPBackend === false) return false
  return packageDeclaresHTTPBackend(props.packageDetail)
})
const publishedRuleID = computed(() => {
  const ruleID = Number(props.publishedEntry?.rule_id)
  return Number.isInteger(ruleID) && ruleID > 0 ? ruleID : 0
})
const mode = computed(() => {
  if (props.intent === 'deploy' || props.intent === 'publish' || props.intent === 'update') return props.intent
  if (publishedRuleID.value) return 'update'
  if (props.instance?.id && hasHTTPBackend.value) return 'publish'
  return 'deploy'
})
const sortedAgents = computed(() => [...props.agents].sort((left, right) => String(left.name || left.id).localeCompare(String(right.name || right.id))))
const visibleAgents = computed(() => {
  const query = agentQuery.value.trim().toLowerCase()
  return sortedAgents.value.filter((agent) => {
    if (agentStatusFilter.value && getAgentStatus(agent) !== agentStatusFilter.value) return false
    if (!query) return true
    return String(agent.name || '').toLowerCase().includes(query) || String(agent.id || '').toLowerCase().includes(query)
  })
})
const selectedTargets = computed(() => {
  if (lockedScope.value) {
    const pinned = String((mode.value === 'update' ? props.publishedEntry?.agent_id : '') || props.instance?.targets?.[0] || '').trim()
    return pinned ? [pinned] : []
  }
  return [...new Set((deployment.targets || []).map((id) => String(id || '').trim()).filter(Boolean))]
})
const selectedAgent = computed(() => sortedAgents.value.find((agent) => agent.id === selectedTargets.value[0]) || null)
const allVisibleSelected = computed(() => (
  visibleAgents.value.length > 0 && visibleAgents.value.every((agent) => selectedTargets.value.includes(agent.id))
))
const submitLabel = computed(() => {
  if (busy.value) return hasHTTPBackend.value ? '发布中…' : '部署中…'
  return hasHTTPBackend.value ? '发布到域名' : '部署'
})
const modalTitle = computed(() => {
  if (mode.value === 'update') return '修改入口域名'
  if (mode.value === 'publish') return '发布到域名'
  return hasHTTPBackend.value ? '部署并发布' : '部署插件实例'
})
const modalSubtitle = computed(() => {
  if (mode.value === 'update') return '更新已发布入口的域名或是否 HTTPS，不会新建入口。'
  if (mode.value === 'publish') return '填写一条入口域名，把已部署的插件发布到所选节点。'
  if (hasHTTPBackend.value) return '选择一个或多个节点并填写入口域名，一次完成部署和发布。'
  return '选择资源组和节点，把插件部署到当前身份可见的范围。'
})
const showConfig = computed(() => mode.value === 'deploy' && !props.formEmpty)
const lockedScope = computed(() => mode.value === 'update')
const needsHttpRuleOptions = computed(() => documentNeedsHttpRuleOptions(props.document))
const visibleHttpRuleOptions = computed(() => httpRuleSelectOptions(
  Array.isArray(props.httpRules) ? props.httpRules : loadedHttpRules.value
))
const persistentBlocker = computed(() => {
  if (!props.resourceGroups.length) return '当前身份没有可见的资源组，无法部署。'
  if (!sortedAgents.value.length) return '当前没有可选择的节点。'
  if (needsHttpRuleOptions.value && !httpRulesLoading.value && !visibleHttpRuleOptions.value.length) {
    return '当前身份没有可见的 HTTP 规则，无法绑定规则。'
  }
  return ''
})
const submitBlocker = computed(() => {
  if (persistentBlocker.value) return persistentBlocker.value
  if (!selectedTargets.value.length) return '请选择至少一个节点。'
  if (hasHTTPBackend.value && !normalizeHost(deployment.host)) return '请填写一条入口域名。'
  return ''
})
const submitDisabled = computed(() => busy.value || httpRulesLoading.value || !!submitBlocker.value || !props.canSubmit)
const submitDocument = computed(() => bindHttpRuleOptions({
  ...(props.document || {}),
  title: props.document?.title || '插件配置',
  actions: [{ type: 'submit', id: 'deploy', label: submitLabel.value }]
}, visibleHttpRuleOptions.value))

watch(() => props.modelValue, (open) => {
  if (!open) return
  resetForm()
  void loadVisibleHttpRules()
})

function walkUIComponents(components, visit) {
  for (const component of components || []) {
    if (!component || typeof component !== 'object' || Array.isArray(component)) continue
    visit(component)
    if (Array.isArray(component.children)) walkUIComponents(component.children, visit)
  }
}

function documentNeedsHttpRuleOptions(document) {
  let needed = false
  walkUIComponents(document?.components, (component) => {
    if (component.options_source === 'http_rule') needed = true
  })
  return needed
}

function bindHttpRuleOptions(document, options) {
  const next = JSON.parse(JSON.stringify(document || {}))
  walkUIComponents(next.components, (component) => {
    if (component.options_source !== 'http_rule') return
    component.type = 'select'
    component.options = options
  })
  return next
}

function httpRuleOptionValue(rule) {
  if (rule.value != null) return String(rule.value).trim()
  const frontend = String(rule.frontend_url || '').trim()
  if (frontend && frontend.length <= 128) return frontend
  const id = String(rule.id ?? '').trim()
  const agentID = String(rule.agent_id || rule.agentId || '').trim()
  const compact = agentID && id ? `${agentID}:${id}` : id
  return compact.length <= 128 ? compact : ''
}

function httpRuleOptionLabel(rule, value) {
  if (rule.label != null && String(rule.label).trim()) return String(rule.label).trim()
  const named = String(rule.name || '').trim()
  if (named) return named
  const tag = Array.isArray(rule.tags) ? String(rule.tags[0] || '').trim() : ''
  if (tag) return tag
  const frontend = String(rule.frontend_url || '').trim()
  if (frontend) {
    try { return new URL(frontend).host || frontend } catch { return frontend.replace(/^https?:\/\//i, '') }
  }
  const agentName = String(rule.agent_name || rule.agentName || '').trim()
  if (agentName) return `${agentName} · ${value}`
  return value
}

function httpRuleSelectOptions(rules) {
  const seen = new Set()
  const options = []
  for (const rule of Array.isArray(rules) ? rules : []) {
    if (!rule || typeof rule !== 'object') continue
    const value = httpRuleOptionValue(rule)
    if (!value || seen.has(value)) continue
    seen.add(value)
    options.push({ value, label: httpRuleOptionLabel(rule, value) })
  }
  return options
}

function hostApiFn(name) {
  try {
    const fn = hostApi[name]
    return typeof fn === 'function' ? fn : null
  } catch {
    return null
  }
}

async function fetchVisibleHttpRules(agentIds) {
  const fetchPage = hostApiFn('fetchHttpRulesPage')
  if (fetchPage) {
    const collected = []
    let page = 1
    const pageSize = 100
    while (page <= 5) {
      const result = await fetchPage({ agentFilter: '__all__', page, pageSize })
      const items = Array.isArray(result?.items) ? result.items : (Array.isArray(result) ? result : [])
      collected.push(...items)
      const total = Number(result?.total)
      if (!Number.isFinite(total) || collected.length >= total || items.length < pageSize) break
      page += 1
    }
    return collected
  }
  const fetchGrouped = hostApiFn('fetchAllAgentsRules')
  if (fetchGrouped && agentIds.length) {
    const groups = await fetchGrouped(agentIds)
    return (Array.isArray(groups) ? groups : []).flatMap((group) => {
      const agentID = group?.agentId || group?.agent_id || ''
      return (Array.isArray(group?.rules) ? group.rules : []).map((rule) => ({
        ...rule,
        agent_id: rule?.agent_id || agentID
      }))
    })
  }
  return []
}

async function loadVisibleHttpRules() {
  loadedHttpRules.value = []
  if (!documentNeedsHttpRuleOptions(props.document) || Array.isArray(props.httpRules)) return
  httpRulesLoading.value = true
  try {
    loadedHttpRules.value = await fetchVisibleHttpRules(sortedAgents.value.map((agent) => agent.id))
  } catch {
    loadedHttpRules.value = []
  } finally {
    httpRulesLoading.value = false
  }
}

function packageDeclaresHTTPBackend(pkg) {
  if (!pkg || typeof pkg !== 'object') return false
  const manifest = pkg.manifest && typeof pkg.manifest === 'object' ? pkg.manifest : pkg
  const providers = manifest.http_backend_providers || pkg.http_backend_providers
  if (!Array.isArray(providers) || !providers.length) return false
  const extensions = manifest.extension_points || pkg.extension_points
  if (!Array.isArray(extensions) || !extensions.length) return true
  return extensions.includes('http.backend-provider')
}

function parseFrontendURL(value) {
  const raw = String(value || '').trim()
  if (!raw) return { host: '', https: true }
  try {
    const url = new URL(/^[a-z][a-z0-9+.-]*:\/\//i.test(raw) ? raw : `https://${raw}`)
    return { host: url.host, https: url.protocol !== 'http:' }
  } catch {
    return { host: raw.replace(/^https?:\/\//i, ''), https: !/^http:\/\//i.test(raw) }
  }
}

function normalizeHost(value) {
  return parseFrontendURL(value).host.replace(/\s+/g, '')
}

function buildFrontendURL(host, https) {
  const normalized = normalizeHost(host)
  if (!normalized) return ''
  return `${https ? 'https' : 'http'}://${normalized}`
}

function resetForm() {
  agentQuery.value = ''
  agentStatusFilter.value = ''
  const instance = props.instance
  const published = mode.value === 'update' ? parseFrontendURL(props.publishedEntry?.frontend_url) : { host: '', https: true }
  const preferredGroup = String(instance?.resource_group_id || '').trim()
  deployment.resourceGroupID = props.resourceGroups.some((group) => group.id === preferredGroup)
    ? preferredGroup
    : pickDefaultResourceGroupID(props.resourceGroups)
  const pinned = String(props.publishedEntry?.agent_id || '').trim()
  if (mode.value === 'update' && sortedAgents.value.some((agent) => agent.id === pinned)) {
    deployment.targets = [pinned]
  } else if (sortedAgents.value.length === 1) {
    deployment.targets = [sortedAgents.value[0].id]
  } else {
    deployment.targets = []
  }
  deployment.host = published.host
  deployment.https = published.host ? published.https : true
}

function defaultInstanceID() {
  const normalized = String(props.pluginId || 'plugin').toLowerCase().replace(/[^a-z0-9._:/-]+/g, '-').replace(/^[^a-z0-9]+/, '') || 'plugin'
  const used = new Set(props.instances.map((instance) => instance.id))
  const first = `${normalized}-default`.slice(0, 128)
  if (!used.has(first)) return first
  for (let index = 2; index < 10000; index += 1) {
    const suffix = `-${index}`
    const candidate = `${normalized.slice(0, 128 - suffix.length)}${suffix}`
    if (!used.has(candidate)) return candidate
  }
  return first
}

function resolveInstanceID() {
  const existing = String(props.instance?.id || '').trim()
  if (mode.value !== 'deploy') return existing
  return existing || defaultInstanceID()
}

function resolveConfig(payload) {
  if (payload?.config) return stripReadOnlyConfigValues(props.configSchema, payload.config)
  if (props.instance?.config) return stripReadOnlyConfigValues(props.configSchema, props.instance.config)
  return mode.value === 'deploy' ? {} : undefined
}

function toggleVisibleAgents() {
  if (!props.canSubmit || lockedScope.value || busy.value) return
  const ids = visibleAgents.value.map((agent) => agent.id)
  if (allVisibleSelected.value) {
    deployment.targets = deployment.targets.filter((id) => !ids.includes(id))
    return
  }
  const next = new Set(deployment.targets)
  for (const id of ids) next.add(id)
  deployment.targets = [...next]
}

async function deploy(payload) {
  if (busy.value || httpRulesLoading.value || !props.canSubmit) return
  if (submitBlocker.value) {
    messageStore.error(submitBlocker.value)
    return
  }
  const instanceID = resolveInstanceID()
  const resourceGroupID = String(deployment.resourceGroupID || '').trim()
  const targets = selectedTargets.value
  const frontendURL = hasHTTPBackend.value ? buildFrontendURL(deployment.host, deployment.https) : ''
  if (mode.value !== 'deploy' && !instanceID) {
    messageStore.error('缺少已部署实例，无法发布入口。')
    return
  }
  if (!props.resourceGroups.some((group) => group.id === resourceGroupID)) {
    messageStore.error(props.resourceGroups.length ? '请选择一个可见的资源组。' : '当前身份没有可见的资源组，无法部署。')
    return
  }
  if (!targets.length) {
    messageStore.error('请选择至少一个节点。')
    return
  }
  if (hasHTTPBackend.value && !frontendURL) {
    messageStore.error('请填写一条入口域名。')
    return
  }
  const request = {
    instance_id: instanceID,
    resource_group_id: resourceGroupID,
    targets,
    policy_chains: Array.isArray(props.instance?.policy_chains) ? props.instance.policy_chains : [],
    secret_replacements: payload?.secret_replacements || {}
  }
  const config = resolveConfig(payload)
  if (config !== undefined) request.config = config
  busy.value = true
  let configured = false
  try {
    if (hasHTTPBackend.value) {
      let published = null
      for (const target of targets) {
        const next = { ...request, targets: [target], frontend_url: frontendURL }
        if (mode.value === 'update' && publishedRuleID.value) next.rule_id = publishedRuleID.value
        published = await publishPlugin(props.pluginId, next)
      }
      messageStore.success(mode.value === 'update' ? '入口已更新' : mode.value === 'publish' ? '入口已发布' : '插件已部署并发布')
      emit('deployed', published?.instance?.id || published?.id || instanceID)
      emit('update:modelValue', false)
      return
    }
    const created = await configurePlugin(props.pluginId, { ...request, bindings: [] })
    configured = true
    if (props.desiredLifecycle !== 'enabled' && props.currentLifecycle !== 'active') await enablePlugin(props.pluginId)
    messageStore.success('插件已部署')
    emit('deployed', created?.id || instanceID)
    emit('update:modelValue', false)
  } catch (cause) {
    const message = sanitizePluginText(cause?.message || (hasHTTPBackend.value ? '发布插件入口失败' : '部署插件实例失败'))
    messageStore.error(configured ? `配置已提交，但启用失败：${message}` : message)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <BaseModal
    :model-value="modelValue"
    :title="modalTitle"
    :subtitle="modalSubtitle"
    size="lg"
    :close-on-click-modal="false"
    :show-footer="true"
    data-test="plugin-deploy-modal"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <section class="plugin-deployment" :aria-label="modalTitle">
      <div class="plugin-deployment__metadata">
        <label>
          <span>资源组</span>
          <select
            v-model="deployment.resourceGroupID"
            data-test="deployment-resource-group"
            :disabled="!resourceGroups.length || !canSubmit || lockedScope || busy"
          >
            <option v-if="!resourceGroups.length" value="">暂无可见资源组</option>
            <option v-for="group in resourceGroups" :key="group.id" :value="group.id">{{ resourceGroupDisplayName(group) }}</option>
          </select>
        </label>
      </div>
      <fieldset class="plugin-deployment__agents">
        <legend>节点</legend>
        <p class="plugin-deployment__agent-hint">可同时选多个节点；发布入口时会对每个所选节点各写一条域名。</p>
        <div v-if="lockedScope && selectedAgent" class="plugin-deployment__picker">
          <div class="plugin-deployment__agent plugin-deployment__agent--locked">
            <span
              class="plugin-deployment__agent-dot"
              :class="`plugin-deployment__agent-dot--${getAgentStatus(selectedAgent)}`"
              aria-hidden="true"
            />
            <span class="plugin-deployment__agent-copy">
              <strong>{{ selectedAgent.name || selectedAgent.id }}</strong>
              <small>将更新该节点上的原入口</small>
            </span>
          </div>
        </div>
        <div v-else-if="sortedAgents.length" class="plugin-deployment__picker">
          <div class="plugin-deployment__picker-toolbar">
            <label class="plugin-deployment__agent-search">
              <svg width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
                <circle cx="11" cy="11" r="7" />
                <path d="M20 20l-3.5-3.5" />
              </svg>
              <input
                v-model="agentQuery"
                type="search"
                placeholder="搜索节点"
                aria-label="搜索节点"
                :disabled="!canSubmit || busy"
              >
            </label>
            <div class="plugin-deployment__agent-filters" role="group" aria-label="节点状态">
              <button
                v-for="option in agentStatusOptions"
                :key="option.value || 'all'"
                type="button"
                class="plugin-deployment__agent-filter"
                :class="{ 'plugin-deployment__agent-filter--active': agentStatusFilter === option.value }"
                :disabled="!canSubmit || busy"
                @click="agentStatusFilter = option.value"
              >{{ option.label }}</button>
            </div>
            <button
              type="button"
              class="plugin-deployment__agent-filter"
              :class="{ 'plugin-deployment__agent-filter--active': allVisibleSelected }"
              :disabled="!canSubmit || busy || !visibleAgents.length"
              @click="toggleVisibleAgents"
            >{{ allVisibleSelected ? '取消全选' : '全选' }}</button>
          </div>
          <div
            v-if="visibleAgents.length"
            class="plugin-deployment__agent-list"
            role="group"
            aria-label="选择节点"
            aria-multiselectable="true"
          >
            <label
              v-for="agent in visibleAgents"
              :key="agent.id"
              class="plugin-deployment__agent"
              :class="{
                'plugin-deployment__agent--selected': selectedTargets.includes(agent.id),
                [`plugin-deployment__agent--${getAgentStatus(agent)}`]: true
              }"
            >
              <input
                v-model="deployment.targets"
                type="checkbox"
                name="plugin-deployment-target"
                :value="agent.id"
                data-test="deployment-agent"
                :disabled="!canSubmit || lockedScope || busy"
              >
              <span
                class="plugin-deployment__agent-dot"
                :class="`plugin-deployment__agent-dot--${getAgentStatus(agent)}`"
                aria-hidden="true"
              />
              <span class="plugin-deployment__agent-copy">
                <strong>{{ agent.name || agent.id }}</strong>
                <small>{{ getAgentStatusLabel(getAgentStatus(agent)) }}</small>
              </span>
            </label>
          </div>
          <p v-else class="plugin-deployment__picker-empty">没有匹配的节点。</p>
        </div>
        <p v-else class="plugin-deployment__empty">当前没有可选择的节点。</p>
      </fieldset>
      <fieldset v-if="hasHTTPBackend" class="plugin-deployment__entry">
        <legend>入口域名</legend>
        <div class="plugin-deployment__entry-fields">
          <label>
            <span>域名</span>
            <input
              v-model="deployment.host"
              type="text"
              data-test="deployment-domain"
              autocomplete="off"
              spellcheck="false"
              placeholder="例如 media.example.com"
              :disabled="!canSubmit || busy"
            >
          </label>
          <label class="plugin-deployment__https">
            <input v-model="deployment.https" type="checkbox" data-test="deployment-https" :disabled="!canSubmit || busy">
            <span>使用 HTTPS</span>
          </label>
        </div>
        <p class="plugin-deployment__empty">HTTPS 开启后按该入口申请托管证书。</p>
      </fieldset>
      <p v-if="!canSubmit" class="plugin-deployment__readonly">当前身份只能查看下一步，不能提交。</p>
      <p v-else-if="persistentBlocker" class="plugin-deployment__empty">{{ persistentBlocker }}</p>
      <div v-if="formEmpty && mode === 'deploy'" class="plugin-deployment__empty-config">
        <p class="plugin-config-empty">此插件没有需要先填写的配置，可直接{{ hasHTTPBackend ? '发布到域名' : '部署' }}。</p>
        <button class="btn btn-primary" type="button" :disabled="submitDisabled" @click="deploy({ config: {}, secret_replacements: {} })">
          {{ submitLabel }}
        </button>
      </div>
      <div v-else-if="!showConfig" class="plugin-deployment__empty-config">
        <button class="btn btn-primary" type="button" :disabled="submitDisabled" @click="deploy({ config: instance?.config || {}, secret_replacements: {} })">
          {{ submitLabel }}
        </button>
      </div>
      <PluginDeclarativeUI
        v-else
        :document="submitDocument"
        :config="{}"
        :secret-fields="[]"
        :saving="submitDisabled"
        :can-configure="true"
        :can-act="false"
        @submit="deploy"
      />
    </section>
    <template #footer>
      <button
        class="btn btn-secondary"
        type="button"
        data-test="plugin-modal-cancel"
        :disabled="busy"
        @click="emit('update:modelValue', false)"
      >
        取消
      </button>
    </template>
  </BaseModal>
</template>

<style scoped>
.plugin-deployment { display: grid; gap: var(--space-5); min-width: 0; }
.plugin-deployment__metadata { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--space-4); }
.plugin-deployment__metadata label, .plugin-deployment__entry-fields label { display: grid; gap: var(--space-2); color: var(--color-text-secondary); font-size: var(--text-sm); }
.plugin-deployment__metadata select, .plugin-deployment__entry-fields input[type="text"] { min-width: 0; padding: .6rem .75rem; border: 1px solid var(--color-border-default); border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); font: inherit; }
.plugin-deployment__metadata select:focus-visible, .plugin-deployment__entry-fields input[type="text"]:focus-visible { outline: none; border-color: var(--color-primary); box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 18%, transparent); }
.plugin-deployment__agents, .plugin-deployment__entry { display: grid; gap: var(--space-2); min-width: 0; margin: 0; padding: 0; border: 0; }
.plugin-deployment__agents legend, .plugin-deployment__entry legend { margin-bottom: var(--space-1); color: var(--color-text-primary); font-weight: 600; font-size: var(--text-sm); }
.plugin-deployment__agent-hint { margin: 0; color: var(--color-text-muted); font-size: var(--text-xs); }
.plugin-deployment__picker {
  min-width: 0; overflow: hidden;
  border: 1px solid var(--color-border-default); border-radius: var(--radius-md);
  background: var(--color-bg-surface);
}
.plugin-deployment__picker-toolbar {
  display: flex; flex-wrap: wrap; align-items: center; gap: .5rem .65rem;
  padding: .45rem .6rem; border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
}
.plugin-deployment__agent-search {
  display: flex; align-items: center; gap: .4rem; flex: 1 1 11rem; min-width: 0;
  color: var(--color-text-muted);
}
.plugin-deployment__agent-search input {
  flex: 1; min-width: 0; padding: .2rem 0; border: 0; background: transparent;
  color: var(--color-text-primary); font: inherit; font-size: var(--text-sm); outline: none;
}
.plugin-deployment__agent-search input::-webkit-search-cancel-button { appearance: none; }
.plugin-deployment__agent-filters { display: flex; flex-wrap: wrap; gap: .25rem; }
.plugin-deployment__agent-filter {
  padding: .15rem .55rem; border: 0; border-radius: 999px;
  background: var(--color-bg-surface); color: var(--color-text-secondary);
  font: inherit; font-size: var(--text-xs); cursor: pointer;
}
.plugin-deployment__agent-filter--active { background: var(--color-primary); color: #fff; }
.plugin-deployment__agent-filter:disabled { cursor: not-allowed; opacity: .6; }
.plugin-deployment__agent-list { max-height: 12.75rem; overflow: auto; min-width: 0; }
.plugin-deployment__agent {
  display: flex; align-items: center; gap: .55rem; margin: 0; min-height: 2.25rem;
  padding: 0 .7rem; border: 0; border-bottom: 1px solid var(--color-border-subtle);
  background: transparent; cursor: pointer;
}
.plugin-deployment__agent:last-child { border-bottom: 0; }
.plugin-deployment__agent:hover { background: var(--color-bg-hover); }
.plugin-deployment__agent--selected { background: color-mix(in srgb, var(--color-primary) 8%, transparent); }
.plugin-deployment__agent--locked { cursor: default; }
.plugin-deployment__agent input {
  flex-shrink: 0; width: .9rem; height: .9rem; margin: 0;
  accent-color: var(--color-primary);
}
.plugin-deployment__agent-dot { width: 7px; height: 7px; border-radius: 50%; flex-shrink: 0; background: var(--color-text-muted); }
.plugin-deployment__agent-dot--online { background: var(--color-success); }
.plugin-deployment__agent-dot--offline { background: var(--color-text-muted); }
.plugin-deployment__agent-dot--failed { background: var(--color-danger); }
.plugin-deployment__agent-dot--pending { background: var(--color-warning); }
.plugin-deployment__agent-copy {
  min-width: 0; flex: 1; display: flex; align-items: center; justify-content: space-between; gap: .75rem;
}
.plugin-deployment__agent strong, .plugin-deployment__agent small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.plugin-deployment__agent strong { font-size: .8125rem; font-weight: 600; }
.plugin-deployment__agent small, .plugin-deployment__empty, .plugin-deployment__readonly, .plugin-deployment__picker-empty { color: var(--color-text-muted); }
.plugin-deployment__picker-empty { margin: 0; padding: .85rem .75rem; font-size: var(--text-sm); }
.plugin-deployment__empty, .plugin-deployment__readonly { margin: 0; font-size: var(--text-sm); }
.plugin-deployment__entry-fields { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: var(--space-4); align-items: end; }
.plugin-deployment__https { display: flex !important; align-items: center; gap: var(--space-2); min-height: 2.6rem; }
.plugin-deployment__https input { accent-color: var(--color-primary); }
.plugin-deployment__empty-config { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); }
.plugin-deployment__empty-config p { margin: 0; }
.plugin-config-empty { color: var(--color-text-muted); font-size: var(--text-xs); }

@media (max-width: 640px) {
  .plugin-deployment__entry-fields { grid-template-columns: 1fr; }
  .plugin-deployment__empty-config { align-items: stretch; flex-direction: column; }
}
</style>
