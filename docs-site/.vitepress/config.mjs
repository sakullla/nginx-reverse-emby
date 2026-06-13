import { defineConfig } from 'vitepress'

const base = process.env.VITEPRESS_BASE || '/nginx-reverse-emby/'

export default defineConfig({
  title: 'Nginx-Reverse-Emby',
  description: '面向 Emby、Jellyfin、HTTP、L4、Relay 与 WireGuard 的纯 Go 反向代理控制面。',
  base,
  lang: 'zh-CN',
  cleanUrls: true,
  lastUpdated: true,
  head: [
    ['meta', { name: 'theme-color', content: '#2563eb' }]
  ],
  themeConfig: {
    logo: '/logo.svg',
    siteTitle: 'Nginx-Reverse-Emby',
    search: {
      provider: 'local'
    },
    nav: [
      { text: '指南', link: '/guide/getting-started' },
      { text: '参考', link: '/reference/environment' },
      { text: '运维', link: '/operations/backup-restore' },
      { text: 'GitHub', link: 'https://github.com/sakullla/nginx-reverse-emby' }
    ],
    sidebar: [
      {
        text: '指南',
        items: [
          { text: '快速开始', link: '/guide/getting-started' },
          { text: 'Docker Compose', link: '/guide/docker-compose' },
          { text: 'Agent 接入', link: '/guide/agent' }
        ]
      },
      {
        text: '参考',
        items: [
          { text: '环境变量', link: '/reference/environment' },
          { text: '证书管理', link: '/reference/certificates' },
          { text: 'Relay', link: '/reference/relay' },
          { text: 'WireGuard', link: '/reference/wireguard' }
        ]
      },
      {
        text: '运维',
        items: [
          { text: '备份与恢复', link: '/operations/backup-restore' },
          { text: '迁移', link: '/operations/migration' },
          { text: '常见问题', link: '/operations/faq' }
        ]
      }
    ],
    socialLinks: [
      { icon: 'github', link: 'https://github.com/sakullla/nginx-reverse-emby' }
    ],
    footer: {
      message: '基于 GNU General Public License v3.0 发布。',
      copyright: 'Copyright © Nginx-Reverse-Emby contributors'
    }
  },
  markdown: {
    lineNumbers: true
  }
})
