<template>
  <div :class="['rule-item-modern', `view-${viewMode}`, { 'is-disabled': !rule.enabled }]">
    <div class="pro-card">
      <!-- Grid Layout -->
      <template v-if="viewMode === 'grid'">
        <div class="pro-card-header">
          <span class="pro-id">#{{ rule.id }}</span>
          <button @click="toggleStatus" :class="['status-switch', rule.enabled ? 'active' : 'off']" :title="rule.enabled ? '点击停用' : '点击启用'">
            <span class="switch-slider"></span>
          </button>
        </div>

        <div class="pro-card-body">
          <div class="endpoint-row">
            <div class="endpoint-icon"><svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg></div>
            <div class="endpoint-val" @click="copyToClipboard(rule.frontend_url)" title="点击复制">
              {{ rule.frontend_url }}
            </div>
          </div>

          <div class="arrow-down"><svg viewBox="0 0 24 24"><line x1="12" y1="5" x2="12" y2="19"/><polyline points="19 12 12 19 5 12"/></svg></div>

          <div class="endpoint-row">
            <div class="endpoint-icon server"><svg viewBox="0 0 24 24"><rect x="2" y="2" width="20" height="8" rx="2" ry="2"/><rect x="2" y="14" width="20" height="8" rx="2" ry="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/></svg></div>
            <div class="endpoint-val" @click="copyToClipboard(rule.backend_url)" title="点击复制">
              {{ rule.backend_url }}
            </div>
          </div>

          <div class="pro-tags" v-if="rule.tags?.length">
            <span
              v-for="tag in rule.tags"
              :key="tag"
              :class="['pro-tag', getTagColorClass(tag), { 'tag-selected': ruleStore.selectedTags.includes(tag) }]"
              @click="filterByTag(tag)"
              :title="`点击筛选 '${tag}' 标签`"
            >
              {{ tag }}
            </span>
          </div>
        </div>

        <div class="pro-card-footer">
          <button @click="showEditModal = true" class="tool-btn" title="编辑">
            <svg viewBox="0 0 24 24"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
            <span>编辑</span>
          </button>
          <button @click="handleDelete" class="tool-btn del" title="删除">
            <svg viewBox="0 0 24 24"><path d="M3 6h18m-2 0v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6m3 0V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/></svg>
            <span>删除</span>
          </button>
        </div>
      </template>

      <!-- List Layout -->
      <template v-else>
        <div class="list-line">
          <div class="list-meta">
            <span class="pro-id">#{{ rule.id }}</span>
            <button @click="toggleStatus" :class="['status-switch-mini', rule.enabled ? 'active' : 'off']" :title="rule.enabled ? '点击停用' : '点击启用'">
              <span class="switch-slider-mini"></span>
            </button>
          </div>

          <div class="list-content">
            <div class="list-url" @click="copyToClipboard(rule.frontend_url)">{{ rule.frontend_url }}</div>
            <div class="list-arrow"><svg viewBox="0 0 24 24"><line x1="5" y1="12" x2="19" y2="12"/><polyline points="12 5 19 12 12 19"/></svg></div>
            <div class="list-url target" @click="copyToClipboard(rule.backend_url)">{{ rule.backend_url }}</div>
          </div>

          <div class="list-tags" v-if="rule.tags?.length">
            <span
              v-for="tag in rule.tags"
              :key="tag"
              :class="['pro-tag tiny', getTagColorClass(tag), { 'tag-selected': ruleStore.selectedTags.includes(tag) }]"
              @click="filterByTag(tag)"
              :title="`点击筛选 '${tag}' 标签`"
            >
              {{ tag }}
            </span>
          </div>

          <div class="list-actions">
            <button @click="showEditModal = true" class="mini-btn">
              <svg viewBox="0 0 24 24"><path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/></svg>
              <span>编辑</span>
            </button>
            <button @click="handleDelete" class="mini-btn del">
              <svg viewBox="0 0 24 24"><path d="M3 6h18m-2 0v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6m3 0V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/></svg>
              <span>删除</span>
            </button>
          </div>
        </div>
      </template>
    </div>

    <!-- Modals -->
    <Teleport to="body">
      <BaseModal v-model="showEditModal" title="修改代理规则" :subtitle="'规则 ID: #' + rule.id" :show-default-footer="false">
        <RuleForm :initial-data="rule" @success="showEditModal = false" />
      </BaseModal>
    </Teleport>

    <Teleport to="body">
      <BaseModal v-model="showDeleteModal" title="确认删除" confirm-text="确认删除" confirm-variant="danger" :loading="isDeletingRule" @confirm="confirmDelete">
        <div style="display: flex; flex-direction: column; gap: 12px; align-items: center; text-align: center;">
          <div style="width: 56px; height: 56px; border-radius: 50%; background: var(--color-danger-bg); display: flex; align-items: center; justify-content: center;">
            <svg viewBox="0 0 24 24" style="width: 28px; height: 28px; stroke: var(--color-danger); stroke-width: 2.5; fill: none;">
              <path d="M3 6h18M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"/>
              <line x1="10" y1="11" x2="10" y2="17"/>
              <line x1="14" y1="11" x2="14" y2="17"/>
            </svg>
          </div>
          <p style="font-size: 0.95rem; color: var(--color-text-primary); line-height: 1.6; font-weight: 500; margin: 0;">
            确定要移除规则 <strong style="color: var(--color-danger); font-weight: 700;">#{{ rule.id }}</strong> 吗？
          </p>
          <p style="font-size: 0.85rem; color: var(--color-text-muted); line-height: 1.5; margin: 0;">
            此操作不可撤销
          </p>
        </div>
      </BaseModal>
    </Teleport>
  </div>
