<template>
  <div class="traffic-policy-form">
    <div class="traffic-policy-form__cards">
      <div class="traffic-policy-form__card traffic-policy-form__card--primary" data-testid="traffic-policy-card-quota">
        <div class="traffic-policy-form__card-heading">
          <span class="traffic-policy-form__card-badge">优先</span>
          <h4 class="traffic-policy-form__card-title">额度与阻断</h4>
        </div>
        <p class="traffic-policy-form__card-lead">先确认月额度与超额阻断，这是策略主决策面</p>
        <div class="traffic-policy-form__card-body">
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

      <div class="traffic-policy-form__card" data-testid="traffic-policy-card-billing">
        <div class="traffic-policy-form__card-heading">
          <h4 class="traffic-policy-form__card-title">计费方向与周期</h4>
        </div>
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
        </div>
      </div>

      <div class="traffic-policy-form__card" data-testid="traffic-policy-card-retention">
        <div class="traffic-policy-form__card-heading">
          <h4 class="traffic-policy-form__card-title">数据保留策略</h4>
        </div>
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

      <div class="traffic-policy-form__card traffic-policy-form__card--full traffic-policy-form__card--muted" data-testid="traffic-policy-card-advanced">
        <div class="traffic-policy-form__card-heading">
          <h4 class="traffic-policy-form__card-title">高级设置</h4>
        </div>
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

function updateField(key, value) {
  emit('update:modelValue', {
    ...props.modelValue,
    [key]: value
  })
}
</script>

<style scoped>
.traffic-policy-form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}
.traffic-policy-form__cards {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0.875rem;
}
.traffic-policy-form__card {
  background:
    linear-gradient(
      180deg,
      color-mix(in srgb, var(--color-bg-surface) 92%, var(--color-bg-subtle)),
      var(--color-bg-surface)
    );
  border: 1px solid color-mix(in srgb, var(--color-border-default) 90%, transparent);
  border-radius: var(--radius-lg);
  padding: 0.875rem 1rem 1rem;
  min-width: 0;
  box-shadow: 0 1px 2px color-mix(in srgb, var(--color-text-primary) 4%, transparent);
}
.traffic-policy-form__card--primary {
  border-color: color-mix(in srgb, var(--color-primary-200, var(--color-primary-50)) 65%, var(--color-border-default));
  box-shadow:
    0 1px 2px color-mix(in srgb, var(--color-text-primary) 5%, transparent),
    0 8px 18px color-mix(in srgb, var(--color-text-primary) 3%, transparent);
  background:
    linear-gradient(
      180deg,
      color-mix(in srgb, var(--color-primary-50) 42%, var(--color-bg-surface)),
      var(--color-bg-surface) 48%
    );
}
.traffic-policy-form__card--muted {
  border-style: dashed;
  box-shadow: none;
  background: color-mix(in srgb, var(--color-bg-subtle) 45%, var(--color-bg-surface));
}
.traffic-policy-form__card--full {
  grid-column: 1 / -1;
}
.traffic-policy-form__card-heading {
  display: flex;
  align-items: center;
  gap: 0.45rem;
  margin: 0 0 0.55rem;
  padding-bottom: 0.5rem;
  border-bottom: 1px solid color-mix(in srgb, var(--color-border-subtle) 85%, transparent);
}
.traffic-policy-form__card-badge {
  display: inline-flex;
  align-items: center;
  padding: 0.08rem 0.4rem;
  border-radius: 999px;
  border: 1px solid color-mix(in srgb, var(--color-primary-200, var(--color-primary-50)) 70%, var(--color-border-default));
  background: color-mix(in srgb, var(--color-primary-50) 75%, var(--color-bg-surface));
  color: var(--color-text-secondary);
  font-size: 0.6875rem;
  font-weight: 700;
  letter-spacing: 0.03em;
  flex-shrink: 0;
}
.traffic-policy-form__card-title {
  margin: 0;
  font-size: 0.875rem;
  font-weight: 650;
  letter-spacing: 0.01em;
  color: var(--color-text-primary);
}
.traffic-policy-form__card--primary .traffic-policy-form__card-title {
  font-size: 0.9375rem;
  font-weight: 700;
}
.traffic-policy-form__card-lead {
  margin: 0 0 0.7rem;
  color: var(--color-text-muted);
  font-size: 0.75rem;
  line-height: 1.4;
}
.traffic-policy-form__card-body {
  display: flex;
  flex-direction: column;
  gap: 0.7rem;
}
.traffic-policy-form__field { display: flex; flex-direction: column; gap: 0.3rem; min-width: 0; }
.traffic-policy-form__field--switch { flex-direction: row; align-items: center; justify-content: space-between; gap: 0.75rem; }
.traffic-policy-form__label {
  color: var(--color-text-secondary);
  font-size: 0.8125rem;
  font-weight: 550;
}
.traffic-policy-form__badge {
  margin-left: 0.35rem;
  padding: 0.05rem 0.4rem;
  border-radius: 999px;
  background: color-mix(in srgb, var(--color-bg-subtle) 88%, transparent);
  color: var(--color-text-muted);
  font-size: 0.6875rem;
  font-weight: 500;
  display: inline-block;
}
.traffic-policy-form__input {
  width: 100%;
  min-width: 0;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  padding: 0.5rem 0.625rem;
  font-size: 0.875rem;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
}
.traffic-policy-form__input:focus {
  outline: none;
  border-color: color-mix(in srgb, var(--color-primary, #3b82f6) 55%, var(--color-border-default));
  box-shadow: 0 0 0 3px color-mix(in srgb, var(--color-primary, #3b82f6) 16%, transparent);
}
.traffic-policy-form__hint {
  color: var(--color-text-muted);
  font-size: 0.75rem;
  line-height: 1.4;
}
.traffic-policy-form__quota { display: grid; grid-template-columns: minmax(0, 1fr) 5.5rem; gap: 0.5rem; }
.traffic-policy-form__footer {
  display: flex;
  justify-content: flex-end;
  padding-top: 0.125rem;
}
.traffic-policy-form__save {
  min-width: 5.5rem;
}
@media (max-width: 720px) {
  .traffic-policy-form__cards {
    grid-template-columns: 1fr;
  }
  .traffic-policy-form__card--full {
    grid-column: auto;
  }
}
</style>
