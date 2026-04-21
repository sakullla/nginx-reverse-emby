# 系统设置 UI 重新设计 — 设计文档

## 目标

对 `panel/frontend/src/pages/SettingsPage.vue` 及其子组件进行视觉风格和排版优化，提升信息层级、交互质感与现代感，不改动功能逻辑与数据流。

## 范围

- `SettingsPage.vue` — 整体布局与容器样式
- `SettingsNav.vue` — 左侧导航栏视觉升级
- `SettingsGeneral.vue` — 通用设置卡片优化
- `SettingsDataMgmt.vue` — 导出导入重新设计
- `SettingsAbout.vue` — 关于页面优化 + 固定项目地址

不涉及 API 改动、路由改动、新增功能逻辑。

---

## 1. 整体布局（SettingsPage.vue）

### 改动点

- 外层 `.settings-layout`：
  - 边框从 `1.5px solid var(--color-border-default)` 改为 `1px solid var(--color-border-subtle)`
  - 增加 `background: var(--color-bg-subtle)` 作为底色
  - 圆角保持 `var(--radius-2xl)`
- `.settings-content`：
  - 内边距从 `1.5rem 2rem` 微调为 `1.5rem 2rem 2rem`，底部留白更多
- 页面标题区：
  - 增加描述文字 "管理面板偏好与系统信息"
  - 标题与内容之间增加 `1px solid var(--color-border-subtle)` 分隔线
- 响应式：
  - 去掉 2560px 超大屏特殊断点，简化响应式逻辑

---

## 2. 导航栏（SettingsNav.vue）

### 改动点

- 导航项 padding 从 `0.6rem 1.25rem` 调整为 `0.55rem 1rem 0.55rem 0.875rem`
- 激活态：
  - 去掉左侧 `border-left` 竖条
  - 背景改为 `var(--color-primary-subtle)`，圆角 8px
  - 文字色 `var(--color-primary)`，font-weight 600
  - 右侧增加 3px 圆角色条（`border-radius: 4px 0 0 4px`，位于左侧）
- hover 态：背景 `var(--color-bg-subtle)`，圆角 8px
- 顶部 "设置" label：letter-spacing 从 0.05em 减到 0.03em
- 移动端：
  - 横向滚动，底部边框 1px 分隔线
  - 激活态从底部 2px 横条改为底部圆角胶囊

---

## 3. 通用设置（SettingsGeneral.vue）

### 外观主题 Section

- 主题按钮从横排小按钮改为**圆角卡片网格**
- 每个主题卡片包含：
  - 顶部一小条该主题色带预览
  - 下方文字标签居中
- 卡片尺寸：`padding: 0.75rem`，`gap: 0.75rem`
- 激活态：边框变 `var(--color-primary)`，背景 `var(--color-primary-subtle)`，带微妙阴影
- 选中指示：卡片内部底部居中的细线

### 部署模式 Section

- 每条 info-row 左侧加小图标：
  - 角色 → 🖥️
  - 本地 Agent → ⚡
- "已启用"状态用绿色圆点 + 文字组合
- 行间距 `padding: 0.6rem 0`
- 描述文字改为 "查看当前系统的运行角色与本地 Agent 状态"

---

## 4. 数据管理 — 导出区（SettingsDataMgmt.vue）

### 资源选择区

- 顶部增加快捷操作栏：
  - 左侧 "全选 / 取消全选" 文字按钮
  - 右侧 "已选 X / 共 6 项" 计数
- 资源卡片 2 列网格（移动端 1 列），每张卡片包含：
  - 左上角资源图标：
    - 节点 → 🖥️
    - HTTP 规则 → 🌐
    - L4 规则 → 🔌
    - 中继监听 → 📡
    - 证书 → 🔒
    - 版本策略 → 📋
  - 中央资源名称
  - 右上角数量徽章（圆形小徽章）
- 选中态：边框变 `var(--color-primary)`，背景 `var(--color-primary-subtle)`，右上角白色勾选
- hover 态：`translateY(-2px)` + 轻微阴影
- 卡片整体可点击

### 导出按钮

- 右对齐
- 文字改为 "导出选中备份"
- 按钮前加下载图标
- disabled 时显示提示 "请至少选择一项"

---

