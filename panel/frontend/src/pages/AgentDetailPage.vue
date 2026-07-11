<template>
  <div class="agent-detail" v-if="agent">
    <div class="agent-detail__stack">
    <div class="agent-detail__back">
      <RouterLink to="/agents" class="back-link">← {{ detailLabels.backToAgents }}</RouterLink>
    </div>

    <BaseListCard class="agent-detail__summary-card agent-detail__panel" :title="agent.name" :status="statusTone" :clickable="false">
      <template #header-left>
        <AgentStatusBadge :agent="agent" class="agent-detail__status-badge" />
        <BaseBadge tone="primary" size="sm" class="agent-detail__mode-badge">{{ getModeLabel(agent.mode) }}</BaseBadge>
        <span
          class="agent-detail__sync-badge"
          data-testid="detail-sync-status"
          :data-tone="syncStatusTone"
          :title="detailLabels.metrics.syncStatus"
        >
          <span class="agent-detail__sync-badge-label">{{ detailLabels.metrics.syncStatus }}</span>
          <BaseBadge
            :tone="syncStatusTone"
            size="sm"
            class="agent-detail__sync-badge-value"
          >{{ syncStatusLabel }}</BaseBadge>
        </span>
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
            data-testid="detail-action-delete"
            tone="danger"
            :title="agent?.is_local ? '本地节点不可删除' : detailLabels.actions.deleteAgent"
            :disabled="agent?.is_local"
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

        <div class="agent-detail__secondary-band">
          <div class="agent-detail__secondary-label">{{ detailLabels.secondaryMetrics }}</div>
          <div class="agent-detail-metrics agent-detail-metrics--aligned agent-detail-metrics--embedded agent-detail__resource-metrics">
            <AgentMetricTile
              data-testid="detail-metric-cpu"
              icon="i-mdi-cpu-64-bit"
              :label="detailLabels.metrics.cpu"
              :value="cpuUsage(agentMetricsData)"
              :percent="agentMetricsData.cpu_usage_percent"
              :tone="barTone(agentMetricsData.cpu_usage_percent)"
              display-mode="ring"
            />
            <AgentMetricTile
              data-testid="detail-metric-memory"
              icon="i-mdi-memory"
              :label="detailLabels.metrics.memory"
              :value="bytesPair(agentMetricsData.memory_used_bytes, agentMetricsData.memory_total_bytes)"
              :percent="agentMetricsData.memory_usage_percent"
              :tone="barTone(agentMetricsData.memory_usage_percent)"
              display-mode="ring"
            />
            <AgentMetricTile
              data-testid="detail-metric-disk"
              icon="i-mdi-harddisk"
              :label="detailLabels.metrics.disk"
              :value="bytesPair(agentMetricsData.disk_used_bytes, agentMetricsData.disk_total_bytes)"
              :percent="agentMetricsData.disk_usage_percent"
              :tone="barTone(agentMetricsData.disk_usage_percent)"
              display-mode="ring"
            />
            <AgentMetricTile
              data-testid="detail-metric-network"
              icon="i-mdi-network"
              :label="detailLabels.metrics.network"
              :network-down="rate(networkMetrics?.rx_bytes_per_second)"
              :network-up="rate(networkMetrics?.tx_bytes_per_second)"
            />
          </div>

          <div class="agent-detail-metrics agent-detail-metrics--aligned agent-detail-metrics--raised agent-detail-metrics--horizontal agent-detail__count-metrics">
            <StatCard
              tone="primary"
              :value="httpRulesCount"
              :label="detailLabels.metrics.httpRules"
              :to="rulesHttpTo"
            >
              <template #icon>
                <span class="i-mdi-link-variant" aria-hidden="true" />
              </template>
            </StatCard>
            <StatCard
              tone="success"
              :value="l4RulesCount"
              :label="detailLabels.metrics.l4Rules"
              :to="rulesL4To"
            >
              <template #icon>
                <span class="i-mdi-server-network" aria-hidden="true" />
              </template>
            </StatCard>
            <StatCard
              tone="warning"
              :value="certificatesCount"
              :label="detailLabels.metrics.certificates"
              :to="certsTo"
            >
              <template #icon>
                <span class="i-mdi-certificate" aria-hidden="true" />
              </template>
            </StatCard>
            <StatCard
              tone="primary"
              :value="relayListenersCount"
              :label="detailLabels.metrics.relayListeners"
              :to="listenersTo"
            >
              <template #icon>
                <span class="i-mdi-transit-connection-variant" aria-hidden="true" />
              </template>
            </StatCard>
          </div>
        </div>
      </div>
    </BaseListCard>

    <div
      v-if="agent.last_apply_status === 'failed' && agent.last_apply_message"
      class="agent-detail__error agent-detail__alert"
      role="alert"
    >
      <div class="error-block">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <circle cx="12" cy="12" r="10"/>
          <line x1="12" y1="8" x2="12" y2="12"/>
          <line x1="12" y1="16" x2="12.01" y2="16"/>
        </svg>
        <div class="error-block__content">
          <div class="error-block__title">{{ detailLabels.sync.failedTitle }}</div>
          <div class="error-block__text">{{ agent.last_apply_message }}</div>
        </div>
      </div>
    </div>

    <div class="agent-detail__sections agent-detail__detail-panels">
      <TrafficCollapsibleSection class="agent-detail__section" :title="detailLabels.sections.rules" :subtitle="rulesSubtitle" default-expanded>
        <BaseListCard class="rules-list-card agent-detail__panel agent-detail__panel--inset" :clickable="false">
          <div class="simple-list" data-testid="detail-rules-list">
            <div
              v-for="rule in allRules"
              :key="`${rule._type}-${rule.id}`"
              class="simple-list__row simple-list__row--clickable"
              @click="navigateToRule(rule)"
            >
              <div class="simple-list__main">
                <span class="simple-list__primary" :title="ruleEntry(rule)">{{ ruleEntry(rule) }}</span>
                <span
                  v-if="ruleBackend(rule)"
                  class="simple-list__secondary"
                  :title="ruleBackend(rule)"
                >{{ ruleBackend(rule) }}</span>
                <div v-if="listTags(rule.tags).length" class="simple-list__tags">
                  <BaseBadge
                    v-for="tag in listTags(rule.tags).slice(0, 3)"
                    :key="tag"
                    tone="primary"
                    size="sm"
                  >{{ tag }}</BaseBadge>
                  <BaseBadge
                    v-if="listTags(rule.tags).length > 3"
                    tone="neutral"
                    size="sm"
                  >+{{ listTags(rule.tags).length - 3 }}</BaseBadge>
                </div>
              </div>
              <div class="simple-list__side">
                <BaseBadge :tone="rule._type === 'http' ? 'primary' : 'success'" size="sm">{{ ruleTypeLabel(rule) }}</BaseBadge>
                <BaseBadge :tone="rule.enabled !== false ? 'success' : 'neutral'" size="sm">{{ rule.enabled !== false ? detailLabels.ruleEnabled : detailLabels.ruleDisabled }}</BaseBadge>
              </div>
            </div>
            <p v-if="!allRules.length" class="empty-hint">{{ detailLabels.empty.rules }}</p>
          </div>
        </BaseListCard>
      </TrafficCollapsibleSection>

      <TrafficCollapsibleSection class="agent-detail__section" :title="detailLabels.sections.certificates" :subtitle="certificatesSubtitle">
        <BaseListCard class="rules-list-card agent-detail__panel agent-detail__panel--inset" :clickable="false">
          <div class="simple-list" data-testid="detail-certificates-list">
            <div
              v-for="cert in certificates"
              :key="cert.id"
              class="simple-list__row simple-list__row--clickable"
              @click="navigateToCertificate(cert)"
            >
              <div class="simple-list__main">
                <span class="simple-list__primary" :title="certificatePrimary(cert)">{{ certificatePrimary(cert) }}</span>
                <span
                  v-if="certificateSecondary(cert)"
                  class="simple-list__secondary"
                  :title="certificateSecondary(cert)"
                >{{ certificateSecondary(cert) }}</span>
                <div v-if="listTags(cert.tags).length" class="simple-list__tags">
                  <BaseBadge
                    v-for="tag in listTags(cert.tags).slice(0, 3)"
                    :key="tag"
                    tone="primary"
                    size="sm"
                  >{{ tag }}</BaseBadge>
                  <BaseBadge
                    v-if="listTags(cert.tags).length > 3"
                    tone="neutral"
                    size="sm"
                  >+{{ listTags(cert.tags).length - 3 }}</BaseBadge>
                </div>
              </div>
              <div class="simple-list__side">
                <BaseBadge :tone="certificateStatusBadge(cert).tone" size="sm">{{ certificateStatusBadge(cert).label }}</BaseBadge>
              </div>
            </div>
            <p v-if="!certificates.length" class="empty-hint">{{ detailLabels.empty.certificates }}</p>
          </div>
        </BaseListCard>
      </TrafficCollapsibleSection>

      <TrafficCollapsibleSection class="agent-detail__section" :title="detailLabels.sections.relayListeners" :subtitle="relayListenersSubtitle">
        <BaseListCard class="rules-list-card agent-detail__panel agent-detail__panel--inset" :clickable="false">
          <div class="simple-list" data-testid="detail-listeners-list">
            <div
              v-for="listener in relayListeners"
              :key="listener.id"
              class="simple-list__row simple-list__row--clickable"
              @click="navigateToListener(listener)"
            >
              <div class="simple-list__main">
                <span class="simple-list__primary" :title="listenerPrimary(listener)">{{ listenerPrimary(listener) }}</span>
                <span
                  v-if="listenerSecondary(listener)"
                  class="simple-list__secondary"
                  :title="listenerSecondary(listener)"
                >{{ listenerSecondary(listener) }}</span>
                <div v-if="listTags(listener.tags).length" class="simple-list__tags">
                  <BaseBadge
                    v-for="tag in listTags(listener.tags).slice(0, 3)"
                    :key="tag"
                    tone="primary"
                    size="sm"
                  >{{ tag }}</BaseBadge>
                  <BaseBadge
                    v-if="listTags(listener.tags).length > 3"
                    tone="neutral"
                    size="sm"
                  >+{{ listTags(listener.tags).length - 3 }}</BaseBadge>
                </div>
              </div>
              <div class="simple-list__side">
                <BaseBadge
                  v-if="listenerTransportLabel(listener)"
                  tone="neutral"
                  subtone="secondary"
                  size="sm"
                >{{ listenerTransportLabel(listener) }}</BaseBadge>
                <BaseBadge :tone="listener.enabled !== false ? 'success' : 'neutral'" size="sm">{{ listener.enabled !== false ? detailLabels.ruleEnabled : detailLabels.ruleDisabled }}</BaseBadge>
              </div>
            </div>
            <p v-if="!relayListeners.length" class="empty-hint">{{ detailLabels.empty.relayListeners }}</p>
          </div>
        </BaseListCard>
      </TrafficCollapsibleSection>

      <TrafficCollapsibleSection
        v-if="trafficStatsEnabled"
        class="agent-detail__section"
        :title="detailLabels.sections.traffic"
      >
        <div class="traffic-sections">
          <BaseListCard
            class="traffic-card traffic-card--health agent-detail__panel agent-detail__panel--inset"
            :class="{ 'traffic-card--health-blocked': trafficHealthBlocked }"
            :clickable="false"
          >
            <template #header-left>
              <svg class="traffic-section-card__icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/>
              </svg>
              <span class="traffic-section-card__title">{{ detailLabels.sections.trafficHealth }}</span>
            </template>
            <template #header-right>
              <BaseBadge
                data-testid="traffic-health-badge"
                :tone="trafficHealthBadge.tone"
                size="sm"
              >
                {{ trafficHealthBadge.label }}
              </BaseBadge>
            </template>
            <TrafficSummaryCards
              :summary="trafficSummary"
              :direction="trafficPolicyForm.direction"
              :network-metrics="networkMetrics"
              :loading="trafficSummaryLoading"
              @open-analysis="analysisModalVisible = true"
              @open-management="managementModalVisible = true"
            />
            <div class="traffic-monitor__divider" />
            <div class="traffic-tab__trend traffic-tab__trend--demoted">
              <div class="traffic-tab__trend-header">
                <span class="traffic-tab__trend-title">流量趋势</span>
                <div class="traffic-trend__controls traffic-trend__controls--compact" role="group" aria-label="趋势粒度">
                  <button
                    v-for="option in trafficTrendGranularityOptions"
                    :key="option.value"
                    class="traffic-trend__mode"
                    :class="{ 'traffic-trend__mode--active': trafficTrendGranularity === option.value }"
                    type="button"
                    @click="trafficTrendGranularity = option.value"
                  >
                    {{ option.label }}
                  </button>
                </div>
              </div>
              <div class="traffic-tab__trend-chart">
                <TrafficTrendChart
                  :points="trafficTrendPoints"
                  :granularity="trafficTrendGranularity"
                  :quota-bytes="trafficSummary.monthly_quota_bytes ?? null"
                  :refresh-key="agentStatsRefreshKey"
                  :loading="trafficTrendLoading"
                />
              </div>
            </div>
          </BaseListCard>
        </div>

        <BaseModal
          v-model="analysisModalVisible"
          :title="detailLabels.sections.trafficAnalysisModal"
          :subtitle="trafficAnalysisModalSubtitle"
          size="lg"
          :show-footer="false"
        >
          <div class="traffic-scenario-modal traffic-scenario-modal--analysis" data-testid="traffic-analysis-modal-body">
            <div class="traffic-scenario-modal__context" data-testid="traffic-analysis-context">
              <div class="traffic-scenario-modal__context-main">
                <span class="traffic-scenario-modal__context-label">当前总流量</span>
                <span class="traffic-scenario-modal__context-value">{{ trafficUsedDisplay }}</span>
              </div>
              <span v-if="trafficAnalysisContextHint" class="traffic-scenario-modal__context-hint">{{ trafficAnalysisContextHint }}</span>
            </div>
            <section class="traffic-scenario-modal__section" data-testid="traffic-analysis-section-breakdown">
              <header class="traffic-scenario-modal__section-header">
                <h3 class="traffic-scenario-modal__section-title">分项构成</h3>
                <p class="traffic-scenario-modal__section-desc">按规则 / 监听 / 主机接口查看用量占比，点击行可钻取趋势</p>
              </header>
              <div class="traffic-scenario-modal__panel traffic-scenario-modal__panel--table">
                <TrafficBreakdownTable :tabs="trafficBreakdownTabs" :clickable="true" @click-row="openBreakdownTrendModal" />
              </div>
            </section>
          </div>
        </BaseModal>

        <BaseModal
          v-model="managementModalVisible"
          :title="detailLabels.sections.trafficManagementModal"
          :subtitle="trafficManagementModalSubtitle"
          size="lg"
          :show-footer="false"
        >
          <div class="traffic-scenario-modal traffic-scenario-modal--management" data-testid="traffic-management-modal-body">
            <div class="traffic-scenario-modal__context" data-testid="traffic-management-context">
              <div class="traffic-scenario-modal__context-main">
                <span class="traffic-scenario-modal__context-label">当前剩余</span>
                <span class="traffic-scenario-modal__context-value">{{ trafficRemainingDisplay }}</span>
              </div>
              <span v-if="trafficManagementContextHint" class="traffic-scenario-modal__context-hint">{{ trafficManagementContextHint }}</span>
            </div>
            <section class="traffic-scenario-modal__section traffic-scenario-modal__section--primary" data-testid="traffic-management-section-policy">
              <header class="traffic-scenario-modal__section-header">
                <h3 class="traffic-scenario-modal__section-title">额度与策略</h3>
                <p class="traffic-scenario-modal__section-desc">调整月额度、阻断与计费策略，保存后立即生效</p>
              </header>
              <div class="traffic-scenario-modal__panel traffic-scenario-modal__panel--policy">
                <TrafficPolicyForm v-model="trafficPolicyForm" :saving="updateTrafficPolicyMutation.isPending.value || updateAgent.isPending.value" @save="saveTrafficPolicy" />
              </div>
            </section>
            <section class="traffic-scenario-modal__section traffic-scenario-modal__section--secondary" data-testid="traffic-management-section-history">
              <header class="traffic-scenario-modal__section-header">
                <h3 class="traffic-scenario-modal__section-title">历史与维护</h3>
                <p class="traffic-scenario-modal__section-desc">查看保留策略摘要，执行校准或清理过期数据</p>
              </header>
              <div class="traffic-scenario-modal__panel traffic-scenario-modal__panel--history">
                <TrafficHistoryManager
                  :policy="trafficPolicyForm"
                  :calibrating="calibrateTrafficMutation.isPending.value"
                  :cleaning="cleanupTrafficMutation.isPending.value"
                  @calibrate="calibrateModalVisible = true"
                  @calibrate-zero="showCalibrateZeroConfirm"
                  @cleanup="showCleanupConfirm"
                />
              </div>
            </section>
          </div>
        </BaseModal>

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
      </TrafficCollapsibleSection>

      <TrafficCollapsibleSection class="agent-detail__section" :title="detailLabels.sections.systemInfo">
        <div class="info-sections">
          <BaseListCard class="info-card agent-detail__panel agent-detail__panel--inset" :title="detailLabels.systemCards.package" :clickable="false">
            <div class="info-grid">
              <div class="info-row info-row--clean"><span>版本</span><span>{{ agent.version || agent.runtime_package_version || '—' }}</span></div>
              <div class="info-row info-row--clean"><span>平台</span><span>{{ agent.runtime_package_platform || agent.platform || '—' }}</span></div>
              <div class="info-row info-row--clean"><span>架构</span><span>{{ agent.runtime_package_arch || '—' }}</span></div>
              <div class="info-row info-row--clean"><span>运行包 SHA</span><span :title="agent.runtime_package_sha256 || ''">{{ shortSha(agent.runtime_package_sha256) }}</span></div>
              <div class="info-row info-row--clean"><span>目标包 SHA</span><span :title="agent.desired_package_sha256 || ''">{{ shortSha(agent.desired_package_sha256) }}</span></div>
              <div class="info-row info-row--clean"><span>包状态</span><span>{{ packageStatusLabel(agent.package_sync_status) }}</span></div>
            </div>
          </BaseListCard>

          <BaseListCard class="info-card agent-detail__panel agent-detail__panel--inset" :title="detailLabels.systemCards.identity" :clickable="false">
            <div class="info-grid">
              <div class="info-row info-row--clean"><span>角色</span><span>{{ getModeLabel(agent.mode) }}</span></div>
              <div class="info-row info-row--clean"><span>IP</span><span>{{ agent.last_seen_ip || '—' }}</span></div>
              <div class="info-row info-row--clean"><span>最后活跃</span><span>{{ agent.last_seen_at ? new Date(agent.last_seen_at).toLocaleString() : '—' }}</span></div>
            </div>
          </BaseListCard>

          <BaseListCard class="info-card agent-detail__panel agent-detail__panel--inset" :title="detailLabels.systemCards.sync" :clickable="false">
            <div class="info-grid">
              <div class="info-row info-row--clean">
                <span>同步状态</span>
                <BaseBadge :tone="syncStatusTone" size="sm">{{ syncStatusLabel }}</BaseBadge>
              </div>
              <div v-if="agent.last_apply_message" class="info-row info-row--clean"><span>同步消息</span><span>{{ agent.last_apply_message }}</span></div>
            </div>
          </BaseListCard>
        </div>
      </TrafficCollapsibleSection>

      <TrafficCollapsibleSection
        class="agent-detail__section"
        :title="detailLabels.sections.syncEvents"
        :subtitle="syncStatusLabel"
        :default-expanded="agent.last_apply_status === 'failed'"
      >
        <BaseListCard class="info-card agent-detail__panel agent-detail__panel--inset" :clickable="false">
          <div class="info-grid">
            <div class="info-row info-row--clean">
              <span>{{ detailLabels.sync.status }}</span>
              <BaseBadge :tone="syncStatusTone" size="sm">{{ syncStatusLabel }}</BaseBadge>
            </div>
            <div class="info-row info-row--clean"><span>{{ detailLabels.sync.message }}</span><span>{{ agent.last_apply_message || '—' }}</span></div>
            <div class="info-row info-row--clean"><span>{{ detailLabels.sync.time }}</span><span>{{ agent.last_apply_at ? new Date(agent.last_apply_at).toLocaleString() : '—' }}</span></div>
          </div>
        </BaseListCard>
      </TrafficCollapsibleSection>
    </div>

    <DeleteConfirmDialog
      :show="confirmDialog.visible"
      :title="confirmDialog.title"
      :message="confirmDialog.message"
      :confirm-text="confirmDialog.confirmText"
      :loading="confirmDialog.loading"
      @confirm="onConfirmDialogConfirm"
      @cancel="confirmDialog.visible = false"
    />
    </div>
  </div>
  <div v-else-if="isLoading" class="agent-detail agent-detail__loading">
    <div class="spinner"></div>
  </div>
  <div v-else class="agent-detail agent-detail__not-found">
    <svg width="48" height="48" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
      <circle cx="12" cy="12" r="10"/>
      <line x1="12" y1="8" x2="12" y2="12"/>
      <line x1="12" y1="16" x2="12.01" y2="16"/>
    </svg>
    <p>{{ detailLabels.notFoundTitle }}</p>
    <RouterLink to="/agents" class="agent-detail__not-found-link">{{ detailLabels.backToAgents }}</RouterLink>
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
import { fetchAgentStats, fetchSystemInfo } from '../api'
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
import BaseModal from '../components/base/BaseModal.vue'
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
const analysisModalVisible = ref(false)
const managementModalVisible = ref(false)
const trafficSummaryLoading = computed(() => Boolean(trafficSummaryQuery.isLoading.value))
const trafficTrendLoading = computed(() => Boolean(trafficTrendQuery.isLoading.value))
const trafficSummary = computed(() => trafficSummaryQuery.data.value ?? {})
const trafficTrendPoints = computed(() => normalizeTrafficTrendPoints(trafficTrendQuery.data.value ?? [], trafficPolicyForm.value.direction))
const trafficHealthBlocked = computed(() => !trafficSummaryLoading.value && Boolean(trafficSummary.value.blocked))
const trafficHealthBadge = computed(() => {
  if (trafficSummaryLoading.value) {
    return { tone: 'neutral', label: '加载中' }
  }
  if (trafficSummary.value.blocked) {
    return { tone: 'danger', label: '已阻断' }
  }
  return { tone: 'success', label: '正常' }
})
const trafficUsedDisplay = computed(() => {
  if (trafficSummaryLoading.value) return '—'
  return formatBytes(trafficSummary.value.used_bytes)
})
const trafficRemainingDisplay = computed(() => {
  if (trafficSummaryLoading.value) return '—'
  const quota = trafficSummary.value.monthly_quota_bytes
  if (quota == null || quota === '') return '无限制'
  if (trafficSummary.value.remaining_bytes != null && trafficSummary.value.remaining_bytes !== '') {
    return formatBytes(trafficSummary.value.remaining_bytes)
  }
  const used = Number(trafficSummary.value.used_bytes) || 0
  return formatBytes(Math.max(0, Number(quota) - used))
})
const trafficAnalysisContextHint = computed(() => {
  if (trafficSummaryLoading.value) return '加载中…'
  const quota = trafficSummary.value.monthly_quota_bytes
  if (quota == null || quota === '') return '未设置月额度'
  return `额度 ${formatQuota(quota, '无限制')}`
})
const trafficManagementContextHint = computed(() => {
  if (trafficSummaryLoading.value) return '加载中…'
  if (trafficSummary.value.blocked) return '当前已超额阻断'
  const quota = trafficSummary.value.monthly_quota_bytes
  if (quota == null || quota === '') return '未设置月额度'
  return `额度 ${formatQuota(quota, '无限制')}`
})
const trafficAnalysisModalSubtitle = computed(() => '按分项构成查看总流量，点击行可钻取趋势')
const trafficManagementModalSubtitle = computed(() => '优先调整额度与阻断，再管理计费、保留与历史维护')
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

