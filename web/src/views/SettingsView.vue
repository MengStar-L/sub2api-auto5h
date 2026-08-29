<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { CheckCircle2, Save } from 'lucide-vue-next'
import { api } from '../api'
import type { Settings } from '../types'

const form = reactive<Settings>({
  base_url: '', api_key: '', global_model: '', allow_private_http: false,
  sync_interval_seconds: 300, reset_grace_seconds: 30, max_retries: 3,
  retry_base_seconds: 30, request_timeout_seconds: 90, max_concurrency: 4, direct_wakeup_enabled: false,
})
const configured = ref(false)
const originalDirectWakeup = ref(false)
const riskAcknowledged = ref(false)
const password = reactive({ username: 'admin', current_password: '', new_password: '' })
const busy = ref(false)
const message = ref('')
const error = ref('')

async function load() {
  try {
    const settings = await api<Settings>('/api/settings')
    Object.assign(form, settings, { api_key: '' }); configured.value = Boolean(settings.api_key_configured)
    originalDirectWakeup.value = settings.direct_wakeup_enabled
  } catch (reason) { error.value = reason instanceof Error ? reason.message : '加载失败' }
}
async function test() { await submit('/api/settings/test', '连接验证通过', false) }
async function save() { await submit('/api/settings', '设置已保存', true) }
function settingsPayload() {
  return {
    base_url: form.base_url,
    api_key: form.api_key,
    global_model: form.global_model,
    allow_private_http: form.allow_private_http,
    sync_interval_seconds: form.sync_interval_seconds,
    reset_grace_seconds: form.reset_grace_seconds,
    max_retries: form.max_retries,
    retry_base_seconds: form.retry_base_seconds,
    request_timeout_seconds: form.request_timeout_seconds,
    max_concurrency: form.max_concurrency,
    direct_wakeup_enabled: form.direct_wakeup_enabled,
    direct_wakeup_risk_acknowledged: riskAcknowledged.value,
  }
}
async function changePassword() {
  busy.value = true; error.value = ''; message.value = ''
  try {
    await api('/api/auth/password', { method: 'PUT', body: JSON.stringify(password) })
    password.current_password = ''; password.new_password = ''; message.value = '管理员密码已更新，其他会话已退出'
  } catch (reason) { error.value = reason instanceof Error ? reason.message : '密码更新失败' }
  finally { busy.value = false }
}
async function submit(path: string, success: string, persist: boolean) {
  busy.value = true; error.value = ''; message.value = ''
  try {
    const result = await api<Record<string, unknown>>(path, { method: persist ? 'PUT' : 'POST', body: JSON.stringify(settingsPayload()) })
    message.value = persist ? success : `${success} · sub2api ${String(result.version ?? '')}`
    if (persist) { form.api_key = ''; configured.value = true; originalDirectWakeup.value = form.direct_wakeup_enabled; riskAcknowledged.value = false }
  } catch (reason) { error.value = reason instanceof Error ? reason.message : '操作失败' }
  finally { busy.value = false }
}
onMounted(load)
</script>

<template>
  <form class="settings-form" @submit.prevent="save">
    <section class="settings-band"><header><h2>sub2api 连接</h2><span v-if="configured" class="configured"><CheckCircle2 :size="15" />已配置密钥</span></header><div class="form-grid"><label class="wide">服务地址<input v-model="form.base_url" type="url" required /></label><label class="wide">Admin API Key<input v-model="form.api_key" type="password" autocomplete="off" :placeholder="configured ? '留空则保持不变' : ''" /></label><label class="wide">默认文本模型<input v-model="form.global_model" required /></label></div><label class="checkbox-row"><input v-model="form.allow_private_http" type="checkbox" />允许回环或私网 IP 使用 HTTP</label></section>
    <section class="settings-band"><header><h2>调度</h2></header><div class="form-grid"><label>账号同步间隔（秒）<input v-model.number="form.sync_interval_seconds" type="number" min="60" max="3600" required /></label><label>刷新后延迟（秒）<input v-model.number="form.reset_grace_seconds" type="number" min="0" max="600" required /></label><label>重试次数<input v-model.number="form.max_retries" type="number" min="0" max="6" required /></label><label>退避基数（秒）<input v-model.number="form.retry_base_seconds" type="number" min="5" max="600" required /></label><label>请求超时（秒）<input v-model.number="form.request_timeout_seconds" type="number" min="15" max="300" required /></label><label>最大并发<input v-model.number="form.max_concurrency" type="number" min="1" max="16" required /></label></div></section>
    <section class="settings-band direct-wakeup-band">
      <header><div><h2>官方 Codex 直连唤醒</h2><p>按需读取目标账号的临时认证材料，并严格沿用该账号在 sub2api 中配置的代理。</p></div><label class="switch-row"><span>{{ form.direct_wakeup_enabled ? '已启用' : '已关闭' }}</span><input v-model="form.direct_wakeup_enabled" type="checkbox" aria-label="启用官方 Codex 直连唤醒" /></label></header>
      <div class="security-note"><strong>安全边界</strong><span>认证令牌和代理密码只在单次请求的内存中使用，不写入 SQLite、事件或日志。配置代理失败时不会回退为服务器直连。</span></div>
      <label v-if="form.direct_wakeup_enabled && !originalDirectWakeup" class="risk-ack"><input v-model="riskAcknowledged" type="checkbox" /><span>我确认该功能会让本程序使用账号 OAuth 访问令牌和代理，直接请求 ChatGPT 官方 Codex 接口。</span></label>
    </section>
    <section class="settings-band"><header><h2>管理员密码</h2></header><div class="form-grid"><label>用户名<input v-model="password.username" autocomplete="username" /></label><label>当前密码<input v-model="password.current_password" type="password" autocomplete="current-password" /></label><label class="wide">新密码<input v-model="password.new_password" type="password" minlength="12" maxlength="128" autocomplete="new-password" /></label></div><button class="secondary command" type="button" :disabled="busy || !password.current_password || !password.new_password" @click="changePassword">更新密码</button></section>
    <p v-if="error" class="inline-alert">{{ error }}</p><p v-if="message" class="success-alert">{{ message }}</p>
    <div class="action-row"><button class="secondary command" type="button" :disabled="busy" @click="test"><CheckCircle2 :size="17" />测试连接</button><button class="primary command" type="submit" :disabled="busy"><Save :size="17" />保存设置</button></div>
  </form>
</template>
