<template>
  <aside class="sidebar" :class="{ 'sidebar--collapsed': collapsed }">
    <div class="sidebar__header">
      <RouterLink to="/" class="sidebar__brand-link" aria-label="Nginx Proxy 首页" title="Nginx Proxy">
        <span class="sidebar__logo"><BrandMark /></span>
        <span v-show="!collapsed" class="sidebar__brand-text">
          <span class="sidebar__brand">Nginx Proxy</span>
          <span class="sidebar__brand-sub">反向代理控制台</span>
        </span>
      </RouterLink>
    </div>

    <!-- Expanded navigation -->
    <nav class="sidebar__nav" v-show="!collapsed">
      <template v-for="item in navItems" :key="item.label">
        <!-- Single item -->
        <RouterLink v-if="item.type === 'item'" :to="item.to" class="sidebar__nav-item" :class="getItemActiveClass(item)" :aria-current="isItemActive(item) ? 'page' : undefined">
          <component :is="item.icon" />
          <span>{{ item.label }}</span>
        </RouterLink>

        <!-- Group item -->
        <div v-else class="nav-group">
          <button class="nav-group__header" :class="{ 'nav-group__header--active': isGroupActive(item) }" :aria-expanded="isGroupOpen(item.label) ? 'true' : 'false'" @click="toggleGroup(item.label)">
            <component :is="item.icon" />
            <span>{{ item.label }}</span>
            <svg class="nav-group__chevron" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" :class="{ 'nav-group__chevron--open': isGroupOpen(item.label) }">
              <polyline points="6 9 12 15 18 9"/>
            </svg>
          </button>
          <Transition name="nav-group">
            <div v-show="isGroupOpen(item.label)" class="nav-group__children">
              <template v-for="child in item.children" :key="child.href || child.to">
                <a
                  v-if="child.href"
                  :href="child.href"
                  class="sidebar__nav-item sidebar__nav-item--child"
                  :title="child.title || child.label"
                  :aria-label="navItemAriaLabel(child)"
                >
                  <component :is="child.icon" />
                  <span>{{ child.label }}</span>
                </a>
                <RouterLink
                  v-else
                  :to="child.to"
                  class="sidebar__nav-item sidebar__nav-item--child"
                  :class="{
                    'sidebar__nav-item--child-active': isChildActive(child),
                    'sidebar__nav-item--advanced': child.advanced,
                  }"
                  :title="child.title || child.label"
                  :aria-label="navItemAriaLabel(child)"
                  :aria-current="isChildActive(child) ? 'page' : undefined"
                >
                  <component :is="child.icon" />
                  <span>{{ child.label }}</span>
                  <span v-if="child.advanced" class="sidebar__nav-badge">高级</span>
                </RouterLink>
              </template>
            </div>
          </Transition>
        </div>
      </template>
    </nav>

    <!-- Collapsed navigation with hover popup -->
    <nav class="sidebar__nav sidebar__nav--collapsed" v-show="collapsed">
      <template v-for="item in navItems" :key="item.to || item.label">
        <RouterLink
          v-if="item.type === 'item'"
          :to="item.to"
          class="sidebar__nav-icon"
          :title="item.label"
          :class="{ 'sidebar__nav-icon--active': isItemActive(item) }"
          :aria-current="isItemActive(item) ? 'page' : undefined"
        >
          <component :is="item.icon" />
        </RouterLink>

        <div v-else class="sidebar__nav-icon-wrap" :class="{ 'sidebar__nav-icon-wrap--pinned': pinnedGroup === item.label }" @keydown.escape="pinnedGroup = null">
          <button
            type="button"
            class="sidebar__nav-icon"
            :class="{ 'sidebar__nav-icon--active': isGroupActive(item) }"
            :title="item.label"
            :aria-label="item.label"
            :aria-expanded="pinnedGroup === item.label"
            @click="togglePinnedGroup(item.label)"
          >
            <component :is="item.icon" />
          </button>
          <div class="sidebar__hover-popup">
            <template v-for="child in item.children" :key="child.href || child.to">
              <a
                v-if="child.href"
                :href="child.href"
                class="sidebar__hover-popup__item"
                :title="child.title || child.label"
                :aria-label="navItemAriaLabel(child)"
              >
                <component :is="child.icon" />
                <span>{{ child.label }}</span>
              </a>
              <RouterLink
                v-else
                :to="child.to"
                class="sidebar__hover-popup__item"
                :class="{
                  'sidebar__hover-popup__item--active': isChildActive(child),
                  'sidebar__hover-popup__item--advanced': child.advanced,
                }"
                :title="child.title || child.label"
                :aria-label="navItemAriaLabel(child)"
                :aria-current="isChildActive(child) ? 'page' : undefined"
              >
                <component :is="child.icon" />
                <span>{{ child.label }}</span>
                <span v-if="child.advanced" class="sidebar__nav-badge">高级</span>
              </RouterLink>
            </template>
          </div>
        </div>
      </template>
    </nav>

    <div class="sidebar__footer">
      <button class="sidebar__collapse-btn" type="button" @click="toggleCollapse" :aria-label="collapsed ? '展开侧栏' : '折叠侧栏'" :title="collapsed ? '展开侧栏' : '折叠侧栏'">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
          <rect x="3" y="4" width="18" height="16" rx="2.5"/>
          <path d="M9 4v16"/>
          <path v-if="collapsed" d="m13 10 2 2-2 2"/>
          <path v-else d="m15 10-2 2 2 2"/>
        </svg>
        <span v-show="!collapsed">收起侧栏</span>
      </button>
    </div>
  </aside>
