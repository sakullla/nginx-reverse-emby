import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PluginConfigForm from './PluginConfigForm.vue'

describe('PluginConfigForm', () => {
  it('renders only host declarative fields and keeps secret values write-only', async () => {
    const wrapper = mount(PluginConfigForm, {
      props: {
        schema: {
          type: 'object',
          required: ['mode', 'api_token'],
          properties: {
            mode: { type: 'string', title: '模式', enum: ['observe', 'block'] },
            api_token: { type: 'string', title: 'API Token', format: 'password', default: 'package-secret' },
            package_ui: { type: 'string', contentMediaType: 'text/html', default: '<script>packageCode()</script>' },
            remote: { $ref: 'https://plugins.example/component.json' }
          }
        },
        config: { mode: 'observe', api_token: 'server-secret' }
      }
    })

    expect(wrapper.findAll('script')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('packageCode')
    expect(wrapper.html()).not.toContain('package-secret')
    expect(wrapper.html()).not.toContain('server-secret')
    expect(wrapper.get('input[type="password"]').element.value).toBe('')

    await wrapper.get('select').setValue('block')
    await wrapper.get('input[type="password"]').setValue('new-write-only-value')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')[0][0]).toEqual({ config: { mode: 'block' }, secret_replacements: { '/api_token': 'new-write-only-value' } })
  })
})
