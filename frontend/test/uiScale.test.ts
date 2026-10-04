// ═══ 观感缩放的回归 ═══════════════════════════════════
// --ui-scale 的取值域只有三个边界值得钉: 基准档(540 及以下恒为 1, 默认档与改动前
// 逐像素一致)、宿主窗口上限档(648 → 1.2)、以及防御上限(再宽也不超过 1.25)。
// 这里只测纯函数, 不碰 document 与 ResizeObserver —— 挂载与持续校准是接线,
// 由 useUiScale 自己的 onMounted 负责, 没有 DOM 就不再重复测一遍。
//
// 为什么值得有: 这条规则坏了只表现为"界面观感不对", vue-tsc 与其余用例全绿,
// 没有任何环节会失败(注释里还曾写死过 480~720 的旧窗口范围, 已经漂过一次)。

import assert from 'node:assert/strict';
import { test } from 'node:test';

import { BASE_WIDTH, MAX_SCALE, uiScaleFor } from '../src/composables/useUiScale.ts';

test('① 基准档不缩: 内宽 540 时缩放为 1', () => {
  assert.equal(BASE_WIDTH, 540);
  assert.equal(uiScaleFor(540), 1);
  assert.equal(uiScaleFor(BASE_WIDTH), 1);
});

test('② 比基准还窄也不缩小(下限夹在 1)', () => {
  // 540 以下的视口只在"工作区比窗口高度下限还矮"时才会出现
  assert.equal(uiScaleFor(480), 1);
  assert.equal(uiScaleFor(0), 1);
});

test('③ 宿主窗口上限档 648 时是 1.2', () => {
  assert.equal(uiScaleFor(648), 1.2);
});

test('④ 1.25 是防御上限: 到 675 才够得着, 再宽也不超过它', () => {
  assert.equal(MAX_SCALE, 1.25);
  assert.equal(uiScaleFor(675), 1.25); // 675/540 恰好 1.25
  assert.equal(uiScaleFor(676), 1.25);
  assert.equal(uiScaleFor(10000), 1.25);
});

test('⑤ 单调不减: 窗口变宽, 缩放不会变小', () => {
  let prev = uiScaleFor(0);
  for (let w = 0; w <= 1200; w += 7) {
    const cur = uiScaleFor(w);
    assert.ok(cur >= prev, `内宽 ${w} 时缩放 ${cur} 小于前一档 ${prev}`);
    prev = cur;
  }
});
