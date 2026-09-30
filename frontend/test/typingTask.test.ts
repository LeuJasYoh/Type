// ═══ useTypingTask 的状态迁移回归 ═══════════════════════
// 这份测试只为"定时器 + IPC 轮询"那座状态机存在, 不引入测试框架: 用 Node
// 自带的测试跑器直接跑 TS(见 package.json 的 test 脚本与 test/ts-resolve.mjs)。
// window / document 与定时器都在这里打桩, 时间由 advance() 手动推进, 所以
// 断言不依赖真实时钟, 也不依赖 WebView2。
//
// 钉住的四条既有行为(改轮询前先读这些用例):
//   ① 启动后立刻显示倒计时, 不等第一次轮询
//   ② 终止态停轮询并复位运行标志
//   ③ 取消后不恢复轮询
//   ④ 挂载时同步后端状态(页面重载后接回仍在跑的任务)
// 另有两条防回归: 迟到的响应不得覆盖用户刚做出的操作; 重启轮询不得产生第二条链。

import assert from 'node:assert/strict';
import { beforeEach, test } from 'node:test';

import { effectScope } from 'vue';

import { useTypingTask } from '../src/composables/useTypingTask.ts';
import type { TypingPhase, TypingStatus } from '../src/types.ts';

type Handler = () => void;
type Deferred = { resolve: (s: TypingStatus) => void; reject: (e: unknown) => void };

function makeStatus(phase: TypingPhase, over: Partial<TypingStatus> = {}): TypingStatus {
  return { phase, message: `msg:${phase}`, progress: -1, secondsLeft: 0, targetWindow: '', ...over };
}

const COUNTDOWN = (): TypingStatus =>
  makeStatus('countdown', { message: '剩余 5 秒 — 请聚焦目标窗口...', secondsLeft: 5, targetWindow: '目标窗口 A' });

let now = 0;
let seq = 0;
let calls = 0;
let reply: TypingStatus | null = null; // 非空即立即应答; 为空则把请求挂起, 由用例手动放行
let timers: Map<number, { at: number; fn: Handler }>;
let inflight: Deferred[];
let docListeners: Map<string, Set<Handler>>;
let winListeners: Map<string, Set<Handler>>;

const settle = (): Promise<void> => new Promise((r) => setImmediate(r));

function bind(map: Map<string, Set<Handler>>, type: string, fn: Handler): void {
  if (!map.has(type)) map.set(type, new Set());
  map.get(type)!.add(fn);
}

function fire(map: Map<string, Set<Handler>>, type: string): void {
  for (const fn of [...(map.get(type) ?? [])]) fn();
}

function unbind(map: Map<string, Set<Handler>>, type: string, fn: Handler): void {
  map.get(type)?.delete(fn);
}

// advance 推进假时钟: 按到期顺序逐拍触发, 每拍之后让微任务跑完
async function advance(ms: number): Promise<void> {
  const target = now + ms;
  for (;;) {
    let pick: number | null = null;
    let at = Number.POSITIVE_INFINITY;
    for (const [id, t] of timers) {
      if (t.at <= target && t.at < at) {
        at = t.at;
        pick = id;
      }
    }
    if (pick === null) break;
    const t = timers.get(pick)!;
    timers.delete(pick);
    now = t.at;
    t.fn();
    await settle();
  }
  now = target;
  await settle();
}

beforeEach(() => {
  now = 0;
  seq = 0;
  calls = 0;
  reply = null;
  timers = new Map();
  inflight = [];
  docListeners = new Map();
  winListeners = new Map();

  const win = {
    setTimeout: (fn: Handler, ms = 0): number => {
      const id = ++seq;
      timers.set(id, { at: now + Math.max(0, ms), fn });
      return id;
    },
    clearTimeout: (id: number): void => {
      timers.delete(id);
    },
    clearInterval: (id: number): void => {
      timers.delete(id);
    },
    addEventListener: (type: string, fn: Handler): void => bind(winListeners, type, fn),
    removeEventListener: (type: string, fn: Handler): void => unbind(winListeners, type, fn),
    startTyping: (): Promise<string> => Promise.resolve('started'),
    cancelTyping: (): Promise<string> => Promise.resolve('cancelled'),
    getTypingStatus: (): Promise<TypingStatus> => {
      calls += 1;
      if (reply) return Promise.resolve(reply);
      return new Promise<TypingStatus>((resolve, reject) => {
        inflight.push({ resolve, reject });
      });
    },
  };
  const doc = {
    addEventListener: (type: string, fn: Handler): void => bind(docListeners, type, fn),
    removeEventListener: (type: string, fn: Handler): void => unbind(docListeners, type, fn),
  };
  Object.assign(globalThis, { window: win, document: doc });
});

