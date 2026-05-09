<template>
  <aside class="sidebar" :class="{ 'sidebar--collapsed': collapsed }">
    <div class="sidebar__header">
      <span class="sidebar__brand" v-show="!collapsed">Nginx Proxy</span>
      <button class="sidebar__collapse-btn" @click="toggleCollapse" :title="collapsed ? '展开' : '折叠'">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" :class="{ 'rotate-180': collapsed }">
          <polyline points="15 18 9 12 15 6"/>
        </svg>
      </button>
    </div>

    <!-- Expanded navigation -->
    <nav class="sidebar__nav" v-show="!collapsed">
      <template v-for="item in navItems" :key="item.label">
        <!-- Single item -->
        <RouterLink v-if="item.type === 'item'" :to="item.to" class="sidebar__nav-item" :class="getItemActiveClass(item)">
          <component :is="item.icon" />
          <span>{{ item.label }}</span>
        </RouterLink>

        <!-- Group item -->
        <div v-else class="nav-group">
          <button class="nav-group__header" @click="toggleGroup(item.label)">
            <component :is="item.icon" />
            <span>{{ item.label }}</span>
            <svg class="nav-group__chevron" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" :class="{ 'nav-group__chevron--open': isGroupOpen(item.label) }">
              <polyline points="6 9 12 15 18 9"/>
            </svg>
          </button>
          <Transition name="nav-group">
            <div v-show="isGroupOpen(item.label)" class="nav-group__children">
              <RouterLink
                v-for="child in item.children"
                :key="child.to"
                :to="child.to"
                class="sidebar__nav-item sidebar__nav-item--child"
                :class="{ 'sidebar__nav-item--child-active': isChildActive(child) }"
              >
                <component :is="child.icon" />
                <span>{{ child.label }}</span>
              </RouterLink>
            </div>
          </Transition>
        </div>
      </template>
    </nav>

    <!-- Collapsed navigation with hover popup -->
    <nav class="sidebar__nav sidebar__nav--collapsed" v-show="collapsed">
      <template v-for="item in singleItems" :key="item.to">
        <RouterLink :to="item.to" class="sidebar__nav-icon" :title="item.label" :class="{ 'sidebar__nav-icon--active': isItemActive(item) }">
          <component :is="item.icon" />
        </RouterLink>
      </template>

      <div v-for="group in groupItems" :key="group.label" class="sidebar__nav-icon-wrap">
        <div class="sidebar__nav-icon" :class="{ 'sidebar__nav-icon--active': isGroupActive(group) }">
          <component :is="group.icon" />
        </div>
        <div class="sidebar__hover-popup">
          <RouterLink
            v-for="child in group.children"
            :key="child.to"
            :to="child.to"
            class="sidebar__hover-popup__item"
            :class="{ 'sidebar__hover-popup__item--active': isChildActive(child) }"
          >
            <component :is="child.icon" />
            <span>{{ child.label }}</span>
          </RouterLink>
        </div>
      </div>
    </nav>
  </aside>
</template>

<script setup>
import { ref, computed, h } from 'vue'
import { RouterLink, useRoute } from 'vue-router'

