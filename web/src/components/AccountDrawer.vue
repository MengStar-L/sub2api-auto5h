<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { Play, RefreshCw, Save, X } from 'lucide-vue-next'
import { api } from '../api'
import type { Account, Attempt, Cycle } from '../types'
import StatusBadge from './StatusBadge.vue'

const props = defineProps<{ account: Account }>()
const emit = defineEmits<{ close: []; updated: [] }>()
const busy = ref(false)
const error = ref('')
const cycles = ref<Cycle[]>([])
const attempts = ref<Record<string, Attempt[]>>({})
const models = ref<string[]>([])
const form = reactive({
  enabled: props.account.policy.enabled,
  model_override: props.account.policy.model_override ?? '',
  grace_override_seconds: props.account.policy.grace_override_seconds ?? null as number | null,
  max_retries_override: props.account.policy.max_retries_override ?? null as number | null,
  retry_base_override_seconds: props.account.policy.retry_base_override_seconds ?? null as number | null,
})
const canRun = computed(() => props.account.policy.enabled && ['due', 'retry_wait', 'attention', 'quota_retry', 'pending_check', 'failed', 'success_unverified'].includes(props.account.runtime_state))

function when(value?: number) { return value ? new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value * 1000)) : '—' }
function nullable(value: number | null) { return value === null || Number.isNaN(value) ? null : value }

async function loadHistory() {
  cycles.value = await api<Cycle[]>(`/api/accounts/${props.account.id}/cycles`)
}
async function loadModels() {
  try { models.value = await api<string[]>(`/api/accounts/${props.account.id}/models`) } catch { models.value = [] }
}
async function loadAttempts(cycle: Cycle) {
  attempts.value[cycle.id] = await api<Attempt[]>(`/api/cycles/${cycle.id}/attempts`)
}
async function save() {
  busy.value = true; error.value = ''
  try {
    await api(`/api/accounts/${props.account.id}/policy`, { method: 'PUT', body: JSON.stringify({
      enabled: form.enabled, model_override: form.model_override.trim() || null,
      grace_override_seconds: nullable(form.grace_override_seconds), max_retries_override: nullable(form.max_retries_override),
      retry_base_override_seconds: nullable(form.retry_base_override_seconds),
    }) })
    emit('updated')
  } catch (reason) { error.value = reason instanceof Error ? reason.message : '保存失败' }
  finally { busy.value = false }
}
async function refreshQuota() {
  busy.value = true; error.value = ''
  try { await api(`/api/accounts/${props.account.id}/quota-refresh`, { method: 'POST', body: '{}' }); emit('updated') }
  catch (reason) { error.value = reason instanceof Error ? reason.message : '刷新失败' }
  finally { busy.value = false }
}
async function run() {
  if (!window.confirm('确认执行额度预检？仅在窗口确实可启动时才会发送 hi。')) return
  busy.value = true; error.value = ''
  try { await api(`/api/accounts/${props.account.id}/run`, { method: 'POST', body: '{}' }); await loadHistory(); emit('updated') }
  catch (reason) { error.value = reason instanceof Error ? reason.message : '执行失败' }
  finally { busy.value = false }
}
onMounted(() => Promise.all([loadHistory(), loadModels()]))
</script>

<template>
  <div class="drawer-backdrop" @click.self="emit('close')">
    <aside class="drawer" aria-label="账号详情">
      <header class="drawer-header"><div><strong>{{ account.name || account.email || `账号 ${account.remote_id}` }}</strong><span>sub2api #{{ account.remote_id }}</span></div><button class="icon-button ghost" title="关闭" @click="emit('close')"><X :size="20" /></button></header>
      <div class="drawer-body">
        <section class="detail-strip"><div><span>套餐</span><strong>{{ account.plan_type || '未知' }}</strong></div><div><span>状态</span><StatusBadge :state="account.runtime_state" /></div><div><span>下次动作</span><strong>{{ when(account.next_action_at) }}</strong></div></section>
        <p v-if="account.last_error" class="inline-alert">{{ account.last_error }}</p>
        <section class="drawer-section">
          <h2>自动化策略</h2>
          <label class="toggle-row"><span>自动激活</span><input v-model="form.enabled" type="checkbox" :disabled="!account.eligible" /></label>
          <label>文本模型<input v-model="form.model_override" list="account-models" placeholder="使用全局模型" /></label>
          <datalist id="account-models"><option v-for="model in models" :key="model" :value="model" /></datalist>
          <div class="form-grid compact">
            <label>刷新后延迟<input v-model.number="form.grace_override_seconds" type="number" min="0" max="600" placeholder="全局" /></label>
            <label>重试次数<input v-model.number="form.max_retries_override" type="number" min="0" max="6" placeholder="全局" /></label>
            <label>退避基数<input v-model.number="form.retry_base_override_seconds" type="number" min="5" max="600" placeholder="全局" /></label>
          </div>
          <p v-if="error" class="form-error">{{ error }}</p>
          <div class="action-row"><button class="primary command" :disabled="busy" @click="save"><Save :size="17" />保存</button><button class="secondary command" :disabled="busy" @click="refreshQuota"><RefreshCw :size="17" />刷新额度</button><button v-if="canRun" class="warning command" :disabled="busy" @click="run"><Play :size="17" />立即执行</button></div>
        </section>
        <section class="drawer-section"><h2>额度</h2><dl class="quota-list"><div><dt>5h 使用</dt><dd>{{ account.five_used_percent == null ? '未知' : `${Math.round(account.five_used_percent)}%` }}</dd></div><div><dt>5h 重置</dt><dd>{{ when(account.five_reset_at) }}</dd></div><div><dt>7d 使用</dt><dd>{{ account.seven_used_percent == null ? '未知' : `${Math.round(account.seven_used_percent)}%` }}</dd></div><div><dt>7d 重置</dt><dd>{{ when(account.seven_reset_at) }}</dd></div></dl></section>
        <section class="drawer-section"><h2>周期与尝试</h2><div class="timeline"><article v-for="cycle in cycles" :key="cycle.id"><button @click="loadAttempts(cycle)"><span>{{ cycle.cycle_key }}</span><StatusBadge :state="cycle.status" /><small>{{ when(cycle.created_at) }} · {{ cycle.attempt_count }} 次尝试</small></button><ul v-if="attempts[cycle.id]"><li v-for="attempt in attempts[cycle.id]" :key="attempt.id"><strong>#{{ attempt.attempt_number }} {{ attempt.outcome }}</strong><span>{{ attempt.message || attempt.error_code || '—' }}</span></li></ul></article><p v-if="!cycles.length" class="empty-state">暂无周期记录</p></div></section>
      </div>
    </aside>
  </div>
</template>
