你说得对。源码已经核对清楚：当前官方 Accelerator Sources 0.1.0 根本不是你要的“加速代理”，它的 README 明确写着：

- 只保存第三方 Docker/GitHub 加速源地址和探测状态；
- 只生成可复制的配置文本；
- 不代理内容、不监听端口、不修改 Agent 配置；
- 生产入口因为所需 Host 接口还不存在，甚至会主动失败。

所以这里不是配置方式的问题，而是插件功能定义做错了；现有的 targets 也只是实例作用域，不能形成“公网 HTTPS → 指定 Agent → GitHub/Docker 上游”的流量链。我接下来先
核对 Agent 插件运行时和现有 HTTPS/证书路由能力，确定能否把它改成 Agent 侧服务，还是应由控制面生成 Agent HTTP 路由；这会决定 bwrap 相关改动是否应该保留。

配置入口在：

    插件 → 已安装插件 → Accelerator Sources → 实例配置

    当前页面地址：http://127.0.0.1:8080/plugins/accelerator-sources

    我已用 Chrome MCP 确认，页面中部能看到“实例配置”。但目前表单只显示：

    - generation
    - schedule_seconds

    真正需要的 probe 和 sources（加速源地址列表）没有渲染出来，因为现有表单不支持嵌套对象和数组。

    所以不是你没找到，而是当前 UI 确实无法完整配置这个插件。我之前通过 API 创建的测试实例才让这块区域显示出来。