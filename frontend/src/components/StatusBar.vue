<script setup lang="ts">
import { computed } from 'vue';
import type { TypingPhase } from '../types';

const props = defineProps<{
  phase: TypingPhase;
  message: string;
  /** 0-100, -1 表示隐藏 */
  progress: number;
}>();

const barClass = computed(() => ['status-bar', props.phase !== 'idle' ? props.phase : '']);

const progressActive = computed(() => props.progress >= 0);
const progressWidth = computed(() => (props.progress >= 0 ? Math.min(props.progress, 100) + '%' : '0%'));
</script>

<template>
  <div :class="barClass">
    <div class="status-text">{{ message }}</div>
  </div>
  <!-- 进度条 -->
  <div class="progress-wrap" :class="{ active: progressActive }">
    <div class="progress-track">
      <div class="progress-fill" :style="{ width: progressWidth }"></div>
    </div>
  </div>
</template>
