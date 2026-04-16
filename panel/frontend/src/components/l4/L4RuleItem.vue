<template>
  <div class="l4-card" :class="{ 'l4-card--disabled': !rule.enabled }">
    <div class="l4-card__header">
      <div class="l4-card__badges">
        <span class="l4-card__id">#{{ rule.id }}</span>
        <span class="l4-card__proto" :class="`l4-card__proto--${rule.protocol}`">
          {{ rule.protocol?.toUpperCase() }}
        </span>
        <span class="l4-card__status" :class="`l4-card__status--${status}`">
          {{ statusLabel }}
        </span>
      </div>
      <div class="l4-card__actions">
        <button class="l4-card__action l4-card__action--toggle" :title="rule.enabled ? '停用' : '启用'" @click="$emit('toggle', rule)">
          <svg v-if="rule.enabled" width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/></svg>
          <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><polygon points="5 3 19 12 5 21 5 3"/></svg>
        </button>
        <button class="l4-card__action" title="复制" @click="$emit('copy', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/></svg>
        </button>
        <button class="l4-card__action" title="编辑" @click="$emit('edit', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
        </button>
        <button v-if="canDiagnose" class="l4-card__action l4-card__action--diagnose" title="诊断" @click="$emit('diagnose', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12h4l2-6 4 12 2-6h6"/></svg>
        </button>
        <button class="l4-card__action l4-card__action--delete" title="删除" @click="$emit('delete', rule)">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="3 6 5 6 21 6"/><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/></svg>
        </button>
      </div>
    </div>
    <div class="l4-card__mapping">
      <div class="l4-card__endpoint">
        <span class="l4-card__url-icon">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/></svg>
        </span>
        <code class="l4-card__addr">{{ rule.listen_host }}:{{ rule.listen_port }}</code>
      </div>
      <div class="l4-card__endpoint">
        <span class="l4-card__url-icon">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M5 12h14"/><path d="M12 5l7 7-7 7"/></svg>
        </span>
        <code class="l4-card__addr" v-if="!hasMultipleBackends">{{ primaryBackend }}</code>
        <code class="l4-card__addr" v-else :title="backendsTooltip">{{ primaryBackend }} <span class="l4-card__more">+{{ backendCount - 1 }}</span></code>
        <span class="l4-card__lb" :title="lbTitle">{{ lbLabel }}</span>
      </div>
    </div>
    <div v-if="tuningTags.length" class="l4-card__tuning">
      <span v-for="tag in tuningTags" :key="tag" class="l4-card__tuning-tag">{{ tag }}</span>
    </div>
    <div v-if="rule.tags?.length" class="l4-card__tags">
      <span v-for="tag in rule.tags" :key="tag" class="tag">{{ tag }}</span>
    </div>
  </div>
</template>

<script setup>
import { computed } from 'vue'
import { getRuleEffectiveStatus } from '../../utils/syncStatus'

const props = defineProps({ rule: { type: Object, required: true }, agent: { type: Object, default: null } })
defineEmits(['edit', 'delete', 'copy', 'toggle', 'diagnose'])

const status = computed(() => getRuleEffectiveStatus(props.rule, props.agent))
const statusLabel = computed(() => ({ active: '启用', pending: '待同步', failed: '同步失败', disabled: '已禁用' }[status.value] || '未知'))
const canDiagnose = computed(() => String(props.rule?.protocol || '').toLowerCase() === 'tcp')

const backends = computed(() => {
  if (Array.isArray(props.rule.backends) && props.rule.backends.length > 0) return props.rule.backends
  if (props.rule.upstream_host && props.rule.upstream_port) return [{ host: props.rule.upstream_host, port: props.rule.upstream_port }]
  return []
})
const backendCount = computed(() => backends.value.length)
const hasMultipleBackends = computed(() => backendCount.value > 1)
const primaryBackend = computed(() => { const b = backends.value[0]; return b ? `${b.host}:${b.port}` : '-' })
const backendsTooltip = computed(() => backends.value.map((b, i) => {
  let s = `${i + 1}. ${b.host}:${b.port}`
  if (b.weight > 1) s += ` (权重${b.weight})`
  if (b.backup) s += ' [备用]'
  return s
}).join('\n'))

const LB_MAP = { round_robin: 'RR', random: 'RND' }
const LB_TITLES = { round_robin: '轮询 (Round Robin)', random: '随机 (Random)' }
const lbLabel = computed(() => LB_MAP[props.rule.load_balancing?.strategy] || 'RR')
const lbTitle = computed(() => LB_TITLES[props.rule.load_balancing?.strategy] || '轮询')