const CERT_STATUS = {
  active: { label: agentDetailLabels.certStatus.active, tone: 'success' },
  pending: { label: agentDetailLabels.certStatus.pending, tone: 'warning' },
  issuing: { label: agentDetailLabels.certStatus.issuing, tone: 'primary' },
  error: { label: agentDetailLabels.certStatus.error, tone: 'danger' },
}

function listTags(tags) {
  if (!Array.isArray(tags)) return []
  return tags.map((tag) => String(tag || '').trim()).filter(Boolean)
}

function formatIssuedAt(value) {
  if (value == null || value === '') return ''
  try {
    // API may return RFC3339 string or unix seconds
    const date = typeof value === 'number'
      ? new Date(value > 1e12 ? value : value * 1000)
      : new Date(value)
    if (Number.isNaN(date.getTime())) return ''
    return date.toLocaleString('zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    })
  } catch {
    return ''
  }
}

function certificatePrimary(cert) {
  return cert?.domain || cert?.name || cert?.id || '—'
}

function certificateSecondary(cert) {
  const parts = []
  const domain = String(cert?.domain || '').trim()
  const name = String(cert?.name || '').trim()
  if (domain && name && name !== domain) parts.push(name)
  const issued = formatIssuedAt(cert?.last_issue_at)
  if (issued) parts.push(`${agentDetailLabels.certIssuedAt} ${issued}`)
  return parts.join(' · ')
}

function certificateStatusBadge(cert) {
  if (cert?.enabled === false) {
    return { label: agentDetailLabels.certStatus.disabled, tone: 'neutral' }
  }
  const mapped = CERT_STATUS[cert?.status]
  if (mapped) return mapped
  const raw = String(cert?.status || '').trim()
  return {
    label: raw || agentDetailLabels.certStatus.unknown,
    tone: 'neutral',
  }
}

function normalizePort(port) {
  const value = Number(port)
  return Number.isInteger(value) && value > 0 ? value : null
}

function resolveListenerBindHosts(listener) {
  if (Array.isArray(listener?.bind_hosts) && listener.bind_hosts.length) {
    return listener.bind_hosts
      .map((item) => String(item || '').trim())
      .filter(Boolean)
  }
  const legacyHost = String(listener?.listen_host || '').trim()
  return legacyHost ? [legacyHost] : []
}

function listenerEndpoint(listener) {
  const publicHost = String(listener?.public_host || '').trim()
  const bindHosts = resolveListenerBindHosts(listener)
  const host = publicHost || bindHosts[0] || ''
  const port = normalizePort(listener?.public_port) ?? normalizePort(listener?.listen_port)
  if (host && port) return `${host}:${port}`
  if (host) return host
  if (port) return `:${port}`
  // Legacy mock/compat only — not an API formal field
  const legacy = String(listener?.listen_addr || '').trim()
  return legacy
}

function listenerPrimary(listener) {
  return listener?.name || listenerEndpoint(listener) || listener?.id || '—'
}

function listenerSecondary(listener) {
  const name = String(listener?.name || '').trim()
  const endpoint = listenerEndpoint(listener)
  if (name && endpoint && endpoint !== name) return endpoint
  if (!name && endpoint) return endpoint
  return ''
}

function listenerTransportLabel(listener) {
  const mode = String(listener?.transport_mode || '').trim()
  if (mode === 'quic') return agentDetailLabels.listenerTransport.quic
  if (mode === 'wireguard') return agentDetailLabels.listenerTransport.wireguard
  if (mode === 'tls_tcp' || mode === 'tcp' || mode === 'tls') {
    return agentDetailLabels.listenerTransport.tls_tcp
  }
  return ''
}

function navigateToManagedList(path, id) {
  router.push({
    path,
    query: {
      agentId: agentId.value,
      search: `#id=${id}`
    }
  })
}

function navigateToRule(rule) {
  navigateToManagedList(rule._type === 'http' ? '/rules' : '/l4', rule.id)
}

function navigateToCertificate(cert) {
  navigateToManagedList('/certs', cert.id)
}

function navigateToListener(listener) {
  navigateToManagedList('/relay-listeners', listener.id)
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

.agent-detail__stack {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.agent-detail__back {
  margin-bottom: 0;
}

.back-link {
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  text-decoration: none;
}

.back-link:hover {
  color: var(--color-primary);
}

.agent-detail__panel {
  border-radius: var(--radius-xl);
}

.agent-detail__panel--inset {
  border: 1px solid var(--color-border-subtle);
  background: var(--color-bg-surface);
  box-shadow: none;
}

.agent-detail__summary-card {
  position: relative;
  overflow: hidden;
  margin-bottom: 0;
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

.agent-detail__summary-card :deep(.base-list-card__header) {
  margin-bottom: var(--space-1);
}

.agent-detail__summary-card :deep(.base-list-card__title) {
  margin-bottom: var(--space-1);
  line-height: 1.25;
}

.agent-detail__sync-badge {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  flex-shrink: 0;
  padding: 0.1rem var(--space-1-5);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-md);
  background: var(--color-bg-subtle);
  line-height: 1;
}

.agent-detail__sync-badge[data-tone="success"] {
  border-color: color-mix(in srgb, var(--color-success) 35%, var(--color-border-default));
  background: color-mix(in srgb, var(--color-success) 10%, var(--color-bg-surface));
}

.agent-detail__sync-badge[data-tone="danger"] {
  border-color: color-mix(in srgb, var(--color-danger) 40%, var(--color-border-default));
  background: color-mix(in srgb, var(--color-danger) 10%, var(--color-bg-surface));
}

.agent-detail__sync-badge[data-tone="warning"] {
  border-color: color-mix(in srgb, var(--color-warning) 40%, var(--color-border-default));
  background: color-mix(in srgb, var(--color-warning) 10%, var(--color-bg-surface));
}

.agent-detail__sync-badge-label {
  font-size: var(--text-xs);
  font-weight: 600;
  color: var(--color-text-secondary);
  letter-spacing: 0.01em;
}

.agent-detail__secondary-band {
  display: flex;
  flex-direction: column;
  gap: var(--space-2-5);
  padding: var(--space-3);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  background: var(--color-bg-subtle);
}

.agent-detail__secondary-label {
  font-size: var(--text-xs);
  font-weight: 600;
  color: var(--color-text-muted);
  text-transform: none;
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

.agent-detail__resource-metrics,
.agent-detail__count-metrics {
  margin-bottom: 0;
}

.agent-detail-metrics--aligned {
  grid-template-columns: repeat(4, minmax(0, 1fr));
  align-items: stretch;
}

/* Resource tiles: shallow embedded shell (dashboard tile tokens). Natural height. */
.agent-detail-metrics--embedded :deep(.agent-metric-tile) {
  border: 1px solid var(--color-dashboard-tile-border);
  border-radius: var(--radius-md);
  background: var(--color-dashboard-tile-bg);
  box-shadow: none;
  justify-content: space-between;
}

.agent-detail-metrics--embedded :deep(.agent-metric-tile__ring-visual) {
  width: 3.75rem;
  height: 3.75rem;
}

/* Business counts: white raised StatCards. */
.agent-detail-metrics--raised :deep(.stat-card) {
  padding: var(--space-2-5) var(--space-3);
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-sm);
}

.agent-detail-metrics--raised :deep(.stat-card__icon) {
  width: 2rem;
  height: 2rem;
}

.agent-detail-metrics--raised :deep(.stat-card__value) {
  font-size: 1.5rem;
  margin-bottom: 0;
}

.agent-detail-metrics--raised :deep(.stat-card__label) {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-tertiary);
}

/* Business counts: icon + value/label in a horizontal row to fill width. */
.agent-detail-metrics--horizontal :deep(.stat-card) {
  display: flex;
  flex-direction: row;
  align-items: center;
  gap: var(--space-3);
  min-width: 0;
}

.agent-detail-metrics--horizontal :deep(.stat-card__icon) {
  flex-shrink: 0;
  margin-bottom: 0;
}

.agent-detail-metrics--horizontal :deep(.stat-card__data) {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  justify-content: center;
}

.agent-detail-metrics--horizontal :deep(.stat-card__value) {
  line-height: 1.15;
}

.agent-detail-metrics--horizontal :deep(.stat-card__label) {
  margin-top: var(--space-0-5, 0.125rem);
}

.agent-detail__meta-rows {
  min-width: 0;
}

.agent-detail__meta-row {
  display: flex;
  align-items: baseline;
  gap: 0.375rem;
  margin: 0;
}

.agent-detail__meta-label {
  font-size: var(--text-xs);
  color: var(--color-text-muted);
  flex-shrink: 0;
}

.agent-detail__meta-value {
  flex: 1;
  min-width: 0;
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

@media (max-width: 1024px) {
  .agent-detail-metrics--aligned {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 375px) {
  .agent-detail-metrics--aligned {
    grid-template-columns: 1fr;
  }
}

.agent-detail__endpoint {
  font-family: var(--font-mono);
}

.agent-detail__error,
.agent-detail__alert {
  margin-bottom: 0;
}

.error-block {
  display: flex;
  align-items: flex-start;
  gap: var(--space-3);
  padding: var(--space-4);
  background: var(--color-danger-50);
  border: 1px solid var(--color-danger);
  border-radius: var(--radius-xl);
  color: var(--color-danger);
}

.error-block svg {
  flex-shrink: 0;
  margin-top: 1px;
}

.error-block__title {
  font-weight: 600;
  font-size: var(--text-sm);
  margin-bottom: var(--space-1);
}

.error-block__text {
  font-size: var(--text-xs);
  line-height: 1.5;
  color: var(--color-danger);
  opacity: 0.95;
  word-break: break-word;
}

.agent-detail__sections,
.agent-detail__detail-panels {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.agent-detail__section {
  min-width: 0;
}

.simple-list { display: flex; flex-direction: column; gap: var(--space-2); }
.simple-list__row {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-surface);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  font-size: var(--text-sm);
}
.simple-list__row--clickable {
  cursor: pointer;
  transition: background-color 150ms ease;
}
.simple-list__row:hover { background: var(--color-bg-hover); }
.simple-list__main {
  min-width: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
}
.simple-list__primary {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-primary);
  font-weight: 600;
}
.simple-list__secondary {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
}
.simple-list__tags {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.375rem;
  min-width: 0;
}
.simple-list__side {
  display: inline-flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: 0.375rem;
  flex-shrink: 0;
}

.traffic-tab__trend { display: flex; flex-direction: column; gap: 0.5rem; }
.traffic-tab__trend--demoted {
  gap: 0.25rem;
  padding: 0.125rem 0 0;
  opacity: 0.9;
}
.traffic-tab__trend-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.5rem;
  font-size: 0.75rem;
  font-weight: 500;
  color: var(--color-text-muted);
}
.traffic-tab__trend-title {
  font-size: 0.75rem;
  font-weight: 500;
  color: var(--color-text-muted);
  letter-spacing: 0.01em;
}
.traffic-tab__trend-chart {
  max-height: 18rem;
  min-height: 14rem;
  overflow: hidden;
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--color-bg-subtle) 88%, var(--color-bg-surface));
  border: 1px solid color-mix(in srgb, var(--color-border-subtle) 80%, transparent);
  padding: 0.25rem 0.375rem 0.125rem;
}
.traffic-tab__trend-chart :deep(canvas),
.traffic-tab__trend-chart :deep(svg) {
  max-height: 16rem;
}
.traffic-tab__breakdown { }
.traffic-trend__controls {
  display: inline-flex;
  gap: 2px;
  padding: 2px;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-md);
}
.traffic-trend__controls--compact {
  padding: 1px;
  gap: 1px;
  background: transparent;
  border-color: transparent;
}
.traffic-trend__mode {
  min-width: 2.25rem;
  padding: 0.2rem 0.45rem;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--color-text-muted);
  font-size: 0.6875rem;
  font-weight: 500;
  cursor: pointer;
  font-family: inherit;
}
.traffic-trend__mode--active {
  background: var(--color-bg-subtle);
  color: var(--color-text-tertiary);
  box-shadow: none;
  font-weight: 600;
}
.empty-hint {
  text-align: center;
  color: var(--color-text-muted);
  padding: var(--space-8);
  font-size: var(--text-sm);
}

