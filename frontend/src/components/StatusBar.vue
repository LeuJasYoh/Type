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
  <!-- 单一根节点: 状态栏+进度条作为一个整体参与外层 flex 布局,
       避免收起时的进度条仍占据一个 gap 槽 -->
  <div class="status-block">
    <div :class="barClass">
      <div class="status-text">{{ message }}</div>
    </div>
    <!-- 进度条：运行时平滑长出 -->
    <div class="progress-wrap" :class="{ active: progressActive }">
      <div class="progress-track">
        <div class="progress-fill" :style="{ width: progressWidth }"></div>
      </div>
    </div>
  </div>
</template>
