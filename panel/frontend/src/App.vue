<template>
  <div id="app">
    <!-- Loading Screen -->
    <div v-if="!ruleStore.isAuthReady" class="loading-screen">
      <div class="loading-logo">
        <div class="loading-logo__ring"></div>
        <div class="loading-logo__core">
          <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>
          </svg>
        </div>
      </div>
      <p class="loading-text">正在连接控制台...</p>
    </div>

    <!-- Token Auth -->
    <TokenAuth v-else-if="!ruleStore.isAuthenticated" />

    <!-- Main Dashboard -->
    <template v-else>
      <div class="app-shell">
        <!-- Mobile Sidebar Overlay -->
        <div v-if="sidebarOpen" class="sidebar-overlay" @click="sidebarOpen = false"></div>

        <!-- Top Navigation Bar -->
        <nav class="topbar">
          <div class="topbar__left">
            <button class="topbar__hamburger" @click="sidebarOpen = !sidebarOpen">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <line x1="3" y1="6" x2="21" y2="6"/>
                <line x1="3" y1="12" x2="21" y2="12"/>
                <line x1="3" y1="18" x2="21" y2="18"/>
              </svg>
            </button>
            <div class="topbar__brand">
              <div class="topbar__logo">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                  <polygon points="13 2 3 14 12 14 11 22 21 10 12 10 13 2"/>
                </svg>
              </div>
              <div class="topbar__title">
                <span class="topbar__name">Nginx Proxy</span>
                <span class="topbar__badge">管理端</span>
              </div>
            </div>
          </div>

          <div class="topbar__center">
            <div class="topbar__nav">
              <button class="topbar__nav-item topbar__nav-item--active">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/>
                  <rect x="14" y="14" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/>
                </svg>
                <span>仪表盘</span>
              </button>
              <button class="topbar__nav-item" @click="showJoinModal = true">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M16 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/>
                  <circle cx="8.5" cy="7" r="4"/>
                  <line x1="20" y1="8" x2="20" y2="14"/><line x1="23" y1="11" x2="17" y2="11"/>
                </svg>
                <span>加入节点</span>
              </button>
              <button class="topbar__nav-item" @click="openGlobalSearch">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <circle cx="11" cy="11" r="8"/>
                  <line x1="21" y1="21" x2="16.65" y2="16.65"/>
                </svg>
                <span>全局搜索</span>
              </button>
            </div>
          </div>

          <div class="topbar__actions">
            <!-- Mobile quick actions (shown when topbar nav is hidden) -->
            <button class="topbar__action topbar__nav-mobile" @click="openGlobalSearch" title="全局搜索">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
              </svg>
            </button>
            <button class="topbar__action topbar__nav-mobile" @click="showJoinModal = true" title="加入节点">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M16 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/>
                <circle cx="8.5" cy="7" r="4"/>
                <line x1="20" y1="8" x2="20" y2="14"/><line x1="23" y1="11" x2="17" y2="11"/>
              </svg>
            </button>
            <ThemeSelector />
            <div class="topbar__divider"></div>
            <button @click="ruleStore.logout" class="topbar__action topbar__action--logout" title="退出登录">
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
                <polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/>
              </svg>
            </button>
          </div>
        </nav>

        <!-- Main Layout -->
        <div class="app-layout">
          <!-- Sidebar -->
          <aside class="sidebar" :class="{ 'sidebar--open': sidebarOpen, 'sidebar--collapsed': sidebarCollapsed }">
            <div class="sidebar__section">
              <div class="sidebar__section-header">
                <span class="sidebar__section-title" v-show="!sidebarCollapsed">Agent 节点</span>
                <div class="sidebar__section-header-actions">
                  <button @click="ruleStore.loadAgents" class="sidebar__section-action" title="刷新">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <polyline points="23 4 23 10 17 10"/>
                      <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>
                    </svg>
                  </button>
                  <button @click="toggleSidebarCollapse" class="sidebar__section-action sidebar__collapse-btn" :title="sidebarCollapsed ? '展开侧栏' : '收起侧栏'">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" :style="{ transform: sidebarCollapsed ? 'rotate(180deg)' : '' }">
                      <polyline points="15 18 9 12 15 6"/>
                    </svg>
                  </button>
                </div>
              </div>

              <div v-if="!sidebarCollapsed && ruleStore.agents.length" class="sidebar__search">
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <circle cx="11" cy="11" r="8"/>
                  <line x1="21" y1="21" x2="16.65" y2="16.65"/>
                </svg>
                <input
                  v-model="agentSearchQuery"
                  type="text"
                  class="sidebar__search-input"
                  placeholder="搜索节点名称、地址..."
                >
                <button v-if="agentSearchQuery" class="sidebar__search-clear" @click="agentSearchQuery = ''" title="清除">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                    <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
                  </svg>
                </button>
              </div>
              <div v-if="!sidebarCollapsed && agentSearchQuery" class="sidebar__search-meta">
                找到 <strong>{{ filteredAgents.length }}</strong> / {{ ruleStore.agents.length }} 个节点
              </div>

              <div class="sidebar__agents">
                <div
                  v-for="agent in filteredAgents"
                  :key="agent.id"
                  class="sidebar__agent"
                  :class="{ 'sidebar__agent--active': ruleStore.selectedAgentId === agent.id }"
                  @click="handleSelectAgent(agent.id); sidebarOpen = false"
                  :title="getAgentTooltip(agent)"
                >
                  <div class="sidebar__agent-indicator" :class="`sidebar__agent-indicator--${getAgentSyncStatus(agent)}`"></div>
                  <div class="sidebar__agent-info" v-show="!sidebarCollapsed">
                    <div class="sidebar__agent-name">{{ agent.name }}</div>
                    <div class="sidebar__agent-meta" :title="formatAgentUrl(agent.agent_url, agent.mode, agent.last_seen_ip)">
                      <span class="sidebar__agent-mode-icon" :title="getAgentModeLabel(agent.mode)">
                        <!-- local: monitor -->
                        <svg v-if="agent.mode === 'local'" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                          <rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/>
                        </svg>
                        <!-- master: server -->
                        <svg v-else-if="agent.mode === 'master'" width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                          <rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/>
                        </svg>
                        <!-- pull: download -->
                        <svg v-else width="11" height="11" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                          <polyline points="8 17 12 21 16 17"/><line x1="12" y1="3" x2="12" y2="21"/>
                        </svg>
                      </span>
                      <span class="sidebar__agent-url-text">{{ getAgentUrlPart(agent.agent_url, agent.mode, agent.last_seen_ip) }}</span>
                    </div>
                  </div>
                  <div class="sidebar__agent-actions" v-show="!sidebarCollapsed" @click.stop>
                    <button
                      class="sidebar__agent-action"
                      title="重命名"
                      @click.stop="handleRenameAgent(agent)"
                    >
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                        <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                      </svg>
                    </button>
                    <button
                      v-if="!agent.is_local"
                      class="sidebar__agent-action sidebar__agent-action--danger"
                      title="删除节点"
                      @click.stop="handleDeleteAgent(agent)"
                    >
                      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                        <polyline points="3 6 5 6 21 6"/>
                        <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
                        <path d="M10 11v6M14 11v6"/>
                        <path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/>
                      </svg>
                    </button>
                  </div>
                </div>

                <div v-if="!ruleStore.agents.length" class="sidebar__empty">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
                    <ellipse cx="12" cy="5" rx="9" ry="3"/>
                    <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
                    <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
                  </svg>
                  <span v-show="!sidebarCollapsed">暂无节点</span>
                </div>
                <div v-else-if="!filteredAgents.length" class="sidebar__empty">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
                    <circle cx="11" cy="11" r="7"/>
                    <line x1="21" y1="21" x2="16.65" y2="16.65"/>
                  </svg>
                  <span v-show="!sidebarCollapsed">未找到匹配节点</span>
                </div>
              </div>
            </div>

            <div class="sidebar__status-bar" v-show="!sidebarCollapsed">
              <div class="sidebar__status-dot" :class="ruleStore.selectedAgent?.status === 'online' ? 'sidebar__status-dot--online' : 'sidebar__status-dot--offline'"></div>
              <span class="sidebar__status-text">
                {{ ruleStore.selectedAgent ? (ruleStore.selectedAgent.status === 'online' ? '正常' : '离线') : '未选择' }}
              </span>
              <span class="sidebar__status-sep">·</span>
              <span class="sidebar__status-counts">{{ ruleStore.agents.length }} 节点 · {{ ruleStore.onlineAgentsCount }} 在线</span>
            </div>
          </aside>

          <!-- Content Area -->
          <main class="content">
            <!-- Content Header -->
            <div class="content__header">
              <div class="content__header-left">
                <div class="content__breadcrumb">
                  <span class="content__breadcrumb-item">控制台</span>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <polyline points="9 18 15 12 9 6"/>
                  </svg>
                  <span class="content__breadcrumb-item content__breadcrumb-item--active">
                    {{ ruleStore.selectedAgent?.name || '规则管理' }}
                  </span>
                </div>
                <h2 class="content__title">
                  {{ ruleStore.selectedAgent?.name || '规则管理' }}
                </h2>
                <p class="content__subtitle">
                  <template v-if="ruleStore.hasSelectedAgent">
                    <template v-if="activeTab === 'http'">HTTP 规则 {{ ruleStore.rules.length }} 条，启用 {{ activeRulesCount }} 条，显示 {{ ruleStore.filteredRules.length }} 条</template>
                    <template v-else-if="activeTab === 'l4'">L4 规则 {{ ruleStore.l4Rules.length }} 条，启用 {{ activeL4Count }} 条，显示 {{ ruleStore.filteredL4Rules.length }} 条</template>
                    <template v-else>统一证书 {{ ruleStore.certificates.length }} 项，启用 {{ activeCertsCount }} 项，显示 {{ ruleStore.filteredCertificates.length }} 项</template>
                  </template>
                  <template v-else>请选择左侧节点管理规则</template>
                </p>
              </div>
              <div class="content__header-right">
                <div v-if="activeTab === 'http' && ruleStore.hasSelectedAgent && ruleStore.hasRules" class="content__search">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <circle cx="11" cy="11" r="8"/>
                    <line x1="21" y1="21" x2="16.65" y2="16.65"/>
                  </svg>
                  <input
                    v-model="ruleStore.searchQuery"
                    type="text"
                    class="content__search-input"
                    placeholder="搜索 URL / 标签 / ID..."
                  >
                </div>
                <div v-else-if="activeTab === 'l4' && ruleStore.hasSelectedAgent && ruleStore.hasL4Rules" class="content__search">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <circle cx="11" cy="11" r="8"/>
                    <line x1="21" y1="21" x2="16.65" y2="16.65"/>
                  </svg>
                  <input
                    v-model="ruleStore.l4SearchQuery"
                    type="text"
                    class="content__search-input"
                    placeholder="搜索名称 / 协议 / 端口..."
                  >
                </div>
                <div v-else-if="activeTab === 'certs' && ruleStore.certificates.length" class="content__search">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <circle cx="11" cy="11" r="8"/>
                    <line x1="21" y1="21" x2="16.65" y2="16.65"/>
                  </svg>
                  <input
                    v-model="ruleStore.certSearchQuery"
                    type="text"
                    class="content__search-input"
                    placeholder="搜索域名 / 标签..."
                  >
                </div>
                <button
                  v-if="activeTab === 'http'"
                  class="btn btn--primary"
                  :disabled="!ruleStore.hasSelectedAgent"
                  @click="showAddModal = true"
                >
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                    <line x1="12" y1="5" x2="12" y2="19"/>
                    <line x1="5" y1="12" x2="19" y2="12"/>
                  </svg>
                  <span>添加 HTTP 规则</span>
                </button>
                <button
                  v-else-if="activeTab === 'l4'"
                  class="btn btn--primary"
                  :disabled="!ruleStore.hasSelectedAgent"
                  @click="showL4Modal = true"
                >
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                    <line x1="12" y1="5" x2="12" y2="19"/>
                    <line x1="5" y1="12" x2="19" y2="12"/>
                  </svg>
                  <span>添加 L4 规则</span>
                </button>
                <button
                  v-else-if="activeTab === 'certs'"
                  class="btn btn--primary"
                  @click="showCertModal = true"
                >
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
                    <line x1="12" y1="5" x2="12" y2="19"/>
                    <line x1="5" y1="12" x2="19" y2="12"/>
                  </svg>
                  <span>添加证书</span>
                </button>
              </div>
            </div>

            <!-- Stats Row -->
            <div class="stats-row">
              <div class="stat-pill" :class="{ 'stat-pill--clickable': true, 'stat-pill--selected': activeTab === 'http' }" @click="activeTab = 'http'">
                <div class="stat-pill__icon stat-pill__icon--rules">
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/>
                    <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>
                  </svg>
                </div>
                <div class="stat-pill__data">
                  <div class="stat-pill__row">
                    <span class="stat-pill__value">{{ ruleStore.rules.length }}</span>
                    <span class="stat-pill__unit">条</span>
                  </div>
                  <span class="stat-pill__label">HTTP 规则</span>
                </div>
              </div>
              <div class="stat-pill" :class="{ 'stat-pill--clickable': true, 'stat-pill--selected': activeTab === 'l4' }" @click="activeTab = 'l4'">
                <div class="stat-pill__icon stat-pill__icon--l4">
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <rect x="2" y="2" width="20" height="8" rx="2" ry="2"/>
                    <rect x="2" y="14" width="20" height="8" rx="2" ry="2"/>
                  </svg>
                </div>
                <div class="stat-pill__data">
                  <div class="stat-pill__row">
                    <span class="stat-pill__value">{{ ruleStore.l4Rules.length }}</span>
                    <span class="stat-pill__unit">条</span>
                  </div>
                  <span class="stat-pill__label">L4 规则</span>
                </div>
              </div>
              <div class="stat-pill" :class="{ 'stat-pill--clickable': true, 'stat-pill--selected': activeTab === 'certs' }" @click="activeTab = 'certs'">
                <div class="stat-pill__icon stat-pill__icon--certs">
                  <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
                    <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
                  </svg>
                </div>
                <div class="stat-pill__data">
                  <div class="stat-pill__row">
                    <span class="stat-pill__value">{{ ruleStore.certificates.length }}</span>
                    <span class="stat-pill__unit">项</span>
                  </div>
                  <span class="stat-pill__label">统一证书</span>
                </div>
              </div>
            </div>

            <section class="data-management">
              <div class="data-management__header">
                <div>
                  <h3 class="data-management__title">数据管理</h3>
                  <p class="data-management__subtitle">导出或导入与纯 Go 控制面兼容的便携备份包。</p>
                </div>
                <div class="data-management__actions">
                  <button class="btn btn--secondary" :disabled="backupBusy" @click="handleExportBackup">
                    <span v-if="backupBusy" class="spinner spinner--sm mr-2"></span>
                    <span>导出备份</span>
                  </button>
                  <button class="btn btn--primary" :disabled="backupBusy" @click="openBackupImportPicker">
                    <span v-if="backupBusy" class="spinner spinner--sm mr-2"></span>
                    <span>导入备份</span>
                  </button>
                </div>
              </div>

              <input
                ref="backupFileInput"
                type="file"
                accept=".tar.gz,.tgz,application/gzip,application/x-gzip"
                class="data-management__input"
                @change="handleBackupImportChange"
              >

              <div class="backup-report__warning">
                {{ BACKUP_SENSITIVE_WARNING }}
              </div>

              <div v-if="backupImportResult" class="backup-report">
                <div class="backup-report__summary">
                  <div class="backup-report__summary-row">
                    <span class="backup-report__summary-pill backup-report__summary-pill--imported">已导入 {{ backupSummary.imported }}</span>
                    <span class="backup-report__summary-pill">冲突跳过 {{ backupSummary.skipped_conflict }}</span>
                    <span class="backup-report__summary-pill">无效跳过 {{ backupSummary.skipped_invalid }}</span>
                    <span class="backup-report__summary-pill">缺少证书材料 {{ backupSummary.skipped_missing_material }}</span>
                  </div>
                  <div class="backup-report__meta">
                    <span>来源 {{ backupImportResult.manifest?.source_architecture || 'unknown' }}</span>
                    <span>导出于 {{ formatBackupTimestamp(backupImportResult.manifest?.exported_at) }}</span>
                  </div>
                </div>

                <div class="backup-report__details">
                  <div v-for="bucket in backupReportBuckets" :key="bucket.key" class="backup-report__bucket">
                    <div class="backup-report__bucket-header">
                      <span>{{ bucket.label }}</span>
                      <span>{{ bucket.items.length }}</span>
                    </div>
                    <div v-if="bucket.items.length" class="backup-report__bucket-list">
                      <div v-for="(item, index) in bucket.items" :key="`${bucket.key}-${index}`" class="backup-report__item">
                        {{ formatBackupReportItem(item) }}
                      </div>
                    </div>
                    <div v-else class="backup-report__empty">无</div>
                  </div>
                </div>
              </div>
            </section>

            <!-- Rules Content -->
            <div class="rules-section">
              <RuleList v-if="activeTab === 'http'" @add="showAddModal = true" />
              <L4RuleList v-else-if="activeTab === 'l4'" @add="showL4Modal = true" />
              <CertificateList v-else-if="activeTab === 'certs'" @add="showCertModal = true" />
            </div>
          </main>
        </div>
      </div>

      <!-- Add HTTP Rule Modal -->
      <Teleport to="body">
        <BaseModal
          v-model="showAddModal"
          title="添加 HTTP 规则"
          :subtitle="ruleStore.selectedAgent?.name ? `添加到: ${ruleStore.selectedAgent.name}` : ''"
        >
          <RuleForm @success="showAddModal = false" />
        </BaseModal>
      </Teleport>

      <!-- Add L4 Rule Modal -->
      <Teleport to="body">
        <BaseModal
          v-model="showL4Modal"
          title="添加 L4 规则"
          :subtitle="ruleStore.selectedAgent?.name ? `添加到: ${ruleStore.selectedAgent.name}` : ''"
        >
          <L4RuleForm @success="showL4Modal = false" />
        </BaseModal>
      </Teleport>

      <!-- Add Certificate Modal -->
      <Teleport to="body">
        <BaseModal
          v-model="showCertModal"
          title="添加统一证书"
        >
          <CertificateForm @success="showCertModal = false" />
        </BaseModal>
      </Teleport>

      <!-- Join Script Modal -->
      <Teleport to="body">
        <BaseModal
          v-model="showJoinModal"
          title="加入 Agent 节点"
          large
        >
          <div class="join-modal">
            <!-- Platform Tabs -->
            <div class="join-tabs">
              <button
                v-for="platform in joinPlatformCards"
                :key="platform.id"
                class="join-tab"
                :class="{ 'join-tab--active': selectedJoinPlatform === platform.id }"
                @click="selectedJoinPlatform = platform.id"
              >
                <span class="join-tab__icon" v-html="platform.icon"></span>
                <span>{{ platform.label }}</span>
              </button>
            </div>

            <!-- Selected Platform Command -->
            <template v-for="platform in joinPlatformCards" :key="platform.id">
              <div v-if="selectedJoinPlatform === platform.id" class="join-command-block">
                <div class="join-command-meta">
                  <span class="join-command-hint">{{ platform.hint }}</span>
                </div>
                <div class="join-command-wrap">
                  <div class="join-command-scroll">
                    <code class="join-command-code">{{ platform.command }}</code>
                  </div>
                  <button class="join-command-copy" @click="copyJoinCommand(platform)">
                    <svg v-if="copiedPlatform !== platform.id" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <rect x="9" y="9" width="13" height="13" rx="2" ry="2"/>
                      <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
                    </svg>
                    <svg v-else width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                      <polyline points="20 6 9 17 4 12"/>
                    </svg>
                    <span>{{ copiedPlatform === platform.id ? '已复制' : '复制' }}</span>
                  </button>
                </div>
                <ol class="join-steps">
                  <li v-for="step in platform.steps" :key="step" class="join-steps__item">{{ step }}</li>
                </ol>
              </div>
            </template>

            <div class="join-modal__actions">
              <button class="btn btn--secondary" @click="showJoinModal = false">关闭</button>
            </div>
          </div>
        </BaseModal>
      </Teleport>

      <!-- Rename Agent Modal -->
      <Teleport to="body">
        <BaseModal
          v-if="renamingAgent"
          v-model="showRenameModal"
          title="重命名节点"
        >
          <div class="agent-modal">
            <p class="agent-modal__desc">为节点 <strong>{{ renamingAgent.name }}</strong> 输入新名称：</p>
            <div class="input-wrapper">
              <span class="input-wrapper__icon">
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/>
                  <path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>
                </svg>
              </span>
              <input
                v-model="newAgentName"
                type="text"
                class="input"
                placeholder="新节点名称"
                @keydown.enter="confirmRename"
              >
            </div>
            <div class="agent-modal__actions">
              <button class="btn btn--secondary" @click="showRenameModal = false">取消</button>
              <button class="btn btn--primary" :disabled="!newAgentName.trim() || ruleStore.loading" @click="confirmRename">
                <span v-if="ruleStore.loading" class="spinner spinner--sm"></span>
                <span v-else>确认重命名</span>
              </button>
            </div>
          </div>
        </BaseModal>
      </Teleport>

      <!-- Delete Agent Modal -->
      <Teleport to="body">
        <BaseModal
          v-if="deletingAgent"
          v-model="showDeleteAgentModal"
          title="确认删除节点"
        >
          <div class="agent-modal">
            <p class="agent-modal__desc">确定要删除节点 <strong>{{ deletingAgent.name }}</strong> 吗？</p>
            <div class="agent-modal__warn">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"/>
                <line x1="12" y1="9" x2="12" y2="13"/><line x1="12" y1="17" x2="12.01" y2="17"/>
              </svg>
              <span>此操作将移除该节点及其所有规则，无法撤销。</span>
            </div>
            <div class="agent-modal__actions">
              <button class="btn btn--secondary" @click="showDeleteAgentModal = false">取消</button>
              <button class="btn btn--danger" :disabled="ruleStore.loading" @click="confirmDeleteAgent">
                <span v-if="ruleStore.loading" class="spinner spinner--sm"></span>
                <span v-else>确认删除</span>
              </button>
            </div>
          </div>
        </BaseModal>
      </Teleport>

      <!-- Global Search Overlay -->
      <Teleport to="body">
        <div v-if="showGlobalSearch" class="global-search-overlay" @click.self="showGlobalSearch = false">
          <div class="global-search-panel">
            <div class="global-search-header">
              <div class="global-search-input-wrap">
                <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                  <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
                </svg>
                <input
                  ref="globalSearchInput"
                  v-model="ruleStore.globalSearchQuery"
                  type="text"
                  class="global-search-input"
                  placeholder="跨节点搜索规则（URL、标签）..."
                  @input="debouncedGlobalSearch"
                >
                <button v-if="ruleStore.globalSearchQuery" class="global-search-clear" @click="ruleStore.globalSearchQuery = ''; ruleStore.globalSearchResults = []">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
                    <line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>
                  </svg>
                </button>
              </div>
              <button class="global-search-close" @click="showGlobalSearch = false">关闭</button>
            </div>

            <!-- Global stats bar -->
            <div class="global-search-stats">
              <span class="global-search-stat">
                <strong>{{ ruleStore.agents.length }}</strong> 个节点
              </span>
              <span class="global-search-stat-sep">·</span>
              <span class="global-search-stat">
                <strong>{{ ruleStore.onlineAgentsCount }}</strong> 在线
              </span>
              <span v-if="ruleStore.globalSearchQuery" class="global-search-stat-sep">·</span>
              <span v-if="ruleStore.globalSearchQuery && !ruleStore.globalSearchLoading" class="global-search-stat">
                找到 <strong>{{ ruleStore.globalSearchResults.reduce((s, g) => s + g.rules.length + (g.l4Rules?.length || 0) + (g.certificates?.length || 0), 0) }}</strong> 条结果
              </span>
            </div>

            <div class="global-search-body">
              <!-- Loading -->
              <div v-if="ruleStore.globalSearchLoading" class="global-search-loading">
                <div class="spinner"></div>
                <span>搜索中...</span>
              </div>

              <!-- No query -->
              <div v-else-if="!ruleStore.globalSearchQuery" class="global-search-hint">
                <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
                  <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
                </svg>
                <p>输入关键字搜索所有节点的代理规则</p>
              </div>

              <!-- No results -->
              <div v-else-if="ruleStore.globalSearchResults.length === 0" class="global-search-hint">
                <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
                  <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
                </svg>
                <p>未找到匹配 "{{ ruleStore.globalSearchQuery }}" 的规则</p>
              </div>

              <!-- Results -->
              <div v-else class="global-search-results">
                <div
                  v-for="group in ruleStore.globalSearchResults"
                  :key="group.agentId"
                  class="global-search-group"
                >
                  <div class="global-search-group-header">
                    <div class="global-search-group-dot" :class="ruleStore.agents.find(a=>a.id===group.agentId)?.status === 'online' ? 'global-search-group-dot--online' : 'global-search-group-dot--offline'"></div>
                    <span class="global-search-group-name">{{ group.agentName }}</span>
                    <span class="global-search-group-count">{{ group.rules.length + (group.l4Rules?.length || 0) + (group.certificates?.length || 0) }} 条</span>
                  </div>
                  <div
                    v-for="rule in group.rules"
                    :key="'http-' + rule.id"
                    class="global-search-rule"
                    @click="jumpToAgentRule(group.agentId, rule.id)"
                  >
                    <div class="global-search-rule-status" :class="rule.enabled ? 'global-search-rule-status--on' : 'global-search-rule-status--off'"></div>
                    <div class="global-search-rule-info">
                      <div class="global-search-rule-front">{{ rule.frontend_url }}</div>
                      <div class="global-search-rule-back">→ {{ rule.backend_url }}</div>
                    </div>
                    <div class="global-search-rule-tags">
                      <span v-for="tag in (rule.tags || []).slice(0,3)" :key="tag" class="tag tag--sm">{{ tag }}</span>
                    </div>
                  </div>
                  <div
                    v-for="rule in (group.l4Rules || [])"
                    :key="'l4-' + rule.id"
                    class="global-search-rule"
                    @click="jumpToAgentRule(group.agentId, rule.id, 'l4')"
                  >
                    <div class="global-search-rule-status" :class="rule.enabled ? 'global-search-rule-status--on' : 'global-search-rule-status--off'"></div>
                    <div class="global-search-rule-info">
                      <div class="global-search-rule-front">
                        <span class="global-search-rule-proto">{{ rule.protocol?.toUpperCase() }}</span>
                        {{ rule.listen_host }}:{{ rule.listen_port }}
                      </div>
                      <div class="global-search-rule-back">→ {{ rule.upstream_host }}:{{ rule.upstream_port }}</div>
                    </div>
                    <div class="global-search-rule-tags">
                      <span v-for="tag in (rule.tags || []).slice(0,3)" :key="tag" class="tag tag--sm">{{ tag }}</span>
                    </div>
                  </div>
                  <div
                    v-for="cert in (group.certificates || [])"
                    :key="'cert-' + cert.id"
                    class="global-search-rule"
                    @click="jumpToCert(group.agentId, cert.id)"
                  >
                    <div class="global-search-rule-status" :class="cert.status === 'active' && cert.enabled !== false ? 'global-search-rule-status--on' : 'global-search-rule-status--off'"></div>
                    <div class="global-search-rule-info">
                      <div class="global-search-rule-front">
                        <span class="global-search-rule-proto">{{ cert.scope === 'ip' ? 'IP' : 'SSL' }}</span>
                        {{ cert.domain }}
                      </div>
                      <div class="global-search-rule-back">{{ cert.issuer_mode === 'master_cf_dns' ? 'Master DNS' : '本地签发' }}</div>
                    </div>
                    <div class="global-search-rule-tags">
                      <span v-for="tag in (cert.tags || []).slice(0,3)" :key="tag" class="tag tag--sm">{{ tag }}</span>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>
        </div>
      </Teleport>

      <!-- Status Messages -->
      <StatusMessage />

      <!-- Mobile Bottom Navigation -->
      <nav class="mobile-bottom-nav">
        <button class="mobile-bottom-nav__item mobile-bottom-nav__item--active">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/>
            <rect x="14" y="14" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/>
          </svg>
          <span>仪表盘</span>
        </button>
        <button class="mobile-bottom-nav__item" @click="sidebarOpen = !sidebarOpen">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <ellipse cx="12" cy="5" rx="9" ry="3"/>
            <path d="M21 12c0 1.66-4 3-9 3s-9-1.34-9-3"/>
            <path d="M3 5v14c0 1.66 4 3 9 3s9-1.34 9-3V5"/>
          </svg>
          <span>节点</span>
        </button>
        <button class="mobile-bottom-nav__item" @click="openGlobalSearch">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>
          </svg>
          <span>搜索</span>
        </button>
        <button class="mobile-bottom-nav__item" @click="showJoinModal = true">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M16 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2"/>
            <circle cx="8.5" cy="7" r="4"/>
            <line x1="20" y1="8" x2="20" y2="14"/><line x1="23" y1="11" x2="17" y2="11"/>
          </svg>
          <span>加入</span>
        </button>
        <button class="mobile-bottom-nav__item" @click="ruleStore.logout">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4"/>
            <polyline points="16 17 21 12 16 7"/><line x1="21" y1="12" x2="9" y2="12"/>
          </svg>
          <span>退出</span>
        </button>
      </nav>
    </template>
  </div>