.info-sections {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.info-grid {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.info-row,
.info-row--clean {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  font-size: var(--text-sm);
}

.info-row--clean {
  padding: var(--space-2-5) var(--space-4);
}

.info-row span:first-child,
.info-row--clean span:first-child {
  color: var(--color-text-secondary);
  flex-shrink: 0;
}

.info-row span:last-child,
.info-row--clean span:last-child {
  color: var(--color-text-primary);
  font-weight: 500;
  min-width: 0;
  text-align: right;
}

.agent-detail__loading {
  display: flex;
  justify-content: center;
  padding: var(--space-12, 3rem);
}

.agent-detail__not-found {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-4);
  padding: var(--space-12, 4rem) var(--space-8);
  color: var(--color-text-muted);
  text-align: center;
}

.agent-detail__not-found p {
  margin: 0;
  font-size: var(--text-base, 1rem);
}

.agent-detail__not-found-link {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-2-5) var(--space-6);
  border-radius: var(--radius-full);
  border: 1.5px solid var(--color-border-default);
  color: var(--color-text-secondary);
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  text-decoration: none;
  transition: all var(--duration-fast) var(--ease-default);
}

.agent-detail__not-found-link:hover {
  border-color: var(--color-primary);
  color: var(--color-primary);
  background: var(--color-primary-subtle);
}

