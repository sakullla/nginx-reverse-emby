<template>
  <div class="repository-form-overlay" @click.self="$emit('cancel')">
    <section class="repository-form" role="dialog" aria-modal="true" :aria-labelledby="titleId">
      <header class="repository-form__header">
        <div>
          <h2 :id="titleId">{{ isEditing ? '编辑仓库源' : '新增仓库源' }}</h2>
          <p>仓库源固定到可复现的分支或标签；刷新后会记录实际解析的提交 OID。</p>
        </div>
        <button class="repository-form__close" type="button" aria-label="关闭" @click="$emit('cancel')">✕</button>
      </header>

      <form class="repository-form__body" @submit.prevent="submit">
        <div v-if="!isEditing" class="repository-form__field">
          <label for="repository-id">标识</label>
          <input id="repository-id" v-model.trim="form.id" class="repository-form__input" autocomplete="off" placeholder="team-plugins">
          <span v-if="errors.id" class="repository-form__error">{{ errors.id }}</span>
        </div>

        <fieldset class="repository-form__field">
          <legend>用途</legend>
          <div class="repository-form__segments" data-field="purpose">
            <button
              v-for="option in purposeOptions"
              :key="option.value"
              type="button"
              :class="['repository-form__segment', { 'repository-form__segment--active': form.purpose === option.value }]"
              :aria-pressed="form.purpose === option.value"
              @click="form.purpose = option.value"
            >
              <strong>{{ option.label }}</strong>
              <small>{{ option.hint }}</small>
            </button>
          </div>
        </fieldset>

        <div class="repository-form__field">
          <label for="repository-name">名称</label>
          <input id="repository-name" v-model="form.name" class="repository-form__input" autocomplete="off" placeholder="团队插件仓库">
          <span v-if="errors.name" class="repository-form__error">{{ errors.name }}</span>
        </div>

        <div class="repository-form__field">
          <label for="repository-url">Git URL</label>
          <input id="repository-url" v-model="form.url" class="repository-form__input" autocomplete="url" placeholder="https://git.example.com/team/plugins.git">
          <span v-if="errors.url" class="repository-form__error">{{ errors.url }}</span>
        </div>

        <div class="repository-form__ref-row">
          <fieldset class="repository-form__field">
            <legend>引用类型</legend>
            <div class="repository-form__segments repository-form__segments--compact" data-field="ref-kind">
              <button
                v-for="option in refKindOptions"
                :key="option.value"
                type="button"
                :class="['repository-form__segment', { 'repository-form__segment--active': form.ref_kind === option.value }]"
                :aria-pressed="form.ref_kind === option.value"
                @click="form.ref_kind = option.value"
              >{{ option.label }}</button>
            </div>
          </fieldset>
          <div class="repository-form__field">
            <label for="repository-ref">{{ form.ref_kind === 'tag' ? '标签' : '分支' }}</label>
            <input id="repository-ref" v-model="form.ref_name" class="repository-form__input" autocomplete="off" :placeholder="form.ref_kind === 'tag' ? 'v1.0.0' : 'main'">
            <span v-if="errors.ref_name" class="repository-form__error">{{ errors.ref_name }}</span>
          </div>
        </div>

        <div class="repository-form__field">
          <label for="repository-credential">Git 凭据引用（可选）</label>
          <input id="repository-credential" v-model="form.credential_ref" class="repository-form__input" autocomplete="off" placeholder="secret://git/team">
          <p v-if="isEditing && source?.credential_configured" class="repository-form__hint">已配置凭据。留空会保留现有值。</p>
          <label v-if="isEditing && source?.credential_configured" class="repository-form__check">
            <input v-model="form.clear_credential" type="checkbox">
            清除现有 Git 凭据
          </label>
        </div>

        <div class="repository-form__signer-row">
          <div class="repository-form__field">
            <label for="repository-signer-key">签名密钥 ID</label>
            <input id="repository-signer-key" v-model="form.signer_key_id" class="repository-form__input" autocomplete="off">
          </div>
          <div class="repository-form__field">
            <label for="repository-signer-secret">签名密钥引用</label>
            <input id="repository-signer-secret" v-model="form.signer_secret_ref" class="repository-form__input" autocomplete="off" placeholder="secret://signing/team">
            <p v-if="isEditing && source?.signer_fingerprint" class="repository-form__hint">已配置签名材料。留空会保留现有值。</p>
          </div>
        </div>

        <div class="repository-form__field">
          <label for="repository-refresh">自动刷新间隔（可选）</label>
          <input id="repository-refresh" v-model="form.refresh_interval" class="repository-form__input" autocomplete="off" placeholder="30m">
          <p class="repository-form__hint">使用持续时间，例如 15m、2h；留空表示不自动刷新。</p>
        </div>

        <p v-if="errors.submit" class="repository-form__error repository-form__error--submit">{{ errors.submit }}</p>

        <footer class="repository-form__footer">
          <button type="button" class="btn btn-secondary" @click="$emit('cancel')">取消</button>
          <button type="submit" class="btn btn-primary" :disabled="saving">{{ saving ? '保存中…' : '保存' }}</button>
        </footer>
      </form>
    </section>
  </div>
