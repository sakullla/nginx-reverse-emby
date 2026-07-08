<template>
  <div class="agent-detail" v-if="agent">
    <div class="agent-detail__back">
      <RouterLink to="/agents" class="back-link">← {{ detailLabels.backToAgents }}</RouterLink>
    </div>

    <BaseListCard class="agent-detail__summary-card" :title="agent.name" :status="statusTone" :clickable="false">
      <template #header-left>
        <AgentStatusBadge :agent="agent" class="agent-detail__status-badge" />
        <BaseBadge tone="primary" size="sm" class="agent-detail__mode-badge">{{ getModeLabel(agent.mode) }}</BaseBadge>
        <span class="agent-detail__meta-chip" :title="detailLabels.meta.version">
          {{ agent.version || agent.runtime_package_version || '—' }}
        </span>
        <span class="agent-detail__meta-chip" :title="detailLabels.meta.lastSeen">
          {{ agent.last_seen_at ? timeAgo(agent.last_seen_at) : '—' }}
        </span>
        <span v-if="agent.tags && agent.tags.length" class="agent-detail__tags">
          <BaseBadge
            v-for="tag in agent.tags.slice(0, 3)"
            :key="tag"
            tone="neutral"
            size="sm"
          >{{ tag }}</BaseBadge>
          <BaseBadge v-if="agent.tags.length > 3" tone="neutral" size="sm">+{{ agent.tags.length - 3 }}</BaseBadge>
        </span>
      </template>

      <template #header-right>
        <div class="agent-detail-actions">
          <BaseIconButton
            data-testid="detail-action-apply"
            tone="primary"
            :title="detailLabels.actions.applyConfig"
            :disabled="applying"
            @click="handleApplyConfig"
          >
            <span class="i-mdi-sync" aria-hidden="true" />
          </BaseIconButton>
          <BaseIconButton
            data-testid="detail-action-copy-join"
            tone="default"
            :title="detailLabels.actions.copyJoinCommand"
            @click="copyJoinCommand"
          >
            <span class="i-mdi-content-copy" aria-hidden="true" />
          </BaseIconButton>
          <BaseIconButton
            data-testid="detail-action-delete"
            tone="danger"
            :title="agent.value?.is_local ? '本地节点不可删除' : detailLabels.actions.deleteAgent"
            :disabled="agent.value?.is_local"
            @click="showDeleteConfirm"
          >
            <span class="i-mdi-delete" aria-hidden="true" />
          </BaseIconButton>
        </div>
      </template>

      <div class="agent-detail__summary-body">
        <div class="agent-detail__meta-rows">
          <p class="agent-detail__meta-row">
            <span class="agent-detail__meta-label">{{ detailLabels.meta.address }}</span>
            <span class="agent-detail__meta-value agent-detail__endpoint">{{ agent.agent_url || agent.last_seen_ip || '—' }}</span>
          </p>
        </div>

        <div class="agent-detail-metrics agent-detail__resource-metrics">
          <AgentMetricTile
            data-testid="detail-metric-cpu"
            icon="i-mdi-cpu-64-bit"
            :label="detailLabels.metrics.cpu"
            :value="cpuUsage(agentMetricsData)"
            :percent="agentMetricsData.cpu_usage_percent"
            :tone="barTone(agentMetricsData.cpu_usage_percent)"
          />
          <AgentMetricTile
            data-testid="detail-metric-memory"
            icon="i-mdi-memory"
            :label="detailLabels.metrics.memory"
            :value="bytesPair(agentMetricsData.memory_used_bytes, agentMetricsData.memory_total_bytes)"
            :percent="agentMetricsData.memory_usage_percent"
            :tone="barTone(agentMetricsData.memory_usage_percent)"
          />
          <AgentMetricTile
            data-testid="detail-metric-disk"
            icon="i-mdi-harddisk"
            :label="detailLabels.metrics.disk"
            :value="bytesPair(agentMetricsData.disk_used_bytes, agentMetricsData.disk_total_bytes)"
            :percent="agentMetricsData.disk_usage_percent"
            :tone="barTone(agentMetricsData.disk_usage_percent)"
          />
          <AgentMetricTile
            data-testid="detail-metric-network"
            icon="i-mdi-network"
            :label="detailLabels.metrics.network"
            :network-down="rate(networkMetrics?.rx_bytes_per_second)"
            :network-up="rate(networkMetrics?.tx_bytes_per_second)"
          />
        </div>

        <div class="agent-detail-metrics agent-detail__count-metrics">
          <StatCard
            tone="primary"
            :value="httpRulesCount"
            :label="detailLabels.metrics.httpRules"
            :to="rulesHttpTo"
          />
          <StatCard
            tone="success"
            :value="l4RulesCount"
            :label="detailLabels.metrics.l4Rules"
            :to="rulesL4To"
          />
          <StatCard
            tone="warning"
            :value="certificatesCount"
            :label="detailLabels.metrics.certificates"
            :to="certsTo"
          />
          <StatCard
            tone="primary"
            :value="relayListenersCount"
            :label="detailLabels.metrics.relayListeners"
            :to="listenersTo"
          />
          <StatCard
            :tone="syncStatusTone"
            :value="syncStatusLabel"
            :label="detailLabels.metrics.syncStatus"
          />
        </div>
      </div>
    </BaseListCard>

    <div v-if="agent.last_apply_status === 'failed' && agent.last_apply_message" class="agent-detail__error">
      <div class="error-block">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"/>
          <line x1="12" y1="8" x2="12" y2="12"/>
          <line x1="12" y1="16" x2="12.01" y2="16"/>
        </svg>
        <div class="error-block__content">
          <div class="error-block__title">同步失败</div>
          <div class="error-block__text">{{ agent.last_apply_message }}</div>
        </div>
      </div>
    </div>

    <div class="agent-detail__sections">
      <TrafficCollapsibleSection :title="detailLabels.sections.rules" :subtitle="rulesSubtitle" default-expanded>
        <BaseListCard class="rules-list-card" :clickable="false">
          <div class="rules-list">
            <div class="rules-list__header-row">
              <span class="rules-list__col rules-list__col--type">类型</span>
              <span class="rules-list__col rules-list__col--status">状态</span>
              <span class="rules-list__col rules-list__col--entry">入口地址</span>
              <span class="rules-list__col rules-list__col--backend">后端地址</span>
              <span class="rules-list__col rules-list__col--tags">标签</span>
            </div>

            <div
              v-for="rule in allRules"
              :key="`${rule._type}-${rule.id}`"
              class="rules-list__row"
              @click="navigateToRule(rule)"
            >
              <span class="rules-list__col rules-list__col--type" data-label="类型">
                <span class="rule-type-badge" :class="`rule-type-badge--${rule._type}`">{{ ruleTypeLabel(rule) }}</span>
              </span>
              <span class="rules-list__col rules-list__col--status" data-label="状态">
                <span class="rule-status-badge" :class="rule.enabled !== false ? 'rule-status-badge--enabled' : 'rule-status-badge--disabled'">{{ rule.enabled !== false ? detailLabels.ruleEnabled : detailLabels.ruleDisabled }}</span>
              </span>
              <span class="rules-list__col rules-list__col--entry" data-label="入口地址" :title="ruleEntry(rule)">{{ ruleEntry(rule) }}</span>
              <span class="rules-list__col rules-list__col--backend" data-label="后端地址" :title="ruleBackend(rule)">{{ ruleBackend(rule) }}</span>
              <span class="rules-list__col rules-list__col--tags" data-label="标签">
                <span v-if="rule.tags && rule.tags.length" class="rule-tags">
                  <span v-for="tag in rule.tags.slice(0, 3)" :key="tag" class="rule-tag">{{ tag }}</span>
                  <span v-if="rule.tags.length > 3" class="rule-tag rule-tag--more">+{{ rule.tags.length - 3 }}</span>
                </span>
                <span v-else class="rules-list__empty-cell">—</span>
              </span>
            </div>

            <p v-if="!allRules.length" class="empty-hint">{{ detailLabels.empty.rules }}</p>
          </div>
        </BaseListCard>
      </TrafficCollapsibleSection>

      <TrafficCollapsibleSection :title="detailLabels.sections.certificates" :subtitle="certificatesSubtitle">
        <BaseListCard class="rules-list-card" :clickable="false">
          <div class="simple-list">
            <div
              v-for="cert in certificates"
              :key="cert.id"
              class="simple-list__row"
            >
              <span class="simple-list__name">{{ cert.name || cert.domain || cert.id }}</span>
              <BaseBadge tone="neutral" size="sm">{{ cert.status || '—' }}</BaseBadge>
            </div>
            <p v-if="!certificates.length" class="empty-hint">{{ detailLabels.empty.certificates }}</p>
          </div>
        </BaseListCard>
      </TrafficCollapsibleSection>

      <TrafficCollapsibleSection :title="detailLabels.sections.relayListeners" :subtitle="relayListenersSubtitle">
        <BaseListCard class="rules-list-card" :clickable="false">
          <div class="simple-list">
            <div
              v-for="listener in relayListeners"
              :key="listener.id"
              class="simple-list__row"
            >
              <span class="simple-list__name">{{ listener.name || listener.listen_addr || listener.id }}</span>
              <BaseBadge tone="neutral" size="sm">{{ listener.enabled !== false ? detailLabels.ruleEnabled : detailLabels.ruleDisabled }}</BaseBadge>
            </div>
            <p v-if="!relayListeners.length" class="empty-hint">{{ detailLabels.empty.relayListeners }}</p>
          </div>
        </BaseListCard>
      </TrafficCollapsibleSection>

      <TrafficCollapsibleSection v-if="trafficStatsEnabled" :title="detailLabels.sections.traffic">
        <div class="traffic-sections">
          <BaseListCard class="traffic-card" :clickable="false">
            <template #header-left>
              <svg class="traffic-section-card__icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>
              </svg>
              <span class="traffic-section-card__title">监控</span>
            </template>
            <template #header-right>
              <span
                class="traffic-overview__status"
                :class="trafficSummary.blocked ? 'traffic-overview__status--danger' : 'traffic-overview__status--success'"
              >
                {{ trafficSummary.blocked ? '已阻断' : '正常' }}
              </span>
            </template>
            <TrafficSummaryCards
              :summary="trafficSummary"
              :direction="trafficPolicyForm.direction"
              :network-metrics="networkMetrics"
            />
            <div class="traffic-monitor__divider" />
            <div class="traffic-tab__trend">
              <div class="traffic-tab__trend-header">
                <span>流量趋势</span>
                <div class="traffic-trend__controls" role="group" aria-label="趋势粒度">
                  <button
                    v-for="option in trafficTrendGranularityOptions"
                    :key="option.value"
                    class="traffic-trend__mode traffic-trend__mode--large"
                    :class="{ 'traffic-trend__mode--active': trafficTrendGranularity === option.value }"
                    type="button"
                    @click="trafficTrendGranularity = option.value"
                  >
                    {{ option.label }}
                  </button>
                </div>
              </div>
              <TrafficTrendChart
                :points="trafficTrendPoints"
                :granularity="trafficTrendGranularity"
                :quota-bytes="trafficSummary.monthly_quota_bytes ?? null"
                :refresh-key="agentStatsRefreshKey"
              />
            </div>
          </BaseListCard>

          <BaseListCard class="traffic-card" :clickable="false">
            <template #header-left>
              <svg class="traffic-section-card__icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <circle cx="11" cy="11" r="8"/>
                <line x1="21" y1="21" x2="16.65" y2="16.65"/>
              </svg>
              <span class="traffic-section-card__title">分析</span>
            </template>
            <div class="traffic-tab__breakdown">
              <TrafficBreakdownTable :tabs="trafficBreakdownTabs" :clickable="true" @click-row="openBreakdownTrendModal" />
            </div>
          </BaseListCard>

          <BaseListCard class="traffic-card" :clickable="false">
            <template #header-left>
              <svg class="traffic-section-card__icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <circle cx="12" cy="12" r="3"/>
                <path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 0 1 0 2.83 2 2 0 0 1-2.83 0l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-2 2 2 2 0 0 1-2-2v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 0 1-2.83 0 2 2 0 0 1 0-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1-2-2 2 2 0 0 1 2-2h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 0 1 0-2.83 2 2 0 0 1 2.83 0l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 2-2 2 2 0 0 1 2 2v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 0 1 2.83 0 2 2 0 0 1 0 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 2 2 2 2 0 0 1-2 2h-.09a1.65 1.65 0 0 0-1.51 1z"/>
              </svg>
              <span class="traffic-section-card__title">管理</span>
            </template>
            <div class="traffic-maintenance">
              <TrafficPolicyForm v-model="trafficPolicyForm" :saving="updateTrafficPolicyMutation.isPending.value || updateAgent.isPending.value" @save="saveTrafficPolicy" />
              <div class="traffic-maintenance__divider" />
              <TrafficHistoryManager
                :policy="trafficPolicyForm"
                :calibrating="calibrateTrafficMutation.isPending.value"
                :cleaning="cleanupTrafficMutation.isPending.value"
                @calibrate="calibrateModalVisible = true"
                @calibrate-zero="showCalibrateZeroConfirm"
                @cleanup="showCleanupConfirm"
              />
            </div>
          </BaseListCard>
        </div>

        <TrafficTrendModal
          v-model:visible="trendModal.visible"
          :agent-id="agentId"
          :scope-type="trendModal.scopeType"
          :scope-id="trendModal.scopeId"
          :scope-label="trendModal.scopeLabel"
          :direction="trafficPolicyForm.direction"
        />
        <TrafficCalibrateModal
          v-model:visible="calibrateModalVisible"
          :agent-id="agentId"
          :current-used-bytes="trafficSummary.used_bytes ?? 0"
          :cycle-start="trafficSummary.cycle_start ?? ''"
          :cycle-end="trafficSummary.cycle_end ?? ''"
          @confirm="onCalibrateConfirm"
        />
        <DeleteConfirmDialog
          :show="confirmDialog.visible"
          :title="confirmDialog.title"
          :message="confirmDialog.message"
          :confirm-text="confirmDialog.confirmText"
          :loading="confirmDialog.loading"
          @confirm="onConfirmDialogConfirm"
          @cancel="confirmDialog.visible = false"
        />
      </TrafficCollapsibleSection>

      <TrafficCollapsibleSection :title="detailLabels.sections.systemInfo">
        <div class="info-sections">
          <BaseListCard class="info-card" :title="detailLabels.systemCards.package" :clickable="false">
            <div class="info-grid">
              <div class="info-row info-row--clean"><span>版本</span><span>{{ agent.version || agent.runtime_package_version || '—' }}</span></div>
              <div class="info-row info-row--clean"><span>平台</span><span>{{ agent.runtime_package_platform || agent.platform || '—' }}</span></div>
              <div class="info-row info-row--clean"><span>架构</span><span>{{ agent.runtime_package_arch || '—' }}</span></div>
              <div class="info-row info-row--clean"><span>运行包 SHA</span><span :title="agent.runtime_package_sha256 || ''">{{ shortSha(agent.runtime_package_sha256) }}</span></div>
              <div class="info-row info-row--clean"><span>目标包 SHA</span><span :title="agent.desired_package_sha256 || ''">{{ shortSha(agent.desired_package_sha256) }}</span></div>
              <div class="info-row info-row--clean"><span>包状态</span><span>{{ packageStatusLabel(agent.package_sync_status) }}</span></div>
            </div>
          </BaseListCard>

          <BaseListCard class="info-card" :title="detailLabels.systemCards.identity" :clickable="false">
            <div class="info-grid">
              <div class="info-row info-row--clean"><span>角色</span><span>{{ getModeLabel(agent.mode) }}</span></div>
              <div class="info-row info-row--clean"><span>IP</span><span>{{ agent.last_seen_ip || '—' }}</span></div>
              <div class="info-row info-row--clean"><span>最后活跃</span><span>{{ agent.last_seen_at ? new Date(agent.last_seen_at).toLocaleString() : '—' }}</span></div>
            </div>
          </BaseListCard>

          <BaseListCard class="info-card" :title="detailLabels.systemCards.sync" :clickable="false">
            <div class="info-grid">
              <div class="info-row info-row--clean"><span>同步状态</span><span>{{ agent.last_apply_status || '—' }}</span></div>
              <div v-if="agent.last_apply_message" class="info-row info-row--clean"><span>同步消息</span><span>{{ agent.last_apply_message }}</span></div>
            </div>
          </BaseListCard>
        </div>
      </TrafficCollapsibleSection>

      <TrafficCollapsibleSection
        :title="detailLabels.sections.syncEvents"
        :subtitle="syncStatusLabel"
        :default-expanded="agent.last_apply_status === 'failed'"
      >
        <BaseListCard class="info-card" :clickable="false">
          <div class="info-grid">
            <div class="info-row info-row--clean"><span>{{ detailLabels.sync.status }}</span><span>{{ agent.last_apply_status || '—' }}</span></div>
            <div class="info-row info-row--clean"><span>{{ detailLabels.sync.message }}</span><span>{{ agent.last_apply_message || '—' }}</span></div>
            <div class="info-row info-row--clean"><span>{{ detailLabels.sync.time }}</span><span>{{ agent.last_apply_at ? new Date(agent.last_apply_at).toLocaleString() : '—' }}</span></div>
          </div>
        </BaseListCard>
      </TrafficCollapsibleSection>
    </div>
  </div>
  <div v-else-if="isLoading" class="agent-detail__loading">
    <div class="spinner"></div>
  </div>
  <div v-else class="agent-detail__not-found">
    <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
      <circle cx="12" cy="12" r="10"/>
      <line x1="12" y1="8" x2="12" y2="12"/>
      <line x1="12" y1="16" x2="12.01" y2="16"/>
    </svg>
    <p>节点不存在或已删除</p>
    <RouterLink to="/agents" class="btn btn-secondary">返回节点管理</RouterLink>
  </div>
