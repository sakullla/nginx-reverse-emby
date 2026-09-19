<script setup>
import { onBeforeMount } from 'vue'
import { useRouter } from 'vue-router'

const router = useRouter()
onBeforeMount(() => {
  router.replace({ name: 'dashboard' })
})
</script>

<template>
  <p data-test="access-retired">用户与资源管理已下线</p>
</template>
