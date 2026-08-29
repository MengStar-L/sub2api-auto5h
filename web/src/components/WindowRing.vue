<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  value?: number | null
  size?: number
}>(), { size: 36 })

const pct = computed(() => {
  if (props.value == null || Number.isNaN(props.value)) return null
  return Math.min(100, Math.max(0, props.value))
})
</script>

<template>
  <svg
    class="window-ring"
    :class="{ 'window-ring--empty': pct == null, 'window-ring--hot': (pct ?? 0) >= 85 }"
    :width="size"
    :height="size"
    viewBox="0 0 36 36"
    aria-hidden="true"
  >
    <circle class="window-ring__track" cx="18" cy="18" r="14" />
    <circle
      v-if="pct != null"
      class="window-ring__value"
      cx="18"
      cy="18"
      r="14"
      pathLength="100"
      :stroke-dasharray="`${pct} 100`"
    />
  </svg>
</template>