</template>

<script setup>
import { ref, computed, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useQuery } from '@tanstack/vue-query'
import AgentStatusBadge from '../components/AgentStatusBadge.vue'
import BaseListCard from '../components/base/BaseListCard.vue'
import BaseBadge from '../components/base/BaseBadge.vue'
import BaseIconButton from '../components/base/BaseIconButton.vue'
import AgentMetricTile from '../components/AgentMetricTile.vue'
import StatCard from '../components/base/StatCard.vue'
import TrafficCollapsibleSection from '../components/traffic/TrafficCollapsibleSection.vue'
import { useRules } from '../hooks/useRules'
import { useL4Rules } from '../hooks/useL4Rules'
import { useCertificates } from '../hooks/useCertificates'
import { useRelayListeners } from '../hooks/useRelayListeners'
import { useAgents, useDeleteAgent, useUpdateAgent } from '../hooks/useAgents'
import { applyConfig, fetchAgentStats, fetchSystemInfo } from '../api'
import { useJoinCommand } from '../composables/useJoinCommand'
import { useCalibrateTraffic, useCleanupTraffic, useTrafficPolicy, useTrafficSummary, useTrafficTrend, useUpdateTrafficPolicy } from '../hooks/useTraffic'
import { messageStore } from '../stores/messages'
import { buildOutboundProxyPayload } from './outboundProxyURL'
import { getAgentStatus, getAgentStatusLabel, getModeLabel, timeAgo } from '../utils/agentHelpers.js'
import { barTone, bytesPair, cpuUsage, rate } from '../utils/agentMetrics.js'
import { agentDetailLabels } from '../constants/agentDetailLabels'
import {
  accountedBytes,
  formatBytes,
  formatQuota,
  normalizeTrafficBucket,
  normalizeTrafficPolicy,
  normalizeTrafficTrendPoints
} from '../utils/trafficStats.js'
import TrafficTrendChart from '../components/traffic/TrafficTrendChart.vue'
import TrafficTrendModal from '../components/traffic/TrafficTrendModal.vue'
import TrafficSummaryCards from '../components/traffic/TrafficSummaryCards.vue'
import TrafficBreakdownTable from '../components/traffic/TrafficBreakdownTable.vue'
import TrafficPolicyForm from '../components/traffic/TrafficPolicyForm.vue'
import TrafficHistoryManager from '../components/traffic/TrafficHistoryManager.vue'
import TrafficCalibrateModal from '../components/traffic/TrafficCalibrateModal.vue'
import DeleteConfirmDialog from '../components/DeleteConfirmDialog.vue'

