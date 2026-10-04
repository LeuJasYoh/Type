// ═══ 状态栏的取值规则 ═════════════════════════════════
// 三条规则各自是纯函数, 只为了能被 test/statusBarState.test.ts 钉住(坏了只表现为"界面
// 不对", vue-tsc 与其余用例全绿)。进度条的 -1 哨兵语义见 docs/invariants.md「其它不变量」。

import type { TypingPhase } from '../types';

/**
 * 状态栏的类名: idle 不加相位类(纸面), 其余相位各自一套配色(见 style.css 的
 * .status-bar.<phase>)。空串是刻意保留的 —— 它渲染成 class="status-bar ",
 * 与改动前逐字一致
 */
export function statusBarClass(phase: TypingPhase): string[] {
  return ['status-bar', phase !== 'idle' ? phase : ''];
}

export function progressVisible(progress: number): boolean {
  return progress >= 0;
}

export function progressWidth(progress: number): string {
  return progress >= 0 ? Math.min(progress, 100) + '%' : '0%';
}
