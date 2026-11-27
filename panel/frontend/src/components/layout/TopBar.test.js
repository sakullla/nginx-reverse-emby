import { afterEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import TopBar from './TopBar.vue'

const route = reactive({ name: 'dashboard', path: '/', meta: { title: '首页' } })

vi.mock('vue-router', async (importOriginal) => ({
  ...(await importOriginal()),
  useRoute: () => route,
  useRouter: () => ({ replace: vi.fn() }),
  RouterLink: { props: ['to'], template: '<a :href="typeof to === \'string\' ? to : to?.path"><slot /></a>' }
}))

vi.mock('../../api', () => ({ verifyToken: vi.fn() }))
vi.mock('../../api/access', () => ({ logout: vi.fn(async () => undefined) }))
vi.mock('../../context/useAccessControl', () => ({
  useAccessControl: () => ({ actor: ref({ username: 'admin' }) })
}))

const mounted = []

function mountTopBar(props = {}) {
  const wrapper = mount(TopBar, {
    props,
    global: { stubs: { ThemeSelector: true, BrandMark: true } }
  })
  mounted.push(wrapper)
  return wrapper
}

function setRoute(next) {
  route.name = next.name
  route.path = next.path
  route.meta = next.meta
}

afterEach(() => {
  while (mounted.length) mounted.pop().unmount()
  setRoute({ name: 'dashboard', path: '/', meta: { title: '首页' } })
})

describe('TopBar', () => {
  it('shows the page title instead of the brand on desktop', () => {
    const wrapper = mountTopBar()

    expect(wrapper.find('.topbar__brand').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Nginx Proxy')
    const current = wrapper.find('[aria-current="page"]')
    expect(current.text()).toBe('首页')
  })

  it('renders section and the primary title segment for nested titles', () => {
    setRoute({ name: 'certs', path: '/certs', meta: { title: '证书中心 · 公网证书' } })
    const wrapper = mountTopBar()

    const crumbs = wrapper.find('.topbar__crumbs')
    expect(crumbs.text()).toContain('基础设施')
    expect(wrapper.find('[aria-current="page"]').text()).toBe('证书中心')
  })

  it('does not fall back to the brand name when a route has no title', () => {
    setRoute({ name: 'unknown', path: '/x', meta: {} })
    const wrapper = mountTopBar()

    expect(wrapper.find('.topbar__crumbs').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('Nginx Proxy')
  })

  it('shows the brand only in compact (mobile) mode', () => {
    const wrapper = mountTopBar({ compact: true })

    expect(wrapper.find('.topbar__brand').exists()).toBe(true)
    expect(wrapper.find('.topbar__name').text()).toBe('Nginx Proxy')
    expect(wrapper.find('.topbar__crumbs').exists()).toBe(false)
  })

  it('every named app route defines a title for the breadcrumb', async () => {
    const { default: router } = await import('../../router/index.js')
    const named = router.getRoutes().filter((r) => r.name && r.name !== 'login')

    expect(named.length).toBeGreaterThan(0)
    for (const r of named) {
      expect(r.meta?.title, `route ${String(r.name)} needs meta.title`).toBeTruthy()
    }
    expect(router.getRoutes().find((r) => r.name === 'dashboard')?.meta?.title).toBe('首页')
  })
})
