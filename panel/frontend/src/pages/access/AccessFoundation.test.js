import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import QuotaUsage from '../../components/access/QuotaUsage.vue'
import SecretWriteOnlyField from '../../components/access/SecretWriteOnlyField.vue'
import { accessNavigation } from '../../context/useAccessControl'
import router from '../../router'

describe('access security foundation', () => {
  it('renders quota current, limit and recovery context together', () => {
    const wrapper = mount(QuotaUsage, {
      props: { current: 8, limit: 10, recoveryCondition: '下个结算周期' }
    })

    expect(wrapper.text()).toContain('8')
    expect(wrapper.text()).toContain('10')
    expect(wrapper.text()).toContain('下个结算周期')
    expect(wrapper.get('progress').attributes('value')).toBe('80')
  })

  it('keeps secret entry write-only and exposes only the implemented access overview route', () => {
    const wrapper = mount(SecretWriteOnlyField)

    expect(wrapper.get('input').attributes('type')).toBe('password')
    expect(wrapper.get('input').attributes('placeholder')).toContain('不可取回')
    expect(accessNavigation.filter((item) => item.id !== 'resource-groups').every((item) => item.permission && !item.path)).toBe(true)
    expect(accessNavigation.find((item) => item.id === 'resource-groups')).toMatchObject({
      permission: 'resource.read',
      path: '/access/resource-groups'
    })
    expect(router.resolve('/access').name).toBe('access')
    expect(router.resolve('/access/resource-groups').name).toBe('access-resource-groups')
    expect(router.getRoutes().some((route) => route.path === '/access/users')).toBe(false)
  })
})
