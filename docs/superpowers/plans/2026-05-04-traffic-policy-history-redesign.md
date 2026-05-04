# 流量统计策略设置与历史管理重新设计 — 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将流量统计的策略设置和历史管理从简陋的可折叠面板重设计为纵向信息流布局的分组卡片和操作卡片，同时新增校准弹框组件，并同步调整后端数据保留策略默认值。

**Architecture:** 前端保留现有 hooks 和数据流，仅改造组件 UI 布局；后端在 4 处修改默认值，不涉及 API 变更。按 TDD 顺序：先写测试，再改实现。

**Tech Stack:** Vue 3 + Vite + Vitest (frontend), Go + standard testing (backend), TanStack Query for data fetching

---

## 文件结构

| 文件 | 责任 |
|------|------|
| `panel/backend-go/internal/controlplane/storage/sqlite_models.go` | GORM 模型定义，含字段默认值标签 |
| `panel/backend-go/internal/controlplane/storage/traffic_store.go` | 存储层：defaultTrafficPolicy + normalizeTrafficPolicyRow |
| `panel/backend-go/internal/controlplane/service/traffic_service.go` | 服务层：UpdatePolicy 默认值处理 |
| `panel/backend-go/internal/controlplane/service/traffic_accounting.go` | defaultInt 辅助函数所在文件 |
| `panel/frontend/src/components/traffic/TrafficCalibrateModal.vue` | **新增** 校准弹框组件 |
| `panel/frontend/src/components/traffic/TrafficCalibrateModal.test.js` | **新增** 弹框组件测试 |
| `panel/frontend/src/components/traffic/TrafficPolicyForm.vue` | **修改** 策略设置表单，两列网格 → 分组卡片 |
| `panel/frontend/src/components/traffic/TrafficPolicyForm.test.js` | **新增/修改** 策略表单测试 |
| `panel/frontend/src/components/traffic/TrafficHistoryManager.vue` | **修改** 历史管理，按钮列表 → 操作卡片 |
| `panel/frontend/src/components/traffic/TrafficHistoryManager.test.js` | **新增/修改** 历史管理测试 |
| `panel/frontend/src/pages/AgentDetailPage.vue` | **修改** 去掉折叠面板，纵向信息流布局 |
| `panel/frontend/src/pages/AgentDetailPage.test.js` | **修改** 页面集成测试 |

---

### Task 1: 后端 —— 存储层模型与默认值

**Files:**
- Modify: `panel/backend-go/internal/controlplane/storage/sqlite_models.go:147-149`
- Modify: `panel/backend-go/internal/controlplane/storage/traffic_store.go:679-702`
- Test: `panel/backend-go/internal/controlplane/storage/traffic_store_test.go`

- [ ] **Step 1: 修改 `sqlite_models.go` GORM 标签**

```go
// 找到 AgentTrafficPolicyRow 结构体，修改这三个字段的 gorm 标签
HourlyRetentionDays    int    `gorm:"column:hourly_retention_days;not null;default:30"`
DailyRetentionMonths   int    `gorm:"column:daily_retention_months;not null;default:3"`
MonthlyRetentionMonths *int   `gorm:"column:monthly_retention_months;default:36"`
```

- [ ] **Step 2: 修改 `traffic_store.go` 的 `defaultTrafficPolicy`**

```go
func defaultTrafficPolicy(agentID string) AgentTrafficPolicyRow {
	defaultMonthly := 36
	return AgentTrafficPolicyRow{
		AgentID:                agentID,
		Direction:              "both",
		CycleStartDay:          1,
		HourlyRetentionDays:    30,
		DailyRetentionMonths:   3,
		MonthlyRetentionMonths: &defaultMonthly,
	}
}
```

- [ ] **Step 3: 修改 `traffic_store.go` 的 `normalizeTrafficPolicyRow`**

