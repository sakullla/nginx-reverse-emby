<script setup>
import { computed, ref, watch } from 'vue'
import { fetchAllAgentsRules, fetchHttpRulesPage } from '../../api'
import { configurePlugin, invokePluginDynamicAction, publishPlugin } from '../../api/plugins'
import { resolvePointer } from '../../api/pluginCondition'
import { sanitizePluginText, stripReadOnlyConfigValues } from '../../api/pluginSecurity'
import BaseModal from '../base/BaseModal.vue'
import PluginDeclarativeUI from './PluginDeclarativeUI.vue'

const props = defineProps({
  modelValue: { type: Boolean, required: true },
  pluginId: { type: String, required: true },
  instance: { type: Object, default: null },
  document: { type: Object, required: true },
  config: { type: Object, default: () => ({}) },
  secretFields: { type: Array, default: () => [] },
  configSchema: { type: Object, default: null },
  canWrite: { type: Boolean, default: false },
  canPublish: { type: Boolean, default: false },
  hasHTTPBackend: { type: Boolean, default: false },
  packageDetail: { type: Object, default: null },
  publishedEntries: { type: Array, default: () => [] },
  agents: { type: Array, default: () => [] },
  httpRules: { type: Array, default: null },
  intent: { type: String, default: 'configure' }
})
const emit = defineEmits(['update:modelValue', 'saved', 'refreshed'])

const busy = ref(false)
const actionBusy = ref(false)
const publishBusy = ref(false)
const error = ref('')
const publishHost = ref('')
const publishHTTPS = ref(true)
const publishTarget = ref('')
const editingRuleID = ref(0)
const loadedHttpRules = ref([])

const httpBackendDeclared = computed(() => {
  if (props.hasHTTPBackend) return true
  return packageDeclaresHTTPBackend(props.packageDetail)
})
const instanceTargets = computed(() => [...new Set((props.instance?.targets || []).map((target) => String(target || '').trim()).filter(Boolean))])
const instanceEntries = computed(() => (Array.isArray(props.publishedEntries) ? props.publishedEntries : []).filter((entry) => {
  if (!entry || typeof entry !== 'object') return false
  const ruleID = Number(entry.rule_id)
  const agentID = String(entry.agent_id || '').trim()
  const frontendURL = String(entry.frontend_url || '').trim()
  if (!Number.isInteger(ruleID) || ruleID <= 0 || !frontendURL) return false
  if (!instanceTargets.value.length) return true
  return !agentID || instanceTargets.value.includes(agentID)
}))
const needsPublish = computed(() => httpBackendDeclared.value && !instanceEntries.value.length)
const showPublishForm = computed(() => httpBackendDeclared.value)
const modalTitle = computed(() => {
  if (httpBackendDeclared.value && (props.intent === 'publish' || needsPublish.value)) return '发布到域名'
  return `编辑配置 · ${props.instance?.id || ''}`
})
const modalSubtitle = computed(() => {
  if (httpBackendDeclared.value && (props.intent === 'publish' || needsPublish.value)) {
    return '只选一个节点，填写一条入口域名，并选择是否使用 HTTPS。'
  }
  return props.instance ? `资源组 ${props.instance.resource_group_id} · 版本 ${props.instance.config_version}` : ''
})
const publishSubmitLabel = computed(() => {
  if (publishBusy.value) return editingRuleID.value > 0 ? '保存入口中…' : '发布中…'
  return editingRuleID.value > 0 ? '保存入口' : '发布到域名'
})
const publishBlocked = computed(() => {
  if (!httpBackendDeclared.value) return ''
  if (!instanceTargets.value.length) return '当前实例没有可发布的节点。'
  if (!String(publishTarget.value || '').trim()) return '请选择一个节点后再发布。'
  if (!String(publishHost.value || '').trim()) return '请填写一条入口域名。'
  return ''
})
const needsHttpRuleOptions = computed(() => documentNeedsHttpRuleOptions(props.document))
const visibleHttpRuleOptions = computed(() => mergeRuleOptions(
  httpRuleSelectOptions(Array.isArray(props.httpRules) ? props.httpRules : loadedHttpRules.value),
  collectBoundHttpRuleValues(props.document?.components, props.config).map((value) => ({ value, label: value }))
))
const boundDocument = computed(() => bindHttpRuleOptions(props.document || {}, visibleHttpRuleOptions.value))
const httpRuleBlocker = computed(() => {
  if (!needsHttpRuleOptions.value) return ''
  if (httpRuleSelectOptions(Array.isArray(props.httpRules) ? props.httpRules : loadedHttpRules.value).length) return ''
  return '当前没有可见的 HTTP 规则。'
})

