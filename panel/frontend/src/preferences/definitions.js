/**
 * Declarative preference registry.
 * Add new entries here to surface them in SettingsGeneral without one-off wiring.
 */
export const PREFERENCE_DEFINITIONS = [
  {
    key: 'dashboard.trafficView',
    label: '首页流量默认视角',
    description: '流量趋势与 TOP 默认展示节点视角，或按规则（HTTP / L4 / Relay）视角',
    type: 'enum',
    defaultValue: 'nodes',
    options: [
      { value: 'nodes', label: '按节点' },
      { value: 'rules', label: '按规则' }
    ],
    // 兼容早期「按业务」取值
    aliases: {
      business: 'rules'
    }
  }
]

export function getPreferenceDefinition(key) {
  return PREFERENCE_DEFINITIONS.find((item) => item.key === key) || null
}

export function normalizeTrafficView(value, fallback = 'nodes') {
  const raw = String(value || '').trim().toLowerCase()
  if (raw === 'rules' || raw === 'business') return 'rules'
  if (raw === 'nodes') return 'nodes'
  return fallback
}
