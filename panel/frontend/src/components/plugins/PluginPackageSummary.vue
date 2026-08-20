<script setup>
import BaseBadge from '../base/BaseBadge.vue'

defineProps({
  detail: { type: Object, required: true },
  source: { type: Object, default: () => ({}) },
  showIdentity: { type: Boolean, default: true },
  collapsible: { type: Boolean, default: true }
})

function bytes(value) {
  const size = Number(value) || 0
  if (size >= 1024 ** 3) return `${(size / 1024 ** 3).toFixed(1)} GiB`
  if (size >= 1024 ** 2) return `${(size / 1024 ** 2).toFixed(1)} MiB`
  if (size >= 1024) return `${(size / 1024).toFixed(1)} KiB`
  return `${size} B`
}

function artifactLabel(artifact) {
  if (artifact.goos && artifact.goarch) return `${artifact.goos}/${artifact.goarch}`
  return '通用'
}
</script>

<template>
  <section class="package-summary" aria-label="插件包详情">
    <div v-if="showIdentity" class="package-summary__identity">
      <div>
        <BaseBadge :tone="source.kind === 'official' ? 'success' : 'warning'" class="package-summary__source" :class="{ official: source.kind === 'official' }">
          {{ source.kind === 'official' ? '官方来源' : '非官方来源' }}
        </BaseBadge>
        <h2>{{ detail.manifest?.name || detail.manifest?.id || '插件包' }}</h2>
        <p>{{ detail.version }}</p>
      </div>
    </div>

    <component :is="collapsible ? 'details' : 'div'" :class="collapsible ? 'package-summary__technical' : 'package-summary__facts-block'">
      <summary v-if="collapsible">技术详情</summary>

      <div class="package-summary__digest">
        <span>Package SHA-256</span>
        <code>{{ detail.digest || '—' }}</code>
      </div>

      <div class="package-summary__groups">
        <section class="package-summary__group">
          <h3>运行时</h3>
          <dl class="package-summary__facts">
            <div><dt>Runtime</dt><dd>{{ detail.runtime?.kind || '—' }}</dd></div>
            <div><dt>ABI</dt><dd>{{ detail.runtime?.abi || '—' }}</dd></div>
            <div><dt>宿主范围</dt><dd>{{ detail.runtime?.host_scope || '—' }}</dd></div>
            <div><dt>兼容范围</dt><dd>host {{ detail.manifest?.compatibility?.host || '—' }} / agent {{ detail.manifest?.compatibility?.agent || '—' }}</dd></div>
          </dl>
        </section>

        <section class="package-summary__group">
          <h3>签名与来源</h3>
          <dl class="package-summary__facts">
            <div><dt>来源风险</dt><dd>{{ source.risk_label || '风险未标注' }}</dd></div>
            <div><dt>签名算法</dt><dd>{{ detail.signature?.algorithm || '—' }}</dd></div>
            <div><dt>签名 Key ID</dt><dd>{{ detail.signature?.key_id || '—' }}</dd></div>
            <div><dt>签名指纹</dt><dd>{{ source.signer_fingerprint || '由控制面验证' }}</dd></div>
          </dl>
        </section>

        <section class="package-summary__group">
          <h3>资源预算</h3>
          <dl class="package-summary__facts">
            <div><dt>超时预算</dt><dd>{{ detail.resource_budget?.timeout_ms || 0 }} ms</dd></div>
            <div><dt>内存预算</dt><dd>{{ bytes(detail.resource_budget?.memory_bytes) }}</dd></div>
            <div><dt>并发预算</dt><dd>{{ detail.resource_budget?.concurrency || 0 }}</dd></div>
            <div><dt>失败策略</dt><dd>{{ detail.failure_policy?.on_error || '—' }} / {{ detail.failure_policy?.core_fallback || '—' }}</dd></div>
          </dl>
        </section>
      </div>

      <div class="package-summary__section">
        <h3>平台制品与 checksum</h3>
        <p v-if="!(detail.artifacts || []).length" class="package-summary__empty">没有随包发布的平台制品。</p>
        <div v-for="artifact in detail.artifacts || []" :key="artifact.path" class="artifact-row">
          <div class="artifact-row__meta">
            <span>{{ artifactLabel(artifact) }} · {{ artifact.mode }}</span>
            <small>{{ artifact.path }} · {{ bytes(artifact.size) }}</small>
          </div>
          <code>{{ artifact.sha256 }}</code>
        </div>
      </div>

      <div class="package-summary__section">
        <h3>权限差异</h3>
        <p v-if="!(detail.permission_diff?.added || []).length && !(detail.permission_diff?.removed || []).length">相对当前授权无变化</p>
        <div v-else class="package-summary__permissions">
          <p v-for="permission in detail.permission_diff?.added || []" :key="`add-${permission}`" class="permission-added">+ {{ permission }}</p>
          <p v-for="permission in detail.permission_diff?.removed || []" :key="`remove-${permission}`" class="permission-removed">− {{ permission }}</p>
        </div>
      </div>
    </component>
  </section>
