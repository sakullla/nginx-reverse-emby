import { beforeEach, describe, expect, it } from 'vitest'
import { mount, RouterLinkStub } from '@vue/test-utils'
import OnboardingChecklist from './OnboardingChecklist.vue'
import { __resetPreferenceCacheForTests } from '../../hooks/usePreference'

const PREF_KEY = 'pref:onboarding.checklist.v1'

const agentsWithoutRules = [
  { id: 'local', http_rules_count: 0, l4_rules_count: 0 }
]
const agentsWithRules = [
  { id: 'local', http_rules_count: 2, l4_rules_count: 1 }
]

function mountChecklist(props = {}) {
  return mount(OnboardingChecklist, {
    props: { agents: agentsWithoutRules, loaded: true, ...props },
    global: { stubs: { RouterLink: RouterLinkStub } }
  })
}

function storedState() {
  const raw = localStorage.getItem(PREF_KEY)
  return raw ? JSON.parse(raw) : null
}

beforeEach(() => {
  localStorage.clear()
  __resetPreferenceCacheForTests()
})

describe('OnboardingChecklist', () => {
  it('无规则的新实例首次加载展示三步清单并标记已开始', () => {
    const wrapper = mountChecklist()

    expect(wrapper.find('[data-testid="onboarding-checklist"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="onboarding-step-rules"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="onboarding-step-verify"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="onboarding-step-https"]').exists()).toBe(true)
    expect(storedState().started).toBe(true)
  })

  it('入口按钮直达 HTTP / L4 创建页面', () => {
    const wrapper = mountChecklist()

    expect(wrapper.findComponent('[data-testid="onboarding-add-http"]').props('to')).toBe('/rules')
    expect(wrapper.findComponent('[data-testid="onboarding-add-l4"]').props('to')).toBe('/l4')
  })

  it('已有规则的实例不展示清单,也不写入已开始状态', () => {
    const wrapper = mountChecklist({ agents: agentsWithRules })

    expect(wrapper.find('[data-testid="onboarding-checklist"]').exists()).toBe(false)
    expect(storedState()?.started ?? false).toBe(false)
  })

  it('节点数据未就绪时不展示、不写入偏好', () => {
    const wrapper = mountChecklist({ agents: [], loaded: false })

    expect(wrapper.find('[data-testid="onboarding-checklist"]').exists()).toBe(false)
    expect(storedState()).toBeNull()
  })

  it('创建任意规则后第一步自动完成', async () => {
    const wrapper = mountChecklist()
    expect(wrapper.find('[data-testid="onboarding-add-http"]').exists()).toBe(true)

    await wrapper.setProps({ agents: agentsWithRules })

    expect(wrapper.find('[data-testid="onboarding-step-rules"]').classes()).toContain('onboarding__step--done')
    expect(wrapper.find('[data-testid="onboarding-rules-done"]').exists()).toBe(true)
    expect(wrapper.find('[data-testid="onboarding-add-http"]').exists()).toBe(false)
    expect(wrapper.find('[data-testid="onboarding-add-l4"]').exists()).toBe(false)
  })

  it('关闭后刷新不再出现', async () => {
    const first = mountChecklist()
    await first.find('[data-testid="onboarding-dismiss"]').trigger('click')

    expect(first.find('[data-testid="onboarding-checklist"]').exists()).toBe(false)
    expect(storedState().dismissed).toBe(true)

    first.unmount()
    const second = mountChecklist()
    expect(second.find('[data-testid="onboarding-checklist"]').exists()).toBe(false)
  })

  it('手动确认步骤 2/3 后标记完成,三步齐全时收起', async () => {
    const wrapper = mountChecklist()

    await wrapper.find('[data-testid="onboarding-verify-done"]').trigger('click')
    await wrapper.find('[data-testid="onboarding-https-done"]').trigger('click')

    expect(storedState().verifyDone).toBe(true)
    expect(storedState().httpsDone).toBe(true)
    // 尚未创建规则时清单保留在首页
    expect(wrapper.find('[data-testid="onboarding-checklist"]').exists()).toBe(true)

    await wrapper.setProps({ agents: agentsWithRules })
    expect(wrapper.find('[data-testid="onboarding-checklist"]').exists()).toBe(false)
  })

  it('关于页重开后清单再次出现', () => {
    localStorage.setItem(PREF_KEY, JSON.stringify({
      started: true,
      dismissed: true,
      verifyDone: false,
      httpsDone: false
    }))
    __resetPreferenceCacheForTests()
    const dismissed = mountChecklist()
    expect(dismissed.find('[data-testid="onboarding-checklist"]').exists()).toBe(false)

    // SettingsAbout.vue「重新打开上手引导」写入的状态
    localStorage.setItem(PREF_KEY, JSON.stringify({
      started: true,
      dismissed: false,
      verifyDone: false,
      httpsDone: false
    }))
    __resetPreferenceCacheForTests()
    const reopened = mountChecklist()
    expect(reopened.find('[data-testid="onboarding-checklist"]').exists()).toBe(true)
  })
})