const route = useRoute()
const router = useRouter()
const agentId = computed(() => route.params.id)
const detailLabels = agentDetailLabels

const { data: agentsData, isLoading } = useAgents()
const agent = computed(() => agentsData.value?.find(a => a.id === agentId.value))
const updateAgent = useUpdateAgent()
const deleteAgent = useDeleteAgent()
const { copyCommand: copyJoinCommand } = useJoinCommand()
const applying = ref(false)
const outboundProxyURL = ref('')

const { data: httpRulesData } = useRules(agentId)
const httpRules = computed(() => httpRulesData.value ?? [])
const httpRulesCount = computed(() => httpRules.value.length)

const { data: l4RulesData } = useL4Rules(agentId)
const l4Rules = computed(() => l4RulesData.value ?? [])
const l4RulesCount = computed(() => l4Rules.value.length)

const { data: certificatesData } = useCertificates(agentId)
const certificates = computed(() => certificatesData.value ?? [])
const certificatesCount = computed(() => certificates.value.length)

const { data: relayListenersData } = useRelayListeners(agentId)
const relayListeners = computed(() => relayListenersData.value ?? [])
const relayListenersCount = computed(() => relayListeners.value.length)

const allRules = computed(() => [
  ...httpRules.value.map((rule) => ({ ...rule, _type: 'http' })),
  ...l4Rules.value.map((rule) => ({ ...rule, _type: 'l4' }))
])

