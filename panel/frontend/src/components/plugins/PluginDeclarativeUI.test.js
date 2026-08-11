import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PluginDeclarativeUI from './PluginDeclarativeUI.vue'

const document = {
  schema_version: 1,
  title: 'Host UI <script>guest()</script>',
  components: [
    { type: 'section', id: 'general', label: 'General', children: [
      { type: 'text', id: 'name', label: 'Name', binding: '/name' },
      { type: 'secret', id: 'token', label: 'Token', binding: '/token', required: true },
      { type: 'notice', id: 'notice', label: '<img src=x onerror=guest()>', tone: 'warning' }
    ] }
  ],
  actions: [
    { type: 'submit', id: 'save', label: 'Save' },
    { type: 'dynamic', id: 'rotate', label: 'Rotate', target_kind: 'relay', confirm: 'Continue?' }
  ]
}

describe('PluginDeclarativeUI', () => {
  it('renders only fixed host controls and never interprets package markup', async () => {
		const wrapper = mount(PluginDeclarativeUI, { props: { document, config: { name: 'before' }, canConfigure: true, canAct: true } })
    expect(wrapper.findAll('script')).toHaveLength(0)
    expect(wrapper.findAll('img')).toHaveLength(0)
    expect(wrapper.text()).toContain('<script>guest()</script>')
    await wrapper.get('input[type="text"]').setValue('after')
    await wrapper.findAll('button')[0].trigger('click')
    expect(wrapper.emitted('submit')[0][0]).toEqual({ config: { name: 'after' }, secret_replacements: {} })
  })

  it('requires host confirmation and emits only the typed action target', async () => {
    vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
		const wrapper = mount(PluginDeclarativeUI, { props: { document, config: {}, canConfigure: true, canAct: true } })
    await wrapper.get('.declarative-target input').setValue('relay-1')
    await wrapper.findAll('button')[1].trigger('click')
    expect(wrapper.emitted('dynamic')).toBeUndefined()
    await wrapper.findAll('button')[1].trigger('click')
    expect(wrapper.emitted('dynamic')[0][0]).toEqual({ action: document.actions[1], target_id: 'relay-1', confirmed: true })
	})

	it('keeps required declarative secrets write-only and allows only preserve or rotate', async () => {
    const wrapper = mount(PluginDeclarativeUI, { props: { document, config: { name: 'before' }, secretFields: [{ pointer: '/token', present: true }], canConfigure: true } })
    const password = wrapper.get('input[type="password"]')
    expect(password.element.value).toBe('')
    expect(wrapper.html()).not.toContain('existing-secret')
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')[0][0].secret_replacements).toEqual({})
    await password.setValue('rotated-secret')
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')[1][0].secret_replacements).toEqual({ '/token': 'rotated-secret' })
    expect(wrapper.emitted('submit')[1][0].config).not.toHaveProperty('token')
		expect(wrapper.findAll('button').some((button) => button.text() === '清除凭据')).toBe(false)
	})

	it('allows an existing optional secret to be explicitly cleared', async () => {
		const optionalDocument = structuredClone(document)
		optionalDocument.components[0].children[1].required = false
		const wrapper = mount(PluginDeclarativeUI, { props: { document: optionalDocument, config: { name: 'before' }, secretFields: [{ pointer: '/token', present: true }], canConfigure: true } })
		await wrapper.findAll('button').find((button) => button.text() === '清除凭据').trigger('click')
		await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
		expect(wrapper.emitted('submit')[0][0].secret_replacements).toEqual({ '/token': null })
	})

	it('keeps configuration hidden for resource writers while allowing dynamic actions', async () => {
		vi.spyOn(window, 'confirm').mockReturnValue(true)
		const wrapper = mount(PluginDeclarativeUI, { props: { document, config: { name: 'private-config' }, canConfigure: false, canAct: true } })
		expect(wrapper.find('.declarative-section').exists()).toBe(false)
		expect(wrapper.text()).not.toContain('Save')
		expect(wrapper.text()).not.toContain('private-config')
		await wrapper.get('.declarative-target input').setValue('relay-1')
		await wrapper.get('button').trigger('click')
		expect(wrapper.emitted('submit')).toBeUndefined()
		expect(wrapper.emitted('dynamic')[0][0].target_id).toBe('relay-1')
	})
})
