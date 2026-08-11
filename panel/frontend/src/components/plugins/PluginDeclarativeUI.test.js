import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import PluginDeclarativeUI from './PluginDeclarativeUI.vue'

const document = {
  schema_version: 1,
  title: 'Host UI <script>guest()</script>',
  components: [
    { type: 'section', id: 'general', label: 'General', children: [
      { type: 'text', id: 'name', label: 'Name', binding: '/name' },
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
