// ═══ 输入任务状态机 ═══════════════════════════════════
// 封装 startTyping/cancelTyping 的生命周期与 getTypingStatus 轮询。

import { ref } from 'vue';
import { cancelTyping, errMsg, getTypingStatus, startTyping } from '../ipc';
import type { TypingPhase, TypingStatus } from '../types';

function isTerminal(phase: TypingPhase): boolean {
  // idle 不是终止态——首次启动前就是 idle，若算终止则轮询立即停止
  return phase === 'success' || phase === 'error' || phase === 'cancel';
}

const statusOf = (s: Partial<TypingStatus> & Pick<TypingStatus, 'phase' | 'message' | 'progress'>): TypingStatus => ({
  secondsLeft: 0,
  targetWindow: '',
  ...s,
});

export function useTypingTask() {
  const status = ref<TypingStatus>(statusOf({ phase: 'idle', message: '', progress: -1 }));
  const isRunning = ref(false);

  let pollTimer: number | null = null;
  let warmedUp = false; // 首次 tick 只渲染不终止，避免读到上一轮残留的终止态

  function stopPolling(): void {
    if (pollTimer !== null) {
      window.clearInterval(pollTimer);
      pollTimer = null;
    }
  }

  function startPolling(): void {
    stopPolling();
    warmedUp = false;
    const tick = async (): Promise<void> => {
      try {
        const s = await getTypingStatus();
        status.value = s;
        if (warmedUp && isTerminal(s.phase)) {
          stopPolling();
          isRunning.value = false;
          return; // 终止态: 不再排下一次
        }
        warmedUp = true;
      } catch {
        // 页面关闭时可能会 reject，忽略
      }
      // 一次跑完再排下一次, 而不是 setInterval: IPC 偶尔变慢时不会有两个
      // 请求同时在飞, 也就不会出现旧响应盖掉新状态(进度/倒计时回跳)
      if (pollTimer !== null) {
        pollTimer = window.setTimeout(tick, 100);
      }
    };
    // 第一拍必须经由定时器启动, 不能直接调 tick: tick 末尾以
    // "pollTimer !== null" 作为"链条仍在继续"的判据, 而 pollTimer 只在这
    // 个判据保护的分支里被赋值 —— 直接调用会让它保持 null, 第一拍之后链条
    // 必断, 每次任务只刷新一次状态(预览卡首帧/完成后读不到终止态, v1.4.0
    // 轮询改造引入的根因)
    pollTimer = window.setTimeout(() => void tick(), 0);
  }

  async function start(text: string, delay: number, forceRaw: boolean): Promise<void> {
    if (isRunning.value) return;

    if (!text.trim()) {
      status.value = statusOf({ phase: 'error', message: '请输入要模拟键入的文本', progress: -1 });
      return;
    }

    isRunning.value = true;
    // 即时渲染倒计时状态（不等轮询）
    status.value = statusOf({
      phase: 'countdown',
      message: `剩余 ${delay} 秒 — 请聚焦目标窗口...`,
      progress: -1,
      secondsLeft: delay,
    });

    try {
      // Go 端在 handler 内同步写入 countdown 状态后才返回,
      // 因此先 await 再开轮询, 保证首个 tick 读到的必是新状态
      await startTyping(text, delay, forceRaw);
      if (!isRunning.value) return; // 等待期间用户已取消, 不恢复轮询
      startPolling();
    } catch (err: unknown) {
      stopPolling();
      isRunning.value = false;
      status.value = statusOf({ phase: 'error', message: errMsg(err), progress: -1 });
    }
  }

  function cancel(): void {
    void cancelTyping();
    stopPolling();
    isRunning.value = false;
    status.value = statusOf({ phase: 'cancel', message: '已取消', progress: -1 });
  }

  // WebView2 在窗口退到后台时会挂起页面定时器: 恢复可见/焦点时, 只要任务
  // 仍在跑就无条件重启轮询链(链条可能处于任意状态: 待触发被丢失、挂起中),
  // 保证回到窗口立即可读到最新状态
  function healPolling(): void {
    if (isRunning.value) startPolling();
  }
  document.addEventListener('visibilitychange', healPolling);
  window.addEventListener('focus', healPolling);

  return { status, isRunning, start, cancel };
}