const { data: agentStatsData, dataUpdatedAt: agentStatsUpdatedAt } = useQuery({
  queryKey: ['agent-stats', agentId],
  queryFn: () => fetchAgentStats(agentId.value),
  enabled: () => !!agentId.value,
  refetchInterval: 10_000
})
const { data: systemInfoData, isSuccess: isSystemInfoLoaded } = useQuery({
  queryKey: ['system-info'],
  queryFn: fetchSystemInfo
})
const agentStats = computed(() => agentStatsData.value ?? {})
const agentStatsRefreshKey = computed(() => agentStatsUpdatedAt.value || 0)
const systemInfo = computed(() => systemInfoData.value ?? {})
const trafficStatsEnabled = computed(() => isSystemInfoLoaded.value && systemInfo.value?.traffic_stats_enabled !== false)
const trafficPolicyQuery = useTrafficPolicy(computed(() => trafficStatsEnabled.value ? agentId.value : null))
const trafficSummaryQuery = useTrafficSummary(computed(() => trafficStatsEnabled.value ? agentId.value : null))
const trafficTrendGranularityOptions = [
  { value: 'hour', label: '小时' },
  { value: 'day', label: '日' },
  { value: 'month', label: '月' }
]
const trafficTrendGranularity = ref('day')
const trafficTrendQuery = useTrafficTrend(
  computed(() => trafficStatsEnabled.value ? agentId.value : null),
  computed(() => ({ granularity: trafficTrendGranularity.value }))
)
const updateTrafficPolicyMutation = useUpdateTrafficPolicy(computed(() => agentId.value))
const calibrateTrafficMutation = useCalibrateTraffic(computed(() => agentId.value))
const cleanupTrafficMutation = useCleanupTraffic(computed(() => agentId.value))
const quotaUnits = [
  { value: 'B', label: 'B', factor: 1 },
  { value: 'KiB', label: 'KiB', factor: 1024 },
  { value: 'MiB', label: 'MiB', factor: 1024 ** 2 },
  { value: 'GiB', label: 'GiB', factor: 1024 ** 3 },
  { value: 'TiB', label: 'TiB', factor: 1024 ** 4 }
]
const trafficPolicyForm = ref(normalizeTrafficPolicyForm())
const trafficSummary = computed(() => trafficSummaryQuery.data.value ?? {})
const trafficTrendPoints = computed(() => normalizeTrafficTrendPoints(trafficTrendQuery.data.value ?? [], trafficPolicyForm.value.direction))
const trafficBreakdownTabs = computed(() => [
  {
    id: 'http',
    label: 'HTTP',
    rows: normalizeTrafficBreakdownRows(trafficSummary.value.http_rules)
  },
  {
    id: 'l4',
    label: 'L4',
    rows: normalizeTrafficBreakdownRows(trafficSummary.value.l4_rules)
  },
  {
    id: 'relay',
    label: 'Relay',
    rows: normalizeTrafficBreakdownRows(trafficSummary.value.relay_listeners)
  },
  {
    id: 'host',
    label: '主机接口',
    rows: normalizeTrafficBreakdownRows([
      ...(trafficSummary.value.host_total ? [trafficSummary.value.host_total] : []),
      ...(trafficSummary.value.host_interfaces || [])
    ])
  }
].filter(t => t.rows.length > 0))

const agentMetricsData = computed(() => metricsFromAgentStats(agentStats.value) || agent.value?.monitor?.metrics || agent.value?.metrics || {})
const networkMetrics = computed(() => agentMetricsData.value.network || null)

const STATUS_TONE = {
  online: 'success',
  offline: 'neutral',
  failed: 'danger',
  pending: 'warning',
}

