<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import StatusBar from './components/StatusBar.vue';
import { useTheme } from './composables/useTheme';
import { useTypingTask } from './composables/useTypingTask';
import { useUiScale } from './composables/useUiScale';
import { getTopmost, toggleTopmost } from './ipc';

// ─── 主题 ───
const { theme, toggle: toggleTheme } = useTheme();

// ─── 观感缩放 ───
// CSS 里的细节尺寸直接吃 --ui-scale; 图标边长是 SVG 属性、CSS 变量够不着,
// 所以在这里按同一个系数算。viewBox 与 stroke-width 是图形自身的比例, 不参与
const { scale } = useUiScale();

function iconSize(base: number): number {
  // 取两位小数: 576 宽这种非整数倍下 15 * 1.0666… 会算出 16.000000000000004,
  // 直接写进属性很难看, 而两位小数的差异远在渲染精度之下
  return Math.round(base * scale.value * 100) / 100;
}

// ─── 表单状态 ───
const text = ref('');
const delay = ref(5);
const forceRaw = ref(false);
const textDirect = ref(false);

// 按码点计数(与 Go 端 []rune 进度分母一致), emoji 不重复计 2
const charCount = computed(() => Array.from(text.value).length);

// ─── 输入任务 ───
const { status, isRunning, start, cancel } = useTypingTask();

async function onStart(): Promise<void> {
  await start(text.value, delay.value, forceRaw.value, textDirect.value);
}

function onCancel(): void {
  cancel();
}

// ─── 置顶 ───
const pinned = ref(false);

async function onTogglePin(): Promise<void> {
  pinned.value = await toggleTopmost();
}

// 窗口的置顶不随页面重载复位, 而按钮每次都从"未置顶"起: 重载后不读回来的话, 按钮
// 显示"置顶"而窗口仍钉在最上层, 用户点一下反而是取消, 按钮与窗口正好相反
onMounted(async () => {
  try {
    pinned.value = await getTopmost();
  } catch {
    // 绑定不可用(如用普通浏览器打开 dev server)时保持未置顶
  }
});
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
        <svg v-if="theme === 'dark'" :width="iconSize(15)" :height="iconSize(15)" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden>
          <circle cx="12" cy="12" r="4" />
          <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41" />
        </svg>
        <!-- 月亮：浅色时显示，点击进暗色 -->
        <svg v-else :width="iconSize(15)" :height="iconSize(15)" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden>
          <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
        </svg>
      </button>
      <button
        class="pin-btn"
        :class="{ active: pinned }"
        :title="pinned ? '取消窗口置顶' : '窗口置顶'"
        @click="onTogglePin"
      >
        <span>{{ pinned ? '取消置顶' : '置顶' }}</span>
      </button>
    </div>
  </div>

  <div class="app-container">
    <!-- 输入区域 -->
    <div class="section">
      <label class="section-label" for="textInput">输入要模拟键入的文本</label>
      <div class="input-wrap">
        <textarea id="textInput" class="text-input" v-model="text" placeholder="请输入文本..."></textarea>
        <div class="char-count"><span>{{ charCount }}</span> 个字符</div>
      </div>
    </div>

    <div class="options-row">
      <button
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
      <button
        type="button"
        class="pill-toggle"
        role="switch"
        :class="{ on: textDirect }"
        :aria-checked="textDirect"
        :title="textDirect
          ? '当前绕过按键层注入, 不触发补全弹窗与括号配对'
          : '点击开启: 用于带代码补全的在线编辑器'"
        @click="textDirect = !textDirect"
      >
        <span class="pill-dot" aria-hidden></span>
        <span>文本直投</span>
      </button>
      <div class="delay-slider-row">
        <span class="slider-label">延迟</span>
        <input v-model.number="delay" type="range" min="1" max="9" class="slider">
        <span class="delay-value">{{ delay }} 秒</span>
      </div>
    </div>

    <div class="actions-row">
      <button class="btn btn-primary" :disabled="isRunning" @click="onStart">
        <svg :width="iconSize(16)" :height="iconSize(16)" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden><polygon points="5 3 19 12 5 21 5 3" /></svg>
        启动
      </button>
      <button class="btn btn-secondary" :disabled="!isRunning" @click="onCancel">
        <svg :width="iconSize(16)" :height="iconSize(16)" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden><line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" /></svg>
        取消
      </button>
    </div>

    <!-- 目标窗口预览 -->
    <div class="target-row" :class="{ active: !!status.targetWindow }">
      <span class="target-label">目标窗口</span>
      <span class="target-name">{{ status.targetWindow || '—' }}</span>
    </div>

    <!-- 状态与进度 -->
    <StatusBar :phase="status.phase" :message="status.message" :progress="status.progress" />
  </div>
</template>
