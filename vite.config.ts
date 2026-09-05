import vue from '@vitejs/plugin-vue'
import { defineConfig } from 'vite'
import { viteSingleFile } from 'vite-plugin-singlefile'

// 前端源码位于 frontend/, 构建产物 frontend/dist/index.html 为自包含单文件,
// 由 go:embed 嵌入二进制后经 w.SetHtml 加载
export default defineConfig({
  root: 'frontend',
  plugins: [vue(), viteSingleFile()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    target: 'chrome120', // WebView2 (Edge Chromium)
  },
})