const tuningTags = computed(() => {
  const t = props.rule.tuning
  if (!t) return []
  const tags = []
  const isUdp = props.rule.protocol === 'udp'
  const defaultIdle = isUdp ? '20s' : '10m'
  if (t.proxy?.idle_timeout && t.proxy.idle_timeout !== defaultIdle) tags.push(`超时:${t.proxy.idle_timeout}`)
  if (t.proxy?.connect_timeout && t.proxy.connect_timeout !== '10s') tags.push(`连接:${t.proxy.connect_timeout}`)
  if (t.limit_conn?.count && Number(t.limit_conn.count) > 0) tags.push(`限连:${t.limit_conn.count}`)
  const mf = t.upstream?.max_fails
  const ft = t.upstream?.fail_timeout
  if ((mf !== undefined && mf !== 3) || (ft && ft !== '30s')) tags.push(`健检:${mf ?? 3}/${ft || '30s'}`)
  if (t.listen?.reuseport === true && !isUdp) tags.push('reuseport')
  if (t.listen?.so_keepalive === true) tags.push('keepalive')
  if (t.proxy?.buffer_size && t.proxy.buffer_size !== '16k') tags.push(`buf:${t.proxy.buffer_size}`)
  if (t.proxy_protocol?.decode) tags.push('PP接收')
  if (t.proxy_protocol?.send) tags.push('PP发送')
  return tags
})
</script>

<style scoped>
.l4-card {
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  transition: opacity 0.15s;
}
.l4-card--disabled { opacity: 0.6; }
.l4-card__header { display: flex; align-items: center; justify-content: space-between; }
.l4-card__badges { display: flex; align-items: center; gap: 0.5rem; }
.l4-card__id { font-size: 0.75rem; font-family: var(--font-mono); color: var(--color-text-tertiary); }
.l4-card__proto { display: inline-block; font-size: 0.7rem; font-weight: 700; padding: 2px 6px; border-radius: var(--radius-sm); font-family: var(--font-mono); }
.l4-card__proto--tcp { background: var(--color-warning-50); color: var(--color-warning); }
.l4-card__proto--udp { background: #f3e8ff; color: #7c3aed; }
.l4-card__status { font-size: 0.75rem; font-weight: 600; padding: 2px 8px; border-radius: var(--radius-full); }
.l4-card__status--active { background: var(--color-success-50); color: var(--color-success); }
.l4-card__status--disabled { background: var(--color-bg-subtle); color: var(--color-text-muted); }
.l4-card__status--failed { background: var(--color-danger-50); color: var(--color-danger); }
.l4-card__actions { display: flex; gap: 0.25rem; }
.l4-card__action { display: flex; align-items: center; justify-content: center; width: 28px; height: 28px; border-radius: var(--radius-md); border: none; background: transparent; color: var(--color-text-tertiary); cursor: pointer; transition: all 0.15s; }
.l4-card__action:hover { background: var(--color-bg-hover); color: var(--color-text-primary); }
.l4-card__action--delete:hover { background: var(--color-danger-50); color: var(--color-danger); }
.l4-card__action--toggle:hover { background: var(--color-warning-50); color: var(--color-warning); }
.l4-card__action--diagnose:hover { background: rgba(56, 189, 248, 0.12); color: var(--color-primary); }
.l4-card__mapping { display: flex; flex-direction: column; gap: 0.375rem; }
.l4-card__endpoint { display: flex; align-items: center; gap: 0.5rem; min-width: 0; }
.l4-card__addr { font-family: var(--font-mono); font-size: 0.875rem; font-weight: 500; color: var(--color-text-primary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.l4-card__url-icon { display: flex; align-items: center; justify-content: center; color: var(--color-text-tertiary); flex-shrink: 0; }
.l4-card__more { color: var(--color-text-muted); font-weight: 400; }
.l4-card__lb { font-size: 0.7rem; font-weight: 700; padding: 1px 6px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-sm); flex-shrink: 0; }
.l4-card__tuning { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.l4-card__tuning-tag { font-size: 0.7rem; padding: 1px 6px; background: var(--color-bg-subtle); border: 1px solid var(--color-border-subtle); border-radius: var(--radius-sm); color: var(--color-text-secondary); font-family: var(--font-mono); }
.l4-card__tags { display: flex; gap: 0.25rem; flex-wrap: wrap; }
.tag { font-size: 0.75rem; padding: 2px 8px; background: var(--color-primary-subtle); color: var(--color-primary); border-radius: var(--radius-full); font-weight: 500; }
</style>