```go
func normalizeTrafficPolicyRow(row *AgentTrafficPolicyRow) {
	if strings.TrimSpace(row.Direction) == "" {
		row.Direction = "both"
	}
	if row.CycleStartDay == 0 {
		row.CycleStartDay = 1
	}
	if row.HourlyRetentionDays == 0 {
		row.HourlyRetentionDays = 30
	}
	if row.DailyRetentionMonths == 0 {
		row.DailyRetentionMonths = 3
	}
	if row.MonthlyRetentionMonths == nil || *row.MonthlyRetentionMonths == 0 {
		defaultMonthly := 36
		row.MonthlyRetentionMonths = &defaultMonthly
	}
}
```

- [ ] **Step 4: 运行后端存储层测试**

Run: `cd panel/backend-go && go test ./internal/controlplane/storage/... -v`
Expected: PASS（如果测试中有硬编码 180/24 的断言，会在 Task 4 修复）

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/storage/
git commit -m "feat(backend): adjust traffic policy default retention values

- hourly: 180 days -> 30 days
- daily: 24 months -> 3 months
- monthly: nil -> 36 months"
```

---

### Task 2: 后端 —— 服务层默认值与辅助函数

**Files:**
- Modify: `panel/backend-go/internal/controlplane/service/traffic_accounting.go`
- Modify: `panel/backend-go/internal/controlplane/service/traffic_service.go:347-356`
- Test: `panel/backend-go/internal/controlplane/service/traffic_service_test.go`

- [ ] **Step 1: 在 `traffic_accounting.go` 中新增 `defaultIntPtr`**

找到 `defaultInt` 函数，在其下方新增：

```go
func defaultIntPtr(value *int, fallback int) *int {
	if value == nil || *value == 0 {
		return &fallback
	}
	return value
}
```

- [ ] **Step 2: 修改 `traffic_service.go` 的 `UpdatePolicy`**

找到 `UpdatePolicy` 方法中构造 `storage.AgentTrafficPolicyRow` 的代码块：

```go
row := storage.AgentTrafficPolicyRow{
	AgentID:                agentID,
	Direction:              direction,
	CycleStartDay:          cycleStartDay,
	MonthlyQuotaBytes:      input.MonthlyQuotaBytes,
	BlockWhenExceeded:      input.BlockWhenExceeded,
	HourlyRetentionDays:    defaultInt(input.HourlyRetentionDays, 30),
	DailyRetentionMonths:   defaultInt(input.DailyRetentionMonths, 3),
	MonthlyRetentionMonths: defaultIntPtr(input.MonthlyRetentionMonths, 36),
}
```

- [ ] **Step 3: 运行后端服务层测试**

Run: `cd panel/backend-go && go test ./internal/controlplane/service/... -run TestTraffic -v`
Expected: 可能有 FAIL，如果测试断言了旧默认值

- [ ] **Step 4: 更新 `traffic_service_test.go` 中的默认值断言**

搜索测试中硬编码的 `180` 和 `24`，改为 `30` 和 `3`。如果有 `MonthlyRetentionMonths` 的 nil 断言，改为 `36`。

Run: `cd panel/backend-go && go test ./internal/controlplane/service/... -run TestTraffic -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add panel/backend-go/internal/controlplane/service/
git commit -m "feat(backend): update traffic service default retention values

- UpdatePolicy defaults: 30 days / 3 months / 36 months
- Add defaultIntPtr helper for *int fallback"
```

---

### Task 3: 前端 —— 新增 TrafficCalibrateModal 组件

**Files:**
- Create: `panel/frontend/src/components/traffic/TrafficCalibrateModal.vue`
- Create: `panel/frontend/src/components/traffic/TrafficCalibrateModal.test.js`

- [ ] **Step 1: 写测试**

```js
import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import { nextTick } from 'vue'
import TrafficCalibrateModal from './TrafficCalibrateModal.vue'