</template>

<style scoped>
.package-summary {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
}

.package-summary__technical,
.package-summary__facts-block {
  display: grid;
  gap: var(--space-4);
  min-width: 0;
}

.package-summary__technical summary {
  cursor: pointer;
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
}

.package-summary__identity {
  display: flex;
  justify-content: space-between;
  gap: var(--space-5);
}

h2 {
  margin: var(--space-2) 0 var(--space-1);
}

p {
  margin: 0;
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.package-summary__digest {
  display: grid;
  gap: 0.35rem;
  min-width: 0;
  padding: 0.75rem 0.9rem;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: color-mix(in srgb, var(--color-bg-subtle) 55%, var(--color-bg-surface));
}

.package-summary__digest span {
  color: var(--color-text-muted);
  font-size: 0.7rem;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.package-summary__digest code,
.artifact-row code {
  min-width: 0;
  overflow-wrap: anywhere;
  font-family: var(--font-mono);
  font-size: 0.75rem;
  color: var(--color-text-primary);
}

.package-summary__groups {
  display: grid;
  gap: var(--space-3);
}

.package-summary__group,
.package-summary__section {
  min-width: 0;
  padding: 0.85rem 0.95rem;
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  background: var(--color-bg-surface);
}

.package-summary__group h3,
.package-summary__section h3 {
  margin: 0 0 0.7rem;
  color: var(--color-text-secondary);
  font-size: 0.75rem;
  font-weight: 700;
  letter-spacing: 0.04em;
}

.package-summary__facts {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(9.5rem, 1fr));
  gap: 0.75rem 1rem;
  margin: 0;
}

dt {
  color: var(--color-text-muted);
  font-size: 0.7rem;
}

dd {
  margin: 0.2rem 0 0;
  color: var(--color-text-primary);
  font-size: var(--text-sm);
  overflow-wrap: anywhere;
}

.package-summary__empty {
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.artifact-row {
  display: grid;
  gap: 0.35rem;
  min-width: 0;
  padding: 0.65rem 0;
  border-top: 1px solid var(--color-border-subtle);
}

.artifact-row:first-of-type {
  padding-top: 0;
  border-top: 0;
}

.artifact-row__meta {
  display: grid;
  gap: 0.15rem;
  min-width: 0;
}

.artifact-row small {
  color: var(--color-text-muted);
  overflow-wrap: anywhere;
}

.package-summary__permissions {
  display: grid;
  gap: 0.35rem;
}

.permission-added,
.permission-removed {
  margin: 0;
  padding: 0.35rem 0.55rem;
  border-radius: var(--radius-md);
  font-family: var(--font-mono);
  font-size: 0.75rem;
  overflow-wrap: anywhere;
}

.permission-added {
  color: var(--color-danger);
  background: color-mix(in srgb, var(--color-danger) 8%, var(--color-bg-subtle));
}

.permission-removed {
  color: var(--color-success);
  background: color-mix(in srgb, var(--color-success) 8%, var(--color-bg-subtle));
}

@media (max-width: 760px) {
  .package-summary__identity {
    display: grid;
  }

  .package-summary__facts {
    grid-template-columns: 1fr 1fr;
  }
}
</style>
