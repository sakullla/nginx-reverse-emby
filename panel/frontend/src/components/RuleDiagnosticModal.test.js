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
  it('renders HTTP-only adaptive throughput and performance fields in history details', async () => {
    const wrapper = mountModal()
    await wrapper.get('.diagnostic-modal__section-title--toggle').trigger('click')

    expect(wrapper.text()).not.toContain('持续吞吐')
    expect(wrapper.text()).not.toContain('综合性能')

    await wrapper.get('.diagnostic-backend-item__toggle').trigger('click')

    expect(wrapper.text()).toContain('持续吞吐')
    expect(wrapper.text()).toContain('综合性能')
    expect(wrapper.text()).toContain('4.0 MB/s')
  })

  it('keeps throughput metrics visible for HTTP diagnostics even when throughput is unavailable', async () => {
    const wrapper = mountModal({
      task: buildTask('http', { sustained_throughput_bps: 0 })
    })
    await wrapper.get('.diagnostic-modal__section-title--toggle').trigger('click')
    await wrapper.get('.diagnostic-backend-item__toggle').trigger('click')

    expect(wrapper.text()).toContain('持续吞吐')
    expect(wrapper.text()).toContain('-')
    expect(wrapper.text()).not.toContain('0 B/s')
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

    expect(wrapper.text()).toContain('历史统计')
    expect(wrapper.text()).not.toContain('本次延迟 18 ms')
    expect(wrapper.text()).toContain('近24h成功')
    expect(wrapper.text()).toContain('近24h失败')
    expect(wrapper.text()).toContain('历史延迟')
    expect(wrapper.text()).toContain('3')
    expect(wrapper.text()).toContain('0')
    expect(wrapper.text()).not.toContain('历史延迟18 ms')
    expect(wrapper.text()).toContain('50%')
    expect(wrapper.text()).not.toContain('优选状态')
    expect(wrapper.text()).not.toContain('采样置信')
    expect(wrapper.text()).not.toContain('慢启动')
  })

  it('keeps current probe latency in the card and history latency in expanded details', async () => {
    const wrapper = mountModal({
      task: buildTask('http', {
        latency_ms: 0,
        state: 'cold'
      }, [
        {
          backend: 'http://origin.example.test/healthz [127.0.0.1:8096]',
          address: '127.0.0.1:8096',
          summary: {
            sent: 5,
            succeeded: 5,
            failed: 0,
            loss_rate: 0,
            avg_latency_ms: 12.4,
            min_latency_ms: 10,
            max_latency_ms: 14,
            quality: '极佳'
          },
          adaptive: {
            preferred: true,
            latency_ms: 0,
            stability: 0.5,
            recent_succeeded: 0
          }
        }
      ])
    })
    await wrapper.get('.diagnostic-modal__section-title--toggle').trigger('click')

    const cardMetrics = wrapper.findAll('.diagnostic-backend-item__metrics .diagnostic-metric').map(metric => metric.text())
    expect(cardMetrics).toContain('延迟18 ms')
    expect(cardMetrics).toContain('成功3 / 3')

    await wrapper.get('.diagnostic-backend-item__toggle').trigger('click')

    expect(wrapper.text()).toContain('18 ms')
    const historyFactors = wrapper.findAll('.diagnostic-backend-item__details-grid .diagnostic-factor').map(factor => factor.text())
    expect(historyFactors).toContain('历史延迟-')
    expect(historyFactors).not.toContain('历史延迟18 ms')
    expect(historyFactors).not.toContain('历史延迟12.4 ms')
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
    expect(wrapper.text()).toContain('历史延迟 -')
    expect(wrapper.text()).toContain('稳定性 50%')
    expect(wrapper.text()).toContain('近24h成功 0')
    expect(wrapper.text()).not.toContain('近24h失败 1')
    expect(wrapper.text()).not.toContain('恢复中')
    expect(wrapper.text()).not.toContain('80%')
  })

  it('uses the preferred resolved child adaptive values for parent backend display fields', async () => {
    const wrapper = mountModal({
      task: buildTask('http', {
        stability: 0.5,
        recent_succeeded: 0,
        recent_failed: 0,
        latency_ms: 0,
        performance_score: 0,
        traffic_share_hint: 'cold'
      }, [
        {
          backend: 'https://origin.example.test/healthz [104.21.15.245:443]',
          address: '104.21.15.245:443',
          summary: {
            sent: 5,
            succeeded: 5,
            failed: 0,
            loss_rate: 0,
            avg_latency_ms: 176.6,
            min_latency_ms: 170,
            max_latency_ms: 185,
            quality: '良好'
          },
          adaptive: {
            preferred: true,
            stability: 1,
            recent_succeeded: 19,
            recent_failed: 1,
            latency_ms: 176.6,
            performance_score: 0.73,
            sustained_throughput_bps: 72.6 * 1024 * 1024,
            traffic_share_hint: 'normal',
            outlier: false
          }
        },
        {
          backend: 'https://origin.example.test/healthz [172.67.165.85:443]',
          address: '172.67.165.85:443',
          summary: {
            sent: 5,
            succeeded: 5,
            failed: 0,
            loss_rate: 0,
            avg_latency_ms: 217.6,
            min_latency_ms: 210,
            max_latency_ms: 225,
            quality: '一般'
          },
          adaptive: {
            preferred: false,
            stability: 1,
            recent_succeeded: 20,
            recent_failed: 0,
            latency_ms: 217.6,
            performance_score: 0.61,
            sustained_throughput_bps: 40 * 1024 * 1024,
            traffic_share_hint: 'normal',
            outlier: false
          }
        }
      ])
    })

    await wrapper.get('.diagnostic-modal__section-title--toggle').trigger('click')
    await wrapper.get('.diagnostic-backend-item__toggle').trigger('click')

    const parentFactorTexts = wrapper
      .findAll('.diagnostic-backend-item__details-grid .diagnostic-factor')
      .map(factor => factor.text())

    expect(parentFactorTexts).toContain('综合性能0.73')
    expect(parentFactorTexts).toContain('近24h成功19')
    expect(parentFactorTexts).toContain('近24h失败1')
    expect(parentFactorTexts).toContain('流量阶段主流量')
    expect(parentFactorTexts).not.toContain('综合性能0.00')
    expect(parentFactorTexts).not.toContain('流量阶段冷启动探索')
  })

  it('omits unavailable probe summary when real adaptive traffic history exists', async () => {
    const wrapper = mountModal({
      task: {
        ...buildTask('http'),
        result: {
          ...buildTask('http').result,
          backends: [
            {
              backend: 'https://origin.example.test',
              summary: {
                sent: 0,
                succeeded: 0,
                failed: 0,
                avg_latency_ms: 0,
                quality: '不可用'
              },
              adaptive: {
                preferred: true,
                stability: 1,
                recent_succeeded: 8,
                recent_failed: 0,
                latency_ms: 261.5,
                performance_score: 0.2,
                sustained_throughput_bps: 2 * 1024 * 1024,
                traffic_share_hint: 'normal',
                outlier: false
              },
              children: [
                {
                  backend: 'https://origin.example.test [origin.example.test:443]',
                  address: 'origin.example.test:443',
                  summary: {
                    sent: 0,
                    succeeded: 0,
                    failed: 0,
                    avg_latency_ms: 0,
                    quality: '不可用'
                  },
                  adaptive: {
                    preferred: true,
                    stability: 1,
                    recent_succeeded: 8,
                    recent_failed: 0,
                    latency_ms: 261.5
                  }
                }
              ]
            }
          ]
        }
      }
    })
    await wrapper.get('.diagnostic-modal__section-title--toggle').trigger('click')
    await wrapper.get('.diagnostic-backend-item__toggle').trigger('click')

    expect(wrapper.text()).not.toContain('本次测试 0 ms')
    expect(wrapper.text()).not.toContain('成功 0 / 0')
    expect(wrapper.text()).toContain('261.5 ms')
    expect(wrapper.text()).toContain('近24h成功8')
    expect(wrapper.text()).toContain('近24h成功 8')
    expect(wrapper.text()).toContain('2.0 MB/s')
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

  it('renders relay hop latency when isolated timing is reported', () => {
    const wrapper = mountModal({
      task: {
        ...buildTask('http'),
        result: {
          ...buildTask('http').result,
          relay_paths: [
            {
              path: [1, 2],
              success: true,
              latency_ms: 12,
              hops: [
                {
                  from: 'client',
                  success: true,
                  to_listener_id: 1,
                  to_listener_name: 'Relay A',
                  to_agent_name: 'agent-a',
                  latency_ms: 7.2
                },
                {
                  success: true,
                  from_listener_id: 1,
                  from_listener_name: 'Relay A',
                  to: 'backend.example:8096',
                  latency_ms: 12
                }
              ]
            }
          ]
        }
      }
    })

    expect(wrapper.text()).not.toContain('undefined ms')
    expect(wrapper.text()).toContain('入口 → Relay A (agent-a)')
    expect(wrapper.text()).toContain('7.2 ms')
    expect(wrapper.text()).toContain('12 ms')
    expect(wrapper.text()).not.toContain('待测量')
  })

  it('hides opaque relay agent identifiers in hop labels', () => {
    const opaqueAgentID = '3c65c453649aa9f80adc5ee6727a7ba2'
    const wrapper = mountModal({
      task: {
        ...buildTask('http'),
        result: {
          ...buildTask('http').result,
          relay_paths: [
            {
              path: [1],
              success: true,
              latency_ms: 12,
              hops: [
                {
                  from: 'client',
                  success: true,
                  to_listener_id: 1,
                  to_listener_name: 'fastu9929',
                  to_agent_name: opaqueAgentID,
                  latency_ms: 7.2
                },
                {
                  success: true,
                  from_listener_id: 1,
                  from_listener_name: 'fastu9929',
                  from_agent_name: opaqueAgentID,
                  to: 'backend.example:8096',
                  latency_ms: 12
                }
              ]
            }
          ]
        }
      }
    })

    expect(wrapper.text()).toContain('入口 → fastu9929')
    expect(wrapper.text()).toContain('fastu9929 → 后端(backend.example:8096)')
    expect(wrapper.text()).not.toContain(opaqueAgentID)
  })

  it('prefers the provided node name over an opaque task agent id', () => {
    const opaqueAgentID = '31a37f180de98c927326c0eb614a38cb'
    const wrapper = mountModal({
      agentLabel: 'Edge Node A',
      task: {
        ...buildTask('http'),
        agent_id: opaqueAgentID
      }
    })

    expect(wrapper.text()).toContain('节点: Edge Node A')
    expect(wrapper.text()).not.toContain(opaqueAgentID)
  })
})
