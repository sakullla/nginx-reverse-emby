import { useQuery } from '@tanstack/vue-query'
import * as api from '../api'

// 系统信息统一经 vue-query 缓存获取，设置页内多处消费只发 1 次 /info 请求。
// 参照 hooks/useEgressProfiles.js 模式。
export function useSystemInfo() {
  return useQuery({
    queryKey: ['systemInfo'],
    queryFn: () => api.fetchSystemInfo()
  })
}