</template>

<script setup>
import { ref, computed, h, onMounted, watch } from 'vue'
import { RouterLink, useRoute } from 'vue-router'
import { useAccessControl } from '../../context/useAccessControl'
import BrandMark from '../base/BrandMark.vue'
import { pluginChildrenForGroup, usePluginUIRoutes } from '../../hooks/usePluginUIRoutes'

// --- Icon components ---
const ICON_ATTRS = { width: '16', height: '16', viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': '1.8', 'stroke-linecap': 'round', 'stroke-linejoin': 'round', 'aria-hidden': 'true' }
const makeIcon = (paths) => () => h('svg', ICON_ATTRS, paths.map(d => h('path', { d })))
const makeIconMixed = (els) => () => h('svg', ICON_ATTRS, els.map(e => {
  const attrs = { ...e.attrs }
  if (e.tag === 'path') return h('path', { d: attrs.d, ...attrs })
  if (e.tag === 'line') return h('line', { x1: attrs.x1, y1: attrs.y1, x2: attrs.x2, y2: attrs.y2 })
  if (e.tag === 'circle') return h('circle', { cx: attrs.cx, cy: attrs.cy, r: attrs.r })
  if (e.tag === 'rect') return h('rect', { x: attrs.x, y: attrs.y, width: attrs.width, height: attrs.height, rx: attrs.rx, ry: attrs.ry })
  if (e.tag === 'polyline') return h('polyline', { points: attrs.points })
  return null
}))

const icons = {
  home: makeIconMixed([{ tag: 'path', attrs: { d: 'M3 9l9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z' } }, { tag: 'polyline', attrs: { points: '9 22 9 12 15 12 15 22' } }]),
  traffic: makeIcon(['M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71', 'M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71']),
  lock: makeIcon(['M3 11h18a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-9a2 2 0 0 1 2-2z', 'M7 11V7a5 5 0 0 1 10 0v4']),
  key: makeIcon(['M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4']),
  relay: makeIcon(['M8 12h8', 'M6 8h12', 'M10 16h4']),
  service: makeIcon(['M4 5h16v5H4z', 'M4 14h16v5H4z']),
  monitor: () => h('svg', ICON_ATTRS, [h('rect', { x: '2', y: '3', width: '20', height: '14', rx: '2' }), h('line', { x1: '8', y1: '21', x2: '16', y2: '21' }), h('line', { x1: '12', y1: '17', x2: '12', y2: '21' })]),
  settings: () => h('svg', ICON_ATTRS, [h('circle', { cx: '12', cy: '12', r: '3' }), h('path', { d: 'M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z' })]),
  infra: makeIconMixed([{ tag: 'rect', attrs: { x: '2', y: '2', width: '20', height: '8', rx: '2', ry: '2' } }, { tag: 'rect', attrs: { x: '2', y: '14', width: '20', height: '8', rx: '2', ry: '2' } }, { tag: 'line', attrs: { x1: '6', y1: '6', x2: '6.01', y2: '6' } }, { tag: 'line', attrs: { x1: '6', y1: '18', x2: '6.01', y2: '18' } }]),
  globe: makeIconMixed([{ tag: 'circle', attrs: { cx: '12', cy: '12', r: '9' } }, { tag: 'path', attrs: { d: 'M3 12h18' } }, { tag: 'path', attrs: { d: 'M12 3a14 14 0 0 1 0 18M12 3a14 14 0 0 0 0 18' } }]),
  layers: makeIcon(['M12 3 3 8l9 5 9-5-9-5z', 'm3 13 9 5 9-5', 'm3 17.5 9 5 9-5']),
  shield: makeIcon(['M12 3 4.5 6v5.5c0 4.6 3.2 8.4 7.5 9.5 4.3-1.1 7.5-4.9 7.5-9.5V6L12 3z', 'm9 12 2 2 4-4']),
  store: makeIcon(['M4 9V19a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1V9', 'M3 5h18l-1.5 4a2.5 2.5 0 0 1-4.5 0 2.5 2.5 0 0 1-5 0 2.5 2.5 0 0 1-4.5 0L3 5z', 'M10 20v-5h4v5']),
  package: makeIcon(['M21 8 12 3 3 8v8l9 5 9-5V8z', 'm3 8 9 5 9-5', 'M12 13v8']),
  plugin: makeIcon(['M8.5 3a2.5 2.5 0 1 0 5 0H18a2 2 0 0 1 2 2v4.5a2.5 2.5 0 1 1 0 5V19a2 2 0 0 1-2 2h-4.5a2.5 2.5 0 1 0-5 0H4a2 2 0 0 1-2-2v-4.5a2.5 2.5 0 1 0 0-5V5a2 2 0 0 1 2-2z']),
}

const { refreshActor } = useAccessControl()
const route = useRoute()

function isPathActive(to) {
  return Boolean(to) && (route.path === to || route.path.startsWith(`${to}/`))
}

// --- Nav config ---
const { routes: pluginUIRoutes } = usePluginUIRoutes()

const navItems = computed(() => {
  const items = [
    { type: 'item', label: '首页', to: '/', icon: icons.home, activeMatch: (name) => name === 'dashboard' },
    { type: 'item', label: '服务', to: '/services', icon: icons.service, activeMatch: (name) => name === 'services' },
    {
      type: 'group', label: '流量管理', icon: icons.traffic,
      children: [
        { label: 'HTTP 规则', to: '/rules', icon: icons.globe },
        { label: 'L4 规则', to: '/l4', icon: icons.layers },
      ],
    },
    {
      type: 'group', label: '基础设施', icon: icons.infra,
      children: [
        { label: '证书中心', to: '/certs', icon: icons.shield, activeMatch: (name) => name === 'certs' || name === 'pki' },
        ...pluginChildrenForGroup(pluginUIRoutes.value, '基础设施').map((child) => ({ ...child, icon: icons.key })),
        { label: 'Relay 监听器', to: '/relay-listeners', icon: icons.relay },
        { label: '节点管理', to: '/agents', icon: icons.monitor, activeMatch: (name) => name === 'agents' || name === 'agent-detail' },
      ],
    },
    {
      type: 'group', label: '插件', icon: icons.plugin,
      children: [
        {
          label: '插件市场',
          title: '浏览并安装插件，安装后继续部署或发布',
          to: '/plugins/marketplace',
          icon: icons.store,
          activeMatch: (name) => name === 'plugin-marketplace',
        },
        {
          label: '已安装插件',
          title: '查看尚未部署、待发布、已可用或异常',
          to: '/plugins',
          icon: icons.package,
          activeMatch: (name) => name === 'plugins' || name === 'plugin-detail',
        },
      ],
    },
  ]
  items.push({ type: 'item', label: '设置', to: '/settings', icon: icons.settings })
  return items
})

const collapsed = ref(localStorage.getItem('sidebar_collapsed') === 'true')
const pinnedGroup = ref(null)

function togglePinnedGroup(label) {
  pinnedGroup.value = pinnedGroup.value === label ? null : label
}

const storedGroups = localStorage.getItem('sidebar_open_groups')
const openGroups = ref(new Set(storedGroups == null
  ? ['流量管理', '基础设施', '插件']
  : JSON.parse(storedGroups)))

function isItemActive(item) { return item.activeMatch ? item.activeMatch(route.name) : isPathActive(item.to) }
function isChildActive(child) { return child.activeMatch ? child.activeMatch(route.name) : isPathActive(child.to) }
function navItemAriaLabel(item) {
  return item.title && item.title !== item.label ? `${item.label}，${item.title}` : item.label
}
function isGroupOpen(label) { return openGroups.value.has(label) }
function isGroupActive(group) {
  if (group.children.some((child) => isChildActive(child))) return true
  return Boolean(group.activePathPrefix && route.path.startsWith(group.activePathPrefix))
}

function toggleGroup(label) {
  if (openGroups.value.has(label)) { openGroups.value.delete(label) } else { openGroups.value.add(label) }
  saveGroups()
}

function getItemActiveClass(item) {
  return isItemActive(item) ? (item.activeMatch ? 'active' : 'sidebar__nav-item--active') : ''
}

function saveGroups() { localStorage.setItem('sidebar_open_groups', JSON.stringify([...openGroups.value])) }

function toggleCollapse() {
  collapsed.value = !collapsed.value
  localStorage.setItem('sidebar_collapsed', String(collapsed.value))
}

function openActiveGroups() {
  for (const item of navItems.value) {
    if (item.type === 'group' && isGroupActive(item) && !isGroupOpen(item.label)) {
      openGroups.value.add(item.label)
    }
  }
}

onMounted(() => {
  refreshActor().catch(() => undefined)
  openActiveGroups()
})
watch(() => route.path, openActiveGroups)
watch(() => route.path, () => { pinnedGroup.value = null })
watch(() => navItems.value.map((item) => item.label).join(), openActiveGroups)
</script>

<style scoped>
.sidebar {
  position: relative;
  z-index: var(--z-sticky);
  width: var(--sidebar-width);
  display: flex;
  flex-direction: column;
  background: var(--color-bg-sidebar);
  border-right: 1px solid var(--color-border-subtle);
  flex-shrink: 0;
  overflow: hidden;
  transition: width var(--duration-normal) var(--ease-default);
}

.sidebar--collapsed {
  width: var(--sidebar-width-collapsed);
  overflow: visible;
}

/* ---------- Brand ---------- */
.sidebar__header {
  display: flex;
  align-items: center;
  height: var(--header-height);
  padding: 0 var(--space-4);
  flex-shrink: 0;
  box-sizing: border-box;
}

.sidebar--collapsed .sidebar__header {
  justify-content: center;
  padding: 0;
}

.sidebar__brand-link {
  display: flex;
  align-items: center;
  gap: var(--space-2-5);
  min-width: 0;
  color: var(--color-text-primary);
  text-decoration: none;
  border-radius: var(--radius-md);
}

.sidebar__logo {
  display: flex;
  width: 30px;
  height: 30px;
  border-radius: var(--radius-md);
  background: var(--color-brand-bg);
  color: var(--color-brand-ink);
  box-shadow: inset 0 1px 0 rgb(255 255 255 / 0.25), var(--shadow-sm);
  flex-shrink: 0;
}

.sidebar__brand-text {
  display: grid;
  min-width: 0;
  line-height: 1.2;
}

.sidebar__brand {
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  letter-spacing: -0.02em;
  white-space: nowrap;
}

.sidebar__brand-sub {
  font-size: var(--text-2xs);
  color: var(--color-text-tertiary);
  white-space: nowrap;
}

/* ---------- Expanded nav ---------- */
.sidebar__nav {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: var(--space-2) var(--space-3) var(--space-4);
  display: flex;
  flex-direction: column;
  gap: 1px;
}

.sidebar__nav-item {
  position: relative;
  display: flex;
  align-items: center;
  gap: var(--space-2-5);
  min-height: 2rem;
  padding: 0 var(--space-2-5);
  border-radius: var(--radius-md);
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  text-decoration: none;
  transition: background var(--duration-fast) var(--ease-default), color var(--duration-fast) var(--ease-default);
}

.sidebar__nav-item svg {
  flex-shrink: 0;
  color: var(--color-text-tertiary);
  transition: color var(--duration-fast) var(--ease-default);
}

.sidebar__nav-item span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sidebar__nav-item:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.sidebar__nav-item:hover svg {
  color: var(--color-text-secondary);
}

.sidebar__nav-item.active,
.sidebar__nav-item--active,
.sidebar__nav-item--child-active {
  background: var(--color-nav-selected);
  color: var(--color-text-primary);
  font-weight: var(--font-semibold);
  box-shadow: var(--shadow-xs), 0 0 0 1px var(--color-border-subtle);
}

.sidebar__nav-item.active svg,
.sidebar__nav-item--active svg,
.sidebar__nav-item--child-active svg {
  color: var(--color-primary);
}

.sidebar__nav-badge {
  margin-left: auto;
  padding: 0 var(--space-1-5);
  border-radius: var(--radius-xs);
  background: var(--color-bg-active);
  color: var(--color-text-tertiary);
  font-size: var(--text-2xs);
  font-weight: var(--font-medium);
}

.sidebar__nav:not(.sidebar__nav--collapsed) > .sidebar__nav-item:last-child {
  margin-top: var(--space-4);
}

/* Section captions double as collapsible group toggles */
.nav-group {
  display: flex;
  flex-direction: column;
  margin-top: var(--space-3);
}

.nav-group__header {
  display: flex;
  align-items: center;
  gap: var(--space-1-5);
  width: 100%;
  min-height: 1.75rem;
  padding: 0 var(--space-2-5);
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-tertiary);
  font: inherit;
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  letter-spacing: 0.02em;
  cursor: pointer;
  text-align: left;
}

