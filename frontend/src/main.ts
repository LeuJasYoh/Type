import { createApp } from 'vue';
import App from './App.vue';
import { startViewportReporting } from './viewportReport';
import './style.css';

createApp(App).mount('#app');

// 把真实内容缩放报给宿主: 节拍与"为什么只能从这一侧报"见 docs/invariants.md「其它不变量」。
startViewportReporting();