watch(() => props.modelValue, (open) => {
  if (!open) {
    loadedHttpRules.value = []
    return
  }
  resetPublishForm()
  void loadVisibleHttpRules()
})

function parseFrontendURL(value) {
  const raw = String(value || '').trim()
  if (!raw) return { https: true, host: '' }
  try {
    const parsed = new URL(raw.includes('://') ? raw : `https://${raw}`)
    return { https: parsed.protocol === 'https:', host: parsed.host }
  } catch {
    return { https: !raw.startsWith('http://'), host: raw.replace(/^https?:\/\//i, '') }
  }
}

function buildFrontendURL(host, https) {
  const trimmed = String(host || '').trim()
  if (!trimmed) return ''
  if (/^https?:\/\//i.test(trimmed)) return trimmed
  return `${https ? 'https' : 'http'}://${trimmed}`
}

function agentLabel(agentID) {
  const id = String(agentID || '').trim()
  const agent = (props.agents || []).find((item) => item.id === id)
  return agent?.name || id
}

function entryStatus(entry) {
  if (!entry?.enabled) return '未启用'
  return entry.accessible ? '可访问' : '已发布但还不能访问'
}

function packageDeclaresHTTPBackend(pkg) {
  if (!pkg || typeof pkg !== 'object') return false
  const manifest = pkg.manifest && typeof pkg.manifest === 'object' ? pkg.manifest : pkg
  const providers = manifest.http_backend_providers || pkg.http_backend_providers
  return Array.isArray(providers) && providers.some((provider) => String(provider?.id || provider || '').trim())
}

function packageExtensionPoints(pkg) {
  if (!pkg || typeof pkg !== 'object') return []
  const manifest = pkg.manifest && typeof pkg.manifest === 'object' ? pkg.manifest : {}
  const raw = manifest.extension_points || pkg.extension_points
  if (!Array.isArray(raw)) return []
  return raw.map((point) => String(point || '').trim()).filter(Boolean)
}

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

function hasMissingRequiredHttpRule(components, model, base = '') {
  for (const component of components || []) {
    if (!component || typeof component !== 'object') continue
    const pointer = base + (component.binding || '')
    if (component.options_source === 'http_rule' && component.required) {
      const current = resolvePointer(model, pointer)
      if (typeof current !== 'string' || !current.trim()) return true
    }
    if (component.type === 'array') {
      const items = resolvePointer(model, pointer)
      if (Array.isArray(items) && Array.isArray(component.children)) {
        for (const index of items.keys()) {
          if (hasMissingRequiredHttpRule(component.children, model, `${pointer}/${index}`)) return true
        }
      }
    } else if (Array.isArray(component.children) && hasMissingRequiredHttpRule(component.children, model, base)) {
      return true
    }
  }
  return false
}

function collectBoundHttpRuleValues(components, model, base = '', acc = []) {
  for (const component of components || []) {
    if (!component || typeof component !== 'object') continue
    const pointer = base + (component.binding || '')
    if (component.options_source === 'http_rule') {
      const current = resolvePointer(model, pointer)
      if (typeof current === 'string' && current.trim()) acc.push(current.trim())
    }
    if (component.type === 'array') {
      const items = resolvePointer(model, pointer)
      if (Array.isArray(items) && Array.isArray(component.children)) {
        items.forEach((_, index) => collectBoundHttpRuleValues(component.children, model, `${pointer}/${index}`, acc))
      }
    } else if (Array.isArray(component.children)) {
      collectBoundHttpRuleValues(component.children, model, base, acc)
    }
  }
  return acc
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

function mergeRuleOptions(primary, extras) {
  const seen = new Set()
  const options = []
  for (const option of [...primary, ...extras]) {
    const value = String(option?.value || '').trim()
    if (!value || seen.has(value)) continue
    seen.add(value)
    options.push({ value, label: String(option.label || value) })
  }
  return options
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

async function fetchVisibleHttpRules(agentIds) {
  if (typeof fetchHttpRulesPage === 'function') {
    const collected = []
    let page = 1
    const pageSize = 100
    while (page <= 5) {
      const result = await fetchHttpRulesPage({ agentFilter: '__all__', page, pageSize })
      const items = Array.isArray(result?.items)
        ? result.items
        : (Array.isArray(result?.rules) ? result.rules : (Array.isArray(result) ? result : []))
      collected.push(...items)
      const total = Number(result?.total)
      if (!Number.isFinite(total) || collected.length >= total || items.length < pageSize) break
      page += 1
    }
    return collected
  }
  if (typeof fetchAllAgentsRules === 'function' && agentIds.length) {
    const groups = await fetchAllAgentsRules(agentIds)
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
  try {
    loadedHttpRules.value = await fetchVisibleHttpRules((props.agents || []).map((agent) => agent.id).filter(Boolean))
  } catch {
    loadedHttpRules.value = []
  }
}

function closeWithoutSaving() {
  if (busy.value || publishBusy.value || actionBusy.value) return
  error.value = ''
  emit('update:modelValue', false)
}

// Configure can only persist bindings the package owns. Publish overlays
// plugin_provider HTTP rules as http_rule projections that http.backend-provider cannot store.
function persistableConfigureBindings(instance, pkg, httpBackendHint) {
  const bindings = Array.isArray(instance?.bindings) ? instance.bindings : []
  const points = packageExtensionPoints(pkg)
  const canOwnHTTPRule = points.includes('http.request') || points.includes('http.response')
  const dropProjectedHTTPRule = (httpBackendHint || packageDeclaresHTTPBackend(pkg)) && !canOwnHTTPRule
  return bindings
    .filter((binding) => {
      if (!binding?.consumer) return false
      const kind = String(binding.consumer.kind || '').trim()
      if (kind === 'http_rule' && dropProjectedHTTPRule) return false
      if (kind === 'l4_rule' && points.length && !points.includes('l4.accept')) return false
      return true
    })
    .map((binding) => ({
      consumer: { kind: binding.consumer.kind, id: binding.consumer.id },
      target_agent_id: binding.target_agent_id
    }))
}

function resetPublishForm() {
  error.value = ''
  const preferred = props.intent === 'publish' ? null : instanceEntries.value[0]
  if (preferred) {
    applyEntry(preferred)
    return
  }
  startNewPublish()
}

function applyEntry(entry) {
  const parsed = parseFrontendURL(entry?.frontend_url)
  publishHost.value = parsed.host
  publishHTTPS.value = parsed.https
  editingRuleID.value = Number(entry?.rule_id) || 0
  publishTarget.value = String(entry?.agent_id || instanceTargets.value[0] || '').trim()
}

function startNewPublish() {
  publishHost.value = ''
  publishHTTPS.value = true
  editingRuleID.value = 0
  publishTarget.value = instanceTargets.value.length === 1 ? instanceTargets.value[0] : ''
}

async function save(payload) {
  if (!props.canWrite || !props.instance || busy.value) return
  if (httpRuleBlocker.value && hasMissingRequiredHttpRule(props.document?.components, payload?.config)) {
    error.value = httpRuleBlocker.value
    return
  }
  busy.value = true
  error.value = ''
  try {
    const instance = props.instance
    await configurePlugin(props.pluginId, {
      instance_id: instance.id,
      resource_group_id: instance.resource_group_id,
      targets: instance.targets,
      policy_chains: instance.policy_chains || [],
      bindings: persistableConfigureBindings(instance, props.packageDetail, props.hasHTTPBackend),
      config: stripReadOnlyConfigValues(props.configSchema, payload.config),
      secret_replacements: payload.secret_replacements || {}
    })
    emit('saved')
    emit('update:modelValue', false)
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || '保存插件配置失败')
  } finally {
    busy.value = false
  }
}

async function publish() {
  if (!props.canPublish || !props.instance || !httpBackendDeclared.value || publishBusy.value) return
  const blocker = publishBlocked.value
  if (blocker) {
    error.value = blocker
    return
  }
  const target = String(publishTarget.value || '').trim()
  const frontendURL = buildFrontendURL(publishHost.value, publishHTTPS.value)
  publishBusy.value = true
  error.value = ''
  try {
    const payload = {
      instance_id: props.instance.id,
      resource_group_id: props.instance.resource_group_id,
      targets: [target],
      policy_chains: props.instance.policy_chains || [],
      frontend_url: frontendURL,
      config: stripReadOnlyConfigValues(props.configSchema, props.config),
      secret_replacements: {}
    }
    if (editingRuleID.value > 0) payload.rule_id = editingRuleID.value
    await publishPlugin(props.pluginId, payload)
    emit('saved')
    emit('update:modelValue', false)
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || (editingRuleID.value > 0 ? '保存入口失败' : '发布到域名失败'))
  } finally {
    publishBusy.value = false
  }
}

async function runDynamicAction({ action, target_id, confirmed }) {
  if (!props.canWrite || !props.instance || actionBusy.value) return
  actionBusy.value = true
  error.value = ''
  try {
    await invokePluginDynamicAction(props.pluginId, props.instance.id, action.id, target_id, confirmed)
    emit('refreshed')
  } catch (cause) {
    error.value = sanitizePluginText(cause?.message || `动态操作 ${action.label} 失败`)
  } finally {
    actionBusy.value = false
  }
}
</script>

<template>
  <BaseModal
    :model-value="modelValue"
    :title="modalTitle"
    :subtitle="modalSubtitle"
    size="lg"
    :show-footer="true"
    :close-on-click-modal="false"
    data-test="plugin-instance-config-modal"
    @update:model-value="emit('update:modelValue', $event)"
  >
    <section class="plugin-instance-config" :aria-label="httpBackendDeclared && (intent === 'publish' || needsPublish) ? '发布到域名' : '编辑实例配置'">
      <p v-if="error" class="plugin-alert" role="alert">{{ error }}</p>
      <p v-else-if="httpRuleBlocker" class="plugin-alert" role="alert" data-test="plugin-http-rule-empty">{{ httpRuleBlocker }}</p>

      <section v-if="httpBackendDeclared" class="plugin-publish" aria-label="入口域名">
        <p v-if="needsPublish" class="plugin-next-step" data-test="plugin-publish-needed">
          还差发布：填写一条入口域名后，就能用这个地址访问。
        </p>
        <p v-else-if="!canPublish" class="plugin-next-step">当前身份只能查看下一步，不能提交发布或改入口。</p>

        <ul v-if="instanceEntries.length" class="plugin-published-entries" data-test="plugin-published-entries">
          <li v-for="entry in instanceEntries" :key="`${entry.agent_id}:${entry.rule_id}`" class="plugin-published-entry" data-test="plugin-published-entry">
            <div>
              <strong>{{ entry.frontend_url }}</strong>
              <small>{{ agentLabel(entry.agent_id) }} · {{ entry.frontend_url.startsWith('https:') ? 'HTTPS' : 'HTTP' }} · {{ entryStatus(entry) }}</small>
            </div>
            <button
              v-if="canPublish"
              class="btn btn-secondary btn-sm"
              type="button"
              :disabled="publishBusy"
              @click="applyEntry(entry)"
            >
              修改入口
            </button>
          </li>
        </ul>

        <div v-if="showPublishForm" class="plugin-publish__form">
          <fieldset v-if="instanceTargets.length > 1 && editingRuleID <= 0" class="plugin-publish__targets">
            <legend>发布节点</legend>
            <label v-for="target in instanceTargets" :key="target" class="plugin-publish__target" :class="{ 'plugin-publish__target--selected': publishTarget === target }">
              <input v-model="publishTarget" type="radio" name="plugin-publish-target" :value="target" :disabled="!canPublish || publishBusy">
              <span>{{ agentLabel(target) }}</span>
            </label>
          </fieldset>
          <p v-else-if="publishTarget" class="plugin-publish__node">节点：{{ agentLabel(publishTarget) }}</p>

          <label class="plugin-publish__host">
            <span>入口域名</span>
            <input
              v-model="publishHost"
              type="text"
              autocomplete="off"
              placeholder="例如 media.example.com"
              data-test="plugin-publish-domain"
              :disabled="!canPublish || publishBusy"
            >
          </label>
          <label class="plugin-publish__https">
            <input v-model="publishHTTPS" type="checkbox" data-test="plugin-publish-https" :disabled="!canPublish || publishBusy">
            <span>使用 HTTPS</span>
          </label>

          <div class="plugin-publish__actions">
            <button
              v-if="canPublish"
              class="btn btn-primary"
              type="button"
              data-test="plugin-publish-submit"
              :disabled="publishBusy || !!publishBlocked"
              @click="publish"
            >
              {{ publishSubmitLabel }}
            </button>
            <button
              v-if="canPublish && instanceEntries.length"
              class="btn btn-secondary"
              type="button"
              data-test="plugin-publish-another"
              :disabled="publishBusy"
              @click="startNewPublish"
            >
              再发布一条域名
            </button>
          </div>
        </div>
      </section>

      <PluginDeclarativeUI
        v-if="instance"
        :document="boundDocument"
        :config="config"
        :secret-fields="secretFields"
        :saving="busy"
        :action-busy="actionBusy"
        :can-configure="canWrite"
        :can-act="canWrite"
        @submit="save"
        @dynamic="runDynamicAction"
      />
    </section>
    <template #footer>
      <button
        class="btn btn-secondary"
        type="button"
        data-test="plugin-modal-cancel"
        :disabled="busy || publishBusy || actionBusy"
        @click="closeWithoutSaving"
      >
        取消
      </button>
    </template>
  </BaseModal>
</template>

<style scoped>
.plugin-instance-config { display: grid; gap: var(--space-4); min-width: 0; }
.plugin-alert { margin: 0; color: var(--color-danger); font-size: var(--text-sm); }
.plugin-publish { display: grid; gap: var(--space-4); min-width: 0; }
.plugin-next-step { margin: 0; color: var(--color-text-secondary); font-size: var(--text-sm); }
.plugin-published-entries { display: grid; gap: var(--space-3); margin: 0; padding: 0; list-style: none; }
.plugin-published-entry {
  display: flex; align-items: center; justify-content: space-between; gap: var(--space-3);
  padding: var(--space-3); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
}
.plugin-published-entry div { min-width: 0; display: grid; gap: 2px; }
.plugin-published-entry strong, .plugin-published-entry small { overflow-wrap: anywhere; }
.plugin-published-entry small { color: var(--color-text-muted); }
.plugin-publish__form { display: grid; gap: var(--space-3); min-width: 0; }
.plugin-publish__targets { display: grid; gap: var(--space-2); margin: 0; padding: 0; border: 0; }
.plugin-publish__targets legend { margin-bottom: var(--space-1); color: var(--color-text-primary); font-weight: 600; font-size: var(--text-sm); }
.plugin-publish__target {
  display: flex; align-items: center; gap: var(--space-2); padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-subtle); border-radius: var(--radius-md);
  background: var(--color-bg-surface); cursor: pointer;
}
.plugin-publish__target--selected { border-color: var(--color-primary); background: color-mix(in srgb, var(--color-primary) 6%, var(--color-bg-surface)); }
.plugin-publish__node, .plugin-publish__host { margin: 0; color: var(--color-text-secondary); font-size: var(--text-sm); }
.plugin-publish__host { display: grid; gap: var(--space-2); }
.plugin-publish__host input {
  min-width: 0; padding: .6rem .75rem; border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); font: inherit;
}
.plugin-publish__host input:focus-visible { outline: none; border-color: var(--color-primary); box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary) 18%, transparent); }
.plugin-publish__https { display: flex; align-items: center; gap: var(--space-2); color: var(--color-text-secondary); font-size: var(--text-sm); }
.plugin-publish__actions { display: flex; flex-wrap: wrap; gap: var(--space-2); }

@media (max-width: 640px) {
  .plugin-published-entry { align-items: stretch; flex-direction: column; }
}
</style>