describe('TrafficCalibrateModal', () => {
  it('renders when visible is true', async () => {
    const wrapper = mount(TrafficCalibrateModal, {
      props: {
        visible: true,
        agentId: 'edge-1',
        currentUsedBytes: 1073741824,
        cycleStart: '2026-05-01T00:00:00Z',
        cycleEnd: '2026-06-01T00:00:00Z'
      }
    })
    expect(wrapper.find('.traffic-calibrate-modal').exists()).toBe(true)
    expect(wrapper.text()).toContain('校准当前周期已用流量')
    expect(wrapper.text()).toContain('2026-05-01')
  })

  it('emits confirm with parsed bytes on submit', async () => {
    const wrapper = mount(TrafficCalibrateModal, {
      props: {
        visible: true,
        agentId: 'edge-1',
        currentUsedBytes: 1073741824,
        cycleStart: '2026-05-01T00:00:00Z',
        cycleEnd: '2026-06-01T00:00:00Z'
      }
    })
    const input = wrapper.find('.traffic-calibrate-modal__input')
    await input.setValue('1.5')
    const select = wrapper.find('.traffic-calibrate-modal__unit')
    await select.setValue('GiB')
    await wrapper.find('.traffic-calibrate-modal__confirm').trigger('click')
    await nextTick()
    expect(wrapper.emitted('confirm')).toHaveLength(1)
    expect(wrapper.emitted('confirm')[0]).toEqual([1610612736])
  })

  it('emits update:visible false on cancel', async () => {
    const wrapper = mount(TrafficCalibrateModal, {
      props: {
        visible: true,
        agentId: 'edge-1',
        currentUsedBytes: 0,
        cycleStart: '',
        cycleEnd: ''
      }
    })
    await wrapper.find('.traffic-calibrate-modal__cancel').trigger('click')
    await nextTick()
    expect(wrapper.emitted('update:visible')).toHaveLength(1)
    expect(wrapper.emitted('update:visible')[0]).toEqual([false])
  })
})
```

Run: `cd panel/frontend && npx vitest run src/components/traffic/TrafficCalibrateModal.test.js`
Expected: FAIL — "TrafficCalibrateModal" not found

- [ ] **Step 2: 实现 TrafficCalibrateModal.vue**

```vue
<template>
  <BaseModal
    :model-value="visible"
    title="校准当前周期已用流量"
    size="md"
    @update:model-value="$emit('update:visible', $event)"
  >
    <div class="traffic-calibrate-modal">
      <p class="traffic-calibrate-modal__hint">
        手动调整当前计费周期的已用流量基准值。校准后系统将从原始统计数据中扣除基准值，用于额度计算。
      </p>

      <div class="traffic-calibrate-modal__info">
        <div class="traffic-calibrate-modal__info-row">
          <span class="traffic-calibrate-modal__info-label">当前计费周期</span>
          <span class="traffic-calibrate-modal__info-value">{{ cycleRangeLabel }}</span>
        </div>
        <div class="traffic-calibrate-modal__info-row">
          <span class="traffic-calibrate-modal__info-label">当前原始统计</span>
          <span class="traffic-calibrate-modal__info-value">{{ formatBytes(currentUsedBytes) }}</span>
        </div>
      </div>

      <label class="traffic-calibrate-modal__field">
        <span class="traffic-calibrate-modal__field-label">校准后已用流量</span>
        <div class="traffic-calibrate-modal__field-inputs">
          <input
            v-model="inputValue"
            class="traffic-calibrate-modal__input"
            type="text"
            placeholder="输入流量值，如 1.5 或 1.5 GiB"
          >
          <select v-model="inputUnit" class="traffic-calibrate-modal__unit">
            <option value="B">B</option>
            <option value="KiB">KiB</option>
            <option value="MiB">MiB</option>
            <option value="GiB">GiB</option>
            <option value="TiB">TiB</option>
          </select>
        </div>
        <span class="traffic-calibrate-modal__field-hint">
          支持直接输入字节数，或带单位如 "1.5 GiB"
        </span>
      </label>

      <div class="traffic-calibrate-modal__actions">
        <button class="btn btn-secondary traffic-calibrate-modal__cancel" @click="onCancel">取消</button>
        <button class="btn btn-primary traffic-calibrate-modal__confirm" @click="onConfirm">确认校准</button>
      </div>
    </div>
  </BaseModal>
