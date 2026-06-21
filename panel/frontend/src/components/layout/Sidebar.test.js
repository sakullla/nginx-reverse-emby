import { mount } from '@vue/test-utils'
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({
  route: { name: 'dashboard', path: '/' },
}))

vi.mock('vue-router', () => ({
  useRoute: () => mocks.route,
  RouterLink: {
    name: 'RouterLink',
    props: ['to'],
    template: '<a class="router-link-stub" :href="to"><slot /></a>',
  },
}))

import Sidebar from './Sidebar.vue'

const STORAGE_KEY_COLLAPSED = 'sidebar_collapsed'
const STORAGE_KEY_OPEN_GROUPS = 'sidebar_open_groups'

function mountSidebar() {
  return mount(Sidebar, {
    global: {
      stubs: {
        RouterLink: {
          name: 'RouterLink',
          props: ['to'],
          template: '<a class="router-link-stub" :href="to" :title="$attrs.title"><slot /></a>',
        },
      },
    },
  })
}

function collapsedIconTitles(wrapper) {
  const collapsedNav = wrapper.find('.sidebar__nav--collapsed')
  // Icons render as RouterLink (single items) or .sidebar__nav-icon-wrap (groups).
  // Both expose a title attribute on the inner anchor or wrap; titles come from item.label or group.label.
  const anchors = collapsedNav.findAll('a[title]')
  const wraps = collapsedNav.findAll('.sidebar__nav-icon-wrap')
  const fromAnchors = anchors.map((a) => a.attributes('title'))
  // Group wraps have no title attribute on a stable element; rely on DOM ordering instead.
  return { anchors: fromAnchors, wrapCount: wraps.length }
}

describe('Sidebar collapsed icon order', () => {
  beforeEach(() => {
    localStorage.setItem(STORAGE_KEY_COLLAPSED, 'true')
    localStorage.setItem(STORAGE_KEY_OPEN_GROUPS, '[]')
    mocks.route.name = 'dashboard'
    mocks.route.path = '/'
  })

  it('keeps the same top-level order as navItems (home, traffic, infra, settings)', () => {
    const wrapper = mountSidebar()
    const collapsedNav = wrapper.find('.sidebar__nav--collapsed')
    expect(collapsedNav.exists()).toBe(true)

    // Render order in collapsed DOM: walk direct children of .sidebar__nav--collapsed.
    const childTags = collapsedNav.element.children
    const directChildren = Array.from(childTags)
    const titlesInOrder = directChildren.map((el) => {
      const anchor = el.tagName === 'A' ? el : el.querySelector('a[title]')
      if (anchor && anchor.getAttribute('title')) return anchor.getAttribute('title')
      // Group icon: no title; infer from position by reading the SVG path data hash.
      // We assert order via positions instead — see index assertion below.
      return null
    })

    // Single items have titles; groups render as .sidebar__nav-icon-wrap without title.
    // Expected sequence of single-item titles in DOM order: 首页, ..., ..., 设置
    // The exact group titles are not on the wrap, but their position (2nd and 3rd among 4) is verifiable.
    expect(titlesInOrder.length).toBe(4)
    expect(titlesInOrder[0]).toBe('首页')
    expect(titlesInOrder[3]).toBe('设置')
    // Position 1 and 2 are groups (no title); confirm count of wraps matches.
    expect(directChildren[1].classList.contains('sidebar__nav-icon-wrap')).toBe(true)
    expect(directChildren[2].classList.contains('sidebar__nav-icon-wrap')).toBe(true)
  })

  it('marks the icon for the active child within its group (relay-listeners)', () => {
    mocks.route.name = 'relay-listeners'
    mocks.route.path = '/relay-listeners'
    const wrapper = mountSidebar()
    // The 基础设施 group wrap should carry the active styling since relay-listeners is its child.
    const collapsedNav = wrapper.find('.sidebar__nav--collapsed')
    const wraps = collapsedNav.findAll('.sidebar__nav-icon-wrap')
    // 流量管理 is first group, 基础设施 is second group.
    expect(wraps[1].find('.sidebar__nav-icon--active').exists()).toBe(true)
  })
})

describe('Sidebar collapsed group popup hover bridge', () => {
  // jsdom does not load scoped CSS into document.styleSheets, so we assert against
  // the source CSS embedded in Sidebar.vue. This guards the fix at the file level
  // and is sufficient because hover behavior is browser-rendered and untestable here.
  let source
  beforeAll(async () => {
    const fs = await import('node:fs/promises')
    const path = await import('node:path')
    const url = await import('node:url')
    const here = path.dirname(url.fileURLToPath(import.meta.url))
    source = await fs.readFile(path.join(here, 'Sidebar.vue'), 'utf8')
  })

  it('keeps the popup visible when the popup itself is hovered', () => {
    const pattern = /\.sidebar__hover-popup:hover\s*\{[^}]*opacity:\s*1[^}]*visibility:\s*visible[^}]*pointer-events:\s*auto/
    expect(source).toMatch(pattern)
  })

  it('bridges the wrap hover area to cover the gap between icon and popup', () => {
    const pattern = /\.sidebar__nav-icon-wrap::after\s*\{[^}]*position:\s*absolute[^}]*left:\s*100%[^}]*width:\s*(\d+)px/
    const match = source.match(pattern)
    expect(match).toBeTruthy()
    const widthPx = Number(match[1])
    expect(widthPx).toBeGreaterThanOrEqual(8)
  })
})
