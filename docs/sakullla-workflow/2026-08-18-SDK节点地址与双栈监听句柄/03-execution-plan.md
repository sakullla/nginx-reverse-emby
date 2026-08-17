---
# Runtime 只读取这一处文件头机器区；全部可执行 Recipe/DAG/验证字段都在这里填写。
format: execution_plan
tasks:
  - id: sdk-node-and-listen-handles
    goal: 公开 SDK 提供节点地址投影与双栈监听绑定，使插件能得到可分享主机和实际端口。
    depends_on: []
    covers: [R1, R2]
    scope:
      - plugin-sdk/go/node_addresses.go
      - plugin-sdk/go/node_addresses_test.go
      - plugin-sdk/go/dual_stack_listener.go
      - plugin-sdk/go/dual_stack_listener_test.go
      - plugin-sdk/go/typed_handles.go
    outcomes:
      - NodeAddressSource 与 DualStackListener 可被 Host 注入，插件不探测公网 IP、不开公网套接字。
      - "SelectShareHost 按 DDNS、IPv4、IPv6 取值，且不选出 0.0.0.0、::、回环或 localhost。"
      - JoinShareHostPort 产出可填进现有 L4 的 host:port，IPv6 带方括号。
    verify:
      - go test -C plugin-sdk ./go -count=1 -run "SelectShareHost|ShareableHost|DualStack|JoinShareHostPort|ValidL4"
    test: new
delivery_verification:
  plugin-sdk-go:
    command: go test -C plugin-sdk ./go -count=1
---
# Execution Plan

一条 Task 交付 SDK 合同。R3 以排除验收。Host 把 ddns_domain 注入 Agent 进程属于后续接线，不在本 Recipe 的 plugin-sdk 范围。
