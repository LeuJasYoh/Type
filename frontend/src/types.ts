// ─── 与 Go 端共享的状态契约 ───────────────────────────
// main.go 中 TypingPhase / TypingStatus 的镜像, 修改须双端同步。

/** 输入流程阶段 */
export type TypingPhase =
  | 'idle'
  | 'countdown'
  | 'typing'
  | 'success'
  | 'error'
  | 'cancel';

/** 输入状态 (Go 端序列化无 omitempty, 字段恒存在) */
export interface TypingStatus {
  phase: TypingPhase;
  message: string;
  /** 0-100, -1 表示隐藏 */
  progress: number;
  /** 倒计时剩余秒数 */
  secondsLeft: number;
  /** 当前前台窗口标题(目标窗口预览) */
  targetWindow: string;
}
