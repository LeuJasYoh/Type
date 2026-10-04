# build.ps1 — 一键构建 Type
# 版本号单一来源: cmd/type/main.go 中的 `version` 变量, 此处自动同步到
# frontend/package.json, 并传给 tools/mkres 生成资源;
# 前端 Vue + Vite 构建为 internal/web/dist/index.html 单文件后嵌入,
# 再 go build。产物 Type.exe 输出在仓库根目录。
#
# 用法:  powershell -ExecutionPolicy Bypass -File .\scripts\build.ps1
# 依赖:  Go 1.26+、Node.js 24 (npm, 与 CI 同主版本) —— 不需要 C 工具链, webview 绑定是纯 Go 的

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

# ── 1. 从 cmd/type/main.go 提取版本号 (单一来源) ──────
# 版本可带预发布后缀(如 1.5.0-rc.1): 资源里的数字字段只允许数字, 取后缀前的
# 数字部分; 完整字符串进 ProductVersion 与 package.json(semver 兼容)。
# 数字形式在此独立算一遍, 供末尾读回 exe 比对(与被校验的工具不是同一份实现)
# 模式必须锚定行首的 `var version =` 且大小写敏感: version 是个常见词, 无锚点的
# 子串匹配会命中注释里的 `// version = "1.5.8"`, 或将来某个 `minXxxVersion = "…"`,
# 取到的就是文件里第一个碰巧长得像的值。下游(标签闸门/资源/包名/package.json)
# 全用这个提取值, 而没有任何环节比对"提取值 == 程序真正用的 version" —— 取错值
# 会一路错到底。同款模式共四处: 本文件 / verify.yml / ci.yml / release.yml
$m = @(Select-String -Path "cmd\type\main.go" -CaseSensitive `
    -Pattern '(?m)^\s*var\s+version\s*=\s*"([\d.]+(?:-[0-9A-Za-z.]+)?)"')
if ($m.Count -ne 1) { throw "cmd\type\main.go 里应恰好有一处 var version = ""x.y.z"" (实际 $($m.Count) 处)" }
$ver = $m[0].Matches[0].Groups[1].Value

$base = $ver -replace '-.*$', ''
$parts = @($base -split '\.')
while ($parts.Count -lt 4) { $parts += "0" }
$v4Dot = ($parts[0..3] -join '.')          # 1.2.0.0  (读回 exe 比对用)

# ── 2. 校验构建环境: Node 主版本必须与 .node-version 一致 ──
# 放在改文件之前: 不符时当场停下, 不留"package.json/lock 已改、构建没发生"的半成品。
# Node 主版本决定 vite/rollup 的产出字节, CI 的产物漂移检查正依赖这一点, 而
# .node-version 是唯一权威
$wantNode = (Get-Content "$root\.node-version" -Raw).Trim()
if (-not (Get-Command node -ErrorAction SilentlyContinue)) {
    throw "找不到 node: 前端构建需要 Node.js 主版本 $wantNode (见 .node-version)"
}
$haveNode = (& node --version).TrimStart('v')
if (($haveNode -split '\.')[0] -ne $wantNode) {
    throw "本机 Node 主版本 = $haveNode, 而 .node-version 要求 $wantNode (换主版本会改变前端产出字节)"
}

# ── 3. 同步 frontend/package.json 与 package-lock.json 的根版本 ──
$utf8NoBom = New-Object System.Text.UTF8Encoding($false)
$pj = [System.IO.File]::ReadAllText("$root\frontend\package.json")
$pj = $pj -replace '("version"\s*:\s*)"[^"]+"', ('$1"' + $ver + '"')
[System.IO.File]::WriteAllText("$root\frontend\package.json", $pj, $utf8NoBom)

# lock 里有两处"根版本"(顶层与 packages[""]), 而每个依赖自己也带 version —— 只改前两处。
# 改之前先按**位置**核对: 第一处必须在 "packages" 之前、第二处在它之后。少了这道守卫,
# "顶层 version 被删"这种布局会让第二处落到首个依赖头上, 把别人的版本号静默改掉
# (独立验证实测复现过: 删掉顶层 version 后构建 exit 0, 而首个依赖的 7.29.7 变成了 1.6.1)。
# verify.yml 的版本同步检查有同款守卫, 两处口径必须一致。
# 改写时先动靠后的那处, 前面那处的索引才不会因为字符串变长而失效
$lockPath = "$root\frontend\package-lock.json"
$lk = [System.IO.File]::ReadAllText($lockPath)
$mv = [regex]::Matches($lk, '(?m)^(\s*"version"\s*:\s*)"([^"]+)"')
if ($mv.Count -lt 2) {
    throw "package-lock.json 里只有 $($mv.Count) 处 version(期望至少 2 处): npm 换了 lock 格式?"
}
$packagesAt = $lk.IndexOf('"packages"')
if ($packagesAt -lt 0 -or -not ($mv[0].Index -lt $packagesAt -and $mv[1].Index -gt $packagesAt)) {
    throw "package-lock.json 的前两处 version 不是(顶层, packages[""])这个顺序: npm 换了 lock 布局? 见本段说明"
}
$lk = $lk.Remove($mv[1].Groups[2].Index, $mv[1].Groups[2].Length).Insert($mv[1].Groups[2].Index, $ver)
$lk = $lk.Remove($mv[0].Groups[2].Index, $mv[0].Groups[2].Length).Insert($mv[0].Groups[2].Index, $ver)
[System.IO.File]::WriteAllText($lockPath, $lk, $utf8NoBom)
# 读回核对: 两处根版本都真的成了 $ver, 写回失败或格式又变时不静默放过
$af = [regex]::Matches([System.IO.File]::ReadAllText($lockPath), '(?m)^\s*"version"\s*:\s*"([^"]+)"')
if ($af.Count -lt 2 -or $af[0].Groups[1].Value -ne $ver -or $af[1].Groups[1].Value -ne $ver) {
    throw "package-lock.json 的根版本没同步到 $ver(读回 '$($af[0].Groups[1].Value)' / '$($af[1].Groups[1].Value)')"
}

# ── 4. 构建 Vue 前端 (类型检查 + Vite 单文件打包) ────
Write-Host "构建 Vue 前端 (Node $haveNode) ..."
Push-Location "$root\frontend"
try {
    if (-not (Test-Path "node_modules\vite\package.json")) {
        Write-Host "  首次运行: 按 package-lock.json 精确安装依赖 ..."
        # 与 CI 同一条路径: npm ci 按 lock 装; npm install 会按 ^ 范围重新解析并改写 lock,
        # 于是本地产物字节可能与 CI 不同, 漂移检查会莫名其妙地红
        npm ci --no-audit --no-fund
        if ($LASTEXITCODE -ne 0) { throw "npm ci 失败 (exit $LASTEXITCODE)" }
    }
    npm run build
    if ($LASTEXITCODE -ne 0) { throw "前端构建失败 (exit $LASTEXITCODE)" }
} finally { Pop-Location }
if (-not (Test-Path "internal\web\dist\index.html")) { throw "未找到 internal\web\dist\index.html" }

# ── 5. 编译资源与可执行文件 ───────────────────────────
Write-Host "构建 Type v$ver ..."
# 目标架构与构建标签在这里钉死: 调用者环境里遗留的 GOARCH/GOFLAGS 会把构建带歪,
# 而且症状都指向别处 —— GOARCH=arm64 时 `go run ./tools/mkres` 会先交叉编译再执行,
# 报 "This version of %1 is not compatible with the version of Windows you're running",
# 看着像资源生成坏了; GOFLAGS=-tags=dev 会静默产出带 DevTools 与右键菜单的开发版
# exe, 而 pecheck 只看资源、查不出来。结束时还原, 免得污染调用者会话
# (在交互式会话里执行 .\scripts\build.ps1 是与调用者同一个进程)
$prevGOARCH, $prevGOFLAGS = $env:GOARCH, $env:GOFLAGS
$env:GOARCH = "amd64"
$env:GOFLAGS = ""
try {
    # tools/mkres 生成 cmd/type/version_<arch>.syso (图标 + 版本信息), go build 按
    # 目标架构自动链接; 纯 Go 实现, 取代 windres + version.rc, 不再需要 MinGW
    go run ./tools/mkres -version $ver -icon assets/icon.ico -out cmd/type/version
    if ($LASTEXITCODE -ne 0) { throw "资源生成失败 (exit $LASTEXITCODE)" }
    # -trimpath 抹掉源码绝对路径: 少了它, 产物里嵌着构建机的目录结构
    go build -trimpath -ldflags="-H windowsgui -s -w" -o Type.exe ./cmd/type
    if ($LASTEXITCODE -ne 0) { throw "go build 失败 (exit $LASTEXITCODE)" }

    $f = Get-Item "Type.exe"
    $v = $f.VersionInfo
    Write-Host "构建完成: Type.exe ($([math]::Round($f.Length / 1KB, 1)) KB)"
    Write-Host "  FileVersion:    $($v.FileVersion)"
    Write-Host "  ProductVersion: $($v.ProductVersion)"

    # ── 6. 校验资源真的链进了 exe ─────────────────────────
    # version_<arch>.syso 缺失时 go build 依然成功, 但产物没有版本信息与图标;
    # 读回 exe 核对, 让这种静默失败在构建期就暴露。
    # 上面那条用独立算出的数字比对(与被校验的工具不是同一份实现), 再用
    # tools/pecheck 过一遍 CI 的同一套判据: 架构 + 版本 + DPI manifest + 图标
    if ($v.FileVersion -ne $v4Dot) {
        throw "exe 版本资源异常: FileVersion = '$($v.FileVersion)', want '$v4Dot' (tools/mkres 是否生效?)"
    }
    go run ./tools/pecheck -exe Type.exe -version $ver -arch amd64
    if ($LASTEXITCODE -ne 0) { throw "产物读回校验失败 (exit $LASTEXITCODE)" }
} finally {
    $env:GOARCH = $prevGOARCH
    $env:GOFLAGS = $prevGOFLAGS
}
