---
layout: false
---

<script setup>
import { useRouter } from 'vitepress'
import { onMounted } from 'vue'
const router = useRouter()
onMounted(() => router.go('/guides/agents'))
</script>

<div style="padding: 2rem; font-family: var(--vp-font-family-base);">
  <p>页面已移动到新的地址。正在跳转…</p>
  <p>如果浏览器没有自动跳转，请点击 <a href="/guides/agents">Agent 节点管理</a>。</p>
</div>
