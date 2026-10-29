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

// 滑块已填充比例: 1 秒 → 0%, 9 秒 → 100%; CSS 的 --p 由这里绑上去
const sliderFill = computed(() => `${(((delay.value - 1) / 8) * 100).toFixed(2)}%`);

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
  <!-- 装饰层：冷光与等高线，给玻璃卡片一点可被模糊的底；纯页面内实现 -->
  <div class="ambient" aria-hidden="true">
    <svg viewBox="0 0 576 520" preserveAspectRatio="xMidYMid slice">
      <path
        stroke-opacity="0.095"
        d="M539,210L548,223L551,237L545,251L531,262L511,271L489,277L467,282L447,286L427,290L406,293L385,294L365,291L346,285L331,277L318,270L305,263L290,258L272,254L252,248L234,239L222,229L216,216L217,204L222,191L228,179L235,167L243,155L255,143L272,133L294,128L319,126L344,129L365,133L385,137L402,140L420,142L439,143L457,146L474,152L487,160L497,169L506,179L516,188L527,199L539,210Z"
      />
      <path
        stroke-opacity="0.12"
        d="M638,210L648,231L650,253L639,274L615,292L582,304L545,312L509,316L477,320L447,325L418,330L388,333L357,332L328,326L302,317L279,307L257,298L231,290L201,282L168,273L138,259L116,241L106,221L109,199L121,179L138,161L155,144L173,127L194,110L221,97L253,88L290,86L326,89L358,95L387,101L413,105L441,106L471,106L503,108L535,115L561,126L581,140L596,156L610,173L623,191L638,210Z"
      />
      <path
        stroke-opacity="0.07"
        d="M734,210L741,238L739,267L722,295L690,318L644,333L594,342L545,346L502,349L465,355L428,362L390,370L349,374L307,372L268,364L232,352L198,339L162,327L121,314L77,299L36,280L6,254L-6,225L1,195L24,168L55,144L86,123L116,104L146,85L180,68L219,55L263,49L308,51L351,57L389,63L425,67L464,67L507,65L555,66L604,72L646,86L678,107L699,131L713,157L724,183L734,210Z"
      />
      <path
        stroke-opacity="0.095"
        d="M820,210L818,244L810,278L788,311L750,338L697,358L638,369L579,374L527,378L482,385L439,397L393,411L340,422L283,426L227,420L176,407L129,389L83,369L35,349L-15,327L-62,299L-96,266L-109,229L-96,192L-62,158L-17,130L30,107L72,86L109,65L146,44L189,26L237,14L290,10L343,12L392,17L440,21L491,20L549,18L614,18L680,25L738,43L781,71L807,105L817,140L820,176L820,210Z"
      />
      <path
        stroke-opacity="0.12"
        d="M889,210L875,249L860,286L837,323L799,355L745,380L681,396L616,405L557,411L504,422L452,438L396,459L329,478L255,488L180,484L111,469L52,444L-1,414L-51,384L-101,352L-148,317L-182,276L-193,232L-176,188L-134,149L-76,118L-17,93L35,71L77,47L116,22L158,-4L209,-25L268,-39L332,-43L396,-41L459,-38L524,-36L596,-36L676,-34L758,-23L830,0L882,36L909,79L913,125L903,169L889,210Z"
      />
      <path
        stroke-opacity="0.07"
        d="M941,210L916,252L896,292L874,332L842,370L794,403L732,427L663,443L595,455L532,470L469,491L399,517L317,541L225,555L131,553L44,533L-27,499L-83,458L-129,416L-172,373L-212,330L-243,284L-254,235L-237,186L-192,142L-129,107L-63,79L-5,55L41,28L79,-4L121,-39L175,-72L242,-97L319,-110L400,-112L481,-107L561,-100L647,-94L738,-85L830,-68L912,-38L970,5L997,57L993,112L970,164L941,210Z"
      />
      <path
        stroke-opacity="0.095"
        d="M982,210L949,254L927,297L912,342L890,387L853,430L795,466L724,493L646,513L568,532L490,555L403,582L305,608L195,623L83,619L-18,593L-98,550L-154,497L-193,441L-224,388L-254,338L-280,288L-293,236L-283,184L-245,136L-186,96L-119,63L-58,33L-9,1L31,-38L75,-83L134,-129L211,-166L305,-187L405,-191L505,-182L600,-167L695,-149L792,-130L889,-104L974,-67L1036,-18L1063,41L1055,102L1022,160L982,210Z"
      />
      <path
        stroke-opacity="0.12"
        d="M1025,210L989,258L969,304L962,354L953,409L927,465L875,515L799,555L708,584L612,606L512,627L408,650L293,671L168,683L42,675L-70,643L-156,591L-210,527L-240,460L-259,399L-279,343L-302,291L-322,237L-326,183L-304,129L-257,81L-196,40L-134,3L-80,-37L-32,-83L19,-138L87,-193L178,-240L290,-268L410,-272L528,-257L637,-229L737,-197L834,-164L928,-128L1014,-86L1078,-32L1109,30L1103,95L1069,156L1025,210Z"
      />
    </svg>
  </div>

  <div class="window-caption">
    <span class="caption-title">Type</span>
    <div class="caption-actions">
      <button
        class="icon-btn"
        :title="theme === 'dark' ? '切换到浅色主题' : '切换到深色主题'"
        @click="toggleTheme"
      >
        <!-- 太阳：暗色时显示，点击回浅色 -->
        <!-- prettier-ignore -->
        <svg v-if="theme === 'dark'" :width="iconSize(15)" :height="iconSize(15)" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden>
          <circle cx="12" cy="12" r="4" />
          <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41" />
        </svg>
        <!-- 月亮：浅色时显示，点击进暗色 -->
        <!-- prettier-ignore -->
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
        <textarea
          id="textInput"
          class="text-input"
          v-model="text"
          placeholder="请输入文本..."
        ></textarea>
        <div class="char-count">
          <span>{{ charCount }}</span> 个字符
        </div>
      </div>
    </div>

    <div class="options-row">
      <button
        type="button"
        class="pill-toggle"
        role="switch"
        :class="{ on: forceRaw }"
        :aria-checked="forceRaw"
        :title="
          forceRaw ? '当前将绕过目标程序的粘贴检测直接发送' : '点击开启: 绕过目标程序的粘贴检测'
        "
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
        :title="
          textDirect
            ? '当前绕过按键层注入, 不触发补全弹窗与括号配对'
            : '点击开启: 用于带代码补全的在线编辑器'
        "
        @click="textDirect = !textDirect"
      >
        <span class="pill-dot" aria-hidden></span>
        <span>文本直投</span>
      </button>
      <div class="delay-slider-row">
        <span class="slider-label">延迟</span>
        <input
          v-model.number="delay"
          type="range"
          min="1"
          max="9"
          class="slider"
          :style="{ '--p': sliderFill }"
        />
        <span class="delay-value">{{ delay }} 秒</span>
      </div>
    </div>

    <div class="actions-row">
      <button class="btn btn-primary" :disabled="isRunning" @click="onStart">
        <!-- prettier-ignore -->
        <svg :width="iconSize(16)" :height="iconSize(16)" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden><polygon points="5 3 19 12 5 21 5 3" /></svg>
        启动
      </button>
      <button class="btn btn-secondary" :disabled="!isRunning" @click="onCancel">
        <!-- prettier-ignore -->
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
