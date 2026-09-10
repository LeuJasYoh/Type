// ─── 嵌入前端资源 ─────────────────────────────────────
// Vite 构建的自包含单文件 (vue-tsc + vite build 生成于 internal/web/dist/),
// 供 cmd/type 经 w.SetHtml 加载; 独立成包是因为 go:embed 无法引用上级目录

package web

import _ "embed"

//go:embed dist/index.html
var IndexHTML string
