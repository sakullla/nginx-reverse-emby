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
const inputUnit = ref('B')

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
.btn-primary { background: var(--color-primary); color: white; }
.btn-secondary { background: var(--color-bg-subtle); color: var(--color-text-primary); border: 1px solid var(--color-border-default); }
</style>
