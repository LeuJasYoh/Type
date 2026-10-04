// ═══ 状态栏取值规则的回归 ═════════════════════════════
// 这组规则是"坏了也只看得出界面不对"的典型: 进度条该藏没藏(空闲态凭空长出一条
// 空轨道, 真机上出过事故)、该显示没显示、相位配色不跟着换。此前它们写在组件里,
// 而 Node 跑不到 .vue, 于是零覆盖; 现在抽成纯函数(行为逐字未变), 这里钉边界。
//
// ⑤ 是接线用例: 抽成模块却没接线等于白抽, 所以连调用点一起钉(docs/invariants.md:
// 只断言"某个表达式在文件里出现过"拦不住调用点被删掉)。

import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { test } from 'node:test';

import { progressVisible, progressWidth, statusBarClass } from '../src/components/statusBarState.ts';
import type { TypingPhase } from '../src/types.ts';

const NON_IDLE_PHASES: TypingPhase[] = ['countdown', 'typing', 'success', 'error', 'cancel'];

test('① -1 哨兵: 隐藏进度条(空闲态的取值)', () => {
  assert.equal(progressVisible(-1), false);
  assert.equal(progressWidth(-1), '0%');
});

test('② 0 与 100 都是可见进度', () => {
  assert.equal(progressVisible(0), true);
  assert.equal(progressWidth(0), '0%');
  assert.equal(progressVisible(100), true);
  assert.equal(progressWidth(100), '100%');
});

test('③ 越界的进度值被夹在 100%', () => {
  assert.equal(progressWidth(150), '100%');
});

test('④ 相位类名: idle 不加类(空串与改动前逐字一致), 其余各自一套', () => {
  assert.deepEqual(statusBarClass('idle'), ['status-bar', '']);
  for (const phase of NON_IDLE_PHASES) {
    assert.deepEqual(statusBarClass(phase), ['status-bar', phase]);
  }
});

// ⑤ 是"子串钉", 不是"行为钉": 它只保证这三个调用点仍在 StatusBar.vue 里
// (删掉、或把组件改回内联计算会红), 拦不住"调用了但结果被丢弃", 对纯格式改动
// (重命名 props、加类型断言、括号里多一个空格)还会假红 —— 独立验证实测过这三种。
// 真正的运行时保护来自别处: 入库产物与源码逐字节同步(CI 的漂移检查) + 宿主侧契约
// 测试(见 test/ 里对 --ui-scale 基准的一致性核对)。留着它, 是为了拦住"抽了模块
// 却没接线"这一类最粗的错。
test('⑤ 接线: StatusBar.vue 必须真的调用这三个函数', () => {
  const src = readFileSync(new URL('../src/components/StatusBar.vue', import.meta.url), 'utf8');
  const calls = [
    'statusBarClass(props.phase)',
    'progressVisible(props.progress)',
    'progressWidth(props.progress)',
  ];
  for (const call of calls) {
    assert.ok(src.includes(call), `StatusBar.vue 里没有调用 ${call}`);
  }
});