</template>

<script setup>
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRuleStore } from './stores/rules'
import RuleForm from './components/RuleForm.vue'
import RuleList from './components/RuleList.vue'
import L4RuleForm from './components/L4RuleForm.vue'
import L4RuleList from './components/L4RuleList.vue'
import CertificateForm from './components/CertificateForm.vue'
import CertificateList from './components/CertificateList.vue'
import ThemeSelector from './components/base/ThemeSelector.vue'
import TokenAuth from './components/base/TokenAuth.vue'
import BaseModal from './components/base/BaseModal.vue'
import StatusMessage from './components/StatusMessage.vue'
import { getAgentSyncStatus } from './utils/syncStatus'
import {
  BACKUP_IMPORT_CONFIRMATION_MESSAGE,
  BACKUP_SENSITIVE_WARNING,
  getBackupDownloadRevokeDelayMs,
  getBackupSummaryTotals,
  formatBackupReportItem
} from './utils/backupImport'
import { exportBackupPackage, importBackupPackage } from './api'

const ruleStore = useRuleStore()
const activeTab = ref('http')
const showAddModal = ref(false)
const showL4Modal = ref(false)
const showCertModal = ref(false)
const showJoinModal = ref(false)
const backupFileInput = ref(null)
const backupBusy = ref(false)
const backupImportResult = ref(null)
const sidebarOpen = ref(false)
const sidebarCollapsed = ref(localStorage.getItem('sidebar_collapsed') === 'true')
const agentSearchQuery = ref('')
const copiedPlatform = ref('')
const selectedJoinPlatform = ref('linux')
let refreshTimer = null
let copyStateTimer = null

