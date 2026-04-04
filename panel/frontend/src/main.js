import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { VueQueryPlugin } from '@tanstack/vue-query'
import App from './App.vue'
import router from './router'
import './styles/index.css'
import 'virtual:uno.css'

const app = createApp(App)

app.use(createPinia())
app.use(router)
app.use(VueQueryPlugin, {
  queryClientConfig: {
    defaultOptions: {
      queries: {
        staleTime: 30_000,      // 30s before re-fetching
        refetchInterval: 10_000  // poll every 10s for agents
      }
    }
  }
})

app.mount('#app')