</template>

<script setup>
import { ref } from 'vue'
import { useRuleStore } from '../stores/rules'
import BaseModal from './base/BaseModal.vue'
import RuleForm from './RuleForm.vue'

const props = defineProps({ rule: { type: Object, required: true }, viewMode: { type: String, default: 'grid' } })
const ruleStore = useRuleStore()
const showEditModal = ref(false)
const showDeleteModal = ref(false)
const isDeletingRule = ref(false)

const getTagColorClass = (tag) => {
  let hash = 0
  for (let i = 0; i < tag.length; i++) hash = tag.charCodeAt(i) + ((hash << 5) - hash)
  return `tag-color-${Math.abs(hash % 6) + 1}`
}

const copyToClipboard = (text) => {
  navigator.clipboard.writeText(text).then(() => ruleStore.showSuccess('已复制到剪贴板'))
}

const toggleStatus = async () => {
  try { await ruleStore.modifyRule(props.rule.id, undefined, undefined, undefined, !props.rule.enabled) } catch (err) {}
}

const filterByTag = (tag) => {
  const index = ruleStore.selectedTags.indexOf(tag)
  if (index > -1) {
    // 如果已经选中，则取消选中
    ruleStore.selectedTags.splice(index, 1)
  } else {
    // 如果未选中，则添加到选中列表
    ruleStore.selectedTags.push(tag)
  }
}

const handleDelete = () => { showDeleteModal.value = true }
const confirmDelete = async () => {
  isDeletingRule.value = true
  try { await ruleStore.removeRule(props.rule.id); showDeleteModal.value = false } catch (err) {} finally { isDeletingRule.value = false }
}
</script>

<style scoped>
.rule-item-modern {
  width: 100%;
  max-width: 100%;
  min-width: 0;
}

.pro-card {
  background: var(--color-bg-card);
  border: 1px solid var(--color-border-light);
  border-radius: var(--radius-xl);
  box-shadow: var(--shadow-sm);
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  overflow: hidden;
  position: relative;
  width: 100%;
  max-width: 100%;
  box-sizing: border-box;
}

.pro-card::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  height: 3px;
  background: linear-gradient(90deg, var(--color-primary), var(--color-primary-light));
  opacity: 0;
  transition: opacity 0.3s ease;
}

.pro-card:hover {
  border-color: var(--color-primary-light);
  box-shadow: 0 12px 24px -8px rgba(37, 99, 235, 0.2), 0 6px 12px -4px rgba(0, 0, 0, 0.08);
  transform: translateY(-4px);
}

.pro-card:hover::before {
  opacity: 1;
}

.is-disabled .pro-card {
  opacity: 0.6;
  filter: grayscale(0.3);
}

