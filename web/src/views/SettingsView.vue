<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { CheckCircle2, Save } from 'lucide-vue-next'
import { api } from '../api'
import type { Settings } from '../types'

const form = reactive<Settings>({
  base_url: '', api_key: '', global_model: '', allow_private_http: false,
  sync_interval_seconds: 300, reset_grace_seconds: 30, max_retries: 3,
  retry_base_seconds: 30, request_timeout_seconds: 90, max_concurrency: 4,
})
const configured = ref(false)
const password = reactive({ username: 'admin', current_password: '', new_password: '' })
const busy = ref(false)
const message = ref('')
const error = ref('')

async function load() {
  try {
    const settings = await api<Settings>('/api/settings')
    Object.assign(form, settings, { api_key: '' }); configured.value = Boolean(settings.api_key_configured)
  } catch (reason) { error.value = reason instanceof Error ? reason.message : '加载失败' }
}
async function test() { await submit('/api/settings/test', '连接验证通过', false) }
async function save() { await submit('/api/settings', '设置已保存', true) }
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
    const result = await api<Record<string, unknown>>(path, { method: persist ? 'PUT' : 'POST', body: JSON.stringify(form) })
    message.value = persist ? success : `${success} · sub2api ${String(result.version ?? '')}`
    if (persist) { form.api_key = ''; configured.value = true }
  } catch (reason) { error.value = reason instanceof Error ? reason.message : '操作失败' }
  finally { busy.value = false }
}
onMounted(load)
</script>

<template>
  <form class="settings-form" @submit.prevent="save">
    <section class="settings-band"><header><h2>sub2api 连接</h2><span v-if="configured" class="configured"><CheckCircle2 :size="15" />已配置密钥</span></header><div class="form-grid"><label class="wide">服务地址<input v-model="form.base_url" type="url" required /></label><label class="wide">Admin API Key<input v-model="form.api_key" type="password" autocomplete="off" :placeholder="configured ? '留空则保持不变' : ''" /></label><label class="wide">默认文本模型<input v-model="form.global_model" required /></label></div><label class="checkbox-row"><input v-model="form.allow_private_http" type="checkbox" />允许回环或私网 IP 使用 HTTP</label></section>
    <section class="settings-band"><header><h2>调度</h2></header><div class="form-grid"><label>账号同步间隔（秒）<input v-model.number="form.sync_interval_seconds" type="number" min="60" max="3600" required /></label><label>刷新后延迟（秒）<input v-model.number="form.reset_grace_seconds" type="number" min="0" max="600" required /></label><label>重试次数<input v-model.number="form.max_retries" type="number" min="0" max="6" required /></label><label>退避基数（秒）<input v-model.number="form.retry_base_seconds" type="number" min="5" max="600" required /></label><label>请求超时（秒）<input v-model.number="form.request_timeout_seconds" type="number" min="15" max="300" required /></label><label>最大并发<input v-model.number="form.max_concurrency" type="number" min="1" max="16" required /></label></div></section>
    <section class="settings-band"><header><h2>管理员密码</h2></header><div class="form-grid"><label>用户名<input v-model="password.username" autocomplete="username" /></label><label>当前密码<input v-model="password.current_password" type="password" autocomplete="current-password" /></label><label class="wide">新密码<input v-model="password.new_password" type="password" minlength="12" maxlength="128" autocomplete="new-password" /></label></div><button class="secondary command" type="button" :disabled="busy || !password.current_password || !password.new_password" @click="changePassword">更新密码</button></section>
    <p v-if="error" class="inline-alert">{{ error }}</p><p v-if="message" class="success-alert">{{ message }}</p>
    <div class="action-row"><button class="secondary command" type="button" :disabled="busy" @click="test"><CheckCircle2 :size="17" />测试连接</button><button class="primary command" type="submit" :disabled="busy"><Save :size="17" />保存设置</button></div>
  </form>
</template>
