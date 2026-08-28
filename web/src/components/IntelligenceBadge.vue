<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ status?: string }>()

const label = computed(() => props.status === 'normal' ? '智商正常' : props.status === 'abnormal' ? '智商不正常' : '未测试')
const tone = computed(() => props.status === 'normal' ? 'success' : props.status === 'abnormal' ? 'danger' : '')
</script>

<template>
  <span class="status-badge" :class="tone"><span class="dot" />{{ label }}</span>
</template>
