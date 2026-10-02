// ═══ 观感缩放 ═════════════════════════════════════════
// 窗口尺寸由宿主在启动时按显示器算一次(内宽 480~720), 之后不再变 —— 布局本身
// 靠 flex 自适应, 但固定细节(内边距、圆角、控件高度)在大窗口里会显单薄。这里把
// "窗口内宽 / 540" 折成 --ui-scale 写到根元素上, 交给 CSS 缩放细节。
//
// 三条边界:
// ① 只由宽度驱动, 不读 devicePixelRatio: 窗口宽是宿主按物理像素算出来的, 再乘
//    一个缩放系数会让同一台显示器出现两种结果, 也没法解释给用户;
// ② 夹在 1~1.25: 540 及以下不缩(默认档与改动前逐像素一致), 720 封顶(窗口上限);
// ③ 挂载时读到的宽度未必是最终值: WebView2 首次布局之后才可能稳定下来, 所以
//    挂载时先写一次(避免首帧按 1 渲染再跳), 之后由 ResizeObserver 持续校准,
//    只读一次是不够的。
//
// 缩放值只影响观感细节, 不参与任何布局判据: 字号、边框宽度、过渡时长永不参与
// (理由写在 style.css 的 --ui-scale 处)。

import { onBeforeUnmount, onMounted, ref } from 'vue';

/** 基准宽度: 内宽 540 逻辑像素时缩放为 1 */
const BASE_WIDTH = 540;
/** 上限: 720 宽恰好 1.25, 更宽也不再加(窗口本身有 720 上限) */
const MAX_SCALE = 1.25;

function readScale(): number {
  // 用 clientWidth 而不是 innerWidth: 它才是 CSS 百分比实际解析所依据的宽度
  // (根元素出滚动条时会比 innerWidth 小, 虽然本页面 body 是 overflow:hidden)
  const width = document.documentElement.clientWidth || window.innerWidth;
  return Math.min(MAX_SCALE, Math.max(1, width / BASE_WIDTH));
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
