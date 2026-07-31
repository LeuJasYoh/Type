// ─── webview_go 全局绑定声明 ──────────────────────────
// Go 端通过 w.Bind 注入到 window 的全局函数(均返回 Promise)。

/** 输入流程阶段, 与 Go 端 TypingPhase 一致 */
type TypingPhase =
  | 'idle'
  | 'countdown'
  | 'typing'
  | 'success'
  | 'error'
  | 'cancel';

/** 输入状态, 与 Go 端 TypingStatus 结构对应 */
interface TypingStatus {
  phase: TypingPhase;
  message: string;
  /** 0-100, -1 表示隐藏 */
  progress: number;
  /** 倒计时剩余秒数 */
  secondsLeft?: number;
  /** 当前前台窗口标题(目标窗口预览) */
  targetWindow?: string;
}

interface Window {
  /** 启动输入: text 内容, delay 倒计时秒数, forceRaw 是否绕过剪贴板降级 */
  startTyping(text: string, delay: number, forceRaw: boolean): Promise<string>;
  /** 取消当前输入 */
  cancelTyping(): Promise<string>;
  /** 切换窗口置顶, 返回新状态 */
  toggleTopmost(): Promise<boolean>;
  /** 轮询读取当前输入状态 */
  getTypingStatus(): Promise<TypingStatus>;
}
