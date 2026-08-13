# Execution Plan

4 页重构与动态表单升级拆为 7 个 Task：后端词表先行（无版本号提升）、前端声明式渲染器与合成路径分层、4 页各自独立重构，仅配置详情页依赖表单稳定后的组件切换。R5/R6 为不做事项，由排除覆盖，无 Task Recipe。

```yaml
format: execution_plan
tasks:
  - id: backend-vocab
    goal: "扩展声明式 UI 词表：新增 array 组件与 visibleWhen JSON 谓词，schema_version 保持 1"
    depends_on: []
    covers: [R3, R4]
    scope:
      - panel/backend-go/internal/controlplane/plugins/types.go
      - panel/backend-go/internal/controlplane/plugins/declarative_ui.go
      - panel/backend-go/internal/controlplane/plugins/declarative_ui_test.go
    outcomes:
      - array 组件与 visibleWhen 谓词（op 白名单、field 绑定、value 类型一致）纳入严格解码/预算/绑定校验
      - DeclarativeUISchemaVersion 仍为 1，schema_version=2 拒绝用例保持有效
      - 非法谓词/越界绑定/超预算被拒绝，现有测试全绿
    verify:
      - cd panel/backend-go && go test ./internal/controlplane/plugins/...
    test: extend
  - id: frontend-renderer
    goal: "声明式渲染器支持嵌套对象/数组增删重排、JSON 谓词条件显示与约束校验提示"
    depends_on: []
    covers: [R2, R3]
    scope:
      - panel/frontend/src/components/plugins/PluginDeclarativeUI.vue
      - panel/frontend/src/components/plugins/PluginDeclarativeComponent.vue
      - panel/frontend/src/api/pluginCondition.js
      - panel/frontend/src/components/plugins/PluginDeclarativeUI.test.js
    outcomes:
      - 数组可重复分组新增/删除/重排，条件为假字段隐藏且不参与提交
      - 叶子字段渲染约束提示并给出行内校验错误，仅宿主内置组件渲染
      - 秘密字段嵌套 JSON pointer 留空保留/清除/轮换语义不变
    verify:
      - cd panel/frontend && npx vitest run src/components/plugins/PluginDeclarativeUI.test.js
    test: extend
  - id: frontend-synthesis
    goal: "无 ui_schema 时从 config_schema 递归合成组件树，取代扁平 normalizePluginConfigSchema"
    depends_on: [frontend-renderer]
    covers: [R2]
    scope:
      - panel/frontend/src/api/pluginSecurity.js
      - panel/frontend/src/api/pluginSecurity.test.mjs
      - panel/frontend/src/components/plugins/PluginConfigForm.vue
      - panel/frontend/src/components/plugins/PluginConfigForm.test.js
    outcomes:
      - schemaToUIComponents 递归覆盖嵌套对象/数组并保留约束关键字与秘密标记
      - 扁平 normalizePluginConfigSchema 语义被取代，PluginConfigForm 走合成路径，秘密提交语义不变
      - 任意合法 config_schema（含嵌套/数组）可完整编辑保存
    verify:
      - cd panel/frontend && npx vitest run src/api/pluginSecurity.test.mjs src/components/plugins/PluginConfigForm.test.js
    test: extend
  - id: page-installed
    goal: "已安装列表页统一到共享骨架：页头、筛选、空错态与卡片组件"
    depends_on: []
    covers: [R1]
    scope:
      - panel/frontend/src/pages/plugins/PluginsPage.vue
      - panel/frontend/src/pages/plugins/PluginsPage.test.js
    outcomes:
      - page-header 标题/副标题，ResourceListFilterBar 筛选/搜索
      - 加载/错误/空态用 spinner/EmptyState，卡片复用 BaseListCard/BaseCard
      - 桌面/移动视口无布局回归
    verify:
      - cd panel/frontend && npx vitest run src/pages/plugins/PluginsPage.test.js
    test: extend
  - id: page-detail
    goal: "配置详情页统一到共享骨架：BaseTabs 实例切换、统一确认与按钮层级"
    depends_on: [frontend-renderer, frontend-synthesis]
    covers: [R1]
    scope:
      - panel/frontend/src/pages/plugins/PluginDetailPage.vue
      - panel/frontend/src/pages/plugins/PluginDetailPage.test.js
      - panel/frontend/src/pages/plugins/PluginDetailPage.integration.test.js
    outcomes:
      - 原生 select 替换为 BaseTabs，主操作/危险操作按钮层级明确
      - window.confirm 替换为 DeleteConfirmDialog，导航与状态统一
      - 动态表单切换为单一渲染器，提交载荷/秘密语义保持
    verify:
      - cd panel/frontend && npx vitest run src/pages/plugins/PluginDetailPage.test.js src/pages/plugins/PluginDetailPage.integration.test.js
    test: extend
  - id: page-market
    goal: "市场页统一到共享骨架：安装/升级确认、加载错空态与导航"
    depends_on: []
    covers: [R1]
    scope:
      - panel/frontend/src/pages/plugins/PluginMarketplacePage.vue
      - panel/frontend/src/pages/plugins/PluginMarketplacePage.test.js
    outcomes:
      - 行内 checkbox 门控替换为统一确认组件
      - 加载/错误/空态统一，补返回导航
      - 桌面/移动视口无布局回归
    verify:
      - cd panel/frontend && npx vitest run src/pages/plugins/PluginMarketplacePage.test.js
    test: extend
  - id: page-repositories
    goal: "仓库源页统一到共享骨架：统一确认弹窗与错误通道"
    depends_on: []
    covers: [R1]
    scope:
      - panel/frontend/src/pages/plugins/PluginRepositoriesPage.vue
      - panel/frontend/src/pages/plugins/PluginRepositoriesPage.test.js
    outcomes:
      - 手写 role=alertdialog 替换为统一确认组件
      - loadError/actionError/contentsError 多通道收敛为统一状态
      - 桌面/移动视口无布局回归
    verify:
      - cd panel/frontend && npx vitest run src/pages/plugins/PluginRepositoriesPage.test.js
    test: extend
delivery_verification:
  backend-go-test:
    command: "cd panel/backend-go; go test ./..."
  frontend-vitest:
    command: "cd panel/frontend; npx vitest run"
  frontend-build:
    command: "cd panel/frontend; npm run build -- --logLevel error"
```