const statusTone = computed(() => STATUS_TONE[getAgentStatus(agent.value)] || 'neutral')

const rulesHttpTo = computed(() => ({ path: '/rules', query: { agentId: agentId.value } }))
const rulesL4To = computed(() => ({ path: '/l4', query: { agentId: agentId.value } }))
const certsTo = computed(() => ({ path: '/certs', query: { agentId: agentId.value } }))
const listenersTo = computed(() => ({ path: '/relay-listeners', query: { agentId: agentId.value } }))

const syncStatusTone = computed(() => {
  const status = agent.value?.last_apply_status
  if (status === 'success') return 'success'
  if (status === 'failed') return 'danger'
  if (status === 'pending') return 'warning'
  return 'primary'
})
const syncStatusLabel = computed(() => agent.value?.last_apply_status || '—')

const rulesSubtitle = computed(() => `${httpRulesCount.value} HTTP / ${l4RulesCount.value} L4`)
const certificatesSubtitle = computed(() => String(certificatesCount.value))
const relayListenersSubtitle = computed(() => String(relayListenersCount.value))

function metricsFromAgentStats(stats = {}) {
  if (stats?.metrics) return stats.metrics
  const host = stats?.host
  if (!host || typeof host !== 'object') return null

  const metrics = {}
  let hasMetric = false
  const setMetric = (key, value) => {
    if (value === undefined || value === null) return
    metrics[key] = value
    hasMetric = true
  }

  setMetric('cpu_usage_percent', host.cpu?.usage_percent)
  setMetric('cpu_used_cores', host.cpu?.used_cores)
  setMetric('cpu_total_cores', host.cpu?.total_cores)
  setMetric('memory_usage_percent', host.memory?.usage_percent)
  setMetric('memory_used_bytes', host.memory?.used_bytes)
  setMetric('memory_total_bytes', host.memory?.total_bytes)
  setMetric('disk_usage_percent', host.disk?.usage_percent)
  setMetric('disk_used_bytes', host.disk?.used_bytes)
  setMetric('disk_total_bytes', host.disk?.total_bytes)

  const networkTotal = host.network?.total
  if (networkTotal && typeof networkTotal === 'object' && Object.keys(networkTotal).length > 0) {
    metrics.network = { ...networkTotal }
    hasMetric = true
  }

  return hasMetric ? metrics : null
}

const trendModal = ref({ visible: false, scopeType: '', scopeId: '', scopeLabel: '' })
const calibrateModalVisible = ref(false)
const confirmDialog = ref({ visible: false, type: '', title: '', message: '', confirmText: '', loading: false })

function openBreakdownTrendModal(row) {
  trendModal.value = {
    visible: true,
    scopeType: row.scope_type,
    scopeId: row.scope_id,
    scopeLabel: trafficBreakdownLabel(row)
  }
}

watch(agent, (value) => {
  outboundProxyURL.value = value?.outbound_proxy_url || ''
  if (value) {
    trafficPolicyForm.value = {
      ...trafficPolicyForm.value,
      traffic_stats_interval: value.traffic_stats_interval || ''
    }
  }
}, { immediate: true })

watch([trafficPolicyQuery.data, trafficStatsEnabled], ([policy, enabled]) => {
  if (enabled && policy) {
    trafficPolicyForm.value = normalizeTrafficPolicyForm(policy, agent.value?.traffic_stats_interval || '')
  }
}, { immediate: true })

async function saveOutboundProxy() {
  if (!agent.value || agent.value.is_local) return
  let payload
  try {
    payload = buildOutboundProxyPayload(agent.value.outbound_proxy_url, outboundProxyURL.value)
  } catch (error) {
    messageStore.warning(error.message, '出网代理密码已隐藏')
    return
  }
  if (Object.keys(payload).length === 0) return
  await updateAgent.mutateAsync({
    agentId: agent.value.id,
    payload
  })
}

async function handleApplyConfig() {
  if (!agent.value || applying.value) return
  applying.value = true
  try {
    await applyConfig(agent.value.id)
    messageStore.success('配置已推送')
  } catch (error) {
    messageStore.error(error, '推送配置失败')
  } finally {
    applying.value = false
  }
}

function showDeleteConfirm() {
  if (!agent.value || agent.value.is_local) return
  confirmDialog.value = {
    visible: true,
    type: 'delete-agent',
    title: '确认删除节点',
    message: `删除节点「${agent.value.name}」将同时注销其身份，此操作不可撤销。`,
    confirmText: '删除',
    loading: false
  }
}

async function saveTrafficPolicy() {
  if (!agent.value || !trafficStatsEnabled.value) return
  if (!isIntegerInRange(trafficPolicyForm.value.cycle_start_day, 1, 28)) {
    messageStore.warning('月周期起始日必须是 1 到 28 的整数')
    return
  }
  const monthlyQuotaBytes = quotaInputToBytes(trafficPolicyForm.value.monthly_quota_value, trafficPolicyForm.value.monthly_quota_unit)
  if (monthlyQuotaBytes === undefined) {
    messageStore.warning('月额度必须为空或非负数字')
    return
  }
  if (!isPositiveInteger(trafficPolicyForm.value.hourly_retention_days)) {
    messageStore.warning('小时保留必须是正整数')
    return
  }
  if (!isPositiveInteger(trafficPolicyForm.value.daily_retention_months)) {
    messageStore.warning('日保留必须是正整数')
    return
  }
  if (!isBlankOrPositiveInteger(trafficPolicyForm.value.monthly_retention_months)) {
    messageStore.warning('月保留必须为空或正整数')
    return
  }
  const payload = normalizeTrafficPolicy({
    ...trafficPolicyForm.value,
    monthly_quota_bytes: monthlyQuotaBytes
  })
  await updateTrafficPolicyMutation.mutateAsync(payload)

  const nextInterval = String(trafficPolicyForm.value.traffic_stats_interval || '').trim()
  if (!agent.value.is_local && nextInterval !== (agent.value.traffic_stats_interval || '')) {
    await updateAgent.mutateAsync({
      agentId: agent.value.id,
      payload: { traffic_stats_interval: nextInterval }
    })
  }
}

async function onCalibrateConfirm(usedBytes) {
  if (!agent.value || !trafficStatsEnabled.value) return
  await calibrateTrafficMutation.mutateAsync({ used_bytes: usedBytes })
}

async function calibrateTrafficSummary() {
  if (!agent.value || !trafficStatsEnabled.value) return
  calibrateModalVisible.value = true
}