// Agent rename/delete state
const showRenameModal = ref(false)
const renamingAgent = ref(null)
const newAgentName = ref('')
const showDeleteAgentModal = ref(false)
const deletingAgent = ref(null)

// Global search state
const showGlobalSearch = ref(false)
const globalSearchInput = ref(null)
let globalSearchTimer = null

function toggleSidebarCollapse() {
  sidebarCollapsed.value = !sidebarCollapsed.value
  localStorage.setItem('sidebar_collapsed', sidebarCollapsed.value)
}

function getAgentModeLabel(mode) {
  if (mode === 'local') return '本机节点'
  if (mode === 'master') return '主控模式'
  return '拉取模式'
}

function getAgentUrlPart(url, mode, lastSeenIp) {
  if (!url) {
    if (mode === 'local') return ''
    if (lastSeenIp) return lastSeenIp
    return ''
  }
  try {
    const u = new URL(url)
    const port = u.port && u.port !== '80' && u.port !== '443' ? `:${u.port}` : ''
    return `${u.hostname}${port}`
  } catch {
    return url.replace(/^https?:\/\//, '')
  }
}

function formatAgentUrl(url, mode, lastSeenIp) {
  const modeLabel = getAgentModeLabel(mode)
  if (!url) {
    if (mode === 'local') return modeLabel
    if (lastSeenIp) return `${modeLabel} · ${lastSeenIp}`
    return modeLabel
  }
  try {
    const u = new URL(url)
    const host = u.hostname
    const port = u.port && u.port !== '80' && u.port !== '443' ? `:${u.port}` : ''
    return `${modeLabel} · ${host}${port}`
  } catch {
    return `${modeLabel} · ${url.replace(/^https?:\/\//, '')}`
  }
}

function shellQuote(value) {
  return `'${String(value).replace(/'/g, `'"'"'`)}'`
}

function powerShellQuote(value) {
  return `'${String(value).replace(/'/g, "''")}'`
}

function resetCopyState() {
  copiedPlatform.value = ''
  if (copyStateTimer) {
    window.clearTimeout(copyStateTimer)
    copyStateTimer = null
  }
}

const activeRulesCount = computed(() => {
  return ruleStore.rules.filter(r => r.enabled).length
})

const activeL4Count = computed(() => {
  return ruleStore.l4Rules.filter(r => r.enabled).length
})

const activeCertsCount = computed(() => {
  return ruleStore.certificates.filter(c => c.enabled).length
})

const backupSummary = computed(() => {
  return getBackupSummaryTotals(backupImportResult.value?.summary)
})

const backupReportBuckets = computed(() => {
  const report = backupImportResult.value?.report || {}
  return [
    { key: 'imported', label: '已导入', items: Array.isArray(report.imported) ? report.imported : [] },
    { key: 'skipped_conflict', label: '冲突跳过', items: Array.isArray(report.skipped_conflict) ? report.skipped_conflict : [] },
    { key: 'skipped_invalid', label: '无效跳过', items: Array.isArray(report.skipped_invalid) ? report.skipped_invalid : [] },
    { key: 'skipped_missing_material', label: '缺少证书材料', items: Array.isArray(report.skipped_missing_material) ? report.skipped_missing_material : [] }
  ]
})

// Returns the tooltip text shown when hovering over an agent in the sidebar.
function getAgentTooltip(agent) {
  if (sidebarCollapsed.value) return agent.name
  const status = getAgentSyncStatus(agent)
  if (status === 'failed' && agent.last_apply_message) return `应用失败: ${agent.last_apply_message}`
  if (status === 'pending') return '等待 Agent 心跳同步配置...'
  return ''
}

const filteredAgents = computed(() => {
  const query = agentSearchQuery.value.trim().toLowerCase()
  if (!query) return ruleStore.agents

  return ruleStore.agents.filter((agent) => {
    const searchable = [
      agent.name,
      agent.agent_url,
      agent.status,
      Array.isArray(agent.tags) ? agent.tags.join(' ') : ''
    ]

    return searchable.some(item => String(item || '').toLowerCase().includes(query))
  })
})

const joinScriptUrl = computed(() => {
  return `${window.location.origin}/panel-api/public/join-agent.sh`
})

const joinRegisterToken = computed(() => {
  return ruleStore.token || '<YOUR_TOKEN>'
})

const linuxJoinCommand = computed(() => {
  return `curl -fsSL ${shellQuote(joinScriptUrl.value)} | bash -s -- --register-token ${shellQuote(joinRegisterToken.value)} --install-systemd`
})

const macJoinCommand = computed(() => {
  return `curl -fsSL ${shellQuote(joinScriptUrl.value)} | bash -s -- --register-token ${shellQuote(joinRegisterToken.value)} --install-launchd`
})

const windowsJoinCommand = computed(() => {
  const wslCommand = powerShellQuote(linuxJoinCommand.value)
  return `powershell -NoProfile -ExecutionPolicy Bypass -Command "$cmd=${wslCommand}; if (-not (Get-Command wsl -ErrorAction SilentlyContinue)) { throw '请先安装 WSL'; }; wsl bash -lc $cmd"`
})

const joinPlatformCards = computed(() => {
  return [
    {
      id: 'linux',
      label: 'Linux',
      icon: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2z"/><path d="M8 14s1.5 2 4 2 4-2 4-2"/><line x1="9" y1="9" x2="9.01" y2="9"/><line x1="15" y1="9" x2="15.01" y2="9"/></svg>',
      hint: '自动安装依赖并注册 systemd 开机自启',
      command: linuxJoinCommand.value,
      steps: [
        '确保目标主机已联网，具备 curl 与 bash',
        '以 root 或 sudo 权限在目标主机上执行上方命令',
        '脚本自动安装 Node.js、Nginx，并注册 systemd 服务开机自启',
        '安装完成后节点将在数秒内自动出现在节点列表'
      ]
    },
    {
      id: 'macos',
      label: 'macOS',
      icon: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 20.94c1.5 0 2.75-.08 3.5-.2 1-.16 1.5-.5 1.5-1.24V9a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v10.5c0 .74.5 1.08 1.5 1.24.75.12 2 .2 3.5.2z"/><path d="M12 5V3"/></svg>',
      hint: '自动安装依赖（Homebrew）并注册 launchd 自启',
      command: macJoinCommand.value,
      steps: [
        '建议以当前用户（非 root）执行，避免 Homebrew 权限问题',
        '若未安装 Homebrew，脚本将尝试自动安装',
        '脚本安装 Node.js、Nginx，注册 launchd 开机自启项',
        '安装完成后节点将在数秒内自动出现在节点列表'
      ]
    },
    {
      id: 'windows',
      label: 'Windows',
      icon: '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="8" height="8"/><rect x="13" y="3" width="8" height="8"/><rect x="3" y="13" width="8" height="8"/><rect x="13" y="13" width="8" height="8"/></svg>',
      hint: 'PowerShell + WSL，需预先安装 WSL',
      command: windowsJoinCommand.value,
      steps: [
        '需预先安装 WSL2（Win 10 2004+ / Win 11 原生支持）',
        '以管理员权限打开 PowerShell，执行上方命令',
        '命令将在 WSL2 环境内运行 Linux 安装流程',
        '安装完成后节点将在数秒内自动出现在节点列表'
      ]
    }
  ]
})

function ensureRefreshTimer() {
  if (refreshTimer) return
  refreshTimer = window.setInterval(() => {
    ruleStore.refreshClusterStatus()
  }, 10000)
}

async function handleSelectAgent(agentId) {
  await ruleStore.selectAgent(agentId)
}

function handleRenameAgent(agent) {
  renamingAgent.value = agent
  newAgentName.value = agent.name
  showRenameModal.value = true
}

async function confirmRename() {
  if (!newAgentName.value.trim() || !renamingAgent.value) return
  try {
    await ruleStore.renameAgent(renamingAgent.value.id, newAgentName.value.trim())
    showRenameModal.value = false
  } catch {}
}

function handleDeleteAgent(agent) {
  deletingAgent.value = agent
  showDeleteAgentModal.value = true
}

async function confirmDeleteAgent() {
  if (!deletingAgent.value) return
  try {
    await ruleStore.removeAgent(deletingAgent.value.id)
    showDeleteAgentModal.value = false
  } catch {}
}

async function openGlobalSearch() {
  showGlobalSearch.value = true
  await nextTick()
  globalSearchInput.value?.focus()
}

function openBackupImportPicker() {
  backupFileInput.value?.click()
}

function formatBackupTimestamp(value) {
  if (!value) return '-'
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) return String(value)
  return parsed.toLocaleString()
}

