import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'
import { viteSingleFile } from 'vite-plugin-singlefile'

// 前端项目自包含于 frontend/ (即本配置所在目录); 构建产物输出到
// ../internal/web/dist/index.html 自包含单文件, 由 internal/web 包
// go:embed 嵌入二进制后经 w.SetHtml 加载
export default defineConfig({
  root: '.',
  plugins: [vue(), viteSingleFile()],
  build: {
    outDir: '../internal/web/dist',
    emptyOutDir: true,
    target: 'chrome120', // WebView2 (Edge Chromium)
  },
})