function showCalibrateZeroConfirm() {
  if (!agent.value || !trafficStatsEnabled.value) return
  confirmDialog.value = {
    visible: true,
    type: 'calibrate-zero',
    title: '确认归零',
    message: '将当前计费周期的已用流量重置为零，此操作不可撤销。',
    confirmText: '确认归零',
    loading: false
  }
}

function showCleanupConfirm() {
  if (!agent.value || !trafficStatsEnabled.value) return
  confirmDialog.value = {
    visible: true,
    type: 'cleanup',
    title: '确认清理',
    message: '按保留策略清理过期历史数据，此操作不可撤销。',
    confirmText: '确认清理',
    loading: false
  }
}

async function onConfirmDialogConfirm() {
  if (!agent.value) return
  confirmDialog.value.loading = true
  try {
    if (confirmDialog.value.type === 'calibrate-zero') {
      await calibrateTrafficMutation.mutateAsync({ used_bytes: 0 })
    } else if (confirmDialog.value.type === 'cleanup') {
      await cleanupTrafficMutation.mutateAsync()
    } else if (confirmDialog.value.type === 'delete-agent') {
      await deleteAgent.mutateAsync(agent.value.id)
      router.push('/agents')
    }
  } finally {
    confirmDialog.value.visible = false
    confirmDialog.value.loading = false
  }
}

function normalizeTrafficPolicyForm(policy = {}, trafficStatsInterval = '') {
  const normalized = normalizeTrafficPolicy(policy)
  const quota = bytesToQuotaInput(normalized.monthly_quota_bytes)
  return {
    ...normalized,
    monthly_quota_value: quota.value,
    monthly_quota_unit: quota.unit,
    traffic_stats_interval: trafficStatsInterval
  }
}

function bytesToQuotaInput(bytes) {
  if (bytes == null) {
    return { value: '', unit: 'GiB' }
  }
  const number = Number(bytes)
  if (!Number.isFinite(number) || number < 0) {
    return { value: '', unit: 'GiB' }
  }
  let selectedUnit = quotaUnits[0]
  for (const unit of quotaUnits) {
    if (number >= unit.factor) {
      selectedUnit = unit
    }
  }
  const value = number / selectedUnit.factor
  return {
    value: Number.isInteger(value) ? String(value) : String(Number(value.toFixed(3))),
    unit: selectedUnit.value
  }
}

function quotaInputToBytes(value, unitValue) {
  const rawValue = String(value ?? '').trim()
  if (rawValue === '') return null
  const number = Number(rawValue)
  const unit = quotaUnits.find((item) => item.value === unitValue)
  if (!Number.isFinite(number) || number < 0 || !unit) return undefined
  const bytes = number * unit.factor
  if (!Number.isFinite(bytes) || bytes < 0) return undefined
  return Math.round(bytes)
}

function parseByteInput(value) {
  const rawValue = String(value ?? '').trim()
  if (rawValue === '') return undefined
  const match = rawValue.match(/^([0-9]+(?:\.[0-9]+)?)\s*([kmgt]?i?b)?$/i)
  if (!match) return undefined
  const number = Number(match[1])
  const unitValue = normalizeByteUnit(match[2] || 'B')
  const unit = quotaUnits.find((item) => item.value === unitValue)
  if (!Number.isFinite(number) || number < 0 || !unit) return undefined
  const bytes = number * unit.factor
  if (!Number.isFinite(bytes) || bytes < 0) return undefined
  return Math.round(bytes)
}

function normalizeByteUnit(value) {
  switch (String(value || '').trim().toLowerCase()) {
    case 'b':
      return 'B'
    case 'kib':
    case 'kb':
      return 'KiB'
    case 'mib':
    case 'mb':
      return 'MiB'
    case 'gib':
    case 'gb':
      return 'GiB'
    case 'tib':
    case 'tb':
      return 'TiB'
    default:
      return ''
  }
}

function normalizeTrafficBreakdownRows(rows) {
  if (!Array.isArray(rows)) return []
  return rows.map((row) => ({
    scope_type: String(row?.scope_type || ''),
    scope_id: String(row?.scope_id || ''),
    rx_bytes: Number(row?.rx_bytes) || 0,
    tx_bytes: Number(row?.tx_bytes) || 0,
    accounted_bytes: Number(row?.accounted_bytes) || 0
  })).filter((row) => row.accounted_bytes > 0 || row.rx_bytes > 0 || row.tx_bytes > 0)
}

function trafficBreakdownKey(row) {
  return `${row.scope_type || 'scope'}-${row.scope_id || 'aggregate'}`
}

function trafficBreakdownLabel(row) {
  switch (row.scope_type) {
    case 'http':
      return 'HTTP'
    case 'l4':
      return 'L4'
    case 'relay':
      return 'Relay'
    case 'http_rule':
      return `HTTP 规则 #${row.scope_id}`
    case 'l4_rule':
      return `L4 规则 #${row.scope_id}`
    case 'relay_listener':
      return `Relay 监听 #${row.scope_id}`
    default:
      return row.scope_id ? `${row.scope_type} #${row.scope_id}` : row.scope_type || '-'
  }
}

function trafficDirectionLabel(direction) {
  switch (String(direction || 'both').toLowerCase()) {
    case 'rx':
      return '入站'
    case 'tx':
      return '出站'
    case 'max':
      return '取最大值'
    case 'both':
    default:
      return '双向'
  }
}

function isBlankOrPositiveInteger(value) {
  if (value == null || value === '') return true
  return isPositiveInteger(value)
}

function isPositiveInteger(value) {
  const number = Number(value)
  return Number.isInteger(number) && number > 0
}

function isIntegerInRange(value, min, max) {
  const number = Number(value)
  return Number.isInteger(number) && number >= min && number <= max
}

function formatCycle(start, end) {
  if (!start || !end) return '—'
  return `${new Date(start).toLocaleDateString()} - ${new Date(end).toLocaleDateString()}`
}

function formatTrendLabel(bucketStart) {
  if (!bucketStart) return '—'
  const date = new Date(bucketStart)
  if (Number.isNaN(date.getTime())) return '—'
  if (trafficTrendGranularity.value === 'hour') {
    return date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })
  }
  if (trafficTrendGranularity.value === 'month') {
    return date.toLocaleDateString('zh-CN', { year: '2-digit', month: 'short' })
  }
  return date.toLocaleDateString('zh-CN', { month: 'short', day: 'numeric' })
}

function trafficTrendKey(point, index) {
  return `${point.bucket_start || 'point'}-${index}`
}

