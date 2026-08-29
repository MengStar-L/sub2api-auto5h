<script setup lang="ts">
import { ref } from 'vue'
import { LogIn } from 'lucide-vue-next'
import { api } from '../api'

const emit = defineEmits<{ authenticated: [] }>()
const username = ref('')
const password = ref('')
const error = ref('')
const busy = ref(false)

async function submit() {
  busy.value = true
  error.value = ''
  try {
    await api('/api/auth/login', { method: 'POST', body: JSON.stringify({ username: username.value, password: password.value }) })
    emit('authenticated')
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : '登录失败'
  } finally { busy.value = false }
}
</script>

<template>
  <main class="auth-page">
    <form class="auth-panel" @submit.prevent="submit">
      <div class="auth-title">
        <span class="brand-mark" aria-hidden="true">5h</span>
        <h1>sub2api-auto5h</h1>
      </div>
      <p class="muted-copy">登录后查看 5 小时窗口，并按账号安排自动唤醒。</p>
      <label>用户名<input v-model="username" autocomplete="username" required /></label>
      <label>密码<input v-model="password" type="password" autocomplete="current-password" required /></label>
      <p v-if="error" class="form-error">{{ error }}</p>
      <button class="primary command" type="submit" :disabled="busy"><LogIn :size="18" />{{ busy ? '登录中' : '登录' }}</button>
    </form>
  </main>
</template>