</template>

<script setup>
import { computed, reactive, watch } from 'vue'

const props = defineProps({
  source: { type: Object, default: null },
  saving: { type: Boolean, default: false },
  submitError: { type: String, default: '' }
})

const emit = defineEmits(['save', 'cancel'])

const purposeOptions = [
  { value: 'market', label: '市场索引', hint: '读取市场目录，不编辑市场条目' },
  { value: 'plugin', label: '插件包', hint: '仓库根目录是一份插件包' }
]
const refKindOptions = [
  { value: 'branch', label: '分支' },
  { value: 'tag', label: '标签' }
]

const form = reactive(defaultForm())
const errors = reactive({ id: '', name: '', url: '', ref_name: '', submit: '' })
const isEditing = computed(() => Boolean(props.source?.id))
const titleId = computed(() => `repository-source-form-${isEditing.value ? 'edit' : 'create'}`)

function defaultForm() {
  return {
    id: '',
    name: '',
    url: '',
    purpose: 'market',
    ref_kind: 'branch',
    ref_name: 'main',
    credential_ref: '',
    clear_credential: false,
    signer_key_id: '',
    signer_secret_ref: '',
    refresh_interval: ''
  }
}

watch(
  () => props.source,
  (source) => {
    Object.assign(form, defaultForm(), source ? {
      id: source.id || '',
      name: source.name || '',
      url: source.url || '',
      purpose: source.purpose || 'market',
      ref_kind: source.ref_kind || 'branch',
      ref_name: source.ref_name || '',
      signer_key_id: source.signer_key_id || '',
      refresh_interval: durationFromNanoseconds(source.refresh_interval_ns)
    } : {})
    Object.assign(errors, { id: '', name: '', url: '', ref_name: '', submit: '' })
  },
  { immediate: true }
)

watch(() => props.submitError, (value) => { errors.submit = value || '' }, { immediate: true })

function durationFromNanoseconds(value) {
  const nanoseconds = Number(value) || 0
  if (nanoseconds <= 0) return ''
  if (nanoseconds % 3_600_000_000_000 === 0) return `${nanoseconds / 3_600_000_000_000}h`
  if (nanoseconds % 60_000_000_000 === 0) return `${nanoseconds / 60_000_000_000}m`
  if (nanoseconds % 1_000_000_000 === 0) return `${nanoseconds / 1_000_000_000}s`
  return `${nanoseconds}ns`
}

