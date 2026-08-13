# Task frontend-synthesis

## Attempt History

```yaml
format: task_attempt_history
task_id: frontend-synthesis
history_ref: evidence/history/sha256-16a757fd63fccd544b620d2780f01d289ed75c3d87fd9197ebb7fc9c79cccc29.json
history_count: 1
```

## Execution

```yaml
format: task_run
task_id: frontend-synthesis
execution:
  # allowed: blocked|completed|completed_with_concerns|needs_context
  outcome: completed_with_concerns
  summary: 在 pluginSecurity.js 新增 schemaToUIComponents(schema)，无 ui_schema 时从合法 config_schema 递归合成宿主可渲染的声明式组件树：嵌套 object→section（子组件持完整 pointer 绑定），array→array（对象项子组件持相对绑定、标量项无子组件），writeOnly→secret，string enum→select，number/integer→number（minimum/maximum/multipleOf→step），string 约束→min_length/max_length/pattern，boolean→toggle，readOnly→read_only；$ref、contentMediaType:text/html 与不支持类型被过滤。新增 stripWriteOnlyConfigValues(schema,config) 在渲染前剥离 writeOnly 明文，确保秘密值绝不回写提交载荷。移除扁平 normalizePluginConfigSchema 及 safeDefault/allowedSchemaTypes 辅助（无残留引用）。PluginConfigForm.vue 改为从 config_schema 合成声明式 document 并委派 PluginDeclarativeUI 渲染，秘密保留/轮换/清除提交语义不变（留空省略、输入值进 secret_replacements[pointer]、清除为 null、秘密排除出 config）。vitest 指定两文件 9 用例全绿，components/plugins+api 目录 80 用例全绿。
  verification_refs:
    - cd panel/frontend && npx vitest run src/api/pluginSecurity.test.mjs src/components/plugins/PluginConfigForm.test.js
    - cd panel/frontend && npx vitest run src/components/plugins src/api
  concerns:
    - config_schema 的 default 不再由前端预填：旧扁平 normalizePluginConfigSchema 会用 field.default 初始化缺失字段，合成路径与 ui_schema 路径一致改为仅从 config 初始化，后端也未应用 config-schema default，属行为变更。
    - readOnly 属性渲染为 read_only 禁用但值仍随 config 回写，后端 rejectReadOnlyConfigValue 会拒绝；此为 ui_schema 声明式路径已有的既有问题，不在本任务范围。
```