/* Grid View */
.view-grid .pro-card-header {
  padding: 16px 20px;
  background: linear-gradient(135deg, var(--color-bg-secondary) 0%, var(--color-bg-primary) 100%);
  display: flex;
  justify-content: space-between;
  align-items: center;
  border-bottom: 1px solid var(--color-border-light);
}
.pro-id {
  font-family: var(--font-family-mono);
  font-size: 0.75rem;
  font-weight: 800;
  color: var(--color-text-muted);
}

.status-switch {
  width: 44px;
  height: 24px;
  border-radius: 12px;
  border: 1.5px solid var(--color-border);
  cursor: pointer;
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  position: relative;
  padding: 0;
  background: var(--color-border-light);
  box-shadow: inset 0 2px 4px rgba(0, 0, 0, 0.15);
}

.status-switch.active {
  background: linear-gradient(135deg, var(--color-success) 0%, var(--color-success-dark) 100%);
  box-shadow: 0 2px 8px rgba(16, 185, 129, 0.3);
  border-color: var(--color-success);
}

.status-switch:hover {
  transform: scale(1.05);
}

.status-switch:active {
  transform: scale(0.98);
}

.switch-slider {
  position: absolute;
  top: 1.5px;
  left: 1.5px;
  width: 19px;
  height: 19px;
  border-radius: 50%;
  background: white;
  box-shadow: 0 2px 5px rgba(0, 0, 0, 0.25), 0 1px 3px rgba(0, 0, 0, 0.15);
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  border: 0.5px solid rgba(0, 0, 0, 0.08);
}

.status-switch.active .switch-slider {
  transform: translateX(20px);
  box-shadow: 0 2px 6px rgba(0, 0, 0, 0.3), 0 1px 3px rgba(0, 0, 0, 0.2);
}

.view-grid .pro-card-body {
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
  width: 100%;
}
.endpoint-row {
  display: flex;
  align-items: center;
  gap: 12px;
  transition: all 0.2s ease;
  min-width: 0;
  width: 100%;
}

.endpoint-row:hover {
  transform: translateX(2px);
}
.endpoint-icon {
  width: 36px;
  height: 36px;
  border-radius: 10px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  background: linear-gradient(135deg, var(--color-primary-bg) 0%, var(--color-primary-lighter) 100%);
  color: var(--color-primary);
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  box-shadow: 0 2px 8px rgba(37, 99, 235, 0.15);
}

.endpoint-icon.server {
  background: linear-gradient(135deg, var(--color-secondary-bg) 0%, #d1fae5 100%);
  color: var(--color-secondary);
  box-shadow: 0 2px 8px rgba(16, 185, 129, 0.15);
}

.endpoint-row:hover .endpoint-icon {
  transform: scale(1.08) rotate(3deg);
  box-shadow: 0 4px 12px rgba(37, 99, 235, 0.25);
}

.endpoint-row:hover .endpoint-icon.server {
  box-shadow: 0 4px 12px rgba(16, 185, 129, 0.25);
}

.endpoint-icon svg {
  width: 18px;
  height: 18px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
}

.endpoint-val {
  font-family: var(--font-family-mono);
  font-size: 0.88rem;
  color: var(--color-text-primary);
  background: var(--color-bg-secondary);
  padding: 8px 14px;
  border-radius: 8px;
  flex: 1;
  min-width: 0;
  max-width: 100%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
  transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);
  position: relative;
  border: 1px solid transparent;
  box-sizing: border-box;
}

.endpoint-val:hover {
  background: var(--color-primary-bg);
  color: var(--color-primary);
  border-color: var(--color-primary-light);
  transform: translateX(3px);
  box-shadow: 0 2px 8px rgba(37, 99, 235, 0.15);
}

.endpoint-val:active {
  transform: scale(0.98) translateX(3px);
}

.arrow-down {
  display: flex;
  justify-content: center;
  color: var(--color-primary);
  padding: 4px 0;
}

.arrow-down svg {
  width: 18px;
  height: 18px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
  animation: bounce-arrow 2.5s ease-in-out infinite;
}

@keyframes bounce-arrow {
  0%, 100% {
    transform: translateY(0);
    opacity: 1;
  }
  50% {
    transform: translateY(4px);
    opacity: 0.7;
  }
}

.pro-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 12px;
}

.pro-tag {
  font-size: 0.7rem;
  font-weight: 700;
  padding: 4px 12px;
  border-radius: 8px;
  transition: all 0.2s ease;
  cursor: pointer;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.08);
  border: 1px solid rgba(0, 0, 0, 0.05);
}