.spinner {
  width: 24px;
  height: 24px;
  border: 2px solid var(--color-border-default);
  border-top-color: var(--color-primary);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.traffic-sections {
  display: flex;
  flex-direction: column;
  gap: var(--space-2-5, 0.625rem);
}
.traffic-section-card__title {
  font-size: 0.9375rem;
  font-weight: 650;
  color: var(--color-text-primary);
  letter-spacing: -0.01em;
}
.traffic-section-card__icon {
  color: var(--color-primary);
  flex-shrink: 0;
}
.traffic-card:deep(.base-list-card__body) {
  gap: 0.875rem;
}
.traffic-card--health {
  border-color: color-mix(in srgb, var(--color-primary) 14%, var(--color-border-default));
  box-shadow:
    0 1px 0 color-mix(in srgb, var(--color-primary) 10%, transparent),
    0 8px 20px -16px color-mix(in srgb, var(--color-primary) 28%, transparent);
  background: color-mix(in srgb, var(--color-bg-surface) 96%, var(--color-primary) 4%);
}
.traffic-card--health-blocked {
  border-color: color-mix(in srgb, var(--color-danger, #dc2626) 28%, var(--color-border-default));
  box-shadow:
    0 1px 0 color-mix(in srgb, var(--color-danger, #dc2626) 12%, transparent),
    0 8px 20px -16px color-mix(in srgb, var(--color-danger, #dc2626) 30%, transparent);
  background: color-mix(in srgb, var(--color-bg-surface) 94%, var(--color-danger, #dc2626) 6%);
}
.traffic-card--health:deep(.base-list-card__header) {
  padding-bottom: 0.25rem;
  margin-bottom: 0.125rem;
}
.traffic-card--health:deep(.base-list-card__body) {
  gap: 0.625rem;
}
.traffic-card--health :deep(.traffic-summary-cards) {
  border-color: color-mix(in srgb, var(--color-border-default) 70%, transparent);
  background: color-mix(in srgb, var(--color-bg-surface) 92%, transparent);
  box-shadow: none;
}
.traffic-monitor__divider {
  height: 1px;
  background: color-mix(in srgb, var(--color-border-subtle) 70%, transparent);
  margin: 0.05rem 0 0.05rem;
  opacity: 0.75;
}
.traffic-scenario-modal {
  display: flex;
  flex-direction: column;
  gap: 1.125rem;
}
.traffic-scenario-modal__context {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  justify-content: space-between;
  gap: 0.5rem 1rem;
  padding: 0.875rem 1rem;
  border: 1px solid color-mix(in srgb, var(--color-border-default) 80%, transparent);
  border-radius: var(--radius-lg);
  background:
    linear-gradient(
      135deg,
      color-mix(in srgb, var(--color-primary-50) 55%, transparent),
      color-mix(in srgb, var(--color-bg-subtle) 80%, transparent) 48%,
      color-mix(in srgb, var(--color-bg-surface) 90%, transparent)
    );
  box-shadow: inset 0 1px 0 color-mix(in srgb, var(--color-bg-surface) 70%, transparent);
}
.traffic-scenario-modal__context-main {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
  min-width: 0;
}
.traffic-scenario-modal__context-label {
  color: var(--color-text-tertiary);
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.02em;
}
.traffic-scenario-modal__context-value {
  color: var(--color-text-primary);
  font-size: 1.375rem;
  font-weight: 700;
  line-height: 1.15;
  font-variant-numeric: tabular-nums;
}
.traffic-scenario-modal__context-hint {
  color: var(--color-text-secondary);
  font-size: 0.75rem;
  line-height: 1.4;
  max-width: 18rem;
  text-align: right;
}
.traffic-scenario-modal__section {
  display: flex;
  flex-direction: column;
  gap: 0.75rem;
  min-width: 0;
}
.traffic-scenario-modal__section-header {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}
.traffic-scenario-modal__section-title {
  margin: 0;
  font-size: 0.9375rem;
  font-weight: 650;
  color: var(--color-text-primary);
}
.traffic-scenario-modal__section-desc {
  margin: 0;
  font-size: 0.75rem;
  line-height: 1.45;
  color: var(--color-text-muted);
}
.traffic-scenario-modal__panel {
  min-width: 0;
  padding: 0.75rem;
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: var(--color-bg-surface);
  box-shadow: 0 1px 2px color-mix(in srgb, var(--color-text-primary) 4%, transparent);
}
.traffic-scenario-modal__panel--table {
  padding: 0.625rem 0.75rem 0.75rem;
}
.traffic-scenario-modal__panel :deep(.traffic-breakdown) {
  gap: 0.5rem;
}
.traffic-scenario-modal__panel :deep(.traffic-breakdown__empty) {
  padding: 1.75rem 1rem;
  border-radius: var(--radius-md);
  background: color-mix(in srgb, var(--color-bg-subtle) 75%, transparent);
}
.traffic-scenario-modal__section--secondary {
  padding-top: 0.125rem;
}
.traffic-scenario-modal__panel--policy {
  padding: 0.875rem;
}
.traffic-scenario-modal__panel--history {
  padding: 0.875rem 1rem;
  background:
    linear-gradient(
      180deg,
      color-mix(in srgb, var(--color-bg-subtle) 55%, var(--color-bg-surface)),
      var(--color-bg-surface)
    );
}
.traffic-scenario-modal__panel--policy :deep(.traffic-policy-form) {
  gap: 0.875rem;
}
.traffic-scenario-modal__panel--policy :deep(.traffic-policy-form__cards) {
  gap: 0.75rem;
}
.traffic-scenario-modal__panel--policy :deep(.traffic-policy-form__card) {
  box-shadow: 0 1px 1px color-mix(in srgb, var(--color-text-primary) 3%, transparent);
}
.traffic-scenario-modal__panel--history :deep(.traffic-history-manager) {
  gap: 0.875rem;
}
@media (max-width: 720px) {
  .traffic-scenario-modal__context {
    align-items: flex-start;
  }
  .traffic-scenario-modal__context-hint {
    max-width: none;
    text-align: left;
  }
}

</style>
