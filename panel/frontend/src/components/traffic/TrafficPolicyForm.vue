<template>
  <div class="traffic-policy-form">
    <div class="traffic-policy-form__cards">
      <div class="traffic-policy-form__card">
        <h4 class="traffic-policy-form__card-title">计费配置</h4>
        <div class="traffic-policy-form__card-body">
          <label class="traffic-policy-form__field">
            <span class="traffic-policy-form__label">方向</span>
            <select :value="modelValue.direction" class="traffic-policy-form__input" @change="updateField('direction', $event.target.value)">
              <option value="both">双向</option>
              <option value="rx">入站</option>
              <option value="tx">出站</option>
              <option value="max">取最大值</option>
            </select>
          </label>
          <label class="traffic-policy-form__field">
            <span class="traffic-policy-form__label">月周期起始日</span>
            <input :value="modelValue.cycle_start_day" class="traffic-policy-form__input" type="number" min="1" max="28" @input="updateField('cycle_start_day', Number($event.target.value))">
          </label>
          <label class="traffic-policy-form__field">
            <span class="traffic-policy-form__label">月额度</span>
            <div class="traffic-policy-form__quota">
              <input :value="modelValue.monthly_quota_value" class="traffic-policy-form__input" type="text" placeholder="留空表示无限制" @input="updateField('monthly_quota_value', $event.target.value)">
              <select :value="modelValue.monthly_quota_unit" class="traffic-policy-form__input traffic-policy-form__unit" @change="updateField('monthly_quota_unit', $event.target.value)">
                <option v-for="unit in quotaUnits" :key="unit.value" :value="unit.value">{{ unit.label }}</option>
              </select>
            </div>
          </label>
          <label class="traffic-policy-form__field traffic-policy-form__field--switch">
            <span class="traffic-policy-form__label">超额阻断</span>
            <input :checked="modelValue.block_when_exceeded" type="checkbox" @change="updateField('block_when_exceeded', $event.target.checked)">
          </label>
        </div>
      </div>

      <div class="traffic-policy-form__card">
        <h4 class="traffic-policy-form__card-title">数据保留策略</h4>
        <div class="traffic-policy-form__card-body">
          <label class="traffic-policy-form__field">
            <span class="traffic-policy-form__label">
              小时粒度保留
              <span class="traffic-policy-form__badge">单位：天</span>
            </span>
            <input :value="modelValue.hourly_retention_days" class="traffic-policy-form__input" type="number" min="1" @input="updateField('hourly_retention_days', Number($event.target.value))">
            <span class="traffic-policy-form__hint">约 {{ Math.round(modelValue.hourly_retention_days / 30) }} 个月</span>
          </label>
          <label class="traffic-policy-form__field">
            <span class="traffic-policy-form__label">
              日汇总保留
              <span class="traffic-policy-form__badge">单位：月</span>
            </span>
            <input :value="modelValue.daily_retention_months" class="traffic-policy-form__input" type="number" min="1" @input="updateField('daily_retention_months', Number($event.target.value))">
            <span class="traffic-policy-form__hint">约 {{ modelValue.daily_retention_months * 30 }} 天</span>
          </label>
          <label class="traffic-policy-form__field">
            <span class="traffic-policy-form__label">
              月汇总保留
              <span class="traffic-policy-form__badge">单位：月</span>
            </span>
            <input :value="modelValue.monthly_retention_months" class="traffic-policy-form__input" type="number" min="1" placeholder="留空表示永久" @input="updateField('monthly_retention_months', $event.target.value)">
            <span class="traffic-policy-form__hint">约 {{ Math.round(modelValue.monthly_retention_months / 12) }} 年</span>
          </label>
        </div>
      </div>

      <div class="traffic-policy-form__card traffic-policy-form__card--full">
        <h4 class="traffic-policy-form__card-title">高级设置</h4>
        <div class="traffic-policy-form__card-body">
          <label class="traffic-policy-form__field">
            <span class="traffic-policy-form__label">流量统计上报周期</span>
            <input :value="modelValue.traffic_stats_interval" class="traffic-policy-form__input" type="text" placeholder="例如 30s、1m、5m；留空表示随心跳上报" @input="updateField('traffic_stats_interval', $event.target.value)">
          </label>
        </div>
      </div>
    </div>
    <div class="traffic-policy-form__footer">
      <button class="btn btn-primary traffic-policy-form__save" type="button" :disabled="saving" @click="$emit('save')">保存</button>
    </div>
  </div>
</template>

<script setup>
const props = defineProps({
  modelValue: { type: Object, required: true },
  saving: { type: Boolean, default: false }
})

const emit = defineEmits(['update:modelValue', 'save'])

const quotaUnits = [
  { value: 'B', label: 'B' },
  { value: 'KiB', label: 'KiB' },
  { value: 'MiB', label: 'MiB' },
  { value: 'GiB', label: 'GiB' },
  { value: 'TiB', label: 'TiB' }
]

function updateField(field, value) {
  emit('update:modelValue', { ...props.modelValue, [field]: value })
}
</script>

<style scoped>
.traffic-policy-form__cards {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.75rem;
}
.traffic-policy-form__card {
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  padding: 1rem;
}
.traffic-policy-form__card--full {
  grid-column: 1 / -1;
}
.traffic-policy-form__card-title {
  margin: 0 0 0.75rem;
  font-size: 0.9375rem;
  font-weight: 600;
  color: var(--color-text-primary);
}
.traffic-policy-form__card-body {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
}
.traffic-policy-form__field { display: flex; flex-direction: column; gap: 0.35rem; min-width: 0; }
.traffic-policy-form__field--switch { flex-direction: row; align-items: center; justify-content: space-between; }
.traffic-policy-form__label {
  display: flex;
  align-items: center;
  gap: 0.375rem;
  color: var(--color-text-secondary);
  font-size: 0.8125rem;
  font-weight: 500;
}
.traffic-policy-form__badge {
  display: inline-block;
  padding: 0.1rem 0.4rem;
  font-size: 0.6875rem;
  font-weight: 500;
  color: var(--color-primary);
  background: var(--color-primary-subtle);
  border-radius: var(--radius-sm);
}
.traffic-policy-form__hint {
  font-size: 0.75rem;
  color: var(--color-text-muted);
}
.traffic-policy-form__input {
  width: 100%;
  min-width: 0;
  padding: 0.5rem 0.75rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: 0.875rem;
  box-sizing: border-box;
}
.traffic-policy-form__input:focus { outline: none; border-color: var(--color-primary); box-shadow: var(--shadow-focus); }
.traffic-policy-form__quota { display: grid; grid-template-columns: minmax(0, 1fr) 5.5rem; gap: 0.5rem; }
.traffic-policy-form__unit { font-family: var(--font-mono); }
.traffic-policy-form__footer { display: flex; justify-content: flex-end; margin-top: 0.75rem; }
.btn { padding: 0.5rem 1rem; border-radius: var(--radius-lg); font-size: 0.875rem; font-weight: 500; cursor: pointer; transition: all 0.15s; border: none; font-family: inherit; display: inline-flex; align-items: center; gap: 0.375rem; }
.btn-primary { background: var(--gradient-primary); color: white; }
.btn:disabled { opacity: 0.6; cursor: not-allowed; }
@media (max-width: 720px) { .traffic-policy-form__cards { grid-template-columns: 1fr; } }
</style>
