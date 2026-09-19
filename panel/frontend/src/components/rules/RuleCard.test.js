import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
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

  it('does not expose the internal generation in a rule table tooltip', () => {
    const wrapper = mount(RuleTable, {
      props: { rules: [rule], providerCatalog }
    })

    const title = wrapper.get('.rules-table__url--backend').attributes('title')
    expect(title).toContain('插件已就绪')
    expect(title).not.toContain(generation)
  })
})
