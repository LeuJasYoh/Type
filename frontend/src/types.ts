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
  /** 目标窗口预览: 倒计时期间为最近一个非 Type 的前台窗口, 执行后为锁定的实际注入目标 */
  targetWindow: string;
}
