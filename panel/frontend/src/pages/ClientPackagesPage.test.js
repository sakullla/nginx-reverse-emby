import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { ref } from 'vue'
import ClientPackagesPage from './ClientPackagesPage.vue'

const packages = [
  {
    id: 'flutter_gui-windows-amd64-1-1-0',
    version: '1.1.0',
    platform: 'windows',
    arch: 'amd64',
    kind: 'flutter_gui',
    download_url: 'https://github.com/sakullla/nginx-reverse-emby/releases/download/v1.1.0/nre-client-windows-amd64.zip',
    sha256: 'a'.repeat(64),
    notes: 'Windows Flutter GUI',
    created_at: '2026-04-26T00:00:00Z'
  },
  {
    id: 'worker_script-cloudflare_worker-script-1-1-0',
    version: '1.1.0',
    platform: 'cloudflare_worker',
    arch: 'script',
    kind: 'worker_script',
    download_url: 'https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/v1.1.0/workers/cloudflare/nre-worker.js',
    sha256: 'b'.repeat(64),
    notes: 'Cloudflare Worker script',
    created_at: '2026-04-26T00:01:00Z'
  },
  {
    id: 'worker_script-cloudflare_worker-script-1-1-0-beta-1',
    version: '1.1.0-beta.1',
    platform: 'cloudflare_worker',
    arch: 'script',
    kind: 'worker_script',
    download_url: 'https://raw.githubusercontent.com/sakullla/nginx-reverse-emby/v1.1.0-beta.1/workers/cloudflare/nre-worker.js',
    sha256: 'c'.repeat(64),
    notes: 'Cloudflare Worker prerelease script',
    created_at: '2026-04-26T00:02:00Z'
  }
]

vi.mock('../hooks/useClientPackages', () => ({
  useClientPackages: () => ({ data: ref(packages), isLoading: ref(false) }),
  useCreateClientPackage: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
  useUpdateClientPackage: () => ({ mutateAsync: vi.fn(), isPending: ref(false) }),
  useDeleteClientPackage: () => ({ mutate: vi.fn(), isPending: ref(false) })
}))

function mountPage() {
  return mount(ClientPackagesPage, {
    global: {
      stubs: {
        DeleteConfirmDialog: true,
        Teleport: true
      }
    }
  })
}

describe('ClientPackagesPage', () => {
  it('renders GitHub-distributed client package records', () => {
    const wrapper = mountPage()

    expect(wrapper.text()).toContain('客户端发布包')
    expect(wrapper.text()).toContain('Windows Flutter GUI')
    expect(wrapper.text()).toContain('windows / amd64')
    expect(wrapper.text()).toContain('cloudflare_worker / script')
    expect(wrapper.find('a[href*="nre-client-windows-amd64.zip"]').exists()).toBe(true)
  })

  it('builds a Cloudflare Worker deploy command from a Worker script package', async () => {
    const wrapper = mountPage()

    await wrapper.get('input[name="worker-name"]').setValue('nre-edge')
    await wrapper.get('input[name="worker-master-url"]').setValue('https://panel.example.com/')
    await wrapper.get('input[name="worker-token"]').setValue('worker-secret')
    await wrapper.get('button[data-testid="build-worker-command"]').trigger('click')

    expect(wrapper.text()).toContain('wrangler deploy --name nre-edge')
    expect(wrapper.text()).toContain('NRE_MASTER_URL=https://panel.example.com')
    expect(wrapper.text()).toContain('NRE_WORKER_TOKEN=worker-secret')
    expect(wrapper.text()).toContain('b'.repeat(64))
    expect(wrapper.text()).not.toContain('c'.repeat(64))
  })
})