test('① 启动后立刻显示倒计时, 不等第一次轮询', async () => {
  reply = makeStatus('idle');
  const t = useTypingTask();
  await settle();
  assert.equal(t.status.value.phase, 'idle');

  reply = COUNTDOWN();
  await t.start('你好', 5, false, false);

  assert.equal(t.status.value.phase, 'countdown');
  assert.equal(t.status.value.secondsLeft, 5);
  assert.equal(t.status.value.message, '剩余 5 秒 — 请聚焦目标窗口...');
  assert.equal(t.isRunning.value, true);

  await advance(10);
  assert.equal(t.status.value.targetWindow, '目标窗口 A', '第一拍没有读到后端状态');
});

test('② 终止态停轮询并复位运行标志', async () => {
  reply = makeStatus('idle');
  const t = useTypingTask();
  await settle();

  reply = makeStatus('success', { message: '输入完成' });
  await t.start('你好', 5, false, false);
  await advance(500);

  assert.equal(t.status.value.phase, 'success');
  assert.equal(t.isRunning.value, false);
  const seen = calls;
  await advance(1000);
  assert.equal(calls, seen, '终止态之后仍在轮询');
});

test('③ 取消后不恢复轮询, 状态停在已取消', async () => {
  reply = makeStatus('idle');
  const t = useTypingTask();
  await settle();

  reply = COUNTDOWN();
  await t.start('你好', 5, false, false);
  await advance(200);

  t.cancel();
  assert.equal(t.status.value.phase, 'cancel');
  assert.equal(t.status.value.message, '已取消');
  assert.equal(t.isRunning.value, false);

  const seen = calls;
  await advance(1000);
  assert.equal(calls, seen, '取消之后仍在轮询');
});

test('④ 挂载时同步后端: 后端仍在跑则置运行态并恢复轮询', async () => {
  reply = makeStatus('typing', { message: '正在逐字符输入 1 / 2 ...', progress: 50 });
  const t = useTypingTask();
  await settle();

  assert.equal(t.status.value.phase, 'typing');
  assert.equal(t.isRunning.value, true);

  await advance(300);
  assert.ok(calls >= 3, `轮询没有恢复: 只发了 ${calls} 次请求`);
});

test('⑤ 取消期间迟到的响应不得覆盖已取消状态', async () => {
  reply = makeStatus('idle');
  const t = useTypingTask();
  await settle();

  reply = null; // 请求挂起, 由用例决定什么时候放行
  await t.start('你好', 5, false, false);
  await advance(10);
  assert.equal(inflight.length, 1, '应当有一拍正在飞');

  t.cancel();
  inflight.shift()!.resolve(COUNTDOWN());
  await settle();

  assert.equal(t.status.value.phase, 'cancel', '迟到的倒计时盖掉了用户刚点的取消');
  assert.equal(t.status.value.message, '已取消');
  assert.equal(t.status.value.targetWindow, '');
});

test('⑥ 恢复焦点重启轮询不得产生第二条链', async () => {
  reply = makeStatus('idle');
  const t = useTypingTask();
  await settle();

  reply = COUNTDOWN();
  await t.start('你好', 5, false, false);
  await advance(1000); // 稳态: 每 100ms 一拍
  const steady = calls;
  assert.ok(steady >= 5, `稳态请求数偏少: ${steady}`);

  reply = null; // 让下一拍停在半路
  await advance(100);
  assert.equal(inflight.length, 1, '应当有一拍正在飞');

  fire(winListeners, 'focus'); // 恢复可见/焦点: 无条件重启链条
  reply = COUNTDOWN();
  inflight.shift()!.resolve(COUNTDOWN()); // 旧链的响应这时才回来
  await settle();

  const before = calls;
  await advance(1000);
  const perSecond = calls - before;
  assert.ok(perSecond <= 13, `每秒 ${perSecond} 次请求: 旧链没有作废, 两条链同时在跑`);
  assert.equal(t.status.value.phase, 'countdown');
});

test('⑦ 卸载时摘掉全局监听并停表', async () => {
  reply = makeStatus('idle');
  const scope = effectScope();
  let t: ReturnType<typeof useTypingTask> | undefined;
  scope.run(() => {
    t = useTypingTask();
  });
  await settle();

  reply = COUNTDOWN();
  await t!.start('你好', 5, false, false);
  await advance(200);
  assert.equal(winListeners.get('focus')?.size, 1, 'focus 监听没有挂上');
  assert.equal(docListeners.get('visibilitychange')?.size, 1, 'visibilitychange 监听没有挂上');

  scope.stop();
  assert.equal(winListeners.get('focus')?.size ?? 0, 0, 'focus 监听没摘掉');
  assert.equal(docListeners.get('visibilitychange')?.size ?? 0, 0, 'visibilitychange 监听没摘掉');

  const seen = calls;
  await advance(1000);
  assert.equal(calls, seen, '卸载之后定时器还在跑');
});
