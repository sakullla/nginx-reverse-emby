import { createApp, defineAsyncComponent } from 'vue'
import { createPinia } from 'pinia'
import { QueryClient, VueQueryPlugin } from '@tanstack/vue-query'
import App from './App.vue'
import router from './router'
import { bindCredentialIdentityBoundary } from './api/identityBoundary'
import './styles/index.css'
import 'virtual:uno.css'

const app = createApp(App)
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30_000
    }
  }
})

bindCredentialIdentityBoundary(queryClient)

app.use(createPinia())
app.use(router)
app.component('apexchart', defineAsyncComponent(async () => {
  await Promise.all([
    import('apexcharts/area'),
    import('apexcharts/donut'),
    import('apexcharts/features/legend')
  ])
  return import('vue3-apexcharts/core')
}))
app.use(VueQueryPlugin, { queryClient })

app.mount('#app')