.pro-tag:hover {
  transform: translateY(-2px);
  box-shadow: 0 3px 8px rgba(0, 0, 0, 0.12);
}

.pro-tag.tag-selected {
  box-shadow: 0 0 0 2px var(--color-primary);
  transform: translateY(-2px);
}

.pro-tag.tiny {
  font-size: 0.65rem;
  padding: 3px 10px;
}

.view-grid .pro-card-footer {
  display: flex;
  background: linear-gradient(135deg, var(--color-bg-secondary) 0%, var(--color-bg-primary) 100%);
  border-top: 1px solid var(--color-border-light);
  gap: 1px;
}

.tool-btn {
  flex: 1;
  height: 44px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  background: transparent;
  border: none;
  color: var(--color-text-tertiary);
  cursor: pointer;
  transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  position: relative;
  overflow: hidden;
}

.tool-btn span {
  display: none;
  font-size: 0.9rem;
  font-weight: 500;
  position: relative;
  z-index: 1;
}

.tool-btn::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: var(--color-primary-bg);
  opacity: 0;
  transition: opacity 0.3s ease;
}

.tool-btn:hover::before {
  opacity: 1;
}

.tool-btn:hover {
  color: var(--color-primary);
}

.tool-btn.del:hover {
  color: var(--color-danger);
}

.tool-btn.del:hover::before {
  background: var(--color-danger-bg);
}

.tool-btn svg {
  width: 18px;
  height: 18px;
  stroke: currentColor;
  stroke-width: 2.2;
  fill: none;
  position: relative;
  z-index: 1;
  transition: transform 0.2s ease;
}

.tool-btn:hover svg {
  transform: scale(1.15);
}

.tool-btn:active svg {
  transform: scale(0.95);
}

/* List View */
.view-list .list-line {
  display: flex;
  align-items: center;
  padding: 14px 20px;
  gap: 18px;
  transition: all 0.25s cubic-bezier(0.4, 0, 0.2, 1);
}

.view-list .pro-card:hover .list-line {
  padding-left: 24px;
}
.list-meta {
  display: flex;
  align-items: center;
  gap: 12px;
  width: 80px;
  flex-shrink: 0;
}

.status-switch-mini {
  width: 38px;
  height: 20px;
  border-radius: 10px;
  border: 1.5px solid var(--color-border);
  cursor: pointer;
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  position: relative;
  padding: 0;
  flex-shrink: 0;
  background: var(--color-border-light);
  box-shadow: inset 0 1px 3px rgba(0, 0, 0, 0.15);
}

.status-switch-mini.active {
  background: linear-gradient(135deg, var(--color-success) 0%, var(--color-success-dark) 100%);
  box-shadow: 0 2px 6px rgba(16, 185, 129, 0.3);
  border-color: var(--color-success);
}

.status-switch-mini:hover {
  transform: scale(1.08);
}

.status-switch-mini:active {
  transform: scale(0.95);
}

.switch-slider-mini {
  position: absolute;
  top: 1.5px;
  left: 1.5px;
  width: 15px;
  height: 15px;
  border-radius: 50%;
  background: white;
  box-shadow: 0 2px 4px rgba(0, 0, 0, 0.25), 0 1px 2px rgba(0, 0, 0, 0.15);
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
  border: 0.5px solid rgba(0, 0, 0, 0.08);
}

.status-switch-mini.active .switch-slider-mini {
  transform: translateX(18px);
  box-shadow: 0 2px 5px rgba(0, 0, 0, 0.3), 0 1px 3px rgba(0, 0, 0, 0.2);
}

.list-content { flex: 1; display: flex; align-items: center; gap: 12px; min-width: 0; }
.list-url {
  font-family: var(--font-family-mono);
  font-size: 0.88rem;
  color: var(--color-text-primary);
  background: var(--color-bg-secondary);
  padding: 6px 12px;
  border-radius: 6px;
  max-width: 40%;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  cursor: pointer;
  transition: all 0.2s ease;
  border: 1px solid transparent;
}

.list-url:hover {
  background: var(--color-primary-bg);
  color: var(--color-primary);
  border-color: var(--color-primary-light);
  transform: translateX(2px);
}

.list-url.target {
  color: var(--color-text-secondary);
}

