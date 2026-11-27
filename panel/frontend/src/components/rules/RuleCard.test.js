import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it } from 'vitest'
import { nextTick } from 'vue'
import RuleCard from './RuleCard.vue'
import RuleTable from './RuleTable.vue'

const generation = '6ca5bf3f4ebcf3d41f00b3fd22319854d482f063d34dfdfd3e51eb6c'
const rule = {
  id: 1,
  agent_id: 'local',
  frontend_url: 'http://127.0.0.1',
  enabled: true,
  tags: [],
  backends: [{
    kind: 'plugin_provider',
    plugin_provider: {
      instance_id: 'accelerator-sources-3',
      provider_id: 'default'
    }
  }]
}
const providerCatalog = [{
  agent_id: 'local',
  instance_id: 'accelerator-sources-3',
  provider_id: 'default',
  display_name: 'Accelerator Sources',
  state: 'active',
  ready_generation: generation
}]

describe('plugin provider presentation', () => {
  it('does not expose the internal generation in a rule card', () => {
    const wrapper = mount(RuleCard, {
      props: { rule, providerCatalog }
    })

    expect(wrapper.text()).toContain('插件已就绪')
    expect(wrapper.text()).not.toContain(generation)
  })

  it('opens the editor when the card is clicked', async () => {
    const wrapper = mount(RuleCard, {
      props: { rule, providerCatalog }
    })

    await wrapper.get('.base-list-card').trigger('click')
    expect(wrapper.emitted('edit')?.[0]).toEqual([rule])
  })

  it('recognizes a service-created rule and links back to the service', () => {
    const wrapper = mount(RuleCard, {
      props: {
        rule: {
          ...rule,
          tags: ['nre-access-service:svc-1'],
        },
      },
      global: {
        stubs: {
          RouterLink: { props: ['to'], template: '<a class="service-link" :href="to"><slot /></a>' },
        },
      },
    })

    const link = wrapper.get('a.service-link')
    expect(link.text()).toContain('服务接入')
    expect(link.attributes('href')).toBe('/services?id=svc-1')
    expect(wrapper.text()).toContain('nre-access-service:svc-1')
  })

  it('recognizes a service-created rule in the rule table', () => {
    const wrapper = mount(RuleTable, {
      props: {
        rules: [{
          ...rule,
          tags: ['nre-access-service:svc-9'],
        }],
      },
      global: {
        stubs: {
          RouterLink: { props: ['to'], template: '<a class="service-link" :href="to"><slot /></a>' },
        },
      },
    })

    expect(wrapper.get('a.service-link').attributes('href')).toBe('/services?id=svc-9')
    expect(wrapper.text()).toContain('服务接入')
  })

  it('does not expose the internal generation in a rule table tooltip', () => {
    const wrapper = mount(RuleTable, {
      props: { rules: [rule], providerCatalog }
    })

    const title = wrapper.get('.rules-table__url--backend').attributes('title')
    expect(title).toContain('插件已就绪')
    expect(title).not.toContain(generation)
  })
})

describe('RuleCard actions', () => {
  const plainRule = { id: 5, agent_id: 'local', frontend_url: 'https://a.example.com', enabled: true, tags: [] }

  // Earlier mounts teleport their menu panels to body too; start from a clean body.
  beforeEach(() => {
    document.body.innerHTML = ''
  })

  afterEach(() => {
    document.body.innerHTML = ''
  })

  it('shows only toggle and edit as visible buttons, with the rest in the more menu', async () => {
    const wrapper = mount(RuleCard, { props: { rule: plainRule }, attachTo: document.body })
    const header = wrapper.get('.base-list-card__header-right')
    const visible = header.findAll('.base-icon-button:not(.base-action-menu__trigger)')
    expect(visible.map((button) => button.attributes('aria-label'))).toEqual(['停用', '编辑'])

    for (const [id, event] of [['copy', 'copy'], ['diagnose', 'diagnose'], ['delete', 'delete']]) {
      await wrapper.get('.base-action-menu__trigger').trigger('click')
      document.body.querySelector(`[data-testid="base-action-menu-item-${id}"]`).click()
      await nextTick()
      expect(wrapper.emitted(event)?.at(-1)).toEqual([plainRule])
    }
    expect(wrapper.emitted('edit')).toBeUndefined()
    wrapper.unmount()
  })

  it('toggles selection instead of editing when selectable', async () => {
    const wrapper = mount(RuleCard, { props: { rule: plainRule, selectable: true, selected: false } })
    await wrapper.get('.base-list-card').trigger('click')
    await wrapper.get('input.rule-card__select').trigger('click')
    expect(wrapper.emitted('select')).toEqual([[plainRule], [plainRule]])
    expect(wrapper.emitted('edit')).toBeUndefined()
  })
})