</template>

<script setup>
import { ref, computed } from 'vue'
import BaseModal from '../base/BaseModal.vue'
import { formatBytes } from '../../utils/trafficStats.js'

const props = defineProps({
  visible: { type: Boolean, required: true },
  agentId: { type: String, required: true },
  currentUsedBytes: { type: Number, default: 0 },
  cycleStart: { type: String, default: '' },
  cycleEnd: { type: String, default: '' }
})

const emit = defineEmits(['update:visible', 'confirm'])

const inputValue = ref('')
const inputUnit = ref('GiB')

const cycleRangeLabel = computed(() => {
  if (!props.cycleStart || !props.cycleEnd) return '—'
  const start = new Date(props.cycleStart).toLocaleDateString()
  const end = new Date(props.cycleEnd).toLocaleDateString()
  return `${start} ~ ${end}`
})

function onCancel() {
  emit('update:visible', false)
}

function onConfirm() {
  const bytes = parseInputToBytes(inputValue.value, inputUnit.value)
  if (bytes === undefined) return
  emit('confirm', bytes)
  emit('update:visible', false)
  inputValue.value = ''
}

function parseInputToBytes(value, unit) {
  const raw = String(value ?? '').trim()
  if (raw === '') return undefined
  const match = raw.match(/^(\d+(?:\.\d+)?)\s*([kmgt]?i?b)?$/i)
  if (match) {
    const num = Number(match[1])
    const parsedUnit = normalizeUnit(match[2] || unit)
    const factors = { B: 1, KiB: 1024, MiB: 1024 ** 2, GiB: 1024 ** 3, TiB: 1024 ** 4 }
    const factor = factors[parsedUnit] || factors[unit] || 1
    return Math.round(num * factor)
  }
  const num = Number(raw)
  if (Number.isFinite(num) && num >= 0) {
    const factors = { B: 1, KiB: 1024, MiB: 1024 ** 2, GiB: 1024 ** 3, TiB: 1024 ** 4 }
    const factor = factors[unit] || 1
    return Math.round(num * factor)
  }
  return undefined
}

function normalizeUnit(u) {
  switch (String(u).trim().toLowerCase()) {
    case 'b': return 'B'
    case 'kib': case 'kb': return 'KiB'
    case 'mib': case 'mb': return 'MiB'
    case 'gib': case 'gb': return 'GiB'
    case 'tib': case 'tb': return 'TiB'
    default: return ''
  }
}
</script>

<style scoped>
.traffic-calibrate-modal { padding: 0.5rem 0; }
.traffic-calibrate-modal__hint { font-size: 0.8125rem; color: var(--color-text-secondary); margin: 0 0 1rem; line-height: 1.5; }
.traffic-calibrate-modal__info { background: var(--color-bg-subtle); border-radius: var(--radius-lg); padding: 0.75rem; margin-bottom: 1rem; }
.traffic-calibrate-modal__info-row { display: flex; justify-content: space-between; font-size: 0.8125rem; padding: 0.35rem 0; }
.traffic-calibrate-modal__info-label { color: var(--color-text-tertiary); }
.traffic-calibrate-modal__info-value { color: var(--color-text-primary); font-weight: 500; }
.traffic-calibrate-modal__field { display: block; margin-bottom: 1rem; }
.traffic-calibrate-modal__field-label { display: block; font-size: 0.8125rem; font-weight: 500; color: var(--color-text-primary); margin-bottom: 0.5rem; }
.traffic-calibrate-modal__field-inputs { display: flex; gap: 0.5rem; }
.traffic-calibrate-modal__input { flex: 1; padding: 0.5rem 0.75rem; border: 1px solid var(--color-border-default); border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); font-size: 0.875rem; }
.traffic-calibrate-modal__unit { width: 5.5rem; padding: 0.5rem 0.75rem; border: 1px solid var(--color-border-default); border-radius: var(--radius-md); background: var(--color-bg-surface); color: var(--color-text-primary); font-size: 0.875rem; }
.traffic-calibrate-modal__field-hint { display: block; font-size: 0.75rem; color: var(--color-text-muted); margin-top: 0.25rem; }
.traffic-calibrate-modal__actions { display: flex; justify-content: flex-end; gap: 0.5rem; }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
</style>
```

Run: `cd panel/frontend && npx vitest run src/components/traffic/TrafficCalibrateModal.test.js`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/components/traffic/TrafficCalibrateModal.vue panel/frontend/src/components/traffic/TrafficCalibrateModal.test.js
git commit -m "feat(panel): add TrafficCalibrateModal component

Replaces window.prompt with a proper modal for traffic calibration.
Includes input with unit selector and cycle info display."
```

