// ═══ IPC Bridge ═══════════════════════════════════════
// Go backend binds these via webview.Bind — all return Promises.

const go = {
  startTyping: (text: string, delay: number, forceRaw: boolean) =>
    window.startTyping(text, delay, forceRaw),
  cancelTyping: () => window.cancelTyping(),
  toggleTopmost: () => window.toggleTopmost(),
  getTypingStatus: () => window.getTypingStatus(),
};

// ═══ DOM refs ══════════════════════════════════════════

const $ = <T extends HTMLElement>(id: string): T => {
  const found = document.getElementById(id);
  if (!found) throw new Error(`缺少元素 #${id}`);
  return found as T;
};

const el = {
  text: $<HTMLTextAreaElement>('textInput'),
  charCount: $('charCount'),
  btnStart: $<HTMLButtonElement>('btnStart'),
  btnCancel: $<HTMLButtonElement>('btnCancel'),
  bar: $('statusBar'),
  status: $('statusText'),
  progWrap: $('progressWrap'),
  progFill: $('progressFill'),
  btnPin: $<HTMLButtonElement>('btnPin'),
  pinLabel: $('pinLabel'),
  slider: $<HTMLInputElement>('delaySlider'),
  delayVal: $('delayValue'),
  targetRow: $('targetRow'),
  targetName: $('targetName'),
  forceRaw: $<HTMLInputElement>('chkForceRaw'),
};

// ═══ State ══════════════════════════════════════════════

let isRunning = false;
let pollTimer: number | null = null;

function isTerminal(phase: TypingPhase): boolean {
  // idle 不是终止态——首次启动前就是 idle，若算终止则轮询立即停止
  return phase === 'success' || phase === 'error' || phase === 'cancel';
}

// ═══ Polling ════════════════════════════════════════════

function startPolling(): void {
  stopPolling();
  let warmedUp = false; // 首次 tick 只渲染不终止，避免读到上一轮残留的 PhaseSuccess
  const tick = async (): Promise<void> => {
    try {
      const s = await go.getTypingStatus();
      render(s);
      if (warmedUp && isTerminal(s.phase)) {
        stopPolling();
        isRunning = false;
        setButtons(false);
      }
      warmedUp = true;
    } catch {
      // 页面关闭时可能会 reject，忽略
    }
  };
  void tick(); // 立即执行一次（仅渲染）
  pollTimer = window.setInterval(tick, 100);
}

function stopPolling(): void {
  if (pollTimer !== null) {
    window.clearInterval(pollTimer);
    pollTimer = null;
  }
}

// ═══ Render ═════════════════════════════════════════════

function render(s: TypingStatus): void {
  el.bar.className = 'status-bar';
  if (s.phase !== 'idle') el.bar.classList.add(s.phase);
  el.status.textContent = s.message;

  if (s.progress >= 0) {
    el.progWrap.classList.add('active');
    el.progFill.style.width = Math.min(s.progress, 100) + '%';
  } else {
    el.progWrap.classList.remove('active');
    el.progFill.style.width = '0%';
  }

  // 目标窗口预览
  if (s.targetWindow) {
    el.targetRow.classList.add('active');
    el.targetName.textContent = s.targetWindow;
  } else {
    el.targetRow.classList.remove('active');
    el.targetName.textContent = '—';
  }
}

function setButtons(starting: boolean): void {
  el.btnStart.disabled = starting;
  el.btnCancel.disabled = !starting;
}

// ═══ Actions ════════════════════════════════════════════

/** 从 webview reject 中提取可读错误消息 */
function errMsg(err: unknown): string {
  if (err instanceof Error && err.message) return err.message;
  if (typeof err === 'string' && err) return err;
  return '操作失败';
}

function onStart(): void {
  if (isRunning) return;

  const text = el.text.value;
  if (!text.trim()) {
    render({ phase: 'error', message: '请输入要模拟键入的文本', progress: -1 });
    return;
  }

  const delay = parseInt(el.slider.value, 10);
  const forceRaw = el.forceRaw.checked;

  isRunning = true;
  setButtons(true);
  // 即时渲染倒计时状态（不等首次轮询结果）
  render({
    phase: 'countdown',
    message: `剩余 ${delay} 秒 — 请聚焦目标窗口...`,
    progress: -1,
    secondsLeft: delay,
  });

  startPolling();

  // fire-and-forget：Go 端仅启动 goroutine 后立即返回 "started"，无需 await
  go.startTyping(text, delay, forceRaw).catch((err: unknown) => {
    stopPolling();
    isRunning = false;
    setButtons(false);
    render({ phase: 'error', message: errMsg(err), progress: -1 });
  });
}

function onCancel(): void {
  void go.cancelTyping();
  stopPolling();
  isRunning = false;
  setButtons(false);
  render({ phase: 'cancel', message: '已取消', progress: -1 });
}

async function onTogglePin(): Promise<void> {
  const pinned = await go.toggleTopmost();
  el.btnPin.classList.toggle('active', pinned);
  el.pinLabel.textContent = pinned ? '取消置顶' : '置顶';
  el.btnPin.title = pinned ? '取消窗口置顶' : '窗口置顶';
}

// ═══ Event binding ══════════════════════════════════════

el.slider.addEventListener('input', () => {
  el.delayVal.textContent = el.slider.value + ' 秒';
});

el.text.addEventListener('input', () => {
  // 按码点计数(与 Go 端 []rune 进度分母一致), emoji 不重复计 2
  el.charCount.textContent = String(Array.from(el.text.value).length);
});

el.btnStart.addEventListener('click', onStart);
el.btnCancel.addEventListener('click', onCancel);
el.btnPin.addEventListener('click', onTogglePin);

// ═══ Init ═══════════════════════════════════════════════

render({ phase: 'idle', message: '', progress: -1 });
