import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import CertTable from './certs/CertTable.vue'
import L4RuleTable from './l4/L4RuleTable.vue'
import RelayTable from './relay/RelayTable.vue'
import RuleTable from './rules/RuleTable.vue'
import WGProfileTable from './wireguard/WGProfileTable.vue'

function mountCertTable() {
  return mount(CertTable, {
    props: {
      certificates: [
        {
          id: 1,
          enabled: true,
          status: 'active',
          domain: 'media.example.com',
          usage: 'https',
          certificate_type: 'acme',
          last_issue_at: '2026-06-15T00:00:00Z',
          tags: ['media', 'streaming'],
        },
      ],
    },
  })
}

function mountL4RuleTable() {
  return mount(L4RuleTable, {
    props: {
      agent: { id: 'a1', last_sync_revision: 10 },
      rules: [
        {
          id: 1,
          enabled: true,
          protocol: 'tcp',
          listen_host: '0.0.0.0',
          listen_port: 443,
          backends: [{ host: '10.0.0.1', port: 8080 }],
          load_balancing: { strategy: 'adaptive' },
          tags: ['edge'],
        },
      ],
    },
  })
}

function mountRelayTable() {
  return mount(RelayTable, {
    props: {
      listeners: [
        {
          id: 1,
          name: 'relay-wg-local',
          enabled: true,
          public_host: 'relay.example.com',
          public_port: 51820,
          transport_mode: 'wireguard',
          tls_mode: 'ca_only',
          tags: ['relay'],
        },
      ],
    },
  })
}

function mountRuleTable() {
  return mount(RuleTable, {
    props: {
      agent: { id: 'a1', last_sync_revision: 10 },
      rules: [
        {
          id: 1,
          enabled: true,
          frontend_url: 'https://example.com',
          backends: [{ url: 'http://10.0.0.1:8080' }],
          tags: ['web'],
        },
      ],
    },
  })
}

function mountWGProfileTable() {
  return mount(WGProfileTable, {
    props: {
      profiles: [
        {
          id: 1,
          name: 'wg-edge',
          enabled: true,
          public_endpoint: 'wg.example.com:51820',
          listen_port: 51820,
          client_count: 5,
          tags: ['wg'],
        },
      ],
    },
  })
}

function findRow(wrapper) {
  return wrapper.find('tbody tr.rules-table__row')
}

describe('Table monospace cell coverage', () => {
  describe('CertTable', () => {
    it('renders 签发时间 cell with rules-table__mono class', () => {
      const wrapper = mountCertTable()
      const row = findRow(wrapper)
      const tds = row.findAll('td')
      // 签发时间 is the 5th cell (index 4): 状态/域名/用途/类型/签发时间
      expect(tds[4].classes()).toContain('rules-table__mono')
    })

    it('renders 用途 BaseBadge with mono class', () => {
      const wrapper = mountCertTable()
      const monoBadges = wrapper.findAll('.base-badge--mono')
      const texts = monoBadges.map((b) => b.text())
      expect(texts).toContain('网站 HTTPS')
    })

    it('renders 类型 BaseBadge with mono class', () => {
      const wrapper = mountCertTable()
      const monoBadges = wrapper.findAll('.base-badge--mono')
      const texts = monoBadges.map((b) => b.text())
      expect(texts).toContain('自动签发')
    })
  })

  describe('RelayTable', () => {
    it('renders 名称 cell with rules-table__mono class', () => {
      const wrapper = mountRelayTable()
      const row = findRow(wrapper)
      const tds = row.findAll('td')
      expect(tds[1].classes()).toContain('rules-table__mono')
    })

    it('renders TLS cell with rules-table__mono class', () => {
      const wrapper = mountRelayTable()
      const row = findRow(wrapper)
      const tds = row.findAll('td')
      expect(tds[4].classes()).toContain('rules-table__mono')
    })
  })

  describe('WGProfileTable', () => {
    it('renders 名称 cell with rules-table__mono class', () => {
      const wrapper = mountWGProfileTable()
      const row = findRow(wrapper)
      const tds = row.findAll('td')
      expect(tds[1].classes()).toContain('rules-table__mono')
    })

    it('renders 端口 cell with rules-table__mono class', () => {
      const wrapper = mountWGProfileTable()
      const row = findRow(wrapper)
      const tds = row.findAll('td')
      expect(tds[3].classes()).toContain('rules-table__mono')
    })

    it('renders 客户端数 cell with rules-table__mono class', () => {
      const wrapper = mountWGProfileTable()
      const row = findRow(wrapper)
      const tds = row.findAll('td')
      expect(tds[4].classes()).toContain('rules-table__mono')
    })
  })

  describe('Existing mono coverage (regression)', () => {
    it('CertTable 域名 keeps rules-table__mono', () => {
      const wrapper = mountCertTable()
      const row = findRow(wrapper)
      const tds = row.findAll('td')
      expect(tds[1].classes()).toContain('rules-table__mono')
    })

    it('L4RuleTable 监听地址 keeps rules-table__mono', () => {
      const wrapper = mountL4RuleTable()
      const row = findRow(wrapper)
      const tds = row.findAll('td')
      expect(tds[2].classes()).toContain('rules-table__mono')
    })

    it('RuleTable 前端地址 keeps monospace via rules-table__url', () => {
      const wrapper = mountRuleTable()
      const row = findRow(wrapper)
      const tds = row.findAll('td')
      expect(tds[2].classes()).toContain('rules-table__url')
    })
  })
})
