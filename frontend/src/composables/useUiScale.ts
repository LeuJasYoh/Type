// ═══ 观感缩放 ═════════════════════════════════════════
// 把窗口内宽折成 --ui-scale 写到根元素, 交给 CSS 缩放观感细节: 取值边界与"永不参与"
// 的完整理由见 docs/invariants.md「其它不变量」与 style.css 的 --ui-scale 处, 取值本身由
// test/uiScale.test.ts 钉着边界。
// 挂载时读到的宽度未必是最终值, 故先写一次(免得首帧按 1 渲染再跳)再由 ResizeObserver
// 持续校准, 只读一次不够。

import { onBeforeUnmount, onMounted, ref } from 'vue';

/** 基准宽度: 内宽 540 时缩放为 1。必须等于宿主窗口下限 MinWindowW(契约测试核对) */
export const BASE_WIDTH = 540;
/** 上限: 窗口内宽上限是 648, 648/540 = 1.2, 所以这个 1.25 正常路径取不到 */
export const MAX_SCALE = 1.25;

/** 窗口内宽 → 观感缩放。夹在 1~MAX_SCALE: 540 及以下不缩, 更宽才按比例放大 */
export function uiScaleFor(width: number): number {
  return Math.min(MAX_SCALE, Math.max(1, width / BASE_WIDTH));
}

function readScale(): number {
  // 用 clientWidth 而不是 innerWidth: 它才是 CSS 百分比实际解析所依据的宽度
  // (根元素出滚动条时会比 innerWidth 小, 虽然本页面 body 是 overflow:hidden)
  const width = document.documentElement.clientWidth || window.innerWidth;
  return uiScaleFor(width);
}

export function useUiScale() {
  const scale = ref(1);
  let observer: ResizeObserver | null = null;

  function apply(): void {
    const next = readScale();
    scale.value = next;
    document.documentElement.style.setProperty('--ui-scale', String(next));
  }

  function onWindowResize(): void {
    apply();
  }

  onMounted(() => {
    apply();
    if (typeof ResizeObserver === 'function') {
      observer = new ResizeObserver(apply);
      observer.observe(document.documentElement);
    } else {
      // 理论上到不了这里(WebView2 是 Chromium), 留一条退路免得整块缩放失效
      window.addEventListener('resize', onWindowResize);
    }
  });

  onBeforeUnmount(() => {
    observer?.disconnect();
    observer = null;
    window.removeEventListener('resize', onWindowResize);
  });

  // scale 供 App.vue 里的内联 SVG 用(它们的 width/height 是属性, CSS 变量够不着)
  return { scale };
}
