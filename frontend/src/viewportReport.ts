// ═══ 内容缩放上报 ═════════════════════════════════════
// 「客户区物理像素 → CSS 像素」的比值(devicePixelRatio)只有界面这一侧知道: 宿主按
// 显示器 DPI 猜的值在两者不等时会偏。实测某用户机上窗口按 125% 建成 720×600 物理像素、
// 内容却按 1.75 倍渲染, CSS 视口被压到 411×343, 输入框盖住了选项行 —— 宿主接住这个
// 比值, 才能反过来让窗口去适配内容(见 AGENTS.md「其它不变量」的内容缩放一条)。
//
// 单独成一个模块, 只为这件事可被回归用例钉住: 页面不报了, 界面只会"看起来挤",
// 没有任何环节会失败, 所以这条通道的节拍由 test/viewportReport.test.ts 守着。

import { reportViewport } from './ipc';

/** 第二拍的延迟: 覆盖"WebView2 创建初期读数还没稳定"的情况(有机器上第一拍读到 1) */
export const REPORT_DELAY_MS = 400;

/**
 * 报一次真实内容缩放。
 * read/send 可注入只是为了让 Node 里的单测够得着这条通道, 生产路径用默认值。
 * 读值或调用绑定抛错都吞掉: 绑定不存在(用普通浏览器打开 dev server)时界面不能因此挂掉。
 */
export function reportContentScale(
  read: () => number = () => window.devicePixelRatio,
  send: (dpr: number) => Promise<void> = reportViewport,
): void {
  try {
    void send(read()).catch(() => {});
  } catch {
    // 这一拍失败就算了: 下一拍还会再试, 上报不是界面能不能显示的前提
  }
}

/**
 * 启动上报: 立刻一拍 + 400ms 一拍。
 * 两拍是刻意的: 第一拍让绝大多数机器一次到位; 第二拍给"创建初期读到错值、随后自行
 * 修正"的机器一次改口机会(宿主对同一个比值只处理一次, 值变了才会再校正一次)。
 */
export function startViewportReporting(
  report: () => void = reportContentScale,
  schedule: (fn: () => void, ms: number) => void = setTimeout,
): void {
  report();
  schedule(report, REPORT_DELAY_MS);
}
