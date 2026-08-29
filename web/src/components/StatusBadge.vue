<script setup lang="ts">
defineProps<{ state: string }>()

const labels: Record<string, string> = {
  disabled: '已关闭', pending_check: '待检查', manual_check: '检查中', waiting: '等待刷新', due: '已到期',
  direct_disabled: '直连已关闭', dispatching: '发送中', retry_wait: '等待重试', verifying: '额度核验中',
  accepted_unverified: '请求成功·额度未确认', verified: '已验证',
  success_inferred: '推断成功', skipped_external: '外部已启动', blocked_7d: '7d 已耗尽', quota_retry: '额度重试',
  attention: '需要处理', paused: '已暂停', missing: '已移除', identity_changed: '身份已变化', failed: '失败',
}
const tone = (state: string) => ['verified', 'success_inferred', 'skipped_external'].includes(state) ? 'success'
  : ['attention', 'failed', 'identity_changed', 'missing'].includes(state) ? 'danger'
    : ['blocked_7d', 'retry_wait', 'quota_retry', 'verifying', 'accepted_unverified'].includes(state) ? 'warning' : 'neutral'
</script>

<template><span class="status-badge" :class="tone(state)"><i class="dot" />{{ labels[state] ?? state }}</span></template>