// --- Icon components ---
const makeIcon = (paths) => () => h('svg', { width: '16', height: '16', viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': '2' }, paths.map(d => h('path', { d })))
const makeIconMixed = (els) => () => h('svg', { width: '16', height: '16', viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': '2' }, els.map(e => {
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
  relay: makeIcon(['M8 12h8', 'M6 8h12', 'M10 16h4']),
  monitor: () => h('svg', { width: '16', height: '16', viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': '2' }, [h('rect', { x: '2', y: '3', width: '20', height: '14', rx: '2' }), h('line', { x1: '8', y1: '21', x2: '16', y2: '21' }), h('line', { x1: '12', y1: '17', x2: '12', y2: '21' })]),
  settings: () => h('svg', { width: '16', height: '16', viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', 'stroke-width': '2' }, [h('circle', { cx: '12', cy: '12', r: '3' }), h('path', { d: 'M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z' })]),
  infra: makeIconMixed([{ tag: 'rect', attrs: { x: '2', y: '2', width: '20', height: '8', rx: '2', ry: '2' } }, { tag: 'rect', attrs: { x: '2', y: '14', width: '20', height: '8', rx: '2', ry: '2' } }, { tag: 'line', attrs: { x1: '6', y1: '6', x2: '6.01', y2: '6' } }, { tag: 'line', attrs: { x1: '6', y1: '18', x2: '6.01', y2: '18' } }]),
}

// --- Nav config ---
const navItems = [
  { type: 'item', label: '首页', to: '/', icon: icons.home, activeMatch: (name) => name === 'dashboard' },
  {
    type: 'group', label: '流量管理', icon: icons.traffic,
    children: [
      { label: 'HTTP 规则', to: '/rules', icon: icons.traffic },
      { label: 'L4 规则', to: '/l4', icon: icons.infra },
    ],
  },
  {
    type: 'group', label: '基础设施', icon: icons.infra,
    children: [
      { label: '证书管理', to: '/certs', icon: icons.lock },
      { label: 'Relay 监听器', to: '/relay-listeners', icon: icons.relay },
      { label: '节点管理', to: '/agents', icon: icons.monitor, activeMatch: (name) => name === 'agents' || name === 'agent-detail' },
    ],
  },
  { type: 'item', label: '设置', to: '/settings', icon: icons.settings },
]

const route = useRoute()
const collapsed = ref(localStorage.getItem('sidebar_collapsed') === 'true')
const openGroups = ref(new Set(JSON.parse(localStorage.getItem('sidebar_open_groups') || '[]')))

const singleItems = computed(() => navItems.filter(i => i.type === 'item'))
const groupItems = computed(() => navItems.filter(i => i.type === 'group'))

function isItemActive(item) { return item.activeMatch ? item.activeMatch(route.name) : route.path === item.to }
function isChildActive(child) { return child.activeMatch ? child.activeMatch(route.name) : route.path === child.to }
function isGroupOpen(label) { return openGroups.value.has(label) }
function isGroupActive(group) { return group.children.some(c => isChildActive(c)) }

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
</script>

<style scoped>
.sidebar {
  width: var(--sidebar-width);
  display: flex;
  flex-direction: column;
  background: var(--color-bg-surface);
  border-right: 1px solid var(--color-border-subtle);
  flex-shrink: 0;
  overflow: hidden;
  transition: width var(--duration-normal) var(--ease-default);
  box-shadow: var(--shadow-xs);
}

.sidebar--collapsed {
  width: 64px;
}

.sidebar__header {
  display: flex;
  align-items: center;
  gap: 0.625rem;
  padding: 1rem 1rem 0.75rem;
  border-bottom: 1px solid var(--color-border-subtle);
  min-height: 56px;
  box-sizing: border-box;
}

.sidebar__brand {
  font-size: 0.9375rem;
  font-weight: 700;
  color: var(--color-text-primary);
  flex: 1;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  letter-spacing: -0.01em;
}

.sidebar__collapse-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  border: none;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  flex-shrink: 0;
}

.sidebar--collapsed .sidebar__collapse-btn {
  margin: 0 auto;
}

.sidebar__collapse-btn:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.sidebar__collapse-btn svg {
  transition: transform 0.2s var(--ease-default);
}

.sidebar__collapse-btn svg.rotate-180 {
  transform: rotate(180deg);
}

/* Expanded nav */
.sidebar__nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding: 0.5rem 0.625rem;
}

/* Single nav item */
.sidebar__nav-item {
  display: flex;
  align-items: center;
  gap: 0.625rem;
  padding: 0.5rem 0.75rem;
  border-radius: var(--radius-lg);
  color: var(--color-text-secondary);
  text-decoration: none;
  font-size: 0.875rem;
  font-weight: 500;
  transition: all var(--duration-fast) var(--ease-default);
  position: relative;
  cursor: pointer;
}

.sidebar__nav-item:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.sidebar__nav-item--active,
.sidebar__nav-item.active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-weight: 600;
}

.sidebar__nav-item--active::before {
  content: '';
  position: absolute;
  left: 0;
  top: 50%;
  transform: translateY(-50%);
  width: 3px;
  height: 60%;
  background: var(--color-primary);
  border-radius: 0 2px 2px 0;
}

/* Child nav item (indented) */
.sidebar__nav-item--child {
  padding-left: 2rem;
  font-size: 0.8125rem;
}

.sidebar__nav-item--child::before {
  display: none;
}

.sidebar__nav-item--child-active {
  background: var(--color-primary-subtle) !important;
  color: var(--color-primary) !important;
  font-weight: 600;
}

.sidebar__nav-item--child-active::after {
  content: '';
  position: absolute;
  left: 1rem;
  top: 50%;
  transform: translateY(-50%);
  width: 3px;
  height: 50%;
  background: var(--color-primary);
  border-radius: 0 2px 2px 0;
}

/* Nav group */
.nav-group {
  display: flex;
  flex-direction: column;
}

.nav-group__header {
  display: flex;
  align-items: center;
  gap: 0.625rem;
  padding: 0.5rem 0.75rem;
  border-radius: var(--radius-lg);
  border: none;
  background: transparent;
  color: var(--color-text-secondary);
  font-size: 0.875rem;
  font-weight: 500;
  cursor: pointer;
  width: 100%;
  text-align: left;
  transition: all var(--duration-fast) var(--ease-default);
}

.nav-group__header:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.nav-group__chevron {
  margin-left: auto;
  transition: transform 0.2s var(--ease-default);
  color: var(--color-text-tertiary);
  flex-shrink: 0;
}

.nav-group__chevron--open {
  transform: rotate(180deg);
}

/* Nav group children */
.nav-group__children {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.nav-group-enter-active,
.nav-group-leave-active {
  transition: all 0.2s ease;
  overflow: hidden;
}

.nav-group-enter-from,
.nav-group-leave-to {
  opacity: 0;
  max-height: 0;
}

.nav-group-enter-to,
.nav-group-leave-from {
  opacity: 1;
  max-height: 200px;
}

/* Collapsed nav icons */
.sidebar__nav--collapsed {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  padding: 0.5rem;
}

.sidebar__nav-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: var(--radius-lg);
  color: var(--color-text-secondary);
  text-decoration: none;
  transition: all var(--duration-fast) var(--ease-default);
}

.sidebar__nav-icon:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.sidebar__nav-icon--active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

/* Hover popup */
.sidebar__nav-icon-wrap {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
}

.sidebar__hover-popup {
  position: absolute;
  left: calc(100% + 8px);
  top: 50%;
  transform: translateY(-50%) scale(0.95);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-xl);
  padding: 0.375rem;
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 160px;
  opacity: 0;
  visibility: hidden;
  pointer-events: none;
  transition: all 0.15s var(--ease-default);
  z-index: var(--z-dropdown, 1000);
}

.sidebar__nav-icon-wrap:hover .sidebar__hover-popup {
  opacity: 1;
  visibility: visible;
  pointer-events: auto;
  transform: translateY(-50%) scale(1);
}

.sidebar__hover-popup__item {
  display: flex;
  align-items: center;
  gap: 0.625rem;
  padding: 0.5rem 0.75rem;
  border-radius: var(--radius-md);
  color: var(--color-text-secondary);
  text-decoration: none;
  font-size: 0.8125rem;
  font-weight: 500;
  transition: all var(--duration-fast) var(--ease-default);
  white-space: nowrap;
}

.sidebar__hover-popup__item:hover {
  background: var(--color-bg-hover);
  color: var(--color-text-primary);
}

.sidebar__hover-popup__item--active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  font-weight: 600;
}
</style>
