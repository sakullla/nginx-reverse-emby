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
      { text: '新手教程', link: '/guide/getting-started' },
      { text: 'HTTP 规则', link: '/guide/http-rule' },
      { text: 'L4 + Relay', link: '/guide/l4-relay' },
      { text: '参考', link: '/reference/environment' },
      { text: '运维', link: '/operations/backup-restore' },
      { text: 'GitHub', link: 'https://github.com/sakullla/nginx-reverse-emby' }
    ],
    sidebar: [
      {
        text: '新手教程',
        items: [
          { text: '从 0 到 HTTP 代理', link: '/guide/getting-started' },
          { text: 'Docker Compose 部署', link: '/guide/docker-compose' },
          { text: '添加 HTTP 规则', link: '/guide/http-rule' },
          { text: 'L4 + Relay 从 0 到可用', link: '/guide/l4-relay' },
          { text: 'Agent 接入', link: '/guide/agent' }
        ]
      },
      {
        text: '参考',
        items: [
          { text: '架构与特性', link: '/reference/architecture' },
          { text: '环境变量', link: '/reference/environment' },
          { text: '证书管理', link: '/reference/certificates' },
          { text: 'Relay', link: '/reference/relay' },
          { text: 'WireGuard', link: '/reference/wireguard' },
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
