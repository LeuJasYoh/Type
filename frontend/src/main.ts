import { createApp } from 'vue';
import App from './App.vue';
import { startViewportReporting } from './viewportReport';
import './style.css';

createApp(App).mount('#app');

// 把真实的内容缩放报给宿主(立刻一拍 + 400ms 一拍)。为什么只能从这一侧报、
// 报回去宿主拿它做什么, 见 viewportReport.ts 的文件头。
startViewportReporting();
