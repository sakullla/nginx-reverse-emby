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
      groups.value = []
      error.value = err?.message || '无法加载资源组'
    } finally {
      loading.value = false
    }
  })

  return { groups, loading, error }
}
