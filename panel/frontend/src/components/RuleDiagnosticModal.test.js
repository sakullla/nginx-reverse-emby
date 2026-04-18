import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import RuleDiagnosticModal from './RuleDiagnosticModal.vue'

function buildTask(kind, adaptive = {}, children = []) {
  return {
    state: 'completed',
    result: {
      kind,
      summary: {
        sent: 3,
        succeeded: 3,
        failed: 0,
        loss_rate: 0,
        avg_latency_ms: 18,
        min_latency_ms: 10,
        max_latency_ms: 22,
        quality: '极佳'
      },
      backends: [
        {
          backend: 'backend-a',
          summary: {
            sent: 3,
            succeeded: 3,
            failed: 0,
            loss_rate: 0,
            avg_latency_ms: 18,
            min_latency_ms: 10,
            max_latency_ms: 22,
            quality: '极佳'
          },
          adaptive: {
            preferred: true,
            reason: 'performance_higher',
            stability: 1,
            recent_succeeded: 3,
            recent_failed: 0,
            latency_ms: 18,
            sustained_throughput_bps: 4 * 1024 * 1024,
            performance_score: 0.88,
            state: 'warm',
            sample_confidence: 1,
            slow_start_active: false,
            outlier: false,
            traffic_share_hint: 'normal',
            ...adaptive
          },
          children
        }
      ],
      samples: []
    }
  }
}

function mountModal(props = {}) {
  return mount(RuleDiagnosticModal, {
    props: {
      modelValue: true,
      kind: 'http',
      ruleLabel: 'Test Rule',
      endpointLabel: 'https://edge.example.test',
      task: buildTask('http'),
      ...props
    },
    global: {
      stubs: {
        BaseModal: {
          template: '<div><slot /></div>',
          props: ['modelValue', 'title', 'size', 'closeOnClickModal']
        },
        Transition: false
      }
    }
  })
}

describe('RuleDiagnosticModal', () => {
  it('renders HTTP-only adaptive throughput and performance fields from DOM state', async () => {
    const wrapper = mountModal()
    await wrapper.get('.diagnostic-modal__section-title--toggle').trigger('click')

    expect(wrapper.text()).toContain('持续吞吐')
    expect(wrapper.text()).toContain('综合性能')
    expect(wrapper.text()).toContain('4.0 MB/s')
  })

  it('hides throughput metrics when sustained throughput is absent', async () => {
    const wrapper = mountModal({
      task: buildTask('http', { sustained_throughput_bps: null })
    })
    await wrapper.get('.diagnostic-modal__section-title--toggle').trigger('click')

    expect(wrapper.text()).not.toContain('持续吞吐')
  })

  it('omits HTTP-only adaptive fields for l4 diagnostics', async () => {
    const wrapper = mountModal({
      kind: 'l4_tcp',
      task: buildTask('l4_tcp', {
        sustained_throughput_bps: 4 * 1024 * 1024,
        performance_score: 0.88,
        reason: 'performance_higher'
      })
    })
    await wrapper.get('.diagnostic-modal__section-title--toggle').trigger('click')

    expect(wrapper.text()).not.toContain('持续吞吐')
    expect(wrapper.text()).not.toContain('综合性能')
    expect(wrapper.text()).not.toContain('原因:')
  })
})