.nav-group__header > svg:first-child {
  display: none;
}

.nav-group__header span {
  flex: 1;
}

.nav-group__header:hover {
  color: var(--color-text-secondary);
}

.nav-group__header--active {
  color: var(--color-text-secondary);
}

.nav-group__chevron {
  opacity: 0;
  transform: rotate(-90deg);
  transition: transform var(--duration-normal) var(--ease-default), opacity var(--duration-fast) var(--ease-default);
}

.nav-group__header:hover .nav-group__chevron,
.nav-group__header:focus-visible .nav-group__chevron,
.nav-group__chevron:not(.nav-group__chevron--open) {
  opacity: 1;
}

.nav-group__chevron--open {
  transform: rotate(0deg);
}

.nav-group__children {
  display: flex;
  flex-direction: column;
  gap: 1px;
  margin-top: 1px;
}

.nav-group-enter-active,
.nav-group-leave-active {
  transition: opacity var(--duration-fast) var(--ease-default), transform var(--duration-fast) var(--ease-default);
}

.nav-group-enter-from,
.nav-group-leave-to {
  opacity: 0;
  transform: translateY(-3px);
}

/* ---------- Collapsed nav ---------- */
.sidebar__nav--collapsed {
  align-items: center;
  padding: var(--space-2) 0;
  gap: var(--space-1);
  overflow: visible;
}

