// ═══ 主题切换 ═════════════════════════════════════════
// 只负责"切换"与"写入": 首帧用哪套主题由 index.html 的内联脚本在绘制前定好(必须早于
// Vue, 挪进来就是首帧闪白), 初始值直接读它落在 <html> 上的 .dark —— 同一判定不许有两处。
// 存储键 type-theme 写在两处(index.html 读、这里写), 走散的症状是"切换过主题、重启又
// 变回系统主题"且没有环节会失败; 页面以 about:blank 加载、localStorage 会抛
// SecurityError(见 docs/invariants.md「其它不变量」), 故存取走安全包装、不可用时退回内存。

import { ref } from 'vue';

const THEME_KEY = 'type-theme';

function writeStoredTheme(value: 'light' | 'dark'): void {
  try {
    localStorage.setItem(THEME_KEY, value);
  } catch {
    // 忽略: 本次会话内仍生效, 只是不持久
  }
}

// 读内联脚本的判定结果, 而不是重算一遍
const theme = ref<'light' | 'dark'>(
  document.documentElement.classList.contains('dark') ? 'dark' : 'light',
);

let fadeTimer: number | null = null;

export function useTheme() {
  function toggle(): void {
    theme.value = theme.value === 'light' ? 'dark' : 'light';
    // 先挂过渡类再切 .dark, 全站颜色同步渐变; 300ms 后摘除
    const root = document.documentElement;
    root.classList.add('theme-fade');
    if (fadeTimer !== null) window.clearTimeout(fadeTimer);
    fadeTimer = window.setTimeout(() => {
      root.classList.remove('theme-fade');
      fadeTimer = null;
    }, 300);
    root.classList.toggle('dark', theme.value === 'dark');
    writeStoredTheme(theme.value);
  }
  return { theme, toggle };
}
