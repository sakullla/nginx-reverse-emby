import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import UnoCSS from 'unocss/vite'

export default defineConfig({
  plugins: [
    vue(),
    UnoCSS()
  ],
  test: {
    environment: 'jsdom',
    pool: 'vmThreads',
    setupFiles: ['./src/test/setup.js'],
    exclude: [
      'src/components/l4/proxyEntryAuth.test.js',
      'src/components/traffic/trafficTrendHelpers.test.js',
      'src/context/agentSelection.test.mjs',
      'src/hooks/__tests__/useIdSearch.test.js',
      'src/pages/outboundProxyURL.test.js',
      'src/utils/agentFilter.test.js',
      'src/utils/agentMetrics.test.js',
      'src/utils/resolveResourceAgent.test.js',
      'src/utils/resourceCardStatus.test.js',
      'src/components/egress/EgressProfileForm.test.js',
      'src/components/traffic/TrafficHistoryManager.test.js',
      'src/components/traffic/TrafficPolicyForm.test.js',
      'src/components/traffic/TrafficTrendChart.test.js',
      'src/components/RuleDiagnosticModal.test.js',
      'src/context/ThemeContext.test.mjs',
      'src/utils/__tests__/scrollHighlight.test.js'
    ],
    include: [
      'src/components/**/*.test.js',
      'src/pages/**/*.test.js',
      'src/api/**/*.test.mjs',
      'src/context/**/*.test.mjs',
      'src/hooks/**/*.{test,spec}.{js,mjs}',
      'src/stores/**/*.test.js',
      'src/utils/**/*.{test,spec}.{js,mjs}'
    ]
  },
  build: {
    outDir: 'dist',
    assetsDir: 'assets',
    emptyOutDir: true,
    chunkSizeWarningLimit: 550,
    rollupOptions: {
      output: {
        manualChunks(id) {
          const normalizedId = id.replace(/\\/g, '/')
          if (!normalizedId.includes('node_modules')) return undefined
          // Only isolate large, leaf-ish deps. Do not force axios or a catch-all
          // "vendor" chunk: Rollup may place shared helpers into src/api/client.js
          // while axios lives elsewhere, producing a circular graph that breaks
          // init with "e is not a function" / "Cannot read properties of
          // undefined (reading 'create')" before login.
          if (normalizedId.includes('node_modules/apexcharts/')) return 'apexcharts'
          if (normalizedId.includes('node_modules/vue3-apexcharts/')) return 'vue3-apexcharts'
          if (normalizedId.includes('node_modules/@tanstack/')) return 'query'
          if (normalizedId.includes('node_modules/vue-router/') || normalizedId.includes('node_modules/pinia/')) {
            return 'routing-state'
          }
          return undefined
        }
      }
    }
  },
  server: {
    port: 5173,
    proxy: {
      '/panel-api': {
        target: 'http://localhost:18081',
        changeOrigin: true,
        rewrite: (path) => path.replace(/^\/panel-api/, '/api')
      }
    }
  }
})