.sidebar__nav-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border: 0;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default), color var(--duration-fast) var(--ease-default);
}

.sidebar__nav-icon:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.sidebar__nav-icon--active {
  background: var(--color-nav-selected);
  color: var(--color-primary);
  box-shadow: var(--shadow-xs), 0 0 0 1px var(--color-border-subtle);
}

.sidebar__nav-icon-wrap {
  position: relative;
}

.sidebar__hover-popup {
  position: absolute;
  left: calc(100% + var(--space-2));
  top: 0;
  z-index: var(--z-popover);
  min-width: 11rem;
  padding: var(--space-1);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface-raised);
  box-shadow: var(--shadow-lg);
  opacity: 0;
  visibility: hidden;
  transform: translateX(-4px);
  transition: opacity var(--duration-fast) var(--ease-default), transform var(--duration-fast) var(--ease-default), visibility var(--duration-fast);
}

/* Invisible bridge so the pointer can travel from icon to popup */
.sidebar__hover-popup::before {
  content: '';
  position: absolute;
  top: 0;
  bottom: 0;
  left: calc(-1 * var(--space-2) - 2px);
  width: calc(var(--space-2) + 2px);
}

.sidebar__nav-icon-wrap:hover .sidebar__hover-popup,
.sidebar__nav-icon-wrap:focus-within .sidebar__hover-popup,
.sidebar__nav-icon-wrap--pinned .sidebar__hover-popup,
.sidebar__hover-popup:hover {
  opacity: 1;
  visibility: visible;
  transform: translateX(0);
}

