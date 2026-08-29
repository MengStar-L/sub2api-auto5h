<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { RouterLink, RouterView, useRoute } from 'vue-router'
import { Activity, LogOut, Settings, Users } from 'lucide-vue-next'
import { api } from './api'
import LoginView from './views/LoginView.vue'
import SetupView from './views/SetupView.vue'

const loading = ref(true)
const setupComplete = ref(false)
const setupAvailable = ref(false)
const authenticated = ref(false)
const status = ref<{ global_pause: boolean; pause_reason?: string; active_workers: number }>({ global_pause: false, active_workers: 0 })
const route = useRoute()
let poll: number | undefined

const pageTitle = computed(() => ({ '/accounts': '账号', '/events': '事件', '/settings': '设置' }[route.path] ?? '账号'))
const pageHint = computed(() => ({
  '/accounts': '5 小时窗口与自动唤醒',
  '/events': '最近 90 天的调度与操作记录',
  '/settings': '连接、调度与直连风险确认',
}[route.path] ?? ''))

async function bootstrap() {
  loading.value = true
  try {
    const setup = await api<{ setup_complete: boolean; setup_available: boolean }>('/api/setup/status')
    setupComplete.value = setup.setup_complete
    setupAvailable.value = setup.setup_available
    if (setup.setup_complete) {
      try {
        await api('/api/auth/session')
        authenticated.value = true
        await refreshStatus()
      } catch {
        authenticated.value = false
      }
    }
  } finally {
    loading.value = false
  }
}

async function refreshStatus() {
  if (!authenticated.value) return
  try { status.value = await api<{ global_pause: boolean; pause_reason?: string; active_workers: number }>('/api/status') } catch { authenticated.value = false }
}

async function logout() {
  try { await api('/api/auth/logout', { method: 'POST', body: '{}' }) } finally { authenticated.value = false }
}

onMounted(async () => {
  await bootstrap()
  poll = window.setInterval(refreshStatus, 15_000)
})
onBeforeUnmount(() => window.clearInterval(poll))
</script>

<template>
  <div v-if="loading" class="center-state">
    <span class="loader-ring" aria-hidden="true" />
    <span>正在加载</span>
  </div>
  <SetupView v-else-if="!setupComplete" :available="setupAvailable" @complete="bootstrap" />
  <LoginView v-else-if="!authenticated" @authenticated="bootstrap" />
  <div v-else class="app-shell">
    <aside class="sidebar">
      <div class="brand">
        <span class="brand-mark">5h</span>
        <span class="brand-copy"><strong>sub2api-auto5h</strong><small>额度窗口调度</small></span>
      </div>
      <nav aria-label="主导航">
        <RouterLink to="/accounts" aria-label="账号"><Users :size="18" /><span>账号</span></RouterLink>
        <RouterLink to="/events" aria-label="事件"><Activity :size="18" /><span>事件</span></RouterLink>
        <RouterLink to="/settings" aria-label="设置"><Settings :size="18" /><span>设置</span></RouterLink>
      </nav>
      <button class="icon-text ghost logout" type="button" title="退出登录" @click="logout"><LogOut :size="18" /><span>退出</span></button>
    </aside>
    <main class="main-content">
      <header class="topbar">
        <div>
          <h1>{{ pageTitle }}</h1>
          <p v-if="status.global_pause" class="global-warning">{{ status.pause_reason }}</p>
          <p v-else-if="pageHint" class="muted-copy">{{ pageHint }}</p>
        </div>
        <span class="worker-state"><i :class="status.global_pause ? 'dot danger' : 'dot ok'" />{{ status.global_pause ? '调度已暂停' : `${status.active_workers} 个任务运行中` }}</span>
      </header>
      <RouterView v-slot="{ Component }">
        <Transition name="page" mode="out-in">
          <div :key="route.path" class="page-frame">
            <component :is="Component" />
          </div>
        </Transition>
      </RouterView>
    </main>
  </div>
</template>
