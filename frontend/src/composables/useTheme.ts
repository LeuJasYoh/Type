// ═══ 主题切换 ═════════════════════════════════════════
// 驱动 <html> 上的 .dark class（index.html 里有防闪烁的预置脚本）。
// 注意: exe 经 SetHtml 以 data: URL 加载, 属不透明源,
// localStorage 访问会抛 SecurityError —— 所有存取必须走安全包装,
// 存储不可用时退回内存(当次会话内仍可切换), 初始值跟随系统深浅色。

import { ref } from 'vue';

const THEME_KEY = 'type-theme';

function readStoredTheme(): 'light' | 'dark' | null {
  try {
    return localStorage.getItem(THEME_KEY) === 'dark' ? 'dark'
      : localStorage.getItem(THEME_KEY) === 'light' ? 'light'
      : null;
  } catch {
    return null; // 不透明源(data: URL)或隐私模式: 无持久存储
  }
}

function writeStoredTheme(value: 'light' | 'dark'): void {
  try {
    localStorage.setItem(THEME_KEY, value);
  } catch {
    // 忽略: 本次会话内仍生效, 只是不持久
  }
}

function systemTheme(): 'light' | 'dark' {
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

const theme = ref<'light' | 'dark'>(readStoredTheme() ?? systemTheme());

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
