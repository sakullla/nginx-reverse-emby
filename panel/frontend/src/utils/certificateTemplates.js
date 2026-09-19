export const SYSTEM_RELAY_CA_TAG = 'system:relay-ca'
export const SYSTEM_RELAY_TUNNEL_TAG = 'system:auto-relay-tunnel'

export const CERTIFICATE_TEMPLATES = [
  {
    id: 'https',
    label: '网站 HTTPS',
    description: '面向普通站点，优先走自动签发',
    defaults: {
      scope: 'domain',
      issuer_mode: 'master_cf_dns',
      usage: 'https',
      certificate_type: 'acme',
      self_signed: false
    }
  },
  {
    id: 'uploaded',
    label: '手动上传证书',
    description: '直接粘贴 PEM 证书、私钥与可选 CA 链',
    defaults: {
      scope: 'domain',
      issuer_mode: 'local_http01',
      usage: 'https',
      certificate_type: 'uploaded',
      self_signed: false
    }
  }
]

function getCertificateTags(certificate) {
  return Array.isArray(certificate?.tags) ? certificate.tags : []
}

export function isSystemRelayCA(certificate) {
  return getCertificateTags(certificate).includes(SYSTEM_RELAY_CA_TAG)
}

export function isSystemManagedRelayListenerCertificate(certificate) {
  return (
    certificate?.usage === 'relay_tunnel' &&
    certificate?.certificate_type === 'internal_ca' &&
    getCertificateTags(certificate).includes(SYSTEM_RELAY_TUNNEL_TAG)
  )
}

export function inferCertificateTemplate(certificate) {
  if (!certificate) return 'https'
  if (isSystemRelayCA(certificate)) {
    return ''
  }
  if (certificate.certificate_type === 'internal_ca' && certificate.usage === 'relay_tunnel') {
    return ''
  }
  if (certificate.certificate_type === 'uploaded') {
    return 'uploaded'
  }
  if (certificate.certificate_type === 'internal_ca' && certificate.self_signed) {
    return ''
  }
  return 'https'
}

export function applyCertificateTemplate(form, templateId) {
  const template = CERTIFICATE_TEMPLATES.find((item) => item.id === templateId) || CERTIFICATE_TEMPLATES[0]
  return {
    ...form,
    ...template.defaults
  }
}

export function getCertificateUsageLabel(usage) {
  if (usage === 'https') return '网站 HTTPS'
  if (usage === 'relay_tunnel') return 'Relay 监听'
  if (usage === 'relay_ca') return 'Relay CA'
  if (usage === 'mixed') return '混合用途'
  return usage || '未设置'
}

export function getCertificateSourceLabel(certificateType) {
  if (certificateType === 'acme') return '自动签发'
  if (certificateType === 'uploaded') return '手动上传'
  if (certificateType === 'internal_ca') return '内部自签'
  return certificateType || '未知来源'
}
