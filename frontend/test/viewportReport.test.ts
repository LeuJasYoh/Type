// ═══ 内容缩放上报的回归 ═══════════════════════════════
// 这条通道是"窗口按内容缩放校正"唯一的输入: 页面不报了, 界面只会"看起来挤",
// 没有任何环节会失败。所以这里用可注入的 read/send/schedule 把节拍钉死 ——
// 不依赖 window、不依赖真实定时器、不依赖 WebView2。

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { REPORT_DELAY_MS, reportContentScale, startViewportReporting } from '../src/viewportReport.ts';

test('① 报的是读到的 devicePixelRatio, 原样交给绑定', () => {
  const sent: number[] = [];
  reportContentScale(
    () => 1.75,
    async (dpr) => {
      sent.push(dpr);
    },
  );
  assert.deepEqual(sent, [1.75]);
});

test('② 启动后立刻一拍, 400ms 后再一拍', () => {
  let reports = 0;
  const scheduled: Array<{ ms: number; fn: () => void }> = [];
  startViewportReporting(
    () => {
      reports += 1;
    },
    (fn, ms) => {
      scheduled.push({ ms, fn });
    },
  );
  assert.equal(reports, 1, '挂载后必须立刻报一次');
  assert.equal(scheduled.length, 1, '必须再排一拍');
  assert.equal(scheduled[0].ms, REPORT_DELAY_MS);
  scheduled[0].fn();
  assert.equal(reports, 2, '第二拍必须真的再报一次');
});

test('③ 读值抛错、绑定抛错、绑定 reject 都不许冒泡(普通浏览器里没有这个绑定)', () => {
  assert.doesNotThrow(() =>
    reportContentScale(
      () => 1.25,
      () => {
        throw new TypeError('window.reportViewport is not a function');
      },
    ),
  );
  assert.doesNotThrow(() =>
    reportContentScale(
      () => {
        throw new Error('devicePixelRatio 读不到');
      },
      async () => {},
    ),
  );
  assert.doesNotThrow(() => reportContentScale(() => 1.25, () => Promise.reject(new Error('rejected'))));
});

test('④ 生产默认值真的发出两拍(不传任何注入, 只打桩 window 与 setTimeout)', () => {
  // 前三条都是注入版, 盖不住"默认参数被换成空函数"——那时生产里两拍全是空调用,
  // 校准彻底失效, 而所有闸门都绿(独立复核实测)。这条专门走默认路径
  const sent: number[] = [];
  const timers: Array<() => void> = [];
  const g = globalThis as unknown as {
    window?: unknown;
    setTimeout?: unknown;
  };
  const savedWindow = g.window;
  const savedSetTimeout = g.setTimeout;
  g.window = {
    devicePixelRatio: 1.75,
    reportViewport: async (dpr: number) => {
      sent.push(dpr);
    },
  };
  g.setTimeout = ((fn: () => void) => {
    timers.push(fn);
    return 0;
  }) as unknown as typeof setTimeout;
  try {
    startViewportReporting();
    assert.deepEqual(sent, [1.75], '默认路径必须立刻报一次 devicePixelRatio');
    assert.equal(timers.length, 1, '默认路径必须排出第二拍');
    timers[0]();
    assert.deepEqual(sent, [1.75, 1.75], '第二拍必须再报一次');
  } finally {
    g.window = savedWindow;
    g.setTimeout = savedSetTimeout;
  }
  // 400ms 是文档与注释里写死的值(创建初期读数未稳定的机器的唯一改口机会)
  assert.equal(REPORT_DELAY_MS, 400);
});
