import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { fetchSystemInfo } from '../api'
import { messageStore } from '../stores/messages'

const DEFAULT_TOKEN = 'YOUR_TOKEN'

export function useJoinCommand() {
  const { data: systemInfo, isLoading } = useQuery({
    queryKey: ['system-info'],
    queryFn: fetchSystemInfo,
    staleTime: 60_000,
  })

  const origin = typeof window !== 'undefined' ? window.location.origin : ''

  const commands = computed(() => {
    const token = systemInfo.value?.master_register_token || DEFAULT_TOKEN
    const base = `${origin}/panel-api`
    return {
      linux: `curl -fsSL ${base}/public/join-agent.sh | sh -s -- --register-token ${token} --install-systemd`,
      macos: `curl -fsSL ${base}/public/join-agent.sh | sh -s -- --register-token ${token} --install-launchd`,
      windows: 'Windows 目前请参考 README 手工安装 Go agent 并完成注册',
    }
  })

  async function copyCommand(platform = 'linux') {
    const text = commands.value[platform] || commands.value.linux
    try {
      if (navigator.clipboard && window.isSecureContext) {
        await navigator.clipboard.writeText(text)
      } else {
        const textarea = document.createElement('textarea')
        textarea.value = text
        textarea.style.position = 'fixed'
        textarea.style.left = '-999999px'
        document.body.appendChild(textarea)
        textarea.select()
        const success = document.execCommand('copy')
        document.body.removeChild(textarea)
        if (!success) throw new Error('execCommand failed')
      }
      messageStore.success('已复制到剪贴板')
      return true
    } catch (err) {
      console.error('Copy failed:', err)
      messageStore.error('复制失败，请手动选择复制')
      return false
    }
  }

  return {
    commands,
    copyCommand,
    isLoading,
  }
}