---

### Task 4: 前端 —— 重设计 TrafficPolicyForm.vue

**Files:**
- Modify: `panel/frontend/src/components/traffic/TrafficPolicyForm.vue`
- Create/Modify: `panel/frontend/src/components/traffic/TrafficPolicyForm.test.js`

- [ ] **Step 1: 写测试**

```js
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TrafficPolicyForm from './TrafficPolicyForm.vue'

describe('TrafficPolicyForm', () => {
  function mountForm(props = {}) {
    return mount(TrafficPolicyForm, {
      props: {
        modelValue: {
          direction: 'both',
          cycle_start_day: 1,
          monthly_quota_value: '',
          monthly_quota_unit: 'GiB',
          block_when_exceeded: false,
          hourly_retention_days: 30,
          daily_retention_months: 3,
          monthly_retention_months: 36,
          traffic_stats_interval: ''
        },
        saving: false,
        ...props
      }
    })
  }

  it('renders three card sections', () => {
    const wrapper = mountForm()
    const cards = wrapper.findAll('.traffic-policy-form__card')
    expect(cards.length).toBe(3)
    expect(wrapper.text()).toContain('计费配置')
    expect(wrapper.text()).toContain('数据保留策略')
    expect(wrapper.text()).toContain('高级设置')
  })

  it('shows retention unit badges', () => {
    const wrapper = mountForm()
    expect(wrapper.text()).toContain('单位：天')
    expect(wrapper.text()).toContain('单位：月')
    expect(wrapper.text()).toContain('约 1 个月')
    expect(wrapper.text()).toContain('约 90 天')
    expect(wrapper.text()).toContain('约 3 年')
  })

  it('emits update:modelValue on field change', async () => {
    const wrapper = mountForm()
    const input = wrapper.findAll('.traffic-policy-form__card')[1]
      .findAll('input')[0]
    await input.setValue('60')
    expect(wrapper.emitted('update:modelValue')).toBeTruthy()
    const last = wrapper.emitted('update:modelValue').at(-1)[0]
    expect(last.hourly_retention_days).toBe(60)
  })

  it('emits save on button click', async () => {
    const wrapper = mountForm()
    await wrapper.find('.traffic-policy-form__save').trigger('click')
    expect(wrapper.emitted('save')).toHaveLength(1)
  })
})
```

Run: `cd panel/frontend && npx vitest run src/components/traffic/TrafficPolicyForm.test.js`
Expected: FAIL — 布局还没改

- [ ] **Step 2: 重写 TrafficPolicyForm.vue**

将原有 `TrafficPolicyForm.vue` 的两列网格布局替换为分组卡片布局。保持 `v-model` + `saving` + `@save` 接口不变。

