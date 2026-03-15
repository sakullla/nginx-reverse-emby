# TODO - Playwright 测试计划

## 当前已完成

- [x] 新增 Playwright 依赖与基础配置
- [x] 新增 `npm run test:e2e` 统一入口
- [x] 自动启动前端 Vite 测试服务
- [x] 为前端关键交互补齐 `data-testid`
- [x] 已覆盖前端 Mock 场景：登录、登出、Agent 管理、本机规则 CRUD、主题切换、Join 命令复制、本地 / 远程 apply 提示
- [x] 已覆盖真实 backend 集成场景：NAT Agent 注册、心跳、离线、revision 同步、本机 apply 成功 / 失败
- [x] backend 支持测试专用的 `PANEL_DATA_ROOT` 与 `PANEL_APPLY_COMMAND` 覆盖

## 当前未完成

- [ ] 错误 token 登录失败提示
- [ ] 401 后自动清理本地 token 并回到登录页
- [ ] 本机 apply 失败时的前端提示覆盖
- [ ] grid / list 视图切换测试
- [ ] 远程规则编辑 / 删除后 revision 递增前端链路
- [ ] 多 Agent 并发心跳与恢复在线场景
- [ ] README / 示例文件 smoke tests

## 检查项

### 认证
- [x] 登录页渲染
- [x] 正确 token 登录
- [x] 刷新后保持登录态
- [x] 登出
- [ ] 错误 token 分支
- [ ] 401 回退登录

### 节点管理
- [x] Agent 列表加载
- [x] 本机节点默认存在
- [x] 远程节点切换与移除
- [x] 本机节点不可删除（backend API 已校验）
- [x] online / offline / local / pull 状态校验
- [x] revision / apply 状态校验

### 规则管理
- [x] 本机规则新增、编辑、删除、启停
- [x] 标签筛选与搜索
- [x] 远程 Agent 的 revision / heartbeat 同步
- [ ] 非法 URL 校验
- [ ] 远程规则编辑 / 删除前端链路

### UI 与交互
- [x] 主题切换与持久化
- [x] Join 命令复制
- [x] 新增规则弹窗
- [x] 删除确认弹窗
- [ ] grid / list 视图切换

### backend 集成
- [x] NAT Agent 无 `agent_url` 仍可工作
- [x] 心跳超时后节点离线
- [x] 心跳上报统计信息
- [x] 本机 apply 成功
- [x] 本机 apply 失败
- [x] 远程 apply 返回“等待心跳应用”
- [ ] 多 Agent 并发场景

## 当前测试文件

- `tests/e2e/auth.spec.js`
- `tests/e2e/agents.spec.js`
- `tests/e2e/rules.spec.js`
- `tests/e2e/ui.spec.js`
- `tests/e2e/backend.spec.js`
- `tests/e2e/backend-helper.js`

## 建议后续顺序

1. 补错误 token / 401 分支
2. 补 grid / list 视图切换
3. 补远程规则编辑 / 删除后 revision 链路
4. 补多 Agent 心跳与恢复在线
5. 补 README / 示例文件 smoke tests
