// ═══ 主题切换 ═════════════════════════════════════════
// 这里只负责"切换"与"写入"。首帧该用哪套主题由 index.html 的内联脚本在绘制前
// 定好(它必须早于 Vue 执行, 挪不进组件), 所以初始值直接读它已经落在 <html> 上的
// .dark, 不再自己判一遍 —— 两份判定走散的症状是"初始化错色/首帧闪白", 而这类
// 缺陷没有任何自动检查能覆盖, 与其加护栏不如让它不存在。
//
// 存储: exe 经 SetHtml 加载页面, 文档是 about:blank、源为 null(实测, 见 README
// 已知限制), localStorage 会抛 SecurityError —— 所有存取必须走安全包装,
// 存储不可用时退回内存(当次会话内仍可切换)。也就是说正式版里主题选择**不跨启动
// 保留**, 每次启动按系统深浅色显示。这是刻意接受的: 首帧不闪白优先。

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
