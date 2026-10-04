// ═══ 输入任务状态机 ═══════════════════════════════════
// 封装 startTyping/cancelTyping 的生命周期与 getTypingStatus 轮询。

import { getCurrentScope, onScopeDispose, ref } from 'vue';
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
  // 链条身份: 每次开始/停止都换一代, 旧代的响应即使晚回来也不作数; 为什么不能只靠
  // pollTimer 是否为空判断, 见 docs/invariants.md「目标窗口与轮询」
  let chain = 0;
  let warmedUp = false; // 首次 tick 只渲染不终止，避免读到上一轮残留的终止态

  function stopPolling(): void {
    chain += 1;
    if (pollTimer !== null) {
      window.clearTimeout(pollTimer);
      pollTimer = null;
    }
  }

  function startPolling(): void {
    stopPolling();
    const my = chain; // 本代标识: 只有它还对得上时才允许写状态与续链条
    warmedUp = false;
    const tick = async (): Promise<void> => {
      try {
        const s = await getTypingStatus();
        if (my !== chain) return; // 已被停止或重启接管, 不许盖状态
        status.value = s;
        if (warmedUp && isTerminal(s.phase)) {
          stopPolling();
          isRunning.value = false;
          return; // 终止态: 不再排下一次
        }
        warmedUp = true;
      } catch {
        // 页面关闭时可能会 reject，忽略
        if (my !== chain) return;
      }
      // 一次跑完再排下一次, 而不是 setInterval: IPC 偶尔变慢时不会有两个
      // 请求同时在飞, 也就不会出现旧响应盖掉新状态(进度/倒计时回跳)
      if (my === chain && pollTimer !== null) {
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

  async function start(text: string, delay: number, forceRaw: boolean, textDirect: boolean): Promise<void> {
    if (isRunning.value) return;

    // 只拦真正的空文本。空格与换行是合法的输入内容(打一次回车就是按一下回车),
    // 用 trim() 会把它们连同空文本一起拦掉, 而后端本来就能注入 —— 两层对同一段
    // 输入给出不同结论。后端也有一道同样的校验, 判据在那里, 这里只是省一次往返
    if (text.length === 0) {
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
      await startTyping(text, delay, forceRaw, textDirect);
      if (!isRunning.value) return; // 等待期间用户已取消, 不恢复轮询
      startPolling();
    } catch (err: unknown) {
      stopPolling();
      isRunning.value = false;
      status.value = statusOf({ phase: 'error', message: errMsg(err), progress: -1 });
    }
  }

  function cancel(): void {
    stopPolling();
    isRunning.value = false;
    status.value = statusOf({ phase: 'cancel', message: '已取消', progress: -1 });

    // 后端可能在我们写"已取消"之前就已经跑完了(任务刚结束、界面还没刷到那一拍)。
    // 那种时候"已取消"是句假话: 内容其实已经全部送达, 用户以为没打完、再点一次
    // 启动就把同一段文本打了两遍。所以点完取消再回看一眼真实状态, 后端保留的
    // "输入完成"或失败原因要如实显示出来(后端也做了同一件事: Cancel 不覆盖已成的结局)
    const my = chain; // 本代的令牌: 期间又有人启动/取消时不插嘴
    void (async () => {
      try {
        await cancelTyping();
        const s = await getTypingStatus();
        if (my !== chain) return;
        if (s.phase === 'success' || s.phase === 'error') status.value = s;
      } catch {
        // 绑定不可用(如用普通浏览器打开 dev server)时保持本地的"已取消"
      }
    })();
  }

  // WebView2 在窗口退到后台时会挂起页面定时器: 恢复可见/焦点时, 只要任务
  // 仍在跑就无条件重启轮询链(链条可能处于任意状态: 待触发被丢失、挂起中),
  // 保证回到窗口立即可读到最新状态
  function healPolling(): void {
    if (isRunning.value) startPolling();
  }
  document.addEventListener('visibilitychange', healPolling);
  window.addEventListener('focus', healPolling);

  // 摘监听与停表。现在只在 App 顶层调用一次、组件不卸载, 所以不做也暂时无害;
  // 但条件渲染、KeepAlive 或第二次调用都会让它变成"监听越挂越多、定时器停不掉"
  if (getCurrentScope()) {
    onScopeDispose(() => {
      document.removeEventListener('visibilitychange', healPolling);
      window.removeEventListener('focus', healPolling);
      stopPolling();
    });
  }

  // 挂载时先同步一次后端状态。页面重载有两条不受控的来路 —— 用户按
  // Ctrl+R/F5(浏览器加速键, 网页拦不住), 以及渲染进程崩溃后 WebView2 自动
  // 重载 —— 之后前端状态会归零, 而后端任务可能仍在跑: 不同步的话界面显示
  // 空闲、取消按钮是灰的, 直到再点启动才发现"已有任务在运行"。
  // 起始 phase 为 idle 时不启动轮询, 与首次启动前一致
  async function syncFromBackend(): Promise<void> {
    try {
      const s = await getTypingStatus();
      status.value = s;
      if (s.phase === 'countdown' || s.phase === 'typing') {
        isRunning.value = true;
        startPolling();
      }
    } catch {
      // 绑定不可用(如用普通浏览器打开 dev server)时保持初始 idle
    }
  }
  void syncFromBackend();

  return { status, isRunning, start, cancel };
}
