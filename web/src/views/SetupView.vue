<script setup lang="ts">
import { reactive, ref } from 'vue'
import { CheckCircle2 } from 'lucide-vue-next'
import { api } from '../api'

defineProps<{ available: boolean }>()
const emit = defineEmits<{ complete: [] }>()
const token = ref('')
const error = ref('')
const busy = ref(false)
const form = reactive({
  username: 'admin', password: '', base_url: '', api_key: '', global_model: '', allow_private_http: false,
  sync_interval_seconds: 300, reset_grace_seconds: 30, max_retries: 3,
  retry_base_seconds: 30, request_timeout_seconds: 90, max_concurrency: 4,
})

async function submit() {
  busy.value = true; error.value = ''
  try {
    await api('/api/setup/complete', { method: 'POST', headers: { 'X-Setup-Token': token.value }, body: JSON.stringify(form) })
    emit('complete')
  } catch (reason) { error.value = reason instanceof Error ? reason.message : '设置失败' }
  finally { busy.value = false }
}
</script>

<template>
  <main class="setup-page">
    <form class="setup-panel" @submit.prevent="submit">
      <header class="setup-heading">
        <span class="brand-mark" aria-hidden="true">5h</span>
        <div>
          <h1>初始化</h1>
          <p>sub2api-auto5h</p>
        </div>
      </header>
      <p v-if="!available" class="form-error">主密钥无效或初始化令牌已过期</p>
      <div class="form-grid">
        <label class="wide">Setup Token<input v-model="token" type="password" autocomplete="off" required /></label>
        <label>管理员用户名<input v-model="form.username" autocomplete="username" required /></label>
        <label>管理员密码<input v-model="form.password" type="password" minlength="12" maxlength="128" autocomplete="new-password" required /></label>
        <label class="wide">sub2api 地址<input v-model="form.base_url" type="url" placeholder="https://sub2api.example.com" required /></label>
        <label class="wide">Admin API Key<input v-model="form.api_key" type="password" autocomplete="off" required /></label>
        <label class="wide">默认文本模型<input v-model="form.global_model" placeholder="gpt-5.4" required /></label>
        <label>同步间隔（秒）<input v-model.number="form.sync_interval_seconds" type="number" min="60" max="3600" required /></label>
        <label>刷新后延迟（秒）<input v-model.number="form.reset_grace_seconds" type="number" min="0" max="600" required /></label>
        <label>重试次数<input v-model.number="form.max_retries" type="number" min="0" max="6" required /></label>
        <label>退避基数（秒）<input v-model.number="form.retry_base_seconds" type="number" min="5" max="600" required /></label>
        <label>请求超时（秒）<input v-model.number="form.request_timeout_seconds" type="number" min="15" max="300" required /></label>
        <label>最大并发<input v-model.number="form.max_concurrency" type="number" min="1" max="16" required /></label>
      </div>
      <label class="checkbox-row"><input v-model="form.allow_private_http" type="checkbox" />允许回环或私网 IP 使用 HTTP</label>
      <p v-if="error" class="form-error">{{ error }}</p>
      <button class="primary command" type="submit" :disabled="busy || !available"><CheckCircle2 :size="18" />{{ busy ? '正在验证' : '完成初始化' }}</button>
    </form>
  </main>
</template>