async function handleExportBackup() {
  if (backupBusy.value) return
  backupBusy.value = true
  try {
    const { blob, filename } = await exportBackupPackage()
    const downloadUrl = window.URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = downloadUrl
    anchor.download = filename || 'nre-backup.tar.gz'
    document.body.appendChild(anchor)
    anchor.click()
    document.body.removeChild(anchor)
    window.setTimeout(() => window.URL.revokeObjectURL(downloadUrl), getBackupDownloadRevokeDelayMs())
    ruleStore.showSuccess('备份导出已开始')
  } catch (err) {
    ruleStore.showError(err.message || '导出备份失败')
  } finally {
    backupBusy.value = false
  }
}

async function handleBackupImportChange(event) {
  const file = event.target?.files?.[0]
  if (!file || backupBusy.value) {
    if (event.target) event.target.value = ''
    return
  }
  if (!window.confirm(BACKUP_IMPORT_CONFIRMATION_MESSAGE)) {
    if (event.target) event.target.value = ''
    return
  }

  backupBusy.value = true
  try {
    backupImportResult.value = await importBackupPackage(file)
    await ruleStore.initialize()
    ruleStore.showSuccess('备份导入完成')
  } catch (err) {
    ruleStore.showError(err.message || '导入备份失败')
  } finally {
    backupBusy.value = false
    if (event.target) event.target.value = ''
  }
}

