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

const richDocument = {
  schema_version: 1,
  title: 'Rich',
  components: [
    { type: 'select', id: 'mode', label: 'Mode', binding: '/mode', options: [{ value: 'basic', label: 'Basic' }, { value: 'advanced', label: 'Advanced' }] },
    { type: 'text', id: 'extra', label: 'Extra', binding: '/extra', visible_when: { field: '/mode', op: 'eq', value: 'advanced' } },
    { type: 'number', id: 'port', label: 'Port', binding: '/port', required: true, minimum: 1, maximum: 65535 },
    { type: 'array', id: 'upstreams', label: 'Upstreams', binding: '/upstreams', children: [
      { type: 'text', id: 'host', label: 'Host', binding: '/host' },
      { type: 'number', id: 'port', label: 'Port', binding: '/port', minimum: 1, maximum: 65535 }
    ] }
  ],
  actions: [
    { type: 'submit', id: 'save', label: 'Save' }
  ]
}

describe('PluginDeclarativeUI', () => {
  it('renders only fixed host controls and never interprets package markup', async () => {
    const wrapper = mount(PluginDeclarativeUI, { props: { document, config: { name: 'before' }, secretFields: [{ pointer: '/token', present: true }], canConfigure: true, canAct: true } })
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

  it('supports array add, remove and reorder for repeatable groups', async () => {
    const wrapper = mount(PluginDeclarativeUI, { props: { document: richDocument, config: { port: 80, upstreams: [{ host: 'a' }, { host: 'b' }] }, canConfigure: true } })
    await wrapper.findAll('button').find((button) => button.text() === '+ 添加').trigger('click')
    let itemWrappers = wrapper.findAll('.declarative-array-item')
    expect(itemWrappers).toHaveLength(3)
    await itemWrappers[2].find('input[type="text"]').setValue('c')
    await itemWrappers[2].findAll('button').find((button) => button.text() === '上移').trigger('click')
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit').at(-1)[0].config.upstreams.map((item) => item.host)).toEqual(['a', 'c', 'b'])
  })

  it('removes a repeatable array item', async () => {
    const wrapper = mount(PluginDeclarativeUI, { props: { document: richDocument, config: { port: 80, upstreams: [{ host: 'a' }, { host: 'b' }] }, canConfigure: true } })
    await wrapper.findAll('.declarative-array-item')[0].findAll('button').find((button) => button.text() === '移除').trigger('click')
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')[0][0].config.upstreams.map((item) => item.host)).toEqual(['b'])
  })

  it('hides condition-false fields and prunes them from submit', async () => {
    const wrapper = mount(PluginDeclarativeUI, { props: { document: richDocument, config: { mode: 'basic', extra: 'stale', port: 80 }, canConfigure: true } })
    expect(wrapper.find('input[type="text"]').exists()).toBe(false)
    await wrapper.get('select').setValue('advanced')
    expect(wrapper.find('input[type="text"]').exists()).toBe(true)
    await wrapper.get('select').setValue('basic')
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')[0][0].config).toEqual({ mode: 'basic', port: 80 })
  })

  it('renders constraint hints and inline validation errors', async () => {
    const wrapper = mount(PluginDeclarativeUI, { props: { document: richDocument, config: {}, canConfigure: true } })
    expect(wrapper.text()).toContain('必填')
    expect(wrapper.text()).toContain('范围 1–65535')
    expect(wrapper.text()).not.toContain('此项为必填')
    await wrapper.get('input[type="number"]').setValue('0')
    expect(wrapper.text()).toContain('不能小于 1')
  })

  it('blocks submit and reveals required or range errors', async () => {
    const wrapper = mount(PluginDeclarativeUI, { props: { document: richDocument, config: {}, canConfigure: true } })
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')).toBeUndefined()
    expect(wrapper.text()).toContain('此项为必填')
    await wrapper.get('input[type="number"]').setValue('0')
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')).toBeUndefined()
    expect(wrapper.text()).toContain('不能小于 1')
    await wrapper.get('input[type="number"]').setValue('443')
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')[0][0].config.port).toBe(443)
  })

  it('blocks submit for minLength and pattern failures', async () => {
    const constrainedDocument = {
      schema_version: 1,
      title: 'Constrained',
      components: [
        { type: 'text', id: 'name', label: 'Name', binding: '/name', required: true, min_length: 3, pattern: '^[a-z]+$' }
      ],
      actions: [{ type: 'submit', id: 'save', label: 'Save' }]
    }
    const wrapper = mount(PluginDeclarativeUI, { props: { document: constrainedDocument, config: { name: 'A' }, canConfigure: true } })
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')).toBeUndefined()
    expect(wrapper.text()).toContain('至少 3 个字符')
    await wrapper.get('input[type="text"]').setValue('ABC')
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')).toBeUndefined()
    expect(wrapper.text()).toContain('格式不匹配')
    await wrapper.get('input[type="text"]').setValue('abc')
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')[0][0].config).toEqual({ name: 'abc' })
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

  it('seeds schema defaults into a fresh config before submit', async () => {
    const defaultsDocument = {
      schema_version: 1,
      title: 'Defaults',
      components: [
        { type: 'toggle', id: 'enabled', label: 'Enabled', binding: '/enabled', default: false },
        { type: 'text', id: 'name', label: 'Name', binding: '/name', default: 'default-name' },
        { type: 'select', id: 'mode', label: 'Mode', binding: '/mode', options: [{ value: 'basic', label: 'Basic' }], default: 'basic' }
      ],
      actions: [{ type: 'submit', id: 'save', label: 'Save' }]
    }
    const wrapper = mount(PluginDeclarativeUI, { props: { document: defaultsDocument, config: {}, canConfigure: true } })
    expect(wrapper.get('input[type="checkbox"]').element.checked).toBe(false)
    expect(wrapper.get('input[type="text"]').element.value).toBe('default-name')
    expect(wrapper.get('select').element.value).toBe('basic')
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')[0][0].config).toEqual({ enabled: false, name: 'default-name', mode: 'basic' })
  })

  it('round-trips numeric enum values through a select', async () => {
    const numericDocument = {
      schema_version: 1,
      title: 'Numeric',
      components: [
        { type: 'select', id: 'level', label: 'Level', binding: '/level', options: [{ value: 1, label: '1' }, { value: 2, label: '2' }] }
      ],
      actions: [{ type: 'submit', id: 'save', label: 'Save' }]
    }
    const wrapper = mount(PluginDeclarativeUI, { props: { document: numericDocument, config: { level: 1 }, canConfigure: true } })
    await wrapper.get('select').setValue('2')
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')[0][0].config).toEqual({ level: 2 })
  })

  const extendedDocument = {
    schema_version: 1,
    title: 'Extended',
    components: [
      { type: 'section', id: 'advanced', label: 'Advanced', collapsible: true, default_collapsed: true, children: [
        { type: 'text', id: 'note', label: 'Note', binding: '/note' }
      ] },
      { type: 'grid', id: 'pair', columns: 2, children: [
        { type: 'radio', id: 'mode', label: 'Mode', binding: '/mode', options: [{ value: 'observe', label: 'Observe' }, { value: 'block', label: 'Block' }] },
        { type: 'multiselect', id: 'flags', label: 'Flags', binding: '/flags', options: [{ value: 'fast', label: 'Fast' }, { value: 'safe', label: 'Safe' }] }
      ] },
      { type: 'keyvalue', id: 'labels', label: 'Labels', binding: '/labels' }
    ],
    actions: [{ type: 'submit', id: 'save', label: 'Save' }]
  }

  it('renders grid children and submits radio selections', async () => {
    const wrapper = mount(PluginDeclarativeUI, { props: { document: extendedDocument, config: { mode: 'observe' }, canConfigure: true } })
    expect(wrapper.get('.declarative-grid').exists()).toBe(true)
    const radios = wrapper.findAll('input[type="radio"]')
    expect(radios).toHaveLength(2)
    expect(radios[0].element.checked).toBe(true)
    await radios[1].setValue(true)
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')[0][0].config.mode).toBe('block')
  })

  it('toggles multiselect options and keeps declared option order', async () => {
    const wrapper = mount(PluginDeclarativeUI, { props: { document: extendedDocument, config: { flags: ['safe'] }, canConfigure: true } })
    const boxes = wrapper.findAll('.declarative-multiselect-group input[type="checkbox"]')
    expect(boxes).toHaveLength(2)
    expect(boxes[0].element.checked).toBe(false)
    expect(boxes[1].element.checked).toBe(true)
    await boxes[0].setValue(true)
    await boxes[1].setValue(false)
    await boxes[0].setValue(false)
    await boxes[1].setValue(true)
    await boxes[0].setValue(true)
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')[0][0].config.flags).toEqual(['fast', 'safe'])
  })

  it('edits keyvalue rows with add, rename, value change and remove', async () => {
    const wrapper = mount(PluginDeclarativeUI, { props: { document: extendedDocument, config: { labels: { env: 'prod' } }, canConfigure: true } })
    expect(wrapper.findAll('.declarative-keyvalue__row')).toHaveLength(1)
    const addButton = wrapper.findAll('.declarative-keyvalue .btn').find((button) => button.text() === '+ 添加')
    await addButton.trigger('click')
    const rows = wrapper.findAll('.declarative-keyvalue__row')
    expect(rows).toHaveLength(2)
    await rows[1].findAll('input')[0].setValue('region')
    await rows[1].findAll('input')[1].setValue('cn')
    await rows[0].findAll('button').find((button) => button.text() === '移除').trigger('click')
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')[0][0].config.labels).toEqual({ region: 'cn' })
  })

  it('drops empty keyvalue keys from the submit payload', async () => {
    const wrapper = mount(PluginDeclarativeUI, { props: { document: extendedDocument, config: { labels: { env: 'prod' } }, canConfigure: true } })
    await wrapper.findAll('.declarative-keyvalue .btn').find((button) => button.text() === '+ 添加').trigger('click')
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')[0][0].config.labels).toEqual({ env: 'prod' })
  })

  it('collapses sections only when collapsible and keeps values mounted', async () => {
    const wrapper = mount(PluginDeclarativeUI, { props: { document: extendedDocument, config: { note: 'kept' }, canConfigure: true } })
    // jsdom caches computed visibility; assert the v-show inline style instead.
    expect(wrapper.get('.declarative-section__body').attributes('style')).toContain('display: none')
    expect(wrapper.get('.declarative-section__toggle').attributes('aria-expanded')).toBe('false')
    await wrapper.get('.declarative-section__toggle').trigger('click')
    await wrapper.vm.$nextTick()
    expect(wrapper.get('.declarative-section__body').attributes('style') || '').not.toContain('display: none')
    expect(wrapper.get('.declarative-section__toggle').attributes('aria-expanded')).toBe('true')
    expect(wrapper.get('input[type="text"]').element.value).toBe('kept')
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')[0][0].config.note).toBe('kept')
  })

  it('seeds defaults for synthesized extended components', async () => {
    const defaultsDocument = {
      schema_version: 1,
      title: 'Extended defaults',
      components: [
        { type: 'radio', id: 'mode', label: 'Mode', binding: '/mode', options: [{ value: 'a', label: 'A' }], default: 'a' },
        { type: 'multiselect', id: 'flags', label: 'Flags', binding: '/flags', options: [{ value: 'x', label: 'X' }], default: ['x'] },
        { type: 'keyvalue', id: 'labels', label: 'Labels', binding: '/labels', default: { env: 'prod' } }
      ],
      actions: [{ type: 'submit', id: 'save', label: 'Save' }]
    }
    const wrapper = mount(PluginDeclarativeUI, { props: { document: defaultsDocument, config: {}, canConfigure: true } })
    expect(wrapper.get('input[type="radio"]').element.checked).toBe(true)
    await wrapper.findAll('button').find((button) => button.text() === 'Save').trigger('click')
    expect(wrapper.emitted('submit')[0][0].config).toEqual({ mode: 'a', flags: ['x'], labels: { env: 'prod' } })
  })
})
