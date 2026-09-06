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
        }
        warmedUp = true;
      } catch {
        // 页面关闭时可能会 reject，忽略
      }
    };
    void tick(); // 立即执行一次（仅渲染）
    pollTimer = window.setInterval(tick, 100);
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

  return { status, isRunning, start, cancel };
}
