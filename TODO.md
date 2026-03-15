# TODO - Playwright 全功能测试计划

## 目标

为当前面板补齐一套覆盖 **Master / 本机节点 / 轻量 Agent / NAT 心跳场景** 的 Playwright 端到端测试，确保核心管理流程、错误处理和基础交互都可回归验证。

## 一、测试基础设施

- [ ] 新增 Playwright 依赖与基础配置
- [ ] 增加统一启动脚本：
  - [ ] 启动前端
  - [ ] 启动 backend
  - [ ] 准备测试数据目录
- [ ] 为测试准备独立的临时数据目录，避免污染真实 `panel/data`
- [ ] 增加测试专用环境变量：
  - [ ] 固定 `API_TOKEN`
  - [ ] 固定 `MASTER_REGISTER_TOKEN`
  - [ ] 固定本机节点信息
  - [ ] 关闭真实 nginx reload / 改为 mock apply
- [ ] 为 backend 增加测试夹具脚本或 mock 行为，便于模拟：
  - [ ] 本机 apply 成功
  - [ ] 本机 apply 失败
  - [ ] 轻量 Agent 心跳
  - [ ] 远程节点离线

## 二、认证相关

- [ ] 登录页渲染
- [ ] 正确 token 登录成功
- [ ] 错误 token 登录失败并显示错误提示
- [ ] 登录后刷新页面仍保持登录态
- [ ] 401 时自动清理本地 token 并回到登录页
- [ ] 退出登录成功

## 三、Master / 节点管理

- [ ] 面板首次加载时显示节点列表
- [ ] 本机节点默认存在且可选中
- [ ] 节点状态展示正确：
  - [ ] online
  - [ ] offline
  - [ ] pull/local 模式显示正确
- [ ] 节点 revision / apply 状态展示正确
- [ ] 切换节点后规则列表和统计信息同步刷新
- [ ] 移除远程节点成功
- [ ] 本机节点不可移除

## 四、规则管理（本机节点）

- [ ] 新增规则成功
- [ ] 编辑规则成功
- [ ] 删除规则成功
- [ ] 启用 / 禁用规则成功
- [ ] 标签新增、筛选、取消筛选正确
- [ ] 搜索规则正确
- [ ] 空状态展示正确
- [ ] 规则表单校验正确：
  - [ ] frontend_url 为空
  - [ ] backend_url 为空
  - [ ] 非法 URL

## 五、规则管理（远程 Agent）

- [ ] 在远程节点新增规则后显示“等待 Agent 心跳应用”
- [ ] 在远程节点编辑规则后 revision 增加
- [ ] 在远程节点删除规则后 revision 增加
- [ ] 心跳后远程节点 current_revision 追平 desired_revision
- [ ] 心跳上报 apply 成功后状态更新为成功
- [ ] 心跳上报 apply 失败后状态更新为失败并显示错误

## 六、Agent 心跳 / NAT 场景

- [ ] 通过心跳接口注册后的 Agent 出现在面板
- [ ] NAT Agent 无 `agent_url` 时仍可正常工作
- [ ] 心跳超时后节点显示离线
- [ ] 恢复心跳后节点重新显示在线
- [ ] 心跳上报统计信息后面板可展示最新 stats
- [ ] 多个 Agent 并发心跳时状态更新正确

## 七、应用配置与错误处理

- [ ] 本机节点“应用配置”成功提示正确
- [ ] 本机节点 apply 失败时错误提示正确
- [ ] 远程节点 apply 后提示“等待心跳应用”
- [ ] 后端返回 4xx/5xx 时前端错误提示正确
- [ ] 接口超时 / 节点离线时提示正确

## 八、界面交互

- [ ] 主题切换正常
- [ ] grid / list 视图切换正常
- [ ] 视图模式刷新后保持
- [ ] 搜索关键词清空按钮正常
- [ ] “复制加入脚本”按钮正常
- [ ] 添加规则弹窗打开 / 关闭正常
- [ ] 删除确认弹窗正常

## 九、示例与文档一致性

- [ ] 校验 README 中加入命令与实际脚本参数一致
- [ ] 校验 `AGENT_EXAMPLES.md` 示例可执行
- [ ] 校验 `examples/light-agent.env.example` 字段完整
- [ ] 校验 `examples/light-agent.service.example` 与脚本路径一致

## 十、建议的测试目录结构

- [ ] `tests/auth.spec.ts`
- [ ] `tests/agents.spec.ts`
- [ ] `tests/local-rules.spec.ts`
- [ ] `tests/remote-rules.spec.ts`
- [ ] `tests/heartbeat.spec.ts`
- [ ] `tests/ui.spec.ts`
- [ ] `tests/docs-smoke.spec.ts`
- [ ] `tests/fixtures/`：测试数据与 mock helper

## 十一、执行顺序建议

1. 先搭建 Playwright 基础环境与 mock apply
2. 先覆盖认证 + 本机节点规则 CRUD
3. 再覆盖 Agent 心跳与 NAT 场景
4. 再覆盖 UI 交互与错误分支
5. 最后补文档/示例一致性 smoke tests