function debouncedGlobalSearch() {
  if (globalSearchTimer) clearTimeout(globalSearchTimer)
  globalSearchTimer = setTimeout(() => {
    ruleStore.performGlobalSearch(ruleStore.globalSearchQuery)
  }, 400)
}

async function jumpToAgentRule(agentId, ruleId, tab = 'http') {
  showGlobalSearch.value = false
  await ruleStore.selectAgent(agentId)
  if (tab === 'l4') {
    activeTab.value = 'l4'
    ruleStore.l4SearchQuery = `#id=${ruleId}`
  } else {
    activeTab.value = 'http'
    ruleStore.searchQuery = `#id=${ruleId}`
  }
}

async function jumpToCert(agentId, certId) {
  showGlobalSearch.value = false
  await ruleStore.selectAgent(agentId)
  activeTab.value = 'certs'
  ruleStore.certSearchQuery = `#id=${certId}`
}

async function copyText(text, successMessage = '已复制') {
  const value = String(text || '')
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value)
    } else {
      const textarea = document.createElement('textarea')
      textarea.value = value
      textarea.setAttribute('readonly', 'readonly')
      textarea.style.position = 'fixed'
      textarea.style.opacity = '0'
      document.body.appendChild(textarea)
      textarea.select()
      document.execCommand('copy')
      document.body.removeChild(textarea)
    }
    ruleStore.showSuccess(successMessage)
  } catch (err) {
    ruleStore.showError('复制失败，请手动复制命令')
    throw err
  }
}

async function copyJoinCommand(platform) {
  await copyText(platform.command, `${platform.label} 命令已复制`)
  copiedPlatform.value = platform.id
  if (copyStateTimer) window.clearTimeout(copyStateTimer)
  copyStateTimer = window.setTimeout(() => {
    copiedPlatform.value = ''
    copyStateTimer = null
  }, 2000)
}

watch(showJoinModal, (visible) => {
  if (!visible) resetCopyState()
})

onMounted(async () => {
  await ruleStore.checkAuth()
})

watch(
  () => ruleStore.isAuthenticated,
  async (nextValue, prevValue) => {
    if (nextValue && !prevValue) {
      await ruleStore.initialize()
      ensureRefreshTimer()
    }
  }
)

onUnmounted(() => {
  resetCopyState()
  if (refreshTimer) window.clearInterval(refreshTimer)
})
</script>

<style scoped>
/* ==========================================
   Loading Screen
   ========================================== */
.loading-screen {
  min-height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-6);
  background: var(--theme-bg);
}

.loading-logo {
  position: relative;
  width: 64px;
  height: 64px;
}

.loading-logo__ring {
  position: absolute;
  inset: 0;
  border: 3px solid var(--color-border-default);
  border-top-color: var(--color-primary);
  border-right-color: var(--color-primary-hover);
  border-radius: 50%;
  animation: spin 1s linear infinite;
}

.loading-logo__core {
  position: absolute;
  inset: 50%;
  transform: translate(-50%, -50%);
  color: var(--color-primary);
  animation: pulse 1.5s ease-in-out infinite;
}

.loading-text {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
}

.join-modal {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

/* Platform Tabs */
.join-tabs {
  display: flex;
  gap: var(--space-2);
  background: var(--color-bg-subtle);
  padding: var(--space-1);
  border-radius: var(--radius-xl);
}

.join-tab {
  flex: 1;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: none;
  border-radius: var(--radius-lg);
  background: transparent;
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: background var(--duration-normal) var(--ease-default),
              color var(--duration-normal) var(--ease-default);
  font-family: inherit;
}

.join-tab:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

.join-tab--active {
  background: var(--color-bg-surface);
  color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}

.join-tab__icon {
  display: flex;
  align-items: center;
  color: inherit;
}

/* Command Block */
.join-command-block {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.join-command-meta {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.join-command-hint {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
}

.join-command-wrap {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  background: var(--color-bg-subtle);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: var(--space-3) var(--space-4);
}

.join-command-scroll {
  flex: 1;
  min-width: 0;
  overflow-x: auto;
  scrollbar-width: none;
}

.join-command-scroll::-webkit-scrollbar {
  display: none;
}

.join-command-code {
  display: block;
  font-family: var(--font-mono);
  font-size: var(--text-xs);
  color: var(--color-text-primary);
  line-height: 1.7;
  white-space: pre;
}

.join-command-copy {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-1-5) var(--space-3);
  background: var(--gradient-primary);
  color: white;
  border: none;
  border-radius: var(--radius-lg);
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  cursor: pointer;
  transition: box-shadow var(--duration-normal) var(--ease-default),
              filter var(--duration-normal) var(--ease-default);
  font-family: inherit;
  white-space: nowrap;
}

.join-command-copy:hover {
  box-shadow: var(--shadow-glow);
  filter: brightness(1.1);
}

/* Install Steps */
.join-steps {
  list-style: none;
  padding: var(--space-3) var(--space-4);
  margin: 0;
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  counter-reset: step-counter;
}

.join-steps__item {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2-5);
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  line-height: 1.5;
  counter-increment: step-counter;
}

.join-steps__item::before {
  content: counter(step-counter);
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  min-width: 16px;
  font-size: 10px;
  font-weight: var(--font-semibold);
  font-family: var(--font-mono);
  background: var(--gradient-primary);
  color: white;
  border-radius: 50%;
  flex-shrink: 0;
  margin-top: 1px;
}

.join-modal__actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-3);
}

@keyframes spin { to { transform: rotate(360deg); } }

/* ==========================================
   App Shell
   ========================================== */
.app-shell {
  height: 100dvh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

/* ==========================================
   Sidebar Overlay (Mobile)
   ========================================== */
.sidebar-overlay {
  display: none;
}

/* ==========================================
   Top Navigation Bar
   ========================================== */
.topbar {
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 var(--space-5);
  background: var(--color-bg-surface);
  border-bottom: 1px solid var(--color-border-default);
  backdrop-filter: blur(16px);
  position: sticky;
  top: 0;
  z-index: var(--z-sticky);
  flex-shrink: 0;
}

.topbar__left {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.topbar__hamburger {
  display: none;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: var(--radius-lg);
  color: var(--color-text-secondary);
  cursor: pointer;
  border: none;
  background: transparent;
}

.topbar__brand {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.topbar__logo {
  width: 36px;
  height: 36px;
  background: var(--gradient-primary);
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  color: white;
  box-shadow: var(--shadow-md);
  flex-shrink: 0;
}

.topbar__title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.topbar__name {
  font-size: var(--text-base);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
}

.topbar__badge {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  padding: 2px 8px;
  background: var(--gradient-primary);
  color: white;
  border-radius: var(--radius-full);
  letter-spacing: 0.03em;
}

.topbar__center {
  display: flex;
  align-items: center;
}

.topbar__nav {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  background: var(--color-bg-subtle);
  padding: var(--space-1);
  border-radius: var(--radius-xl);
}

.topbar__nav-item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
  border-radius: var(--radius-lg);
  cursor: pointer;
  transition: background var(--duration-normal) var(--ease-default),
              color var(--duration-normal) var(--ease-default);
  border: none;
  background: transparent;
  font-family: inherit;
  white-space: nowrap;
}

.topbar__nav-item:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

.topbar__nav-item--active {
  color: var(--color-text-inverse);
  background: var(--gradient-primary);
  box-shadow: var(--shadow-sm);
}

.topbar__actions {
  display: flex;
  align-items: center;
  gap: var(--space-3);
}

.topbar__divider {
  width: 1px;
  height: 24px;
  background: var(--color-border-default);
}

.topbar__action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: var(--radius-lg);
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: background var(--duration-normal) var(--ease-default),
              color var(--duration-normal) var(--ease-default);
  border: none;
  background: transparent;
}

.topbar__action:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

.topbar__action--logout:hover {
  color: var(--color-danger);
  background: var(--color-danger-50);
}

/* ==========================================
   App Layout (Sidebar + Content)
   ========================================== */
.app-layout {
  display: flex;
  flex: 1;
  min-height: 0;
}

/* ==========================================
   Sidebar
   ========================================== */
.sidebar {
  width: 280px;
  display: flex;
  flex-direction: column;
  background: var(--color-bg-surface);
  border-right: 1px solid var(--color-border-default);
  backdrop-filter: blur(12px);
  flex-shrink: 0;
  overflow: hidden;
}

.sidebar__section {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-4);
}

