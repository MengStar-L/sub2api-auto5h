<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RefreshCw } from 'lucide-vue-next'
import { api } from '../api'
import type { EventItem } from '../types'

const events = ref<EventItem[]>([])
const loading = ref(false)
const error = ref('')
function when(value: number) { return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'medium' }).format(new Date(value * 1000)) }
async function load() {
  loading.value = true; error.value = ''
  try { events.value = await api<EventItem[]>('/api/events') }
  catch (reason) { error.value = reason instanceof Error ? reason.message : '加载失败' }
  finally { loading.value = false }
}
onMounted(load)
</script>

<template>
  <section class="toolbar"><div><strong>最近 90 天</strong></div><div class="toolbar-spacer" /><button class="icon-button secondary" title="刷新事件" :disabled="loading" @click="load"><RefreshCw :size="18" :class="{ spin: loading }" /></button></section>
  <p v-if="error" class="inline-alert">{{ error }}</p>
  <section class="event-list">
    <article v-for="item in events" :key="item.id"><i class="event-marker" :class="item.level" /><div><header><strong>{{ item.message }}</strong><time>{{ when(item.created_at) }}</time></header><p>{{ item.actor }} · {{ item.action }}<span v-if="item.account_id"> · {{ item.account_id }}</span></p></div></article>
    <p v-if="!events.length && !loading" class="empty-state">暂无事件</p>
  </section>
</template>