.list-url.target:hover {
  background: var(--color-secondary-bg);
  color: var(--color-secondary);
  border-color: var(--color-secondary-light);
}
.list-arrow {
  color: var(--color-primary);
  transition: all 0.2s ease;
}

.list-line:hover .list-arrow {
  transform: translateX(4px);
}

.list-arrow svg {
  width: 16px;
  height: 16px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
}

.list-tags { display: flex; gap: 4px; width: 150px; overflow: hidden; }
.list-actions { display: flex; gap: 6px; }
.mini-btn {
  height: 32px;
  padding: 0 12px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 6px;
  border: 1.5px solid var(--color-border);
  background: var(--color-bg-secondary);
  color: var(--color-text-secondary);
  cursor: pointer;
  transition: all 0.2s cubic-bezier(0.4, 0, 0.2, 1);
  position: relative;
  overflow: hidden;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.08);
  white-space: nowrap;
}

.mini-btn span {
  font-size: 0.85rem;
  font-weight: 500;
  position: relative;
  z-index: 1;
}

.mini-btn::before {
  content: '';
  position: absolute;
  top: 50%;
  left: 50%;
  width: 0;
  height: 0;
  border-radius: 50%;
  background: var(--color-primary-bg);
  transform: translate(-50%, -50%);
  transition: width 0.25s ease, height 0.25s ease;
  z-index: 0;
}

.mini-btn:hover::before {
  width: 120%;
  height: 120%;
}

.mini-btn:hover {
  border-color: var(--color-primary);
  color: var(--color-primary);
  transform: translateY(-2px);
  box-shadow: 0 4px 8px rgba(37, 99, 235, 0.15);
}

.mini-btn.del:hover {
  color: var(--color-danger);
  border-color: var(--color-danger);
  box-shadow: 0 4px 8px rgba(239, 68, 68, 0.15);
}

.mini-btn.del:hover::before {
  background: var(--color-danger-bg);
}

.mini-btn svg {
  width: 15px;
  height: 15px;
  stroke: currentColor;
  stroke-width: 2.5;
  fill: none;
  position: relative;
  z-index: 1;
  transition: transform 0.2s ease;
}

.mini-btn:hover svg {
  transform: scale(1.15);
}

.mini-btn:active {
  transform: translateY(0);
}

.mini-btn:active svg {
  transform: scale(0.9);
}

/* Tag Color Classes */
.tag-color-1 { background: var(--tag-1-bg); color: var(--tag-1-text); }
.tag-color-2 { background: var(--tag-2-bg); color: var(--tag-2-text); }
.tag-color-3 { background: var(--tag-3-bg); color: var(--tag-3-text); }
.tag-color-4 { background: var(--tag-4-bg); color: var(--tag-4-text); }
.tag-color-5 { background: var(--tag-5-bg); color: var(--tag-5-text); }
.tag-color-6 { background: var(--tag-6-bg); color: var(--tag-6-text); }

@media (max-width: 768px) {
  .rule-item-modern {
    width: 100%;
    max-width: 100%;
  }

  .pro-card {
    max-width: 100%;
  }

  .view-grid .pro-card-header {
    padding: 12px 16px;
  }

  .view-grid .pro-card-body {
    padding: 16px;
  }

  .endpoint-row {
    gap: 8px;
  }

  .endpoint-icon {
    width: 32px;
    height: 32px;
  }

  .endpoint-icon svg {
    width: 16px;
    height: 16px;
  }

  .endpoint-val {
    font-size: 0.75rem;
    padding: 6px 10px;
    max-width: calc(100% - 40px);
  }

  .view-list .list-line {
    flex-direction: column;
    align-items: stretch;
    gap: 10px;
  }

  .list-content {
    flex-direction: column;
    align-items: flex-start;
  }

  .list-url {
    max-width: 100%;
    width: 100%;
    font-size: 0.75rem;
  }

  .list-arrow {
    transform: rotate(90deg);
    align-self: center;
  }

  /* 移动端按钮优化：只显示图标，隐藏文字 */
  .tool-btn span,
  .mini-btn span {
    display: none;
  }

  .tool-btn svg,
  .mini-btn svg {
    display: block;
  }

  .tool-btn,
  .mini-btn {
    gap: 0;
    padding: 0 8px;
  }
}
</style>