.sidebar__section-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: var(--space-3);
}

.sidebar__section-header-actions {
  display: flex;
  align-items: center;
  gap: var(--space-1);
}

.sidebar__collapse-btn svg {
  transition: transform var(--duration-normal) var(--ease-bounce);
}

.sidebar__section-title {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  color: var(--color-text-tertiary);
}

.sidebar__search {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2-5) var(--space-3);
  margin-bottom: var(--space-3);
  background: var(--color-bg-subtle);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  transition: all var(--duration-normal) var(--ease-default);
}

.sidebar__search:focus-within {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.sidebar__search svg {
  color: var(--color-text-muted);
  flex-shrink: 0;
}

.sidebar__search-input {
  width: 100%;
  border: none;
  background: transparent;
  outline: none;
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  font-family: inherit;
}

.sidebar__search-input::placeholder {
  color: var(--color-text-muted);
}

.sidebar__section-action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--radius-md);
  color: var(--color-text-muted);
  cursor: pointer;
  transition: all var(--duration-normal) var(--ease-bounce);
  border: none;
  background: transparent;
}

.sidebar__section-action:hover {
  color: var(--color-primary);
  background: var(--color-bg-hover);
}

.sidebar__agents {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.sidebar__agent {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3);
  border-radius: var(--radius-xl);
  cursor: pointer;
  transition: all var(--duration-normal) var(--ease-bounce);
  border: 1.5px solid transparent;
}

.sidebar__agent:hover {
  background: var(--color-bg-hover);
  border-color: var(--color-border-default);
  transform: translateX(2px);
}

.sidebar__agent--active {
  background: var(--color-primary-subtle);
  border-color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}

.sidebar__agent-indicator {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  flex-shrink: 0;
}

.sidebar__agent-indicator--online {
  background: var(--color-primary);
  box-shadow: 0 0 0 3px var(--color-primary-subtle);
  animation: pulse 2s ease-in-out infinite;
}

.sidebar__agent-indicator--pending {
  background: var(--color-warning);
  box-shadow: 0 0 0 3px var(--color-warning-50);
  animation: pulse 1.5s ease-in-out infinite;
}

.sidebar__agent-indicator--failed {
  background: var(--color-danger);
  box-shadow: 0 0 0 3px var(--color-danger-50);
}

.sidebar__agent-indicator--offline {
  background: var(--color-text-muted);
}

.sidebar__agent-info {
  flex: 1;
  min-width: 0;
}

.sidebar__agent-name {
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-primary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sidebar__agent--active .sidebar__agent-name {
  color: var(--color-primary);
  font-weight: var(--font-semibold);
}

.sidebar__agent-meta {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  font-family: var(--font-mono);
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: default;
  display: flex;
  align-items: center;
  gap: 3px;
}

.sidebar__agent-mode-icon {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  opacity: 0.7;
}

.sidebar__agent-url-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sidebar__agent-count {
  font-size: var(--text-xs);
  font-weight: var(--font-semibold);
  padding: 2px 8px;
  background: var(--gradient-primary);
  color: white;
  border-radius: var(--radius-full);
  flex-shrink: 0;
}

.sidebar__empty {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.sidebar__status-bar {
  padding: var(--space-2-5) var(--space-4);
  border-top: 1px solid var(--color-border-subtle);
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: var(--space-2);
  background: var(--color-bg-subtle);
}

.sidebar__status-dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}

.sidebar__status-dot--online {
  background: var(--color-primary);
  box-shadow: 0 0 0 2px var(--color-primary-subtle);
  animation: pulse 2s ease-in-out infinite;
}

.sidebar__status-dot--offline {
  background: var(--color-text-muted);
}

.sidebar__status-text {
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
}

.sidebar__status-sep {
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.sidebar__status-counts {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  margin-left: auto;
}

/* Agent action buttons */
.sidebar__agent-actions {
  display: flex;
  gap: var(--space-1);
  opacity: 0;
  transition: opacity var(--duration-fast) var(--ease-default);
  flex-shrink: 0;
}

.sidebar__agent:hover .sidebar__agent-actions,
.sidebar__agent--active .sidebar__agent-actions {
  opacity: 1;
}

.sidebar__agent-action {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  border-radius: var(--radius-sm);
  border: none;
  background: transparent;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
}

.sidebar__agent-action:hover {
  background: var(--color-bg-hover);
  color: var(--color-primary);
}

.sidebar__agent-action--danger:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

/* Agent modal */
.agent-modal {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.agent-modal__desc {
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
}

.agent-modal__warn {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  background: var(--color-danger-50);
  border: 1px solid var(--color-danger-100);
  border-radius: var(--radius-lg);
  font-size: var(--text-sm);
  color: var(--color-danger);
}

.agent-modal__actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--space-3);
}

/* Global Search Overlay */
.global-search-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  backdrop-filter: blur(4px);
  z-index: var(--z-modal);
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding-top: 8vh;
}

.global-search-panel {
  width: min(760px, 92vw);
  max-height: 80vh;
  background: var(--color-bg-surface-raised, var(--color-bg-surface));
  border-radius: var(--radius-2xl);
  border: 1.5px solid var(--color-border-default);
  box-shadow: var(--shadow-2xl);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.global-search-header {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-4) var(--space-5);
  border-bottom: 1px solid var(--color-border-subtle);
}

.global-search-input-wrap {
  flex: 1;
  display: flex;
  align-items: center;
  gap: var(--space-3);
  background: var(--color-bg-subtle);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  padding: 0 var(--space-4);
  height: 44px;
  transition: border-color var(--duration-fast) var(--ease-default);
}

.global-search-input-wrap:focus-within {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.global-search-input-wrap svg {
  color: var(--color-text-muted);
  flex-shrink: 0;
}

.global-search-input {
  flex: 1;
  border: none;
  background: transparent;
  font-size: var(--text-base);
  color: var(--color-text-primary);
  outline: none;
}

.global-search-input::placeholder {
  color: var(--color-text-muted);
}

.global-search-clear {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border: none;
  background: var(--color-bg-hover);
  border-radius: var(--radius-full);
  color: var(--color-text-secondary);
  cursor: pointer;
}

.global-search-close {
  padding: var(--space-2) var(--space-4);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-lg);
  background: transparent;
  font-size: var(--text-sm);
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
  flex-shrink: 0;
}

.global-search-close:hover {
  background: var(--color-bg-hover);
}

.global-search-stats {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-5);
  border-bottom: 1px solid var(--color-border-subtle);
  background: var(--color-bg-subtle);
}

.global-search-stat {
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
}

.global-search-stat strong {
  color: var(--color-text-primary);
  font-weight: var(--font-semibold);
}

.global-search-stat-sep {
  color: var(--color-text-muted);
  font-size: var(--text-xs);
}

.global-search-body {
  flex: 1;
  overflow-y: auto;
  padding: var(--space-4);
}

.global-search-loading,
.global-search-hint {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-4);
  padding: var(--space-12) var(--space-6);
  color: var(--color-text-muted);
  font-size: var(--text-sm);
}

.global-search-results {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.global-search-group {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.global-search-group-header {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-1) 0;
  margin-bottom: var(--space-1);
}

.global-search-group-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.global-search-group-dot--online { background: var(--color-primary); }
.global-search-group-dot--offline { background: var(--color-text-muted); }

.global-search-group-name {
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
  flex: 1;
}

.global-search-group-count {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  background: var(--color-bg-subtle);
  padding: 2px 8px;
  border-radius: var(--radius-full);
}

.global-search-rule {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-subtle);
  border: 1px solid var(--color-border-subtle);
  border-radius: var(--radius-lg);
  cursor: pointer;
  transition: all var(--duration-fast) var(--ease-default);
}

.global-search-rule:hover {
  border-color: var(--color-primary);
  background: var(--color-primary-subtle);
  transform: translateX(2px);
}

.global-search-rule-status {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}

.global-search-rule-status--on { background: var(--color-primary); }
.global-search-rule-status--off { background: var(--color-text-muted); }

.global-search-rule-info {
  flex: 1;
  min-width: 0;
}

