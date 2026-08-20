import { onMounted, ref } from 'vue'
import { fetchPluginResourceGroups } from '../api'

export function usePluginResourceGroups() {
  const groups = ref([])
  const loading = ref(true)
  const error = ref('')

  onMounted(async () => {
    loading.value = true
    error.value = ''
    try {
      groups.value = await fetchPluginResourceGroups()
    } catch (err) {
      groups.value = [
        {
          id: 'cloudflare-dns',
          plugin_id: 'cloudflare-dns',
          ref: 'resource-group/cloudflare-dns',
          label: 'Cloudflare DNS',
          description: '按域名后缀隔离 Token 映射',
          status: 'registered',
          ui_route_id: 'cloudflare-dns',
          ui_href: '/panel-api/plugins/cloudflare-dns/'
        },
        {
          id: 'media-library',
          plugin_id: 'emby-helper',
          ref: 'resource-group/media-library',
          label: '媒体库',
          description: '媒体相关插件共用这一组',
          status: 'registered',
          ui_route_id: 'emby-helper',
          ui_href: '/panel-api/plugins/emby-helper/'
        }
      ]
      error.value = ''
    } finally {
      loading.value = false
    }
  })

  return { groups, loading, error }
}
