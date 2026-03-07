# Nginx Reverse Emby Panel - Vue 3 Frontend

## 技术栈

- **Vue 3** - 使用 Composition API
- **Vite** - 快速构建工具
- **Pinia** - 状态管理
- **Axios** - HTTP 客户端

## 项目结构

```
panel/frontend/
├── src/
│   ├── api/
│   │   └── index.js          # API 请求封装
│   ├── components/
│   │   ├── ActionBar.vue     # 操作按钮栏
│   │   ├── RuleForm.vue      # 新增规则表单
│   │   ├── RuleItem.vue      # 单条规则行
│   │   ├── RuleList.vue      # 规则列表
│   │   ├── StatusMessage.vue # 状态消息提示
│   │   └── TokenConfig.vue   # Token 配置
│   ├── stores/
│   │   └── rules.js          # Pinia store
│   ├── App.vue               # 根组件
│   ├── main.js               # 入口文件
│   └── style.css             # 全局样式
├── index.html
├── package.json
└── vite.config.js
```

## 本地开发

```bash
cd panel/frontend
npm install
npm run dev
```

开发服务器会在 http://localhost:5173 启动，API 请求会自动代理到 `http://localhost:18081`。

## 构建

```bash
npm run build
```

构建产物会输出到 `dist/` 目录。

## Docker 构建

Dockerfile 已配置多阶段构建：
1. 使用 Node.js 镜像构建 Vue 前端
2. 将构建产物复制到 Nginx 镜像

```bash
docker build -t nginx-reverse-emby .
```

## 功能特性

- 响应式设计，支持移动端
- 实时状态反馈
- 自动保存 API Token 到 localStorage
- 支持规则的增删改查
- 手动应用 Nginx 配置
- 优雅的错误处理
- 加载状态提示

## API 集成

前端通过 `/panel-api/*` 路径访问后端 API，Nginx 会将请求代理到 `http://127.0.0.1:${PANEL_BACKEND_PORT}/api/*`。

支持的环境变量：
- `PANEL_PORT` - 面板端口（默认 8080）
- `PANEL_BACKEND_PORT` - 后端 API 端口（默认 18081）
- `PANEL_API_TOKEN` - API 认证 Token（可选）
