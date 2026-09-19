# build.ps1 — 一键构建 Type
# 版本号单一来源: cmd/type/main.go 中的 `version` 变量, 此处自动同步到
# frontend/package.json, 并传给 tools/mkres 生成资源;
# 前端 Vue + Vite 构建为 internal/web/dist/index.html 单文件后嵌入,
# 再 go build。产物 Type.exe 输出在仓库根目录。
#
# 用法:  powershell -ExecutionPolicy Bypass -File .\scripts\build.ps1
# 依赖:  Go 1.26+、Node.js 20+ (npm) —— 不需要 C 工具链, webview 绑定是纯 Go 的

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

# ── 1. 从 cmd/type/main.go 提取版本号 (单一来源) ──────
# 版本可带预发布后缀(如 1.5.0-rc.1): 资源里的数字字段只允许数字, 取后缀前的
# 数字部分; 完整字符串进 ProductVersion 与 package.json(semver 兼容)。
# 数字形式在此独立算一遍, 供末尾读回 exe 比对(与被校验的工具不是同一份实现)
$m = Select-String -Path "cmd\type\main.go" -Pattern 'version\s*=\s*"([\d.]+(?:-[0-9A-Za-z.]+)?)"'
if (-not $m) { throw "cmd/type/main.go 中未找到 version 变量" }
$ver = $m.Matches[0].Groups[1].Value

$base = $ver -replace '-.*$', ''
$parts = @($base -split '\.')
while ($parts.Count -lt 4) { $parts += "0" }
$v4Dot = ($parts[0..3] -join '.')          # 1.2.0.0  (读回 exe 比对用)

# ── 2. 同步 frontend/package.json ─────────────────────
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
$pj = [System.IO.File]::ReadAllText("$root\frontend\package.json")
$pj = $pj -replace '("version"\s*:\s*)"[^"]+"', ('$1"' + $ver + '"')
[System.IO.File]::WriteAllText("$root\frontend\package.json", $pj, $utf8NoBom)

# ── 3. 构建 Vue 前端 (类型检查 + Vite 单文件打包) ────
Write-Host "构建 Vue 前端 ..."
Push-Location "$root\frontend"
try {
    if (-not (Test-Path "node_modules\vite\package.json")) {
        Write-Host "  首次运行: 安装依赖 ..."
        npm install --no-audit --no-fund
        if ($LASTEXITCODE -ne 0) { throw "npm install 失败 (exit $LASTEXITCODE)" }
    }
    npm run build
    if ($LASTEXITCODE -ne 0) { throw "前端构建失败 (exit $LASTEXITCODE)" }
} finally { Pop-Location }
if (-not (Test-Path "internal\web\dist\index.html")) { throw "未找到 internal\web\dist\index.html" }

# ── 4. 编译资源与可执行文件 ───────────────────────────
Write-Host "构建 Type v$ver ..."
# tools/mkres 生成 cmd/type/version_<arch>.syso (图标 + 版本信息), go build 按
# 目标架构自动链接; 纯 Go 实现, 取代 windres + version.rc, 不再需要 MinGW
go run ./tools/mkres -version $ver -icon assets/icon.ico -out cmd/type/version
if ($LASTEXITCODE -ne 0) { throw "资源生成失败 (exit $LASTEXITCODE)" }
go build -ldflags="-H windowsgui -s -w" -o Type.exe ./cmd/type
if ($LASTEXITCODE -ne 0) { throw "go build 失败 (exit $LASTEXITCODE)" }

$f = Get-Item "Type.exe"
$v = $f.VersionInfo
Write-Host "构建完成: Type.exe ($([math]::Round($f.Length / 1KB, 1)) KB)"
Write-Host "  FileVersion:    $($v.FileVersion)"
Write-Host "  ProductVersion: $($v.ProductVersion)"

# ── 5. 校验资源真的链进了 exe ─────────────────────────
# version_<arch>.syso 缺失时 go build 依然成功, 但产物没有版本信息与图标;
# 读回 exe 核对, 让这种静默失败在构建期就暴露。
# 上面那条用独立算出的数字比对(与被校验的工具不是同一份实现), 再用
# tools/pecheck 过一遍 CI 的同一套判据: 架构 + 版本 + DPI manifest + 图标
if ($v.FileVersion -ne $v4Dot) {
    throw "exe 版本资源异常: FileVersion = '$($v.FileVersion)', want '$v4Dot' (tools/mkres 是否生效?)"
}
go run ./tools/pecheck -exe Type.exe -version $ver -arch amd64
if ($LASTEXITCODE -ne 0) { throw "产物读回校验失败 (exit $LASTEXITCODE)" }
