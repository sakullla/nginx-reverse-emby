import { onMounted, ref } from 'vue'
import { fetchPluginUIRoutes } from '../api'

export function usePluginUIRoutes() {
  const routes = ref([])

  onMounted(async () => {
    try {
      routes.value = await fetchPluginUIRoutes()
    } catch {
      routes.value = []
    }
  })

  return { routes }
}

export function pluginChildrenForGroup(routes, group) {
  return (routes || [])
    .filter((route) => route.group === group)
    .map((route) => ({
      label: route.label,
      href: route.href,
      id: route.id
    }))
}
