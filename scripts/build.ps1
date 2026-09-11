# build.ps1 — 一键构建 Type
# 版本号单一来源: cmd/type/main.go 中的 `version` 变量, 此处自动同步到
# assets/version.rc / assets/winres/winres.json / frontend/package.json;
# 前端 Vue + Vite 构建为 internal/web/dist/index.html 单文件后嵌入,
# 再 windres + go build。产物 Type.exe 输出在仓库根目录。
#
# 用法:  powershell -ExecutionPolicy Bypass -File .\scripts\build.ps1
# 依赖:  Go 1.26+、MinGW-w64 (windres)、Node.js 20+ (npm)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

# ── 1. 从 cmd/type/main.go 提取版本号 (单一来源) ──────
$m = Select-String -Path "cmd\type\main.go" -Pattern 'version\s*=\s*"([\d.]+)"'
if (-not $m) { throw "cmd/type/main.go 中未找到 version 变量" }
$ver = $m.Matches[0].Groups[1].Value

$parts = @($ver -split '\.')
while ($parts.Count -lt 4) { $parts += "0" }
$v4CSV = ($parts[0..3] -join ',')          # 1,2,0,0  (FILEVERSION)
$v4Dot = ($parts[0..3] -join '.')          # 1.2.0.0  (资源 FileVersion)
$v3Dot = ($parts[0..2] -join '.')          # 1.2.0    (资源 ProductVersion)

# ── 2. 同步 assets/version.rc ─────────────────────────
$rc = [System.IO.File]::ReadAllText("$root\assets\version.rc")
$rc = $rc -replace 'FILEVERSION\s+[\d,]+',    "FILEVERSION     $v4CSV"
$rc = $rc -replace 'PRODUCTVERSION\s+[\d,]+', "PRODUCTVERSION  $v4CSV"
$rc = $rc -replace '"FileVersion",\s*"[^"]+"',    ('"FileVersion",      "' + $v4Dot + '"')
$rc = $rc -replace '"ProductVersion",\s*"[^"]+"', ('"ProductVersion",   "' + $v3Dot + '"')
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText("$root\assets\version.rc", $rc, $utf8NoBom)

# ── 3. 同步 frontend/package.json ─────────────────────
$pj = [System.IO.File]::ReadAllText("$root\frontend\package.json")
$pj = $pj -replace '("version"\s*:\s*)"[^"]+"', ('$1"' + $ver + '"')
[System.IO.File]::WriteAllText("$root\frontend\package.json", $pj, $utf8NoBom)

# ── 4. 构建 Vue 前端 (类型检查 + Vite 单文件打包) ────
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

# ── 5. 编译资源与可执行文件 ───────────────────────────
Write-Host "构建 Type v$ver ..."
# windres 以工作目录解析 .rc 内的相对资源路径, 置于 assets/ 使 "icon.ico"
# 指向同目录图标; version.syso 输出到 main 包目录供 go build 自动链接
Push-Location "$root\assets"
try {
    windres -o "$root\cmd\type\version.syso" version.rc
    if ($LASTEXITCODE -ne 0) { throw "windres 失败 (exit $LASTEXITCODE)" }
} finally { Pop-Location }
go build -ldflags="-H windowsgui -s -w" -o Type.exe ./cmd/type
if ($LASTEXITCODE -ne 0) { throw "go build 失败 (exit $LASTEXITCODE)" }

$f = Get-Item "Type.exe"
$v = $f.VersionInfo
Write-Host "构建完成: Type.exe ($([math]::Round($f.Length / 1KB, 1)) KB)"
Write-Host "  FileVersion:    $($v.FileVersion)"
Write-Host "  ProductVersion: $($v.ProductVersion)"

# ── 6. 校验版本资源真的链进了 exe ─────────────────────
# version.syso 缺失时 go build 依然成功, 但产物没有版本信息与图标;
# 读回 exe 比对, 让这种静默失败在构建期就暴露
if ($v.FileVersion -ne $v4Dot) {
    throw "exe 版本资源异常: FileVersion = '$($v.FileVersion)', want '$v4Dot' (windres/version.syso 是否生效?)"
}