关键结构：
- 外层 `.traffic-policy-form__cards` 使用 CSS Grid `repeat(2, 1fr)`
- 前两个卡片（计费配置、数据保留）各占一列
- 第三个卡片（高级设置）占满两列 `grid-column: 1 / -1`
- 数据保留卡片内部三个保留项，每项包含：标签+徽章、输入框+单位、换算说明

表单字段和事件 emit 逻辑保持不变，只改模板和样式。

Run: `cd panel/frontend && npx vitest run src/components/traffic/TrafficPolicyForm.test.js`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/components/traffic/TrafficPolicyForm.vue panel/frontend/src/components/traffic/TrafficPolicyForm.test.js
git commit -m "feat(panel): redesign TrafficPolicyForm with grouped cards

Split form into Billing / Retention / Advanced cards.
Add unit badges and conversion hints for retention fields."
```

---

### Task 5: 前端 —— 重设计 TrafficHistoryManager.vue

**Files:**
- Modify: `panel/frontend/src/components/traffic/TrafficHistoryManager.vue`
- Create/Modify: `panel/frontend/src/components/traffic/TrafficHistoryManager.test.js`

- [ ] **Step 1: 写测试**

```js
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'
import TrafficHistoryManager from './TrafficHistoryManager.vue'

describe('TrafficHistoryManager', () => {
  function mountManager(props = {}) {
    return mount(TrafficHistoryManager, {
      props: {
        policy: {
          hourly_retention_days: 30,
          daily_retention_months: 3,
          monthly_retention_months: 36
        },
        calibrating: false,
        cleaning: false,
        ...props
      }
    })
  }

  it('renders two action cards', () => {
    const wrapper = mountManager()
    const cards = wrapper.findAll('.traffic-history-manager__card')
    expect(cards.length).toBe(2)
    expect(wrapper.text()).toContain('流量校准')
    expect(wrapper.text()).toContain('数据清理')
  })

  it('displays retention policy summary in cleanup card', () => {
    const wrapper = mountManager()
    expect(wrapper.text()).toContain('小时 30 天')
    expect(wrapper.text()).toContain('日 3 个月')
    expect(wrapper.text()).toContain('月 36 个月')
  })

  it('emits calibrate on calibrate button click', async () => {
    const wrapper = mountManager()
    const calibrateBtn = wrapper.findAll('.traffic-history-manager__card')[0]
      .find('button')
    await calibrateBtn.trigger('click')
    expect(wrapper.emitted('calibrate')).toHaveLength(1)
  })

  it('emits cleanup on cleanup button click', async () => {
    const wrapper = mountManager()
    const cleanupBtn = wrapper.findAll('.traffic-history-manager__card')[1]
      .find('button')
    await cleanupBtn.trigger('click')
    expect(wrapper.emitted('cleanup')).toHaveLength(1)
  })

  it('disables buttons when loading', () => {
    const wrapper = mountManager({ calibrating: true, cleaning: true })
    const buttons = wrapper.findAll('button')
    for (const btn of buttons) {
      expect(btn.attributes('disabled')).toBeDefined()
    }
  })
})
```

Run: `cd panel/frontend && npx vitest run src/components/traffic/TrafficHistoryManager.test.js`
Expected: FAIL

- [ ] **Step 2: 重写 TrafficHistoryManager.vue**

将原有按钮列表替换为两个操作卡片：

- **流量校准卡片**：标题 + 说明文案 + "校准为指定值"按钮 + "从现在归零"按钮
- **数据清理卡片**：标题 + 说明文案（含当前保留策略摘要）+ "清理过期数据"按钮（红色样式）

新增 `policy` prop（Object），用于渲染保留策略摘要。文案模板："按保留策略清理过期历史数据。当前策略：小时 {hourly} 天、日 {daily} 个月、月 {monthly} 个月。"

保持 `calibrating` / `cleaning` props 和三个 emit 事件不变。

Run: `cd panel/frontend && npx vitest run src/components/traffic/TrafficHistoryManager.test.js`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/components/traffic/TrafficHistoryManager.vue panel/frontend/src/components/traffic/TrafficHistoryManager.test.js
git commit -m "feat(panel): redesign TrafficHistoryManager with action cards

Replace button list with Calibrate and Cleanup cards.
Add policy prop to display retention summary."
```

