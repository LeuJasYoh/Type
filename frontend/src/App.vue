<script setup lang="ts">
import { computed, ref } from 'vue';
import StatusBar from './components/StatusBar.vue';
import { useTypingTask } from './composables/useTypingTask';
import { toggleTopmost } from './ipc';

// ─── 表单状态 ───
const text = ref('');
const delay = ref(5);
const forceRaw = ref(false);

// 按码点计数(与 Go 端 []rune 进度分母一致), emoji 不重复计 2
const charCount = computed(() => Array.from(text.value).length);

// ─── 输入任务 ───
const { status, isRunning, start, cancel } = useTypingTask();

async function onStart(): Promise<void> {
  await start(text.value, delay.value, forceRaw.value);
}

function onCancel(): void {
  cancel();
}

// ─── 置顶 ───
const pinned = ref(false);

async function onTogglePin(): Promise<void> {
  pinned.value = await toggleTopmost();
}
</script>

<template>
  <div class="window-caption">
    <span>Type</span>
    <button
      id="btnPin"
      class="pin-btn"
      :class="{ active: pinned }"
      :title="pinned ? '取消窗口置顶' : '窗口置顶'"
      @click="onTogglePin"
    >
      <span id="pinLabel">{{ pinned ? '取消置顶' : '置顶' }}</span>
    </button>
  </div>

  <div class="app-container">
    <!-- 输入区域 -->
    <div class="section">
      <label class="section-label">输入要模拟键入的文本</label>
      <div class="input-wrap">
        <textarea id="textInput" v-model="text" placeholder="请输入文本..."></textarea>
        <div class="char-count"><span id="charCount">{{ charCount }}</span> 个字符</div>
      </div>
    </div>

    <div class="options-row">
      <label class="checkbox-label">
        <input id="chkForceRaw" v-model="forceRaw" type="checkbox">
        <span class="checkbox-text">绕过粘贴检测</span>
      </label>
      <div class="delay-slider-row">
        <span class="slider-label">延迟</span>
        <input id="delaySlider" v-model.number="delay" type="range" min="1" max="10" class="slider">
        <span id="delayValue" class="delay-value">{{ delay }} 秒</span>
      </div>
    </div>

    <div class="actions-row">
      <button id="btnStart" class="btn btn-primary" :disabled="isRunning" @click="onStart">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polygon points="5 3 19 12 5 21 5 3"/></svg>
        启动
      </button>
      <button id="btnCancel" class="btn btn-secondary" :disabled="!isRunning" @click="onCancel">
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
        取消
      </button>
    </div>

    <!-- 目标窗口预览 -->
    <div id="targetRow" class="target-row" :class="{ active: !!status.targetWindow }">
      <span class="target-label">目标窗口</span>
      <span id="targetName" class="target-name">{{ status.targetWindow || '—' }}</span>
    </div>

    <!-- 状态与进度 -->
    <StatusBar :phase="status.phase" :message="status.message" :progress="status.progress" />
  </div>
</template>
