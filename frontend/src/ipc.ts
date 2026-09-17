// ═══ IPC Bridge ═══════════════════════════════════════
// Go 端通过 webview.Bind 注入 window 的全局函数(均返回 Promise)。

import type { TypingStatus } from './types';

declare global {
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
}

/** 从 webview reject 中提取可读错误消息 */
export function errMsg(err: unknown): string {
  if (err instanceof Error && err.message) return err.message;
  if (typeof err === 'string' && err) return err;
  return '操作失败';
}

export const startTyping = (text: string, delay: number, forceRaw: boolean) =>
  window.startTyping(text, delay, forceRaw);

export const cancelTyping = () => window.cancelTyping();

export const toggleTopmost = () => window.toggleTopmost();

export const getTypingStatus = () => window.getTypingStatus();
