import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import PluginConfigForm from './PluginConfigForm.vue'

function submitButton(wrapper) {
  return wrapper.findAll('button').find((button) => button.text() === '保存配置并生成 revision')
}

describe('PluginConfigForm', () => {
  it('uses writeOnly schema plus handle presence for preserve and rotation', async () => {
    const wrapper = mount(PluginConfigForm, {
      props: {
        schema: {
          type: 'object', required: ['mode', 'api_credential', 'missing_required'],
          properties: {
            mode: { type: 'string', title: '模式', enum: ['observe', 'block'] },
            token: { type: 'string', title: '普通 Token', format: 'password' },
            api_credential: { type: 'string', title: 'API Credential', writeOnly: true, default: 'package-secret' },
            missing_required: { type: 'string', title: 'Missing Required', writeOnly: true },
            optional_secret: { type: 'string', title: 'Optional Secret', writeOnly: true },
            optional_missing: { type: 'string', title: 'Optional Missing', writeOnly: true },
            package_ui: { type: 'string', contentMediaType: 'text/html', default: '<script>packageCode()</script>' },
            remote: { $ref: 'https://plugins.example/component.json' }
          }
        },
        config: { mode: 'observe', token: 'ordinary-config-value', api_credential: 'server-secret' },
        secretFields: [
          { pointer: '/api_credential', present: true },
          { pointer: '/missing_required', present: false },
          { pointer: '/optional_secret', present: true },
          { pointer: '/optional_missing', present: false }
        ]
      }
    })

    expect(wrapper.findAll('script')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('packageCode')
    expect(wrapper.html()).not.toContain('package-secret')
    expect(wrapper.html()).not.toContain('server-secret')
    const secretInputs = wrapper.findAll('input[type="password"]')
    expect(secretInputs).toHaveLength(4)
    expect(secretInputs[0].attributes('required')).toBeUndefined()
    expect(secretInputs[1].attributes('required')).toBeDefined()
    expect(secretInputs[3].attributes('required')).toBeUndefined()
    expect(wrapper.findAll('button').filter((button) => button.text() === '清除凭据')).toHaveLength(1)

    await submitButton(wrapper).trigger('click')
    expect(wrapper.emitted('submit')[0][0]).toEqual({ config: { mode: 'observe', token: 'ordinary-config-value' }, secret_replacements: {} })

    await wrapper.get('select').setValue('block')
    await secretInputs[0].setValue('new-write-only-value')
    await submitButton(wrapper).trigger('click')
    expect(wrapper.emitted('submit')[1][0]).toEqual({ config: { mode: 'block', token: 'ordinary-config-value' }, secret_replacements: { '/api_credential': 'new-write-only-value' } })

    await wrapper.findAll('button').find((button) => button.text() === '清除凭据').trigger('click')
    await submitButton(wrapper).trigger('click')
    expect(wrapper.emitted('submit')[2][0]).toEqual({ config: { mode: 'block', token: 'ordinary-config-value' }, secret_replacements: { '/api_credential': 'new-write-only-value', '/optional_secret': null } })
  })

  it('synthesizes nested object secrets and round-trips nested config', async () => {
    const wrapper = mount(PluginConfigForm, {
      props: {
        schema: {
          type: 'object',
          properties: {
            name: { type: 'string', title: '名称' },
            credentials: { type: 'object', properties: { token: { type: 'string', title: 'Token', writeOnly: true } } }
          }
        },
        config: { name: 'edge', credentials: { token: 'nested-secret' } },
        secretFields: [{ pointer: '/credentials/token', present: true }]
      }
    })

    expect(wrapper.html()).not.toContain('nested-secret')
    expect(wrapper.findAll('input[type="password"]')).toHaveLength(1)

    await wrapper.get('input[type="password"]').setValue('rotated')
    await submitButton(wrapper).trigger('click')
    expect(wrapper.emitted('submit')[0][0]).toEqual({
      config: { name: 'edge', credentials: {} },
      secret_replacements: { '/credentials/token': 'rotated' }
    })
  })

  it('round-trips an array of objects through edit and submit', async () => {
    const wrapper = mount(PluginConfigForm, {
      props: {
        schema: {
          type: 'object',
          properties: {
            upstreams: { type: 'array', items: { type: 'object', properties: { host: { type: 'string', title: 'Host' } } } }
          }
        },
        config: { upstreams: [{ host: 'a' }, { host: 'b' }] }
      }
    })

    const hostInputs = wrapper.findAll('.declarative-array-item input[type="text"]')
    expect(hostInputs).toHaveLength(2)
    await hostInputs[0].setValue('a-edited')
    await submitButton(wrapper).trigger('click')
    expect(wrapper.emitted('submit')[0][0].config.upstreams.map((item) => item.host)).toEqual(['a-edited', 'b'])
  })
})
