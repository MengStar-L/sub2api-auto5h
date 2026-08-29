<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { CheckSquare, RefreshCw, Search, Square, X } from 'lucide-vue-next'
import { api } from '../api'
import type { Account } from '../types'
import AccountDrawer from '../components/AccountDrawer.vue'
import IntelligenceBadge from '../components/IntelligenceBadge.vue'
import StatusBadge from '../components/StatusBadge.vue'

const accounts = ref<Account[]>([])
const search = ref('')
const stateFilter = ref('all')
const selected = ref(new Set<string>())
const detail = ref<Account | null>(null)
const loading = ref(false)
const error = ref('')

const filtered = computed(() => accounts.value.filter((account) => {
  const needle = search.value.trim().toLowerCase()
  const matchesText = !needle || `${account.name} ${account.email} ${account.remote_id} ${account.plan_type}`.toLowerCase().includes(needle)
  const matchesState = stateFilter.value === 'all' || (stateFilter.value === 'enabled' ? account.policy.enabled : stateFilter.value === 'eligible' ? account.eligible : account.runtime_state === stateFilter.value)
  return matchesText && matchesState
}))
const enabledCount = computed(() => accounts.value.filter((item) => item.policy.enabled).length)
const attentionCount = computed(() => accounts.value.filter((item) => ['attention', 'failed', 'identity_changed'].includes(item.runtime_state)).length)
const waitingCount = computed(() => accounts.value.filter((item) => ['waiting', 'blocked_7d', 'retry_wait', 'quota_retry'].includes(item.runtime_state)).length)
const allSelected = computed(() => filtered.value.length > 0 && filtered.value.every((item) => selected.value.has(item.id)))

async function load() {
  loading.value = true; error.value = ''
  try { accounts.value = await api<Account[]>('/api/accounts') }
  catch (reason) { error.value = reason instanceof Error ? reason.message : '加载失败' }
  finally { loading.value = false }
}

async function sync() {
  loading.value = true; error.value = ''
  try {
    const result = await api<{ accounts: Account[] }>('/api/accounts/sync', { method: 'POST', body: '{}' })
    accounts.value = result.accounts
  } catch (reason) { error.value = reason instanceof Error ? reason.message : '同步失败' }
  finally { loading.value = false }
}

function toggleAll() {
  const next = new Set(selected.value)
  if (allSelected.value) filtered.value.forEach((item) => next.delete(item.id))
  else filtered.value.forEach((item) => next.add(item.id))
  selected.value = next
}

function toggleOne(id: string) {
  const next = new Set(selected.value); next.has(id) ? next.delete(id) : next.add(id); selected.value = next
}

async function batch(enabled: boolean) {
  if (!selected.value.size) return
  error.value = ''
  try {
    const result = await api<{ failed: Record<string, string> }>('/api/accounts/batch-policy', { method: 'POST', body: JSON.stringify({ ids: [...selected.value], enabled }) })
    if (Object.keys(result.failed).length) error.value = `${Object.keys(result.failed).length} 个账号未更新`
    selected.value = new Set(); await load()
  } catch (reason) { error.value = reason instanceof Error ? reason.message : '批量更新失败' }
}

function percent(value?: number) { return value == null ? '未知' : `${Math.round(value)}%` }
function when(value?: number) { return value ? new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'medium' }).format(new Date(value * 1000)) : '—' }
function answerSummary(account: Account) {
  if (account.last_answer_status === 'abnormal') return `返回 ${account.last_answer_text || '空'}`
  if (account.last_answer_status === 'no_answer') return '成功完成但正文为空'
  if (account.last_answer_status === 'legacy_invalid') return 'v0.1.5 未实际发送题目'
  return ''
}
function choose(account: Account) { detail.value = account }
async function updated() { const id = detail.value?.id; await load(); detail.value = accounts.value.find((item) => item.id === id) ?? null }

onMounted(load)
</script>

<template>
  <section class="metric-band" aria-label="账号概览">
    <div><span>账号总数</span><strong>{{ accounts.length }}</strong></div>
    <div><span>自动激活</span><strong>{{ enabledCount }}</strong></div>
    <div><span>等待任务</span><strong>{{ waitingCount }}</strong></div>
    <div><span>需要处理</span><strong :class="{ dangerText: attentionCount }">{{ attentionCount }}</strong></div>
  </section>

  <section class="toolbar">
    <div class="search-field"><Search :size="17" /><input v-model="search" aria-label="搜索账号" placeholder="搜索名称、邮箱或 ID" /></div>
    <select v-model="stateFilter" aria-label="筛选状态">
      <option value="all">全部状态</option><option value="enabled">已启用</option><option value="eligible">可启用</option>
      <option value="waiting">等待刷新</option><option value="attention">需要处理</option><option value="blocked_7d">7d 已耗尽</option>
    </select>
    <div class="toolbar-spacer" />
    <button class="secondary command" :disabled="!selected.size" @click="batch(true)"><CheckSquare :size="17" />启用</button>
    <button class="secondary command" :disabled="!selected.size" @click="batch(false)"><Square :size="17" />关闭</button>
    <button class="icon-button secondary" type="button" title="同步账号" :disabled="loading" @click="sync"><RefreshCw :size="18" :class="{ spin: loading }" /></button>
  </section>
  <p v-if="error" class="inline-alert"><span>{{ error }}</span><button class="icon-button ghost" title="关闭" @click="error = ''"><X :size="16" /></button></p>

  <section class="table-wrap">
    <table class="account-table">
      <thead><tr>
        <th class="check-cell"><button class="check-button" title="全选" @click="toggleAll"><CheckSquare v-if="allSelected" :size="17" /><Square v-else :size="17" /></button></th>
        <th>账号</th><th>套餐</th><th>5h 使用</th><th>5h 重置</th><th>下次动作</th><th>智商</th><th>自动化</th>
      </tr></thead>
      <tbody>
        <tr v-if="!loading && !filtered.length"><td colspan="8" class="empty-state">没有匹配的账号</td></tr>
        <tr v-for="account in filtered" :key="account.id" :class="{ selected: selected.has(account.id) }">
          <td class="check-cell"><button class="check-button" :title="selected.has(account.id) ? '取消选择' : '选择账号'" @click.stop="toggleOne(account.id)"><CheckSquare v-if="selected.has(account.id)" :size="17" /><Square v-else :size="17" /></button></td>
          <td><button class="account-link" @click="choose(account)"><strong>{{ account.name || account.email || `账号 ${account.remote_id}` }}</strong><span>{{ account.email || `sub2api #${account.remote_id}` }}</span></button></td>
          <td><span class="plan-label">{{ account.plan_type || '未知' }}</span><small v-if="!account.eligible">{{ account.eligibility_reason }}</small></td>
          <td><div class="usage-cell"><span>{{ percent(account.five_used_percent) }}</span><progress :value="account.five_used_percent ?? 0" max="100" /></div></td>
          <td>{{ when(account.five_reset_at) }}</td><td>{{ when(account.next_action_at) }}</td>
          <td><div class="assessment-cell"><IntelligenceBadge :status="account.last_answer_status" /><small v-if="answerSummary(account)">{{ answerSummary(account) }}</small></div></td>
          <td><StatusBadge :state="account.runtime_state" /></td>
        </tr>
      </tbody>
    </table>
  </section>
  <AccountDrawer v-if="detail" :account="detail" @close="detail = null" @updated="updated" />
</template>