## 5. 数据管理 — 导入区（SettingsDataMgmt.vue）

### Stepper 步进器

- 三步横向等分排列：选择文件 → 预览确认 → 导入结果
- 每步包含：序号圆圈 + 步骤名称
- 当前步骤：圆圈 `var(--color-primary)` 填充 + 白字
- 已完成：圆圈 `var(--color-primary-subtle)` 填充 + `var(--color-primary)` 字 + 勾选图标
- 未到达：灰色边框 + 灰色文字
- 步骤间带箭头细线连接

### 第一步：选择文件

- 改为**拖拽上传区域**：
  - 虚线边框矩形，内有上传图标 + "点击或拖拽备份文件到此处"
  - 支持 `.tar.gz, .tgz`
  - 拖拽时边框变主题色实线，背景变色
  - 已选文件后变为文件信息卡片（文件名 + 大小 + 删除按钮）
- "预览导入"按钮在文件选择后自动高亮可用

### 第二步：预览确认

- 来源架构、导出时间改为**并排信息卡片**
- 预览表格改为卡片列表：
  - 每行 = 资源图标 + 名称 + 数量 | 状态文字（颜色区分：新增绿色、跳过灰色）
- 底部操作按钮固定："取消"左、"确认导入"右

### 第三步：导入结果

- 四个 summary 卡片用 2x2 网格：
  - 已导入、冲突跳过、无效跳过、缺少证书跳过
  - 数字更大更醒目
- 导入报告默认折叠，点击展开
- "完成"按钮居中

---

## 6. 关于页面（SettingsAbout.vue）

### 顶部品牌区

- 标题下方增加渐变色分隔线：`linear-gradient(90deg, transparent, var(--color-primary), transparent)`
  - 宽度 80px，高度 3px，居中
- tagline 字号加大到 0.9rem，颜色改为 `var(--color-text-secondary)`

### 版本信息 Section

- 每行左侧加图标：
  - 当前版本 → 🏷️
  - 构建时间 → 🕐
  - 架构 → 🏗️
  - Go 版本 → `</>`
- 版本号用 `monospace` + `var(--color-primary)`

### 项目地址 Section — 关键改动

- **去掉 `v-if="info?.project_url"` 条件渲染，始终显示**
- 改为**外链卡片**：
  - GitHub 链接：带 GitHub 图标的外部链接按钮样式
  - Issues 反馈：带 🐛 图标
  - hover 时轻微位移 + 阴影
- 卡片背景 `var(--color-bg-subtle)` + 圆角边框
- **项目 URL 硬编码为 `https://github.com/sakullla/nginx-reverse-emby`**

### 系统状态 Section

- 每行左侧加图标：
  - 角色 → 🖥️
  - 本地 Agent → ⚡（已启用时绿色圆点 pulse 动画）
  - 在线节点 → 🌐
  - 运行时长 → ⏱️
  - 数据目录 → 📁
- 在线节点显示格式：`3 在线 / 5 总计`
- 数据目录用 `monospace` + 0.8rem

---

## 7. 全局共享样式

### Section 卡片统一

```css
.settings-section {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-xl);
  overflow: hidden;
  transition: box-shadow 0.2s var(--ease-default);
}
.settings-section:hover {
  box-shadow: 0 1px 4px color-mix(in srgb, var(--color-border-default) 30%, transparent);
}
```

### Section Header 统一

```css
.settings-section__header {
  padding: 1rem 1.25rem;
  border-bottom: 1px solid var(--color-border-subtle);
  display: flex;
  align-items: center;
  justify-content: space-between;
}
```

### 按钮统一

- `border-radius: var(--radius-lg)`
- `display: inline-flex; align-items: center; gap: 0.4rem;`
- 次按钮 hover 时边框变 `var(--color-primary)`

### Info Row 统一

```css
.info-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0.6rem 0;
  border-bottom: 1px solid var(--color-border-subtle);
}
.info-row:last-child { border-bottom: none; }
```

### 响应式断点

- 768px 以下：移动端
- 768px-1279px：平板
- 1280px 以上：桌面

---

## 不做的（明确排除）

- 不改动 API 调用方式
- 不改动数据模型或状态管理
- 不新增设置项或功能
- 不改动路由结构
- 不改动导入导出的业务逻辑（仅改 UI 表现）
