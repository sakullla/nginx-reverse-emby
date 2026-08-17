<template>
  <div class="resource-groups-page">
    <div class="resource-groups-page__header">
      <div>
        <h1 class="resource-groups-page__title">资源组</h1>
        <p class="resource-groups-page__desc">
          插件声明的隔离边界。鉴权、映射与后续用户绑定都挂在这些组上，而不是面板硬编码。
        </p>
      </div>
    </div>

    <div class="resource-groups-page__toolbar">
      <input
        v-model="query"
        class="input resource-groups-page__search"
        type="search"
        placeholder="搜索名称、引用、插件…"
        aria-label="搜索资源组"
      >
    </div>

    <div v-if="loading" class="resource-groups-page__state">加载中…</div>
    <div v-else-if="error" class="resource-groups-page__state resource-groups-page__state--error">{{ error }}</div>
    <EmptyState
      v-else-if="!groups.length"
      icon="🗂️"
      title="没有已声明的资源组"
      description="安装并启用带 resource.group 元数据的插件后，会在这里出现。"
    />
    <EmptyState
      v-else-if="!filtered.length"
      icon="🔎"
      title="没有匹配的资源组"
      description="换个关键词再试。"
    />

    <div v-else class="resource-group-grid">
      <article
        v-for="group in filtered"
        :key="group.id"
        class="resource-group-card"
        :class="{ 'resource-group-card--active': selected?.id === group.id }"
        @click="selected = group"
      >
        <div class="resource-group-card__top">
          <h2 class="resource-group-card__title">{{ group.label }}</h2>
          <BaseBadge :tone="group.status === 'registered' ? 'success' : 'neutral'" dot>
            {{ statusLabel(group.status) }}
          </BaseBadge>
        </div>
        <code class="resource-group-card__ref">{{ group.ref }}</code>
        <p v-if="group.description" class="resource-group-card__desc">{{ group.description }}</p>
        <div class="resource-group-card__meta">
          <span>插件 {{ group.plugin_id }}</span>
        </div>
        <div class="resource-group-card__actions" @click.stop>
          <a v-if="group.ui_href" class="btn btn-secondary btn-sm" :href="group.ui_href">打开管理页</a>
          <button type="button" class="btn btn-secondary btn-sm" @click="copyRef(group)">{{ copiedId === group.id ? '已复制' : '复制引用' }}</button>
        </div>
      </article>
    </div>

    <aside v-if="selected" class="resource-group-detail">
      <div class="resource-group-detail__header">
        <h2>{{ selected.label }}</h2>
        <button type="button" class="resource-group-detail__close" aria-label="关闭详情" @click="selected = null">✕</button>
      </div>
      <dl class="resource-group-detail__list">
        <div>
          <dt>引用</dt>
          <dd><code>{{ selected.ref }}</code></dd>
        </div>
        <div>
          <dt>插件</dt>
          <dd>{{ selected.plugin_id }}</dd>
        </div>
        <div>
          <dt>状态</dt>
          <dd>{{ statusLabel(selected.status) }}</dd>
        </div>
        <div v-if="selected.description">
          <dt>说明</dt>
          <dd>{{ selected.description }}</dd>
        </div>
        <div v-if="selected.ui_href">
          <dt>关联入口</dt>
          <dd><a :href="selected.ui_href">{{ selected.ui_route_id || selected.ui_href }}</a></dd>
        </div>
      </dl>
      <section class="resource-group-detail__auth">
        <h3>授权</h3>
        <p>用户绑定尚未接入。当前持有面板令牌的会话以管理员身份操作该组。</p>
      </section>
    </aside>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'
import BaseBadge from '../components/base/BaseBadge.vue'
import EmptyState from '../components/base/EmptyState.vue'
import { usePluginResourceGroups } from '../hooks/usePluginResourceGroups'

const { groups, loading, error } = usePluginResourceGroups()
const query = ref('')
const selected = ref(null)
const copiedId = ref('')

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
.resource-groups-page {
  max-width: 960px;
  margin: 0 auto;
}
.resource-groups-page__header {
  margin-bottom: var(--space-6);
  padding-bottom: var(--space-4);
  border-bottom: 1px solid var(--color-border-subtle);
}
.resource-groups-page__title {
  font-size: var(--text-2xl);
  font-weight: var(--font-bold);
  margin: 0 0 var(--space-1);
  color: var(--color-text-primary);
}
.resource-groups-page__desc {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}
.resource-groups-page__toolbar {
  margin-bottom: var(--space-4);
}
.resource-groups-page__search {
  width: 100%;
  max-width: 24rem;
}
.resource-groups-page__state {
  color: var(--color-text-secondary);
  padding: var(--space-8) 0;
}
.resource-groups-page__state--error {
  color: var(--color-danger);
}
.resource-group-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: var(--space-4);
}
.resource-group-card {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: var(--space-4);
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}
.resource-group-card:hover,
.resource-group-card--active {
  border-color: var(--color-primary);
}
.resource-group-card__top {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}
.resource-group-card__title {
  margin: 0;
  font-size: var(--text-lg);
  font-weight: var(--font-semibold);
}
.resource-group-card__ref {
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  overflow-wrap: anywhere;
}
.resource-group-card__desc {
  margin: 0;
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
}
.resource-group-card__meta {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
}
.resource-group-card__actions {
  display: flex;
  gap: var(--space-2);
  margin-top: var(--space-2);
}
.resource-group-detail {
  margin-top: var(--space-6);
  padding: var(--space-5);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
}
.resource-group-detail__header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: var(--space-4);
}
.resource-group-detail__header h2 {
  margin: 0;
  font-size: var(--text-xl);
}
.resource-group-detail__close {
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

@media (max-width: 767px) {
  .resource-groups-page__search { max-width: 100%; }
  .resource-group-card__actions { flex-wrap: wrap; }
}
</style>
