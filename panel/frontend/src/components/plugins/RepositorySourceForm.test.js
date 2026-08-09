import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import RepositorySourceForm from './RepositorySourceForm.vue'

describe('RepositorySourceForm', () => {
  it('creates a plugin source pinned to a tag with optional credential references', async () => {
    const wrapper = mount(RepositorySourceForm)

    await wrapper.get('#repository-id').setValue('team-waf')
    await wrapper.get('#repository-name').setValue('Team WAF')
    await wrapper.get('#repository-url').setValue('https://git.example.com/team/waf.git')
    await wrapper.find('[data-field="purpose"] button:nth-child(2)').trigger('click')
    await wrapper.find('[data-field="ref-kind"] button:nth-child(2)').trigger('click')
    await wrapper.get('#repository-ref').setValue('v1.4.0')
    await wrapper.get('#repository-credential').setValue('secret://git/team')
    await wrapper.get('#repository-signer-key').setValue('release-key')
    await wrapper.get('#repository-signer-secret').setValue('secret://signing/team')
    await wrapper.get('#repository-refresh').setValue('30m')
    await wrapper.get('form').trigger('submit')

    expect(wrapper.emitted('save')).toEqual([[
      {
        id: 'team-waf',
        name: 'Team WAF',
        url: 'https://git.example.com/team/waf.git',
        purpose: 'plugin',
        ref_kind: 'tag',
        ref_name: 'v1.4.0',
        credential_ref: 'secret://git/team',
        signer_key_id: 'release-key',
        signer_secret_ref: 'secret://signing/team',
        refresh_interval: '30m'
      }
    ]])
  })

  it('never hydrates write-only credentials and omits them when an edit leaves them blank', async () => {
    const source = {
      id: 'market',
      name: 'Market',
      url: 'https://git.example.com/market.git',
      purpose: 'market',
      ref_kind: 'branch',
      ref_name: 'main',
      credential_configured: true,
      signer_key_id: 'market-key',
      signer_fingerprint: 'SHA256:abc',
      refresh_interval_ns: 3600000000000,
      current_resolved_oid: 'a'.repeat(40),
      config_revision: 7
    }
    const wrapper = mount(RepositorySourceForm, { props: { source } })

    expect(wrapper.get('#repository-credential').element.value).toBe('')
    expect(wrapper.get('#repository-signer-secret').element.value).toBe('')
    await wrapper.get('form').trigger('submit')

    const payload = wrapper.emitted('save')[0][0]
    expect(payload).not.toHaveProperty('id')
    expect(payload).not.toHaveProperty('credential_ref')
    expect(payload).not.toHaveProperty('signer_key_id')
    expect(payload).not.toHaveProperty('signer_secret_ref')
    expect(payload).not.toHaveProperty('current_resolved_oid')
    expect(payload.config_revision).toBe(7)
    expect(payload.refresh_interval).toBe('1h')
  })

  it('uses an explicit checkbox to clear the configured Git credential', async () => {
    const wrapper = mount(RepositorySourceForm, {
      props: {
        source: {
          id: 'market',
          name: 'Market',
          url: 'https://git.example.com/market.git',
          purpose: 'market',
          ref_kind: 'branch',
          ref_name: 'main',
          credential_configured: true,
          signer_key_id: 'market-key',
          signer_fingerprint: 'SHA256:abc',
          config_revision: 4
        }
      }
    })

    const checks = wrapper.findAll('input[type="checkbox"]')
    await checks[0].setValue(true)
    await wrapper.get('form').trigger('submit')

    expect(wrapper.emitted('save')[0][0]).toMatchObject({
      credential_ref: ''
    })
    expect(wrapper.emitted('save')[0][0]).not.toHaveProperty('signer_secret_ref')
  })

  it('requires the write-only signer reference when changing the signer key', async () => {
    const wrapper = mount(RepositorySourceForm, {
      props: {
        source: {
          id: 'market',
          name: 'Market',
          url: 'https://git.example.com/market.git',
          purpose: 'market',
          ref_kind: 'branch',
          ref_name: 'main',
          signer_key_id: 'old-key',
          signer_fingerprint: 'SHA256:abc',
          config_revision: 4
        }
      }
    })

    await wrapper.get('#repository-signer-key').setValue('new-key')
    await wrapper.get('form').trigger('submit')

    expect(wrapper.emitted('save')).toBeUndefined()
    expect(wrapper.text()).toContain('更换签名密钥 ID 时必须同时提供签名密钥引用')
  })

  it('requires signer identity and its write-only reference for a new source', async () => {
    const wrapper = mount(RepositorySourceForm)
    await wrapper.get('#repository-id').setValue('team-market')
    await wrapper.get('#repository-name').setValue('Team Market')
    await wrapper.get('#repository-url').setValue('https://git.example.com/team/market.git')
    await wrapper.get('form').trigger('submit')

    expect(wrapper.emitted('save')).toBeUndefined()
    expect(wrapper.text()).toContain('请输入签名密钥 ID 和签名密钥引用')
  })

  it('fails closed when an edit source has no observed config revision', async () => {
    const wrapper = mount(RepositorySourceForm, {
      props: {
        source: {
          id: 'market',
          name: 'Market',
          url: 'https://git.example.com/market.git',
          purpose: 'market',
          ref_kind: 'branch',
          ref_name: 'main',
          signer_key_id: 'market-key'
        }
      }
    })

    await wrapper.get('form').trigger('submit')

    expect(wrapper.emitted('save')).toBeUndefined()
    expect(wrapper.text()).toContain('仓库源版本信息无效')
  })
})
