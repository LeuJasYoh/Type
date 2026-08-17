# build.ps1 — 一键构建 KBType
# 版本号单一来源: main.go 中的 `version` 变量, 此处自动同步到
# version.rc / winres.json 资源文件; 前端 TypeScript 经 tsc 编译为
# frontend/script.js 后嵌入, 再 windres + go build。
#
# 用法:  powershell -ExecutionPolicy Bypass -File .\build.ps1
# 依赖:  Go 1.20+、MinGW-w64 (windres)、Node.js 16+ (npm)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $root

# ── 1. 从 main.go 提取版本号 (单一来源) ──────────────
$m = Select-String -Path "main.go" -Pattern 'version\s*=\s*"([\d.]+)"'
if (-not $m) { throw "main.go 中未找到 version 变量" }
$ver = $m.Matches[0].Groups[1].Value

$parts = @($ver -split '\.')
while ($parts.Count -lt 4) { $parts += "0" }
$v4CSV = ($parts[0..3] -join ',')          # 1,2,0,0  (FILEVERSION)
$v4Dot = ($parts[0..3] -join '.')          # 1.2.0.0  (资源 FileVersion)
$v3Dot = ($parts[0..2] -join '.')          # 1.2.0    (资源 ProductVersion)
$v2Dot = ($parts[0..1] -join '.')          # 1.2      (winres.json info)

# ── 2. 同步 version.rc ────────────────────────────────
$rc = [System.IO.File]::ReadAllText("$root\version.rc")
$rc = $rc -replace 'FILEVERSION\s+[\d,]+',    "FILEVERSION     $v4CSV"
$rc = $rc -replace 'PRODUCTVERSION\s+[\d,]+', "PRODUCTVERSION  $v4CSV"
$rc = $rc -replace '"FileVersion",\s*"[^"]+"',    ('"FileVersion",      "' + $v4Dot + '"')
$rc = $rc -replace '"ProductVersion",\s*"[^"]+"', ('"ProductVersion",   "' + $v3Dot + '"')
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
[System.IO.File]::WriteAllText("$root\version.rc", $rc, $utf8NoBom)

# ── 3. 同步 winres/winres.json ────────────────────────
$wj = [System.IO.File]::ReadAllText("$root\winres\winres.json")
$wj = $wj -replace '"file_version":\s*"[^"]+"',    ('"file_version": "'    + $v4Dot + '"')
$wj = $wj -replace '"product_version":\s*"[^"]+"', ('"product_version": "' + $v4Dot + '"')
$wj = $wj -replace '"FileVersion":\s*"[^"]+"',     ('"FileVersion": "'     + $v2Dot + '"')
$wj = $wj -replace '"ProductVersion":\s*"[^"]+"',  ('"ProductVersion": "'  + $v2Dot + '"')
# manifest identity.version (嵌套在 identity 对象内, 需跨行匹配)
$wj = $wj -replace '("identity"\s*:\s*\{[^\}]*?"version"\s*:\s*)"[^"]+"', ('$1"' + $v4Dot + '"')
[System.IO.File]::WriteAllText("$root\winres\winres.json", $wj, $utf8NoBom)

# ── 4. 编译 TypeScript 前端 ──────────────────────────
Write-Host "编译 TypeScript 前端 ..."
if (-not (Test-Path "node_modules\typescript\package.json")) {
    Write-Host "  首次运行: 安装 typescript ..."
    npm install --no-audit --no-fund
    if ($LASTEXITCODE -ne 0) { throw "npm install 失败 (exit $LASTEXITCODE)" }
}
npx tsc -p frontend/tsconfig.json
if ($LASTEXITCODE -ne 0) { throw "tsc 失败 (exit $LASTEXITCODE)" }

# ── 5. 编译资源与可执行文件 ───────────────────────────
Write-Host "构建 KBType v$ver ..."
windres -o version.syso version.rc
if ($LASTEXITCODE -ne 0) { throw "windres 失败 (exit $LASTEXITCODE)" }
go build -ldflags="-H windowsgui -s -w" -o KBType.exe .
if ($LASTEXITCODE -ne 0) { throw "go build 失败 (exit $LASTEXITCODE)" }

$f = Get-Item "KBType.exe"
$v = $f.VersionInfo
Write-Host "构建完成: KBType.exe ($([math]::Round($f.Length / 1KB, 1)) KB)"
Write-Host "  FileVersion:    $($v.FileVersion)"
Write-Host "  ProductVersion: $($v.ProductVersion)"