function trendBarHeight(bytes) {
  const value = Number(bytes) || 0
  const max = Math.max(...trafficTrendPoints.value.map((point) => point.accounted_bytes), 1)
  const ratio = Math.max(0.08, value / max)
  return `${Math.round(ratio * 100)}%`
}

function firstHttpBackend(rule) {
  if (Array.isArray(rule?.backends) && rule.backends.length > 0) {
    const first = String(rule.backends[0]?.url || '').trim()
    if (first) return first
  }
  return ''
}

function formatHttpBackend(rule) {
  const first = firstHttpBackend(rule)
  const count = Array.isArray(rule?.backends) && rule.backends.length > 0 ? rule.backends.length : (first ? 1 : 0)
  if (!first) return '-'
  return count > 1 ? `${first} +${count - 1}` : first
}

function firstL4Backend(rule) {
  if (Array.isArray(rule?.backends) && rule.backends.length > 0) {
    const backend = rule.backends[0]
    if (backend?.host && backend?.port) return `${backend.host}:${backend.port}`
  }
  return ''
}

function formatL4Backend(rule) {
  const first = firstL4Backend(rule)
  const count = Array.isArray(rule?.backends) && rule.backends.length > 0 ? rule.backends.length : (first ? 1 : 0)
  if (!first) return '-'
  return count > 1 ? `${first} +${count - 1}` : first
}

function ruleTypeLabel(rule) {
  return rule._type === 'http' ? 'HTTP' : 'L4'
}

function ruleEntry(rule) {
  if (rule._type === 'http') return rule.frontend_url || '—'
  const protocol = rule.protocol || 'tcp'
  const host = rule.listen_host || '0.0.0.0'
  const port = rule.listen_port ?? '—'
  return `${protocol}://${host}:${port}`
}

function ruleBackend(rule) {
  if (rule._type === 'http') return formatHttpBackend(rule)
  return formatL4Backend(rule)
}

function navigateToRule(rule) {
  const path = rule._type === 'http' ? '/rules' : '/l4'
  router.push({
    path,
    query: {
      agentId: agentId.value,
      search: `#id=${rule.id}`
    }
  })
}

function shortSha(value) {
  const sha = String(value || '').trim()
  if (!sha) return '—'
  return sha.length > 12 ? `${sha.slice(0, 12)}...` : sha
}

function packageStatusLabel(status) {
  if (status === 'aligned') return '已同步'
  if (status === 'pending') return '待更新'
  return '—'
}
</script>

<style scoped>
.agent-detail {
  max-width: 75rem;
  margin: 0 auto;
}

.agent-detail__back { margin-bottom: 1rem; }
.back-link { color: var(--color-text-secondary); font-size: 0.875rem; text-decoration: none; }
.back-link:hover { color: var(--color-primary); }

.agent-detail__summary-card {
  position: relative;
  overflow: hidden;
  margin-bottom: 0.75rem;
}

.agent-detail__summary-card::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: var(--space-1);
  background: var(--color-text-muted);
  transition: background var(--duration-fast) var(--ease-default);
}

.agent-detail__summary-card[data-status="success"]::before { background: var(--color-success); }
.agent-detail__summary-card[data-status="warning"]::before { background: var(--color-warning); }
.agent-detail__summary-card[data-status="danger"]::before { background: var(--color-danger); }
.agent-detail__summary-card[data-status="neutral"]::before { background: var(--color-text-muted); }

.agent-detail__summary-body {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.agent-detail__status-badge { flex-shrink: 0; }

.agent-detail__mode-badge { flex-shrink: 0; }

.agent-detail__meta-chip {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-1) var(--space-2);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  line-height: 1;
}

.agent-detail__tags {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  flex-wrap: wrap;
}

.agent-detail__resource-metrics {
  margin-bottom: var(--space-1);
}

.agent-detail__count-metrics {
  grid-template-columns: repeat(auto-fit, minmax(var(--space-20), 1fr));
}

.agent-detail__meta-row {
  display: flex;
  align-items: baseline;
  gap: 0.375rem;
  margin-bottom: 0.125rem;
}

.agent-detail__meta-label {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
  flex-shrink: 0;
}

.agent-detail__meta-value {
  flex: 1;
  min-width: 0;
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.agent-detail__endpoint {
  font-family: var(--font-mono);
}

.agent-detail__error { margin-bottom: 1rem; }
.error-block { display: flex; align-items: flex-start; gap: 0.75rem; padding: 1rem; background: var(--color-danger-50); border: 1px solid var(--color-danger); border-radius: var(--radius-lg); color: var(--color-danger); }
.error-block svg { flex-shrink: 0; margin-top: 1px; }
.error-block__title { font-weight: 600; font-size: 0.875rem; margin-bottom: 0.25rem; }
.error-block__text { font-size: 0.8125rem; line-height: 1.5; color: var(--color-danger); opacity: 0.95; word-break: break-word; }

.agent-detail__sections { display: flex; flex-direction: column; gap: var(--space-3); }

.simple-list { display: flex; flex-direction: column; gap: var(--space-2); }
.simple-list__row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  font-size: var(--text-sm);
}
.simple-list__row:hover { background: var(--color-bg-hover); }
.simple-list__name {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-primary);
}

