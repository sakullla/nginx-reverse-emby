<script setup>
defineProps({
  detail: { type: Object, required: true },
  source: { type: Object, default: () => ({}) }
})

function bytes(value) {
  const size = Number(value) || 0
  if (size >= 1024 ** 3) return `${(size / 1024 ** 3).toFixed(1)} GiB`
  if (size >= 1024 ** 2) return `${(size / 1024 ** 2).toFixed(1)} MiB`
  if (size >= 1024) return `${(size / 1024).toFixed(1)} KiB`
  return `${size} B`
}
</script>

<template>
  <section class="package-summary" aria-label="插件包详情">
    <div class="package-summary__identity">
      <div>
        <span :class="['package-summary__source', { official: source.kind === 'official' }]">
          {{ source.kind === 'official' ? '官方来源' : '非官方来源' }} · {{ source.risk_label || '风险未标注' }}
        </span>
        <h2>{{ detail.manifest?.name || detail.manifest?.id || '插件包' }}</h2>
        <p>{{ detail.manifest?.id }} · {{ detail.version }}</p>
      </div>
      <dl>
        <div><dt>Runtime</dt><dd>{{ detail.runtime?.kind || '—' }}</dd></div>
        <div><dt>ABI</dt><dd>{{ detail.runtime?.abi || '—' }}</dd></div>
        <div><dt>宿主范围</dt><dd>{{ detail.runtime?.host_scope || '—' }}</dd></div>
      </dl>
    </div>
    <div class="package-summary__digest"><span>Package SHA-256</span><code>{{ detail.digest || '—' }}</code></div>
    <dl class="package-summary__facts">
      <div><dt>签名算法</dt><dd>{{ detail.signature?.algorithm || '—' }}</dd></div>
      <div><dt>签名 Key ID</dt><dd>{{ detail.signature?.key_id || '—' }}</dd></div>
      <div><dt>签名指纹</dt><dd>{{ source.signer_fingerprint || '由控制面验证' }}</dd></div>
      <div><dt>兼容范围</dt><dd>host {{ detail.manifest?.compatibility?.host || '—' }} / agent {{ detail.manifest?.compatibility?.agent || '—' }}</dd></div>
      <div><dt>超时预算</dt><dd>{{ detail.resource_budget?.timeout_ms || 0 }} ms</dd></div>
      <div><dt>内存预算</dt><dd>{{ bytes(detail.resource_budget?.memory_bytes) }}</dd></div>
      <div><dt>并发预算</dt><dd>{{ detail.resource_budget?.concurrency || 0 }}</dd></div>
      <div><dt>失败策略</dt><dd>{{ detail.failure_policy?.on_error || '—' }} / {{ detail.failure_policy?.core_fallback || '—' }}</dd></div>
    </dl>
    <div class="package-summary__section">
      <h3>平台制品与 checksum</h3>
      <div v-for="artifact in detail.artifacts || []" :key="artifact.path" class="artifact-row">
        <span>{{ artifact.goos && artifact.goarch ? `${artifact.goos}/${artifact.goarch}` : '通用' }} · {{ artifact.mode }}</span>
        <code>{{ artifact.sha256 }}</code>
        <small>{{ artifact.path }} · {{ bytes(artifact.size) }}</small>
      </div>
    </div>
    <div class="package-summary__section">
      <h3>权限差异</h3>
      <p v-if="!(detail.permission_diff?.added || []).length && !(detail.permission_diff?.removed || []).length">相对当前授权无变化</p>
      <p v-for="permission in detail.permission_diff?.added || []" :key="`add-${permission}`" class="permission-added">+ {{ permission }}</p>
      <p v-for="permission in detail.permission_diff?.removed || []" :key="`remove-${permission}`" class="permission-removed">− {{ permission }}</p>
    </div>
  </section>
</template>

<style scoped>
.package-summary { display: grid; gap: var(--space-5); }
.package-summary__identity { display: flex; justify-content: space-between; gap: var(--space-5); }
h2 { margin: var(--space-2) 0 var(--space-1); }
p { margin: 0; color: var(--color-text-muted); font-size: var(--text-sm); }
.package-summary__source { display: inline-flex; padding: 2px 8px; border-radius: var(--radius-full); background: var(--color-warning-subtle); color: var(--color-warning); font-size: var(--text-xs); }
.package-summary__source.official { background: var(--color-success-subtle); color: var(--color-success); }
dl { display: grid; gap: var(--space-2); margin: 0; }
.package-summary__identity dl { grid-template-columns: repeat(3, auto); align-content: start; }
.package-summary__facts { grid-template-columns: repeat(auto-fit, minmax(9rem, 1fr)); }
dt { color: var(--color-text-muted); font-size: var(--text-xs); }
dd { margin: 2px 0 0; font-size: var(--text-sm); overflow-wrap: anywhere; }
.package-summary__digest { display: grid; gap: var(--space-1); }
.package-summary__digest span { color: var(--color-text-muted); font-size: var(--text-xs); }
code { overflow-wrap: anywhere; font-size: var(--text-xs); }
.package-summary__section { padding-top: var(--space-4); border-top: 1px solid var(--color-border-subtle); }
.package-summary__section h3 { margin: 0 0 var(--space-3); font-size: var(--text-sm); }
.artifact-row { min-width: 0; display: grid; grid-template-columns: minmax(9rem, .5fr) minmax(0, 1fr); gap: var(--space-2); padding: var(--space-2) 0; }
.artifact-row code, .artifact-row small { min-width: 0; overflow-wrap: anywhere; }
.artifact-row small { grid-column: 1 / -1; color: var(--color-text-muted); }
@media (max-width: 42rem) { .artifact-row { grid-template-columns: minmax(0, 1fr); }.artifact-row small { grid-column: 1; } }
.permission-added { color: var(--color-danger); }
.permission-removed { color: var(--color-success); }
@media (max-width: 760px) { .package-summary__identity { display: grid; } .package-summary__identity dl { grid-template-columns: 1fr 1fr; } }
</style>
