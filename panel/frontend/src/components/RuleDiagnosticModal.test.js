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

  it('keeps throughput metrics visible for HTTP diagnostics even when throughput is unavailable', async () => {
    const wrapper = mountModal({
      task: buildTask('http', { sustained_throughput_bps: null })
    })
    await wrapper.get('.diagnostic-modal__section-title--toggle').trigger('click')

    expect(wrapper.text()).toContain('持续吞吐')
    expect(wrapper.text()).toContain('-')
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

  it('renders adaptive history stats in expanded details', async () => {
    const wrapper = mountModal({
      task: buildTask('http', {
        stability: 0.5,
        recent_succeeded: 0,
        recent_failed: 3,
        latency_ms: 0,
        sample_confidence: 0.55,
        state: 'cold',
        slow_start_active: false
      })
    })
    await wrapper.get('.diagnostic-modal__section-title--toggle').trigger('click')
    await wrapper.get('.diagnostic-backend-item__toggle').trigger('click')

    expect(wrapper.text()).not.toContain('本次延迟 18 ms')
    expect(wrapper.text()).toContain('近24h成功')
    expect(wrapper.text()).toContain('近24h失败')
    expect(wrapper.text()).toContain('延迟')
    expect(wrapper.text()).toContain('3')
    expect(wrapper.text()).toContain('0')
    expect(wrapper.text()).toContain('0 ms')
    expect(wrapper.text()).toContain('50%')
    expect(wrapper.text()).not.toContain('优选状态')
    expect(wrapper.text()).not.toContain('采样置信')
    expect(wrapper.text()).not.toContain('慢启动')
  })

  it('shows resolved child candidates even when only one address is resolved', async () => {
    const wrapper = mountModal({
      task: buildTask('http', {}, [
        {
          backend: 'http://origin.example.test/healthz [127.0.0.1:8096]',
          address: '127.0.0.1:8096',
          summary: {
            sent: 1,
            succeeded: 1,
            failed: 0,
            loss_rate: 0,
            avg_latency_ms: 12,
            min_latency_ms: 12,
            max_latency_ms: 12,
            quality: '极佳'
          },
          adaptive: {
            preferred: true,
            stability: 0.5,
            recent_succeeded: 0,
            recent_failed: 1,
            latency_ms: 0,
            performance_score: 0.92,
            sustained_throughput_bps: 1024 * 1024,
            state: 'cold',
            sample_confidence: 0.05,
            slow_start_active: false
          }
        }
      ])
    })
    await wrapper.get('.diagnostic-modal__section-title--toggle').trigger('click')

    expect(wrapper.text()).not.toContain('已解析候选')
    expect(wrapper.text()).not.toContain('127.0.0.1:8096')
    expect(wrapper.text()).not.toContain('恢复中')
    expect(wrapper.text()).not.toContain('80%')

    await wrapper.get('.diagnostic-backend-item__toggle').trigger('click')

    expect(wrapper.text()).toContain('已解析候选')
    expect(wrapper.text()).toContain('127.0.0.1:8096')
    expect(wrapper.text()).toContain('延迟 0 ms')
    expect(wrapper.text()).toContain('稳定性 50%')
    expect(wrapper.text()).toContain('近24h成功 0')
    expect(wrapper.text()).not.toContain('近24h失败 1')
    expect(wrapper.text()).not.toContain('恢复中')
    expect(wrapper.text()).not.toContain('80%')
  })

  it('shows resolved ip separately in probe samples', async () => {
    const wrapper = mountModal({
      task: {
        ...buildTask('http'),
        result: {
          ...buildTask('http').result,
          samples: [
            {
              attempt: 1,
              backend: 'http://origin.example.test/healthz [127.0.0.1:8096]',
              address: '127.0.0.1:8096',
              success: true,
              latency_ms: 12
            }
          ]
        }
      }
    })

    const toggles = wrapper.findAll('.diagnostic-modal__section-title--toggle')
    await toggles[1].trigger('click')

    expect(wrapper.text()).toContain('http://origin.example.test/healthz')
    expect(wrapper.text()).toContain('127.0.0.1:8096')
  })
})