.rules-list { display: flex; flex-direction: column; gap: 0.25rem; }
.rules-list__header-row,
.rules-list__row {
  display: grid;
  grid-template-columns: 3.5rem 3.5rem 1.2fr 1fr 1fr;
  grid-template-areas: 'type status entry backend tags';
  align-items: center;
  gap: 0.75rem;
  padding: 0.625rem 0.75rem;
  font-size: 0.8125rem;
}
.rules-list__header-row { font-weight: 600; color: var(--color-text-secondary); border-bottom: 1px solid var(--color-border-subtle); padding-bottom: 0.5rem; }
.rules-list__row { cursor: pointer; border-radius: var(--radius-lg); transition: background-color 150ms ease; }
.rules-list__row:hover { background: var(--color-bg-subtle); }
.rules-list__col { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.rules-list__col--type { grid-area: type; }
.rules-list__col--status { grid-area: status; }
.rules-list__col--entry { grid-area: entry; }
.rules-list__col--backend { grid-area: backend; }
.rules-list__col--tags { grid-area: tags; display: flex; justify-content: flex-end; }
.rules-list__empty-cell { color: var(--color-text-muted); }
@media (max-width: 900px) {
  .rules-list__header-row { display: none; }
  .rules-list__row {
    grid-template-columns: auto auto 1fr;
    grid-template-areas:
      'type status tags'
      'entry entry entry'
      'backend backend backend';
    gap: 0.375rem 0.5rem;
    padding: 0.75rem;
    background: var(--color-bg-surface);
    border: 1px solid var(--color-border-subtle);
  }
  .rules-list__row:hover { background: var(--color-bg-hover); }
  .rules-list__col--entry,
  .rules-list__col--backend {
    display: flex;
    align-items: center;
    gap: 0.375rem;
    font-size: 0.8125rem;
    color: var(--color-text-primary);
    white-space: normal;
    word-break: break-all;
    overflow: visible;
  }
  .rules-list__col--entry::before,
  .rules-list__col--backend::before {
    content: attr(data-label) ':';
    flex-shrink: 0;
    font-size: 0.75rem;
    color: var(--color-text-tertiary);
  }
  .rules-list__col--tags { justify-content: flex-start; }
  .rule-tags { justify-content: flex-start; }
}
.rule-type-badge { display: inline-flex; align-items: center; justify-content: center; padding: 0.15rem 0.4rem; border-radius: var(--radius-sm); font-size: 0.6875rem; font-weight: 700; text-transform: uppercase; }
.rule-type-badge--http { background: var(--color-primary-subtle); color: var(--color-primary); }
.rule-type-badge--l4 { background: var(--color-success-subtle, #dcfce7); color: var(--color-success, #16a34a); }
.rule-status-badge { font-size: 0.75rem; font-weight: 500; }
.rule-status-badge--enabled { color: var(--color-success); }
.rule-status-badge--disabled { color: var(--color-text-muted); }
.rule-tags { display: flex; gap: 0.375rem; flex-wrap: wrap; justify-content: flex-end; }
.rule-tag { display: inline-flex; align-items: center; padding: 0.125rem 0.375rem; background: var(--color-bg-subtle); border: 1px solid var(--color-border-default); border-radius: var(--radius-md); font-size: 0.6875rem; color: var(--color-text-secondary); }
.rule-tag--more { background: transparent; border-style: dashed; }
.traffic-overview__status {
  display: inline-flex;
  align-items: center;
  padding: 0.2rem 0.6rem;
  border-radius: var(--radius-full);
  font-size: 0.75rem;
  font-weight: 600;
}
.traffic-overview__status--success {
  background: var(--color-success-subtle, #dcfce7);
  color: var(--color-success, #16a34a);
}
.traffic-overview__status--danger {
  background: var(--color-danger-50);
  color: var(--color-danger);
}
.traffic-tab__trend { display: flex; flex-direction: column; gap: 0.75rem; }
.traffic-tab__trend-header { display: flex; align-items: center; justify-content: space-between; font-size: 0.875rem; font-weight: 600; color: var(--color-text-primary); }
.traffic-tab__breakdown { }
.traffic-trend__controls { display: inline-flex; gap: 2px; padding: 2px; background: var(--color-bg-subtle); border: 1px solid var(--color-border-default); border-radius: var(--radius-md); }
.traffic-trend__mode { min-width: 3.25rem; padding: 0.45rem 0.85rem; border: 0; border-radius: var(--radius-sm); background: transparent; color: var(--color-text-tertiary); font-size: 0.875rem; font-weight: 600; cursor: pointer; font-family: inherit; }
.traffic-trend__mode--active { background: var(--color-bg-surface); color: var(--color-primary); box-shadow: var(--shadow-sm); }
.empty-hint { text-align: center; color: var(--color-text-muted); padding: 2rem; font-size: 0.875rem; }
.info-sections { display: flex; flex-direction: column; gap: 1rem; }
.info-grid { display: flex; flex-direction: column; gap: 0.5rem; }
.info-row,
.info-row--clean { display: flex; justify-content: space-between; padding: 0.75rem 1rem; background: var(--color-bg-surface); border-radius: var(--radius-lg); font-size: 0.875rem; }
.info-row--clean { padding: 0.625rem 1rem; }
.info-row span:first-child,
.info-row--clean span:first-child { color: var(--color-text-secondary); }
.info-row span:last-child,
.info-row--clean span:last-child { color: var(--color-text-primary); font-weight: 500; }
.agent-detail__loading { display: flex; justify-content: center; padding: 3rem; }
.agent-detail__not-found { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 1rem; padding: 4rem 2rem; color: var(--color-text-muted); text-align: center; }
.agent-detail__not-found p { margin: 0; font-size: 1rem; }
.spinner { width: 24px; height: 24px; border: 2px solid var(--color-border-default); border-top-color: var(--color-primary); border-radius: 50%; animation: spin 1s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
.btn { padding: 10px 24px; border-radius: var(--radius-full); font-size: var(--text-sm); font-weight: var(--font-semibold); cursor: pointer; transition: all var(--duration-fast) var(--ease-default); border: 1.5px solid transparent; font-family: inherit; display: inline-flex; align-items: center; justify-content: center; gap: 0.375rem; }
.btn-primary { background: var(--color-primary); color: white; }
.btn-primary:hover { background: var(--color-primary-hover); }
.btn-secondary { background: transparent; color: var(--color-text-secondary); border: 1.5px solid var(--color-border-default); }
.btn-secondary:hover { border-color: var(--color-primary); color: var(--color-primary); background: var(--color-primary-subtle); }
.btn:disabled { opacity: 0.6; cursor: not-allowed; }
.traffic-sections { display: flex; flex-direction: column; gap: 1rem; }
.traffic-section-card__title {
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-text-primary);
}
.traffic-section-card__icon {
  color: var(--color-primary);
  flex-shrink: 0;
}
.traffic-card:deep(.base-list-card__body) { gap: 1rem; }
.traffic-monitor__divider {
  height: 1px;
  background: var(--color-border-subtle);
  margin: 0.25rem 0;
}
.traffic-maintenance { display: flex; flex-direction: column; gap: 1rem; }
.traffic-maintenance__divider { height: 1px; background: var(--color-border-subtle); }
.traffic-maintenance :deep(.traffic-policy-form__cards) { gap: 1rem; }
.traffic-maintenance :deep(.traffic-policy-form__card) { background: transparent; border: none; padding: 0; border-radius: 0; }
.traffic-maintenance :deep(.traffic-policy-form__card-title) { font-size: 0.9375rem; }
.traffic-maintenance :deep(.traffic-history-manager) { gap: 0.75rem; }

</style>
