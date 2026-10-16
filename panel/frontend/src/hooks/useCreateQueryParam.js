import { watch } from 'vue'

/**
 * 让 URL 直接打开创建表单：/rules?create=1、/l4?create=1。
 * 消费一次后移除临时参数，避免刷新或后退时重复弹出。
 */
export function useCreateQueryParam(route, router, openCreate) {
  watch(
    () => route.query.create,
    (value) => {
      if (!value) return
      openCreate()
      const { create, ...rest } = route.query
      router.replace({ query: rest })
    },
    { immediate: true }
  )
}