.sidebar__hover-popup__item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: 2rem;
  padding: 0 var(--space-2-5);
  border-radius: var(--radius-sm);
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  text-decoration: none;
  white-space: nowrap;
}

.sidebar__hover-popup__item svg {
  color: var(--color-text-tertiary);
}

.sidebar__hover-popup__item:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.sidebar__hover-popup__item--active {
  color: var(--color-text-primary);
  font-weight: var(--font-semibold);
}

.sidebar__hover-popup__item--active svg {
  color: var(--color-primary);
}

/* ---------- Footer ---------- */
.sidebar__footer {
  flex-shrink: 0;
  padding: var(--space-2) var(--space-3) var(--space-3);
  border-top: 1px solid var(--color-border-subtle);
}

.sidebar--collapsed .sidebar__footer {
  display: flex;
  justify-content: center;
  padding-inline: 0;
}

.sidebar__collapse-btn {
  display: flex;
  align-items: center;
  gap: var(--space-2-5);
  width: 100%;
  min-height: 2rem;
  padding: 0 var(--space-2-5);
  border: 0;
  border-radius: var(--radius-md);
  background: transparent;
  color: var(--color-text-tertiary);
  font: inherit;
  font-size: var(--text-sm);
  cursor: pointer;
  transition: background var(--duration-fast) var(--ease-default), color var(--duration-fast) var(--ease-default);
}

.sidebar--collapsed .sidebar__collapse-btn {
  width: 36px;
  justify-content: center;
  padding: 0;
}

.sidebar__collapse-btn:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}
</style>