.global-search-rule-front {
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  font-weight: var(--font-medium);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.global-search-rule-back {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
  margin-top: 2px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.global-search-rule-tags {
  display: flex;
  gap: var(--space-1);
  flex-shrink: 0;
}

.global-search-rule-proto {
  display: inline-block;
  font-size: 9px;
  font-weight: var(--font-bold);
  padding: 1px 4px;
  border-radius: var(--radius-sm);
  font-family: var(--font-mono);
  background: var(--color-primary-subtle);
  color: var(--color-primary);
  margin-right: var(--space-1);
  vertical-align: middle;
}

.tag--sm {
  font-size: 10px;
  padding: 1px 6px;
}

/* Sidebar Collapsed State (Desktop only) */
.sidebar--collapsed {
  width: 64px;
}

.sidebar--collapsed .sidebar__section {
  padding: var(--space-3) var(--space-2);
}

.sidebar--collapsed .sidebar__section-header {
  justify-content: center;
  margin-bottom: var(--space-2);
}

.sidebar--collapsed .sidebar__section-header-actions {
  flex-direction: column;
  gap: var(--space-1);
}

.sidebar--collapsed .sidebar__agent {
  justify-content: center;
  padding: var(--space-2);
}

.sidebar--collapsed .sidebar__agent-info {
  display: none;
}

.sidebar--collapsed .sidebar__agent-count {
  display: none;
}

.sidebar--collapsed .sidebar__agent-indicator {
  width: 8px;
  height: 8px;
}

.sidebar--collapsed .sidebar__empty {
  padding: var(--space-4);
}

/* ==========================================
   Content Area
   ========================================== */
.content {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  padding: var(--space-6);
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
}

.content__header {
  display: flex;
  align-items: flex-end;
  justify-content: space-between;
  gap: var(--space-4);
  flex-wrap: wrap;
}

.content__header-left {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.content__breadcrumb {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  font-size: var(--text-xs);
  color: var(--color-text-muted);
}

.content__breadcrumb-item--active {
  color: var(--color-text-secondary);
}

.content__title {
  font-size: var(--text-xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  margin: 0;
}

.content__subtitle {
  font-size: var(--text-sm);
  color: var(--color-text-tertiary);
  margin: 0;
}

.content__header-right {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  flex-shrink: 0;
}

.content__search {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  transition: all var(--duration-normal) var(--ease-default);
  backdrop-filter: blur(8px);
}

.content__search:focus-within {
  border-color: var(--color-primary);
  box-shadow: var(--shadow-focus);
}

.content__search svg {
  color: var(--color-text-muted);
  flex-shrink: 0;
}

.content__search-input {
  border: none;
  background: transparent;
  font-size: var(--text-sm);
  color: var(--color-text-primary);
  width: 180px;
  outline: none;
  font-family: inherit;
}

.content__search-input::placeholder {
  color: var(--color-text-muted);
}

/* ==========================================
   Stats Row
   ========================================== */
.stats-row {
  display: flex;
  gap: var(--space-3);
  flex-wrap: wrap;
}

.stat-pill {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  backdrop-filter: blur(8px);
  min-width: 140px;
  transition: border-color var(--duration-normal) var(--ease-default),
              box-shadow var(--duration-normal) var(--ease-default);
}

.stat-pill:hover {
  border-color: var(--color-border-strong);
  box-shadow: var(--shadow-sm);
}

.stat-pill--active:hover {
  border-color: var(--color-border-strong);
}

/* 可点击的 stat-pill (用于 Tab 切换) */
.stat-pill--clickable {
  cursor: pointer;
  transition: all var(--duration-normal) var(--ease-default);
}

.stat-pill--clickable:hover {
  border-color: var(--color-border-strong);
  box-shadow: var(--shadow-sm);
}

/* 选中态 - Tab 被激活时显示 */
.stat-pill--selected {
  border-color: var(--color-primary) !important;
  background: var(--color-primary-subtle);
  box-shadow: var(--shadow-sm);
}

.stat-pill--selected .stat-pill__icon {
  background: var(--color-primary);
  color: white;
}

.stat-pill--selected .stat-pill__value,
.stat-pill--selected .stat-pill__label {
  color: var(--color-primary);
}

.stat-pill__icon {
  width: 36px;
  height: 36px;
  border-radius: var(--radius-lg);
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}

.stat-pill__icon--servers {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.stat-pill__icon--online {
  background: var(--color-success-50);
  color: var(--color-success);
}

.stat-pill__icon--rules {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.stat-pill__icon--l4 {
  background: var(--color-warning-50);
  color: var(--color-warning);
}

.stat-pill__icon--certs {
  background: var(--color-success-50);
  color: var(--color-success);
}

.stat-pill__icon--online {
  background: var(--color-success-50);
  color: var(--color-success);
}

.stat-pill__icon--active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

/* ==========================================
   Tabs Bar
   ========================================== */
.tabs-bar {
  display: flex;
  gap: var(--space-2);
  background: var(--color-bg-subtle);
  padding: var(--space-1);
  border-radius: var(--radius-xl);
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
  position: sticky;
  top: 0;
  z-index: 10;
}

/* ==========================================
   Main Tabs Bar - 独立于 content 的 sticky 导航
   ========================================== */
.main-tabs-bar {
  display: flex;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-5);
  background: var(--color-bg-primary);
  border-bottom: 1px solid var(--color-border-default);
  position: sticky;
  top: 56px;
  z-index: 50;
  overflow-x: auto;
  -webkit-overflow-scrolling: touch;
}

.main-tabs-bar__item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
  background: transparent;
  border: none;
  border-radius: var(--radius-lg);
  cursor: pointer;
  transition: all var(--duration-normal) var(--ease-default);
  font-family: inherit;
  white-space: nowrap;
}

.main-tabs-bar__item:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

.main-tabs-bar__item--active {
  background: var(--color-bg-surface);
  color: var(--color-primary);
  box-shadow: var(--shadow-sm);
  border: 1px solid var(--color-border-default);
}

.main-tabs-bar__badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  font-size: 10px;
  font-weight: var(--font-semibold);
  background: var(--gradient-primary);
  color: white;
  border-radius: var(--radius-full);
}

.main-tabs-bar__item--active .main-tabs-bar__badge {
  background: var(--color-primary);
}

.tabs-bar__item {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-4);
  font-size: var(--text-sm);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
  border: none;
  background: transparent;
  border-radius: var(--radius-lg);
  cursor: pointer;
  transition: all var(--duration-normal) var(--ease-default);
  font-family: inherit;
  white-space: nowrap;
}

.tabs-bar__item:hover {
  color: var(--color-text-primary);
  background: var(--color-bg-hover);
}

.tabs-bar__item--active {
  background: var(--color-bg-surface);
  color: var(--color-primary);
  box-shadow: var(--shadow-sm);
}

.tabs-bar__badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  font-size: 10px;
  font-weight: var(--font-semibold);
  background: var(--gradient-primary);
  color: white;
  border-radius: var(--radius-full);
}

.tabs-bar__item--active .tabs-bar__badge {
  background: var(--color-primary);
}

.stat-pill__icon--active {
  background: var(--color-primary-subtle);
  color: var(--color-primary);
}

.stat-pill__data {
  display: flex;
  flex-direction: column;
  gap: 2px;
  flex: 1;
  min-width: 0;
}

.stat-pill__row {
  display: flex;
  align-items: baseline;
  gap: var(--space-1);
  line-height: 1;
}

.stat-pill__value {
  font-size: var(--text-xl);
  font-weight: var(--font-bold);
  color: var(--color-text-primary);
  line-height: 1;
}

.stat-pill__value--active {
  color: var(--color-primary);
}

.stat-pill__unit {
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
  color: var(--color-text-secondary);
}

.stat-pill__unit--muted {
  color: var(--color-text-muted);
}

.stat-pill__label {
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
}

.stat-pill__footer {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.stat-pill__bar-wrap {
  flex: 1;
  height: 3px;
  background: var(--color-bg-subtle);
  border-radius: var(--radius-full);
  overflow: hidden;
}

.stat-pill__bar-fill {
  height: 100%;
  background: var(--gradient-primary);
  border-radius: var(--radius-full);
  transition: width var(--duration-slow) var(--ease-out);
}

/* ==========================================
   Data Management
   ========================================== */
.data-management {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: var(--space-4);
  background: var(--color-bg-surface);
  border: 1.5px solid var(--color-border-default);
  border-radius: var(--radius-2xl);
  box-shadow: var(--shadow-sm);
}

.data-management__header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-4);
  flex-wrap: wrap;
}

.data-management__title {
  margin: 0 0 var(--space-1);
  font-size: var(--text-base);
  color: var(--color-text-primary);
}

.data-management__subtitle {
  margin: 0;
  color: var(--color-text-tertiary);
  font-size: var(--text-sm);
}

.data-management__actions {
  display: flex;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.data-management__input {
  display: none;
}

.backup-report {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.backup-report__warning {
  padding: var(--space-3);
  border: 1px solid var(--color-warning);
  border-radius: var(--radius-xl);
  background: var(--color-warning-50);
  color: var(--color-warning);
  font-size: var(--text-xs);
  line-height: 1.6;
}

.backup-report__summary {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  background: var(--color-bg-subtle);
  border-radius: var(--radius-xl);
}

.backup-report__summary-row {
  display: flex;
  gap: var(--space-2);
  flex-wrap: wrap;
}

.backup-report__summary-pill {
  display: inline-flex;
  align-items: center;
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-full);
  background: var(--color-bg-surface);
  color: var(--color-text-secondary);
  font-size: var(--text-xs);
  font-weight: var(--font-medium);
}

.backup-report__summary-pill--imported {
  background: var(--color-success-50);
  color: var(--color-success);
}

.backup-report__meta {
  display: flex;
  gap: var(--space-3);
  flex-wrap: wrap;
  font-size: var(--text-xs);
  color: var(--color-text-tertiary);
}

.backup-report__details {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: var(--space-3);
}

.backup-report__bucket {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  border: 1px solid var(--color-border-default);
  border-radius: var(--radius-xl);
  background: var(--color-bg-subtle);
}

.backup-report__bucket-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  font-size: var(--text-sm);
  font-weight: var(--font-semibold);
  color: var(--color-text-primary);
}

.backup-report__bucket-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.backup-report__item,
.backup-report__empty {
  font-size: var(--text-xs);
  color: var(--color-text-secondary);
  word-break: break-all;
}

/* ==========================================
   Rules Section
   ========================================== */
