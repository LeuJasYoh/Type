// ═══ 状态栏的取值规则 ═════════════════════════════════
// 三条规则各自是一个纯函数, 抽出来的唯一理由是可被回归用例钉住: 进度条的显隐吃的是
// 后端的 -1 哨兵(空闲态凭空长出一条空进度轨道的那次事故见 docs/invariants.md), 而这类规则
// 坏了只表现为"界面不对", vue-tsc 与其余用例全绿, 没有任何环节会失败。
// 与 viewportReport.ts 同一个理由: 单独成模块, 只为了能被 test/ 够着。

import type { TypingPhase } from '../types';

/**
 * 状态栏的类名: idle 不加相位类(纸面), 其余相位各自一套配色(见 style.css 的
 * .status-bar.<phase>)。空串是刻意保留的 —— 它渲染成 class="status-bar ",
 * 与改动前逐字一致
 */
export function statusBarClass(phase: TypingPhase): string[] {
  return ['status-bar', phase !== 'idle' ? phase : ''];
}

/** 进度条是否显示。progress 的语义是 0~100 表示进度、-1 表示隐藏 */
export function progressVisible(progress: number): boolean {
  return progress >= 0;
}

/** 进度条填充宽度。上限 100%: 后端给的是整数百分比, 这里兜住越界值 */
export function progressWidth(progress: number): string {
  return progress >= 0 ? Math.min(progress, 100) + '%' : '0%';
}
