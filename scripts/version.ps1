# version.ps1 — 版本号提取的唯一实现
# 版本号的单一来源是 cmd/type/main.go 的 var version; 本函数是提取它的唯一实现,
# 调用方: scripts/build.ps1 与 .github/workflows/{verify,ci,release}.yml (dot-source 后
# 调用 Get-TypeVersion)。不要再在别处手写提取正则: 下游(标签闸门/资源/包名/package.json)
# 全用这个提取值, 四份副本走散时某一份会静默用旧值。
#
# 正则必须锚定行首的 var version = 且大小写敏感: version 是常见词, 无锚点的子串匹配会
# 命中注释里的同形写法, 或将来某个 minXxxVersion = "…"; "恰好一处"的断言防止匹配到
# 0 处或多处时取到一个不可信的值。
$ErrorActionPreference = "Stop"

function Get-TypeVersion {
    param([string]$RepoRoot = (Split-Path -Parent $PSScriptRoot))
    $main = Join-Path $RepoRoot "cmd/type/main.go"
    $m = @(Select-String -Path $main -CaseSensitive `
        -Pattern '(?m)^\s*var\s+version\s*=\s*"([\d.]+(?:-[0-9A-Za-z.]+)?)"')
    if ($m.Count -ne 1) {
        throw "cmd/type/main.go 里应恰好有一处 var version = ""x.y.z"" (实际 $($m.Count) 处)"
    }
    return $m[0].Matches[0].Groups[1].Value
}
