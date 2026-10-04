// ═══ 内容缩放上报 ═════════════════════════════════════
// 把界面这一侧读到的真实内容缩放(devicePixelRatio)报给宿主, 宿主据此让窗口适配内容;
// 等式、实测与"为什么只有界面这一侧知道", 见 docs/invariants.md「其它不变量」。
// 单独成一个模块, 只为这条通道的节拍可被 test/viewportReport.test.ts 钉住。

import { reportViewport } from './ipc';

/** 第二拍的延迟: 覆盖"WebView2 创建初期读数还没稳定"的情况(有机器上第一拍读到 1) */
export const REPORT_DELAY_MS = 400;

/**
 * 报一次真实内容缩放。read/send 可注入只为让 Node 里的单测够得着这条通道, 生产走默认值;
 * 读值或调用绑定抛错都吞掉(绑定不存在, 如用普通浏览器打开 dev server, 界面也不能挂)。
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
 * 启动上报: 立刻一拍 + 400ms 一拍(第一拍覆盖绝大多数机器, 第二拍给创建初期读错值的
 * 机器改口机会; 处置细节见 docs/invariants.md「其它不变量」)。
 */
export function startViewportReporting(
  report: () => void = reportContentScale,
  schedule: (fn: () => void, ms: number) => void = setTimeout,
): void {
  report();
  schedule(report, REPORT_DELAY_MS);
}
