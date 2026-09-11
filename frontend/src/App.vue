<script setup lang="ts">
import { computed, ref } from 'vue';
import StatusBar from './components/StatusBar.vue';
import { useTheme } from './composables/useTheme';
import { useTypingTask } from './composables/useTypingTask';
import { toggleTopmost } from './ipc';

// ─── 主题 ───
const { theme, toggle: toggleTheme } = useTheme();

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
    <span class="caption-title">Type</span>
    <div class="caption-actions">
      <button
        class="icon-btn"
        :title="theme === 'dark' ? '切换到浅色主题' : '切换到深色主题'"
        @click="toggleTheme"
      >
        <!-- 太阳：暗色时显示，点击回浅色 -->
        <svg v-if="theme === 'dark'" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden>
          <circle cx="12" cy="12" r="4" />
          <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41" />
        </svg>
        <!-- 月亮：浅色时显示，点击进暗色 -->
        <svg v-else width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden>
          <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
        </svg>
      </button>
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
      <button
        id="chkForceRaw"
        type="button"
        class="pill-toggle"
        role="switch"
        :class="{ on: forceRaw }"
        :aria-checked="forceRaw"
        :title="forceRaw ? '当前将绕过目标程序的粘贴检测直接发送' : '点击开启: 绕过目标程序的粘贴检测'"
        @click="forceRaw = !forceRaw"
      >
        <span class="pill-dot" aria-hidden></span>
        <span>绕过粘贴检测</span>
      </button>
      <div class="delay-slider-row">
        <span class="slider-label">延迟</span>
        <input id="delaySlider" v-model.number="delay" type="range" min="1" max="9" class="slider">
        <span id="delayValue" class="delay-value">{{ delay }} 秒</span>
      </div>
    </div>

    <div class="actions-row">
      <button id="btnStart" class="btn btn-primary" :disabled="isRunning" @click="onStart">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden><polygon points="5 3 19 12 5 21 5 3" /></svg>
        启动
      </button>
      <button id="btnCancel" class="btn btn-secondary" :disabled="!isRunning" @click="onCancel">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden><line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" /></svg>
        取消
      </button>
    </div>

    <!-- 目标窗口预览 -->
    <div id="targetRow" class="target-row" :class="{ active: !!status.targetWindow }">
      <span class="target-label">目标窗口</span>
      <span id="targetName" class="target-name">{{
        status.targetWindow || (status.phase === 'countdown' ? '请切换到目标窗口…' : '—')
      }}</span>
    </div>

    <!-- 状态与进度 -->
    <StatusBar :phase="status.phase" :message="status.message" :progress="status.progress" />
  </div>
</template>