function submit() {
  Object.assign(errors, { id: '', name: '', url: '', ref_name: '', submit: '' })
  if (!isEditing.value && !form.id.trim()) errors.id = '请输入仓库源标识'
  if (!form.name.trim()) errors.name = '请输入仓库源名称'
  if (!form.url.trim()) errors.url = '请输入 Git URL'
  if (!form.ref_name.trim()) errors.ref_name = `请输入${form.ref_kind === 'tag' ? '标签' : '分支'}名称`
  if (!isEditing.value && (!form.signer_key_id.trim() || !form.signer_secret_ref.trim())) {
    errors.submit = '请输入签名密钥 ID 和签名密钥引用'
  }
  const signerKeyChanged = isEditing.value && form.signer_key_id.trim() !== (props.source?.signer_key_id || '')
  if (signerKeyChanged && !form.signer_secret_ref.trim()) {
    errors.submit = '更换签名密钥 ID 时必须同时提供签名密钥引用'
  }
  if (errors.id || errors.name || errors.url || errors.ref_name) return
  if (errors.submit) return

  const payload = {
    name: form.name.trim(),
    url: form.url.trim(),
    purpose: form.purpose,
    ref_kind: form.ref_kind,
    ref_name: form.ref_name.trim(),
    refresh_interval: form.refresh_interval.trim()
  }
  if (!isEditing.value) payload.id = form.id.trim()
  if (form.credential_ref.trim()) payload.credential_ref = form.credential_ref.trim()
  if (form.clear_credential) payload.credential_ref = ''
  if (!isEditing.value || form.signer_secret_ref.trim()) payload.signer_key_id = form.signer_key_id.trim()
  if (form.signer_secret_ref.trim()) payload.signer_secret_ref = form.signer_secret_ref.trim()
  emit('save', payload)
}
</script>

<style scoped>
.repository-form-overlay {
  position: fixed;
  inset: 0;
  z-index: var(--z-modal);
  display: grid;
  place-items: center;
  padding: var(--space-4);
  background: rgba(37, 23, 54, 0.42);
  backdrop-filter: blur(8px);
}

.repository-form {
  width: min(760px, 96vw);
  max-height: calc(100dvh - var(--space-8));
  overflow: auto;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  background: var(--color-bg-surface);
  box-shadow: var(--shadow-xl);
}

.repository-form__header {
  display: flex;
  justify-content: space-between;
  gap: var(--space-4);
  padding: var(--space-5) var(--space-6);
  border-bottom: 1px solid var(--color-border-subtle);
}

.repository-form__header h2,
.repository-form__header p { margin: 0; }
.repository-form__header h2 { font-size: var(--text-lg); }
.repository-form__header p { margin-top: var(--space-1); color: var(--color-text-muted); font-size: var(--text-sm); }

.repository-form__close {
  align-self: flex-start;
  border: 0;
  background: transparent;
  color: var(--color-text-muted);
  cursor: pointer;
}

.repository-form__body {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: var(--space-6);
}

.repository-form__field { display: flex; flex-direction: column; gap: var(--space-2); min-width: 0; }
.repository-form__field label,
.repository-form__field legend { color: var(--color-text-secondary); font-size: var(--text-sm); font-weight: var(--font-medium); }
.repository-form__field { border: 0; margin: 0; padding: 0; }

.repository-form__input {
  box-sizing: border-box;
  width: 100%;
  min-width: 0;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-primary);
  font-size: var(--text-sm);
}

.repository-form__segments { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--space-2); }
.repository-form__segment {
  display: flex;
  flex-direction: column;
  gap: 2px;
  align-items: flex-start;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  cursor: pointer;
  text-align: left;
}
.repository-form__segment small { color: var(--color-text-muted); }
.repository-form__segment--active { border-color: var(--color-primary); background: var(--color-primary-subtle); color: var(--color-primary); }
.repository-form__segments--compact .repository-form__segment { align-items: center; }

.repository-form__ref-row,
.repository-form__signer-row { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: var(--space-3); }
.repository-form__hint { margin: 0; color: var(--color-text-muted); font-size: var(--text-xs); }
.repository-form__check { display: flex; align-items: center; gap: var(--space-2); font-weight: normal !important; }
.repository-form__error { color: var(--color-danger); font-size: var(--text-xs); }
.repository-form__error--submit { margin: 0; }
.repository-form__footer { display: flex; justify-content: flex-end; gap: var(--space-2); padding-top: var(--space-2); }

@media (max-width: 680px) {
  .repository-form__ref-row,
  .repository-form__signer-row { grid-template-columns: 1fr; }
  .repository-form__body,
  .repository-form__header { padding: var(--space-4); }
}
</style>
