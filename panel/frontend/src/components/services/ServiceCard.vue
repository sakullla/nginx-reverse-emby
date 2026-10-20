<template>
  <article class="service-card" data-testid="service-card">
    <header class="service-card__header">
      <h2>{{ service.name || '未命名服务' }}</h2>
      <p data-testid="service-summary">{{ service.summary || '控制面没有返回摘要' }}</p>
    </header>
    <dl>
      <div>
        <dt>访问地址</dt>
        <dd>{{ service.entry_url || '—' }}</dd>
      </div>
      <div>
        <dt>入口节点</dt>
        <dd>{{ entryAgentLabel || service.entry_agent_id || '—' }}</dd>
      </div>
      <div>
        <dt>路径</dt>
        <dd data-testid="service-path">{{ pathLabel }}</dd>
      </div>
      <div>
        <dt>未完成环节</dt>
        <dd data-testid="service-steps">{{ steps.length ? steps.join('；') : '无' }}</dd>
      </div>
      <div v-if="service.switch_phase">
        <dt>切换阶段</dt>
        <dd>{{ service.switch_phase }}</dd>
      </div>
      <div>
        <dt>证书</dt>
        <dd data-testid="service-certificate">{{ certificate.label }}<template v-if="certificate.detail">：{{ certificate.detail }}</template></dd>
      </div>
      <div>
        <dt>流量</dt>
        <dd data-testid="service-traffic">{{ traffic.text }}<template v-if="traffic.detail"> · {{ traffic.detail }}</template></dd>
      </div>
    </dl>
    <ul v-if="probes.length" class="service-card__probes">
      <li v-for="(probe, index) in probes" :key="`${probe.agentId}-${probe.purpose}-${index}`" data-testid="service-probe">
        节点探测 {{ probe.agentId || '未知节点' }} {{ probe.text }}
        <template v-if="probe.target"> · {{ probe.target }}</template>
        <template v-if="probe.checkedAt"> · {{ probe.checkedAt }}</template>
        <template v-if="probe.verification"> · {{ probe.verification }}</template>
      </li>
    </ul>
    <p v-if="service.client_confirmation?.confirmed" class="service-card__confirm">用户确认页面可打开</p>
    <div class="service-card__actions">
      <a v-if="openHref" class="btn btn-secondary" :href="openHref" target="_blank" rel="noopener">打开</a>
      <button type="button" class="btn btn-secondary" @click="$emit('copy', service)">复制</button>
      <button type="button" class="btn btn-secondary" @click="$emit('detail', service)">详情</button>
      <button type="button" class="btn btn-secondary" @click="$emit('edit', service)">修改</button>
      <button type="button" class="btn btn-secondary" @click="$emit('delete', service)">删除</button>
    </div>
  </article>
</template>

<script setup>
import { computed } from 'vue'
import {
  certificateText,
  incompleteSteps,
  pathText,
  probeText,
  safeHttpHref,
  serviceTrafficText,
} from '../../utils/accessService.js'

const props = defineProps({
  service: { type: Object, required: true },
  entryAgentLabel: { type: String, default: '' },
})

defineEmits(['copy', 'detail', 'edit', 'delete'])

const pathLabel = computed(() => pathText(props.service?.path))
const steps = computed(() => incompleteSteps(props.service))
const certificate = computed(() => certificateText(props.service?.certificate))
const traffic = computed(() => serviceTrafficText(props.service?.traffic))
const probes = computed(() => (Array.isArray(props.service?.probes) ? props.service.probes : []).map((probe) => probeText(probe)))
const openHref = computed(() => safeHttpHref(props.service?.entry_url))
</script>

<style scoped>
.service-card {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  min-width: 0;
  padding: 1rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg, 12px);
  background: var(--color-bg-panel, var(--color-bg-surface));
}

.service-card__header h2,
.service-card__header p {
  margin: 0;
}

.service-card__header p,
.service-card__confirm,
.service-card dt {
  color: var(--color-text-secondary);
}

.service-card dl {
  display: grid;
  gap: 0.45rem;
  margin: 0;
}

.service-card dl div {
  display: grid;
  grid-template-columns: 6.5rem minmax(0, 1fr);
  gap: 0.5rem;
}

.service-card dd {
  margin: 0;
  overflow-wrap: anywhere;
}

.service-card__probes {
  margin: 0;
  padding-left: 1.1rem;
  color: var(--color-text-secondary);
  font-size: 0.875rem;
}

.service-card__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 0.4rem;
}
</style>