---

### Task 6: 前端 —— 调整 AgentDetailPage.vue 布局

**Files:**
- Modify: `panel/frontend/src/pages/AgentDetailPage.vue`
- Modify: `panel/frontend/src/pages/AgentDetailPage.test.js`

- [ ] **Step 1: 修改 AgentDetailPage.vue**

在流量统计标签页（`activeTab === 'traffic'`）中：

1. 将三个 `TrafficCollapsibleSection` 替换为 `<section class="traffic-section">` + `<h3>` 标题
2. `TrafficHistoryManager` 新增 `:policy="trafficPolicyForm"` prop
3. `TrafficHistoryManager` 的 `@calibrate` 事件改为 `calibrateModalVisible = true`
4. 在模板末尾添加 `<TrafficCalibrateModal>` 组件，绑定 `v-model:visible="calibrateModalVisible"`
5. 在 script setup 中：
   - `import TrafficCalibrateModal from '../components/traffic/TrafficCalibrateModal.vue'`
   - 新增 `const calibrateModalVisible = ref(false)`
   - 新增 `function onCalibrateConfirm(usedBytes) { calibrateTrafficMutation.mutateAsync({ used_bytes: usedBytes }) }`
   - 删除 `calibrateTrafficSummary` 函数中的 `window.prompt` 逻辑（或保留作为 fallback）

保留现有的 `calibrateTrafficToZero` 和 `cleanupTrafficHistory` 函数不变。

- [ ] **Step 2: 更新 AgentDetailPage.test.js**

补充测试：
- 流量统计标签页不再渲染 `TrafficCollapsibleSection`
- `TrafficCalibrateModal` 在点击"校准为指定值"后显示
- 弹框确认后调用 `fetchTrafficSummary`（通过 mock 断言）

Run: `cd panel/frontend && npx vitest run src/pages/AgentDetailPage.test.js`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add panel/frontend/src/pages/AgentDetailPage.vue panel/frontend/src/pages/AgentDetailPage.test.js
git commit -m "feat(panel): convert traffic tab to vertical flow layout

Remove TrafficCollapsibleSection, use section headings instead.
Integrate TrafficCalibrateModal for calibration workflow."
```

---

### Task 7: 全量验证

- [ ] **Step 1: 前端全量测试**

Run: `cd panel/frontend && npx vitest run`
Expected: ALL PASS

- [ ] **Step 2: 后端全量测试**

Run: `cd panel/backend-go && go test ./...`
Expected: ALL PASS

- [ ] **Step 3: 构建检查**

Run: `cd panel/frontend && npm run build`
Expected: SUCCESS（无 TypeScript/Vite 构建错误）

Run: `cd panel/backend-go && go build ./cmd/nre-control-plane`
Expected: SUCCESS

- [ ] **Step 4: Commit**

```bash
git commit --allow-empty -m "chore: traffic stats redesign complete

- Vertical flow layout for traffic stats tab
- Grouped card layout for policy form
- Action card layout for history manager
- New TrafficCalibrateModal replacing window.prompt
- Backend default retention: 30d / 3m / 36m"
```

---

## Self-Review Checklist

- [x] **Spec coverage:** 整体布局 ✅ Task 6 | 策略分组卡片 ✅ Task 4 | 历史操作卡片 ✅ Task 5 | 校准弹框 ✅ Task 3 | 后端默认值 ✅ Task 1-2 | 测试策略 ✅ Task 3-7
- [x] **Placeholder scan:** 无 TBD/TODO/"implement later"/"similar to"
- [x] **Type consistency:** `defaultIntPtr` 在 Task 2 定义，在 Task 1 的存储层不使用（存储层用指针字面量）；前后端字段名一致
