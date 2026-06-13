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
    ['meta', { name: 'theme-color', content: '#2563eb' }],
    ['link', { rel: 'icon', href: `${base}favicon.ico`, sizes: 'any' }]
  ],
  themeConfig: {
    logo: '/favicon.ico',
    siteTitle: 'Nginx-Reverse-Emby',
    search: {
      provider: 'local',
      options: {
        translations: {
          button: {
            buttonText: '搜索',
            buttonAriaLabel: '搜索文档'
          },
          modal: {
            displayDetails: '显示详细列表',
            resetButtonTitle: '清除搜索',
            backButtonTitle: '关闭搜索',
            noResultsText: '没有找到结果',
            footer: {
              selectText: '选择',
              selectKeyAriaLabel: 'Enter',
              navigateText: '切换',
              navigateUpKeyAriaLabel: '上箭头',
              navigateDownKeyAriaLabel: '下箭头',
              closeText: '关闭',
              closeKeyAriaLabel: 'Escape'
            }
          }
        }
      }
    },
    outline: {
      label: '本页目录'
    },
    docFooter: {
      prev: '上一页',
      next: '下一页'
    },
    darkModeSwitchLabel: '外观',
    lightModeSwitchTitle: '切换到浅色模式',
    darkModeSwitchTitle: '切换到深色模式',
    sidebarMenuLabel: '菜单',
    returnToTopLabel: '返回顶部',
    nav: [
      { text: '快速开始', link: '/guide/quickstart' },
      { text: 'HTTP 规则', link: '/guide/http-rule' },
      { text: 'L4 + Relay', link: '/guide/l4-relay' },
      { text: 'WireGuard', link: '/guide/wireguard' },
      { text: '参考', link: '/reference/environment' },
      { text: '运维', link: '/operations/backup-restore' },
      { text: 'GitHub', link: 'https://github.com/sakullla/nginx-reverse-emby' }
    ],
    sidebar: [
      {
        text: '入门',
        items: [
          { text: '快速开始', link: '/guide/quickstart' },
          { text: '部署', link: '/guide/deploy' },
          { text: 'Agent 接入', link: '/guide/agent' },
          { text: '证书与 HTTPS', link: '/guide/certificates' }
        ]
      },
      {
        text: '规则与路由',
        items: [
          { text: 'HTTP 反向代理', link: '/guide/http-rule' },
          { text: 'L4 规则与 Relay', link: '/guide/l4-relay' },
          { text: 'WireGuard 隧道', link: '/guide/wireguard' }
        ]
      },
      {
        text: '参考',
        items: [
          { text: '架构与特性', link: '/reference/architecture' },
          { text: '环境变量', link: '/reference/environment' },
          { text: 'Relay', link: '/reference/relay' },
          { text: '流量统计与额度', link: '/reference/traffic' },
          { text: '开发与构建', link: '/reference/development' }
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
