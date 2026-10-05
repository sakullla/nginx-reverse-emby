import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import SettingsAbout from './SettingsAbout.vue'
import { __resetPreferenceCacheForTests } from '../../hooks/usePreference'

const routerPush = vi.fn()

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: routerPush })
}))

vi.mock('../../hooks/useSystemInfo', () => ({
  useSystemInfo: () => ({
    data: ref({
      app_version: '1.2.3',
      build_time: '2026-10-05T00:00:00Z',
      local_apply_runtime: 'go',
      go_version: 'go1.25',
      role: 'local',
      local_agent_enabled: true,
      online_agents: 1,
      total_agents: 1,
      started_at: null,
      data_dir: '/data'
    }),
    isLoading: ref(false)
  })
}))

const PREF_KEY = 'pref:onboarding.checklist.v1'

function mountAbout() {
  return mount(SettingsAbout)
}

beforeEach(() => {
  localStorage.clear()
  __resetPreferenceCacheForTests()
  routerPush.mockClear()
})

describe('SettingsAbout 定位与入口', () => {
  it('使用通用反代 + L4 转发定位,不再出现媒体管理旧文案', () => {
    const text = mountAbout().text()
    expect(text).toContain('通用 HTTP/HTTPS 反代 + L4 TCP/UDP 转发控制面板')
    expect(text).not.toContain('媒体管理控制面板')
  })

  it('提供指向文档站的外链', () => {
    const link = mountAbout().get('[data-testid="about-docs-link"]')
    expect(link.attributes('href')).toBe('https://sakullla.github.io/nginx-reverse-emby/')
    expect(link.attributes('target')).toBe('_blank')
    expect(link.attributes('rel')).toContain('noopener')
  })

  it('重新打开上手引导会重置偏好并跳转首页', async () => {
    localStorage.setItem(PREF_KEY, JSON.stringify({
      started: true,
      dismissed: true,
      verifyDone: true,
      httpsDone: true
    }))
    __resetPreferenceCacheForTests()

    const wrapper = mountAbout()
    await wrapper.get('[data-testid="reopen-onboarding"]').trigger('click')

    expect(routerPush).toHaveBeenCalledWith('/')
    expect(JSON.parse(localStorage.getItem(PREF_KEY))).toEqual({
      started: true,
      dismissed: false,
      verifyDone: false,
      httpsDone: false
    })
  })
})