.rules-section {
  flex: 1;
  min-width: 0;
}

/* ==========================================
   Utility Classes
   ========================================== */
.space-y-4 > * + * { margin-top: var(--space-4); }
.flex { display: flex; }
.justify-end { justify-content: flex-end; }
.gap-3 { gap: var(--space-3); }
.mr-2 { margin-right: var(--space-2); }
.bg-subtle { background: var(--color-bg-subtle); }
.p-4 { padding: var(--space-4); }
.rounded-lg { border-radius: var(--radius-lg); }
.text-xs { font-size: var(--text-xs); }
.text-sm { font-size: var(--text-sm); }
.text-secondary { color: var(--color-text-secondary); }
.text-tertiary { color: var(--color-text-tertiary); }
.break-all { word-break: break-all; }
.overflow-auto { overflow: auto; }
.max-h-48 { max-height: 12rem; }

/* ==========================================
   Responsive: 4K (2560px+)
   ========================================== */
@media (min-width: 2560px) {
  .topbar {
    height: 64px;
    padding: 0 var(--space-8);
  }

  .sidebar:not(.sidebar--collapsed) {
    width: 340px;
  }

  .content {
    padding: var(--space-8);
    gap: var(--space-6);
  }

  .content__title {
    font-size: var(--text-2xl);
  }

  .stat-pill {
    padding: var(--space-4) var(--space-5);
  }

  .stat-pill__icon {
    width: 44px;
    height: 44px;
  }

  .stat-pill__value {
    font-size: var(--text-2xl);
  }
}

/* ==========================================
   Responsive: Large Desktop (1440px - 2559px)
   ========================================== */
@media (min-width: 1440px) and (max-width: 2559px) {
  .sidebar:not(.sidebar--collapsed) {
    width: 300px;
  }

  .content {
    padding: var(--space-6);
  }
}

/* ==========================================
   Responsive: Desktop (1024px - 1439px)
   ========================================== */
@media (max-width: 1439px) {
  .sidebar:not(.sidebar--collapsed) {
    width: 260px;
  }
}

/* ==========================================
   Responsive: Tablet (769px - 1023px)
   ========================================== */
@media (max-width: 1023px) {
  .topbar__nav {
    display: none;
  }

  .sidebar {
    position: fixed;
    top: 56px;
    left: 0;
    bottom: 0;
    z-index: var(--z-fixed);
    transform: translateX(-100%);
    transition: transform var(--duration-normal) var(--ease-default);
    width: 280px !important;
  }

  .sidebar--open {
    transform: translateX(0);
  }

  .sidebar--collapsed {
    width: 280px !important;
  }

  .sidebar--collapsed .sidebar__section {
    padding: var(--space-4);
  }

  .sidebar--collapsed .sidebar__section-header {
    justify-content: space-between;
  }

  .sidebar--collapsed .sidebar__section-header-actions {
    flex-direction: row;
  }

  .sidebar--collapsed .sidebar__agent {
    justify-content: flex-start;
    padding: var(--space-3);
  }

  .sidebar--collapsed .sidebar__agent-info {
    display: block;
  }

  .sidebar--collapsed .sidebar__agent-count {
    display: block;
  }

  .sidebar__collapse-btn {
    display: none;
  }

  .sidebar-overlay {
    display: block;
    position: fixed;
    inset: 0;
    top: 56px;
    background: rgba(0, 0, 0, 0.3);
    backdrop-filter: blur(4px);
    z-index: calc(var(--z-fixed) - 1);
  }

  .topbar__hamburger {
    display: flex;
  }

  .content {
    padding: var(--space-5);
    z-index: 1;
  }

  .main-tabs-bar {
    left: 0;
    padding: var(--space-2) var(--space-4);
  }

  .stats-row {
    gap: var(--space-2);
  }
}

/* ==========================================
   Responsive: Mobile (481px - 768px)
   ========================================== */
@media (max-width: 768px) {
  .topbar {
    padding: 0 var(--space-3);
  }

  .topbar__title {
    display: none;
  }

  .topbar__hamburger {
    display: flex;
  }

  .content {
    padding: var(--space-4);
    gap: var(--space-4);
    z-index: 1;
  }

  .content__header {
    flex-direction: column;
    align-items: stretch;
    gap: var(--space-3);
  }

  .content__header-right {
    flex-direction: column;
    align-items: stretch;
    gap: var(--space-2);
  }

  .join-modal__actions {
    flex-direction: column;
  }

  .join-modal__actions .btn {
    width: 100%;
  }

  .content__search {
    width: 100%;
  }

  .content__search-input {
    width: 100%;
    flex: 1;
  }

  .content__title {
    font-size: var(--text-lg);
  }

  .data-management__actions {
    width: 100%;
  }

  .data-management__actions .btn {
    flex: 1;
    justify-content: center;
  }

  .backup-report__details {
    grid-template-columns: 1fr;
  }

  .stats-row {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: var(--space-2);
  }

  .stat-pill {
    padding: var(--space-2-5) var(--space-3);
  }

  .stat-pill__icon {
    width: 32px;
    height: 32px;
  }

  .stat-pill__value {
    font-size: var(--text-lg);
  }

  .main-tabs-bar {
    left: 0;
    padding: var(--space-2) var(--space-3);
  }

  .main-tabs-bar__item {
    padding: var(--space-2) var(--space-3);
  }
}

/* ==========================================
   Responsive: Small Mobile (<= 480px)
   ========================================== */
@media (max-width: 480px) {
  .topbar {
    height: 52px;
    padding: 0 var(--space-2);
  }

  .topbar__logo {
    width: 32px;
    height: 32px;
  }

  .topbar__actions {
    gap: var(--space-2);
  }

  .topbar__divider {
    display: none;
  }

  .content {
    padding: var(--space-3);
    gap: var(--space-3);
  }

  .content__breadcrumb {
    display: none;
  }

  .content__title {
    font-size: var(--text-base);
  }

  .content__subtitle {
    font-size: var(--text-xs);
  }

  .stats-row {
    grid-template-columns: repeat(2, 1fr);
    gap: var(--space-2);
  }

  .stat-pill {
    padding: var(--space-2);
    gap: var(--space-2);
  }

  .stat-pill__icon {
    width: 28px;
    height: 28px;
  }

  .stat-pill__icon svg {
    width: 14px;
    height: 14px;
  }

  .stat-pill__value {
    font-size: var(--text-base);
  }

  .stat-pill__label {
    font-size: 10px;
  }
}

/* ==========================================
   Mobile Bottom Navigation
   ========================================== */
.mobile-bottom-nav {
  display: none;
}

@media (max-width: 768px) {
  .mobile-bottom-nav {
    display: flex;
    position: fixed;
    bottom: 0;
    left: 0;
    right: 0;
    height: 60px;
    background: var(--color-bg-surface);
    border-top: 1px solid var(--color-border-default);
    backdrop-filter: blur(16px);
    z-index: var(--z-sticky);
    padding: 0 var(--space-2);
    padding-bottom: env(safe-area-inset-bottom, 0);
  }

  .mobile-bottom-nav__item {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 2px;
    border: none;
    background: transparent;
    color: var(--color-text-muted);
    font-size: 10px;
    font-family: inherit;
    cursor: pointer;
    padding: var(--space-1) var(--space-2);
    border-radius: var(--radius-lg);
    transition: all var(--duration-fast) var(--ease-default);
    position: relative;
  }

  .mobile-bottom-nav__item:active {
    transform: scale(0.92);
    background: var(--color-bg-hover);
  }

  .mobile-bottom-nav__item--active {
    color: var(--color-primary);
  }

  .mobile-bottom-nav__item--active svg {
    stroke: var(--color-primary);
  }

  .mobile-bottom-nav__badge {
    position: absolute;
    top: 4px;
    right: calc(50% - 14px);
    min-width: 16px;
    height: 16px;
    padding: 0 4px;
    background: var(--gradient-primary);
    color: white;
    border-radius: var(--radius-full);
    font-size: 10px;
    font-weight: var(--font-bold);
    display: flex;
    align-items: center;
    justify-content: center;
    line-height: 1;
  }

  /* Push content up so bottom nav doesn't overlap */
  .content {
    padding-bottom: calc(60px + var(--space-4));
  }

  .app-shell .sidebar {
    bottom: 60px;
  }

  .sidebar-overlay {
    bottom: 60px !important;
  }
}

/* ==========================================
   Topbar mobile quick actions
   ========================================== */
.topbar__nav-mobile {
  display: none;
}

@media (max-width: 1023px) {
  .topbar__nav-mobile {
    display: flex;
  }
}

@media (max-width: 768px) {
  /* Hide topbar mobile nav since bottom nav handles it */
  .topbar__nav-mobile {
    display: none;
  }
}

/* ==========================================
   Sidebar search rework
   ========================================== */
.sidebar__search-clear {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border: none;
  background: var(--color-bg-hover);
  border-radius: var(--radius-full);
  color: var(--color-text-secondary);
  cursor: pointer;
  flex-shrink: 0;
  transition: all var(--duration-fast) var(--ease-default);
}

.sidebar__search-clear:hover {
  background: var(--color-danger-50);
  color: var(--color-danger);
}

.sidebar__search-meta {
  font-size: 11px;
  color: var(--color-text-muted);
  padding: 0 var(--space-1) var(--space-2);
}

.sidebar__search-meta strong {
  color: var(--color-primary);
  font-weight: var(--font-semibold);
}

/* ==========================================
   Global search: full-screen on mobile
   ========================================== */
@media (max-width: 768px) {
  .global-search-overlay {
    padding-top: 0;
    align-items: stretch;
  }

  .global-search-panel {
    width: 100%;
    max-height: calc(100vh - 60px);
    border-radius: 0;
    border-left: none;
    border-right: none;
    border-top: none;
  }
}
</style>
