import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PluginConfigForm from './PluginConfigForm.vue'

describe('PluginConfigForm', () => {
  it('uses writeOnly schema plus handle presence for preserve and rotation', async () => {
    const wrapper = mount(PluginConfigForm, {
      props: {
        schema: {
          type: 'object', required: ['mode', 'api_credential'],
          properties: {
            mode: { type: 'string', title: '模式', enum: ['observe', 'block'] },
            token: { type: 'string', title: '普通 Token', format: 'password' },
            api_credential: { type: 'string', title: 'API Credential', writeOnly: true, default: 'package-secret' },
            optional_secret: { type: 'string', title: 'Optional Secret', writeOnly: true },
            package_ui: { type: 'string', contentMediaType: 'text/html', default: '<script>packageCode()</script>' },
            remote: { $ref: 'https://plugins.example/component.json' }
          }
        },
        config: { mode: 'observe', token: 'ordinary-config-value', api_credential: 'server-secret' },
        secretFields: ['/api_credential', '/optional_secret']
      }
    })

    expect(wrapper.findAll('script')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('packageCode')
    expect(wrapper.html()).not.toContain('package-secret')
    expect(wrapper.html()).not.toContain('server-secret')
    const secretInputs = wrapper.findAll('input[type="password"]')
    expect(secretInputs).toHaveLength(2)
    expect(secretInputs[0].attributes('required')).toBeUndefined()

    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')[0][0]).toEqual({ config: { mode: 'observe', token: 'ordinary-config-value' }, secret_replacements: {} })

    await wrapper.get('select').setValue('block')
    await secretInputs[0].setValue('new-write-only-value')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')[1][0]).toEqual({ config: { mode: 'block', token: 'ordinary-config-value' }, secret_replacements: { '/api_credential': 'new-write-only-value' } })

    await wrapper.get('button.secret-field__clear').trigger('click')
    await wrapper.get('form').trigger('submit')
    expect(wrapper.emitted('submit')[2][0]).toEqual({ config: { mode: 'block', token: 'ordinary-config-value' }, secret_replacements: { '/api_credential': 'new-write-only-value', '/optional_secret': null } })
  })
})
