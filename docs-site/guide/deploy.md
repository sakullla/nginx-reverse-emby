---
layout: false
---

<script setup>
import { useRouter, withBase } from 'vitepress'
import { onMounted } from 'vue'
const router = useRouter()
onMounted(() => router.go(withBase('/getting-started/deploy')))
</script>

<div style="padding: 2rem; font-family: var(--vp-font-family-base);">
  <p>页面已移动。正在跳转…</p>
  <p>如果没有自动跳转，请点击 <a :href="withBase('/getting-started/deploy')">部署指南</a>。</p>
</div>
