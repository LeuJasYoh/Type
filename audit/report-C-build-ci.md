# 审计报告 C：构建链、CI/CD 与 tools

审计范围：`scripts/build.ps1`、`scripts/gen_icon.py`、`.github/workflows/ci.yml`、`.github/workflows/release.yml`、
`tools/mkres`、`tools/pecheck`、`tools/equivcheck`、`tools/wmcharprobe`、`go.mod` / `go.sum`、
`.node-version`、`.gitattributes`、`.gitignore`、`internal/web/web.go`、`cmd/type/devserver_dev.go`、
`cmd/type/devserver_prod.go`、`frontend/package.json` 与 `frontend/package-lock.json`。

审计环境：Windows，go1.27.1 windows/amd64，node v24.14.0，npm 11.9.0。
本次审计未执行 `scripts/build.ps1`，未打标签、未推送，除本文件外未写入任何仓库文件
（所有需要“构建一次”的验证都在 `%TEMP%` 的副本里做）。

## 工作区状态确认（审计结束后复核）

- `git status --porcelain` 输出 `?? audit/`，`git status --short` 同样只有这一行；
  即唯一的新增是未跟踪的 `audit/` 目录（本报告所在目录）。
- `git diff --stat` 与 `git diff --cached --stat` 均为空，没有任何已跟踪文件被修改。
- 入库前端产物没有被重新生成：`git hash-object --path=internal/web/dist/index.html internal/web/dist/index.html`
  与 `git rev-parse HEAD:internal/web/dist/index.html` 的返回值都是 `0cc9796c7eefeddb4cacd1c85c6a5f5bd7f3c0fe`。
- 时间戳也对得上：`internal/web/dist/index.html` 的修改时间是 2026-09-27 15:09:02，
  仓库根的 `Type.exe` 是 2026-09-27 15:09:03，都早于本次审计（2026-09-30 10:58），
  说明 `scripts/build.ps1` 本次确实没有运行过。
- 仓库根的 `Type.exe` 是早就存在的构建产物，被 `.gitignore` 忽略
  （`git check-ignore -v Type.exe` 输出 `.gitignore:4:*.exe  Type.exe`），不是本次产生的。

## 结论摘要

1. [重要] 发布工作流不复跑 verify 作业的检查。`release.yml` 只做 go test + mkres + 构建 + pecheck，
   没有前端产物漂移检查，也没有 gofmt/vet。带着过期 `internal/web/dist/index.html` 的提交一旦打上标签，
   发布会静默带上与 `frontend/src` 不符的界面，而这正是漂移检查存在的理由。
2. [次要] 版本号“只改一处”的承诺漏了一个载体。`frontend/package-lock.json` 的版本仍是 1.3.4，
   `package.json` 已是 1.5.5，`build.ps1` 只重写后者。实测 `npm ci` 对这两处版本不一致完全不管，
   所以这个漂移没有任何环节会报出来，`ci.yml` 的版本校验也拦不住。
3. [次要] 依赖里有已知漏洞，CI 没有任何漏洞扫描。`golang.org/x/sys@v0.5.0` 直接链进产品 exe
   （GO-2026-5024 / CVE-2026-39824，v0.44.0 修复），`golang.org/x/image@v0.12.0` 经 `tools/mkres` 进构建链
   （GO-2026-5031 / CVE-2026-42500，`bmp.Decode`，v0.41.0 修复）。govulncheck 实测可达 0 条，
   import 到的包内 2 条，require 的模块内 9 条。
4. [次要] `ci.yml:142-148` 的 arm64“测试可编译”是空转。它对 `./cmd/type` 跑 `go test -c`，
   而该包没有测试文件，实测 exit 0 且不产出任何文件；真正有测试的 `internal/typing` 与 `internal/win32`
   反而没做 arm64 测试编译（实测两者都能正常编出 arm64 测试二进制）。
5. [次要] `build.ps1` 从不在 CI 里被真正执行，workflow 中只出现在注释与提示文字里，
   它唯一的自动守门是 AGENTS.md 记载的 BOM 陷阱，而 `ci.yml` 没有任何 BOM 检查。
   BOM 本次实测仍在（前三字节 `EF BB BF`），所以这是回归风险，不是当前缺陷。

## 逐条发现

### F1 [严重度: 重要] 发布闸门不含前端漂移检查与 gofmt/vet

位置：`.github/workflows/release.yml:73-97`（对照 `.github/workflows/ci.yml:32-81`）

证据：
- `release.yml` 的步骤只有：checkout(31) → setup-go(33) → 版本校验(40-70) → `go test -count=1 ./...`(73-74)
  → mkres(77-78) → 双架构构建 + pecheck + 打包(82-97) → 建 Release(100-136)。
  全文件没有 setup-node，没有 npm，没有任何漂移检查；`uses:` 只有 `actions/checkout@v5` 与 `actions/setup-go@v6`。
- `ci.yml:59-81` 的漂移检查（`git hash-object --path=` 与 HEAD blob 比对）只存在于 CI 的 verify 作业。
- `release.yml:72` 的注释自己写明“打标签的提交未必走过 main 的 CI”，也就是说这条风险作者已经意识到，
  但补的措施只有 go test。
- `internal/web/web.go:9-10` 是 `go:embed dist/index.html`，嵌入的是入库文件，构建期不会重新生成。

影响：`frontend/src` 改了、`internal/web/dist/index.html` 没重新生成（或生成后忘了提交）的提交被打上 `v*` 标签时，
Release 里的 exe 会带着旧界面发出去，而且没有任何环节会失败。用户看到的是“新版本装上了，界面还是老的”。
go 侧的编译错误有 `go test` 兜住，gofmt/vet 类问题（例如 printf 参数不匹配）则完全没人管。

建议（任选一条即可）：
1. 把 `ci.yml` 的 verify 步骤抽成 `workflow_call` 的可复用工作流，`ci.yml` 与 `release.yml` 都调用它；
2. 或者最小改动：在 `release.yml` 加 setup-node（`node-version-file: .node-version`）+ `npm ci` +
   与 `ci.yml:59-81` 完全相同的漂移检查，并把 `gofmt -l ./cmd ./internal ./tools` 与 `go vet ./...` 一起加进去
   （这两条不需要 Node，随时可加）。

### F2 [严重度: 次要] package-lock.json 的版本停留在 1.3.4，版本同步漏一个载体

位置：`frontend/package-lock.json:3`、`frontend/package-lock.json:9`、`frontend/package.json:3`、`scripts/build.ps1:27-31`、`ci.yml:86-95`

证据：
- `frontend/package-lock.json` 根对象与 `packages."".version` 都是 `"1.3.4"`，`frontend/package.json:3` 是 `"1.5.5"`。
- `scripts/build.ps1:29-31` 只对 `frontend/package.json` 做 `-replace`，从不碰 `package-lock.json`。
- 实测 `npm ci` 在 package.json 版本 1.5.5、lock 版本 1.3.4 的目录下 exit 0（输出 `up to date, audited 1 package`），
  即 npm 只校验依赖树，不校验根包版本字段，所以 CI 天天跑 `npm ci` 也不会发现。
- `ci.yml:94` 是 `if ($text -notmatch [regex]::Escape($ver))`，`release.yml:64` 是 `Select-String ... -Quiet`，
  两处都只在 package.json 里找“版本字符串是否出现过”，不校验 `version` 字段本身。

影响：AGENTS.md 的“版本号只改 `cmd/type/main.go` 一处，其余由 build.ps1 与构建期资源生成自动同步”不成立，
package-lock.json 是一个没人同步、也没人检查的第三个版本载体，目前已经落后两个小版本。
实际功能影响很小（实测入库的 `internal/web/dist/index.html` 里既不含 1.5.5 也不含 1.3.4，界面版本来自 Go 侧），
但它是“复制上一版忘了改”这类事故的温床，也让 `npm ci` 之后的 lockfile 元数据与实际版本对不上。

建议：
1. `build.ps1` 同步 package.json 后，追加 `npm install --package-lock-only --no-audit --no-fund`（在 frontend 目录），
   让 lockfile 的两个版本字段跟上；或直接对 lockfile 做同样的正则替换。
2. 把两处版本校验改成解析 JSON 后比字段：`(Get-Content -Raw frontend/package.json | ConvertFrom-Json).version -eq $ver`，
   lock 同理；顺带加上 `package-lock.json` 的版本一致性检查。

### F3 [严重度: 次要] 依赖含已知漏洞，CI 无漏洞扫描

位置：`go.mod:13-14`、`go.mod:6-7`、`.github/workflows/ci.yml`（无相关步骤）

证据（govulncheck 实跑，`go run golang.org/x/vuln/cmd/govulncheck@latest ./...`，只写模块缓存，未改 go.mod/go.sum）：
- 结论行：`Your code is affected by 0 vulnerabilities.`，另有
  “2 vulnerabilities in packages you import and 9 vulnerabilities in modules you require”。
- 可达性之外的两条关键项：
  - `GO-2026-5024` / `CVE-2026-39824`：`golang.org/x/sys/windows` 的 `NewNTUnicodeString` 不检查长度溢出，
    v0.44.0 起修复；当前 `go.mod:14` 是 `golang.org/x/sys v0.5.0`。
  - `GO-2026-5031` / `CVE-2026-42500`：`golang.org/x/image/bmp` 的 `Decode` 对越界调色板索引会 panic，
    v0.41.0 起修复；当前 `go.mod:13` 是 `golang.org/x/image v0.12.0`。

原始输出节选（逐字粘贴，未改动任何字符）。命令为
`go run golang.org/x/vuln/cmd/govulncheck@latest -show verbose ./...`，共 117 行；
下面引用第 23-42 行（Symbol Results 与 Package Results）与第 112-117 行（汇总）：

```text
=== Symbol Results ===

No vulnerabilities found.

=== Package Results ===

Vulnerability #1: GO-2026-5031
    Panic when reading out of bound palette index in golang.org/x/image/bmp
  More info: https://pkg.go.dev/vuln/GO-2026-5031
  Module: golang.org/x/image
    Found in: golang.org/x/image@v0.12.0
    Fixed in: golang.org/x/image@v0.41.0

Vulnerability #2: GO-2026-5024
    Invoking integer overflow in NewNTUnicodeString in golang.org/x/sys/windows
  More info: https://pkg.go.dev/vuln/GO-2026-5024
  Module: golang.org/x/sys
    Found in: golang.org/x/sys@v0.5.0
    Fixed in: golang.org/x/sys@v0.44.0
    Platforms: windows
```

```text
Your code is affected by 0 vulnerabilities.
This scan also found 2 vulnerabilities in packages you import and 9
vulnerabilities in modules you require, but your code doesn't appear to call
these vulnerabilities.
```

Module Results 一段（第 44-113 行）的 9 条全部属于 `golang.org/x/image@v0.12.0`，
逐条抄录为“编号 / 修复版本”：`GO-2026-6222` → v0.45.0、`GO-2026-5066` → v0.43.0、
`GO-2026-5062` → v0.43.0、`GO-2026-5061` → v0.43.0、`GO-2026-5032` → v0.41.0、
`GO-2026-4962` → v0.39.0、`GO-2026-4961` → v0.42.0、`GO-2026-4815` → v0.38.0、
`GO-2024-2937` → v0.18.0。

（复核结论：本节正文里写的编号、`Found in` 版本、`Fixed in` 版本、可达性结论与原始输出完全一致，
无出入。评级依据也因此可以复核：包级 2 条被 govulncheck 判定为“代码没有调用到”，
符号级结果为 `No vulnerabilities found.`，所以定为“次要”而不是“阻塞/重要”。）

证据补充：
- 实测 `go list -deps ./cmd/type` 显示产品 exe 的外部包只有
  `golang.org/x/sys/windows`、`go-webview2`、`go-winloader`（`x/image` 不在其中）；
  `go list -deps ./tools/mkres` 显示构建工具会用到 `golang.org/x/image/bmp`、`nfnt/resize`、`winres`。
- 仓库内 `NewNTUnicodeString` 出现次数为 0，与“不可达”的结论一致。
- `go list -m -u all` 显示 `golang.org/x/sys` 可升到 v0.48.0、`golang.org/x/image` 可升到 v0.46.0。

影响：两条 CVE 当前都不可达（一条要求调用未被调用的 API，一条要求解析攻击者提供的 BMP，而 mkres 只读仓库内的
`assets/icon.ico`），所以不构成实际风险。真正的缺口是流程：依赖版本一旦落后 40 多个小版本，
下一次真实可达的漏洞不会有任何提示，因为 CI 里没有漏洞扫描，也没有定期升级机制。

建议：
1. 在 `ci.yml` 的 verify 作业加一步 `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`（只读、无需额外权限）。
2. 升级 `golang.org/x/sys` 到 >= v0.44.0；`golang.org/x/image` 至少到 >= v0.41.0（清掉包级那条），
   若要连 9 条模块级告警一起清掉需要 >= v0.45.0（`go list -m -u all` 显示可用版本是 v0.46.0）。
   两条都是间接依赖，改 `go get` 后跑一遍 `gofmt/vet/test` 与 `pecheck` 即可确认无回归（本次未做升级实测，见“待确认”）。

### F4 [严重度: 次要] arm64 的“测试可编译”步骤没有编译任何东西

位置：`.github/workflows/ci.yml:142-148`

证据（本机按 CI 的命令原样复现）：
- `GOARCH=arm64; go test -c -o "$env:TEMP\type-arm64.test.exe" ./cmd/type` 输出
  `? github.com/LeuJasYoh/type/cmd/type [no test files]`，exit 0，目标文件不存在。
- 同样条件下对真正有测试的包：`./internal/typing` 编出 4534272 字节、`./internal/win32` 编出 4737536 字节，
  都 exit 0。
- `git ls-tree` 显示 `cmd/type` 下确实没有 `_test.go`。

影响：该步骤注释写的目的是“arm64 上至少要证明测试代码也能编译”，实际只是重复了上一步 `go vet ./...` 已经做过的事
（vet 会类型检查测试文件，但不链接测试二进制）。arm64 上的测试链接、测试专用代码路径完全没有被验证。

建议：把该步骤改成对真正有测试的包做 `go test -c`，实测可用：
`go test -c -o "$env:TEMP" ./internal/typing ./internal/win32`（多包 + 目录形式的 `-o` 已验证 exit 0，
产出 `typing.test.exe` 与 `win32.test.exe`）。若想覆盖全部包，用 `./...` 保持同样写法。

### F5 [严重度: 次要] build.ps1 从不被 CI 执行，BOM 回归没有守卫

位置：`scripts/build.ps1:1`（首三字节）、`.github/workflows/ci.yml`（全文无 BOM 检查、无 build.ps1 调用）、`AGENTS.md:123-126`

证据：
- 实测 `scripts/build.ps1` 前 3 字节 `EF BB BF`（完整开头 `EF BB BF 23 20 62 75 69`，即 BOM + `# bui`），
  文件内 `EF BB BF` 只出现 1 次，BOM 目前是好的。
- 在 `.github/workflows/*.yml` 里搜 `BOM`、`EF BB BF`、`0xEF` 均无匹配；
  搜 `build.ps1` 只命中注释与提示文字（`ci.yml:78`、`ci.yml:89`、`ci.yml:116`、`ci.yml:129`、`release.yml:65`、`release.yml:80`），
  没有任何 `run:` 真正执行它。
- `AGENTS.md:123-126` 说明了 BOM 丢失的后果：Windows PowerShell 5.1 按 ANSI 解码无 BOM 脚本，
  中文注释的尾字节吞掉换行，下一行代码并进注释成为死代码，ProductVersion 同步曾因此静默失效。

影响：`build.ps1` 是本地构建与版本同步的唯一入口，却是 CI 的盲区。它一旦因 BOM 丢失或 PS 5.1/7 差异出问题，
只有开发者本机能发现；而“死代码”式故障的症状是产物版本属性为空，不报错。
现在的兜底是 build.ps1 末尾的 FileVersion 读回（`build.ps1:67-69`）与 CI 的 package.json 版本检查（`ci.yml:86-95`），
能覆盖一部分，但覆盖不到脚本本身的语法/编码问题。

建议：
1. 在 `ci.yml` 加一条几行的 BOM 守卫（放在 verify 作业靠前位置）：
   `$b = [System.IO.File]::ReadAllBytes('scripts/build.ps1')[0..2]`，
   然后断言 `0xEF 0xBB 0xBF`，否则 `exit 1`。
2. 可选：在 CI 里真跑一次 `powershell -ExecutionPolicy Bypass -File scripts/build.ps1`（该作业已有 Node 环境时最省事）。
   CI 工作区可丢弃，产物漂移也能被随后的漂移检查抓住；顺带能验证 PS 5.1 与 pwsh 7 两条路径。

### F6 [严重度: 次要] 构建未加 -trimpath，exe 里嵌了本地绝对路径，换目录构建字节不同

位置：`scripts/build.ps1:53`、`.github/workflows/ci.yml:134`、`.github/workflows/release.yml:87`

证据（均在 `%TEMP%` 下构建，未污染仓库）：
- 同一目录连续两次 `go build -ldflags="-H windowsgui -s -w" -o A.exe ./cmd/type` 结果字节一致
  （SHA256 `D231532F...C2419B`），PE 头的 TimeDateStamp 为 0，即同环境构建是确定性的。
- 加 `-trimpath` 后产物变化：`3CACF50D...EFDE08`，体积从 3631616 降到 3621888 字节（差 9728 字节）。
- 在未加 `-trimpath` 的 A.exe 中搜到 9 处 `D:/Projects/Type`，例如
  `D:/Projects/Type/internal/typing/typing.go`；加 `-trimpath` 的 C.exe 中为 0 处。

影响：发布产物不可独立复现。同样的提交在不同检出目录（开发者本机与 CI 的 `D:\a\...`）构建出的 exe 字节不同，
所以“这个 Release 的 exe 是不是这个提交构建的”无法用哈希回答。项目当前没有任何东西按字节比对 exe
（漂移检查只比对前端 HTML），所以不构成发布错误，但这是一条成本极低的卫生项，还顺带少暴露本地路径。

建议：给三处构建命令统一加 `-trimpath`（`scripts/build.ps1:53`、`ci.yml:134`、`release.yml:87`），
并在 AGENTS.md 的“常用命令”里同步。若决定不加，建议在 AGENTS.md 明确写“exe 不保证字节可复现，只有前端产物保证”，
免得后来者按 README 里“字节级可复现”的措辞推广到 exe。

### F7 [严重度: 次要] ci.yml 未声明 permissions，第三方 action 未固定到 SHA

位置：`.github/workflows/ci.yml:1-11`、`.github/workflows/release.yml:24-25`、`ci.yml:15,17,22,109,111`、`release.yml:31,33`

证据：
- `release.yml:24-25` 有 `permissions: contents: write`；`ci.yml` 全文搜 `permissions` 为 0 命中，
  即 verify 与 build 两个作业都用仓库默认令牌权限。
- 全部 7 处 `uses:` 都是浮动 tag：`actions/checkout@v5`、`actions/setup-go@v6`、`actions/setup-node@v5`，
  没有一处固定到 40 位 commit SHA。

影响：如果仓库的默认 workflow 权限是 read-write（Settings 里的默认值，取决于仓库创建时间与设置，
见“待确认”），那么 CI 中的任意第三方依赖（`npm ci` 装的 1000+ 个包、或任一 action 被投毒）都能拿到可写的
`GITHUB_TOKEN`，而本仓库的发布流程正是靠推标签触发，写权限的后果会比较重。固定 SHA 的作用是让 tag 被移动时
CI 行为不跟着变；这里三个都是 GitHub 官方 action，风险等级低于第三方 action，所以合并成一条次要项。

建议：`ci.yml` 顶层加 `permissions: contents: read`（build 作业不需要任何写权限）；
如果要更严，把三个 action 固定到 commit SHA 并附注释写明对应版本号（Dependabot 可自动跟）。

### F8 [严重度: 次要] 版本号提取与校验都是子串匹配，且在五处各写一份

位置：`scripts/build.ps1:18`、`.github/workflows/ci.yml:91`、`.github/workflows/ci.yml:120`、`.github/workflows/release.yml:43`、`tools/mkres/main.go:53`、`ci.yml:94`、`release.yml:64`

证据：
- 四处 PowerShell 用的是同一条正则 `version\s*=\s*"([\d.]+(?:-[0-9A-Za-z.]+)?)"` 配 `Select-String`，
  而 `Select-String` 默认大小写不敏感且不要求行首，因此 `oldVersion = "1.0"`、`ProductVersion = "..."`，
  甚至注释里写的 `// version = "1.5.4"` 都会被当成版本声明。
- 实测当前 `cmd/type/main.go` 里该模式只命中 1 处（第 18 行 `var version = "1.5.5"`），
  所以现在不会取错值，属于回归风险而非现存缺陷。
- `mkres/main.go:53` 的 `versionRe` 是锚定的（`^...$`），与那四处并不是真正的“同款”。
- 同步校验同样是子串：`ci.yml:94` 用 `-notmatch`、`release.yml:64` 用 `Select-String -Quiet`，
  只要版本字符串在文件里出现过就算通过（见 F2）。

影响：单一来源机制靠“取第一个匹配”工作，一旦 main.go 里在 `var version` 之前出现任何形如
`xxxversion = "..."` 的行（大小写不敏感），版本号会被静默取错；后果是 package.json 与资源版本一起被改成错的值，
而 build.ps1 末尾的读回校验用的是同一份错值，仍然会“通过”。

建议：把提取改成锚定且区分大小写，例如 `(?m)^\s*var\s+version\s*=\s*"([^"]+)"` 并加 `-CaseSensitive`；
更彻底的做法是让 CI 只保留一份实现（一个 `.ps1` 或一个小 Go 工具），另外三处调用它，避免五处正则各自漂移。
同步校验改为解析 JSON 比字段（见 F2 建议）。

### F9 [严重度: 次要] equivcheck 的比对范围与文档命令都会误导

位置：`tools/equivcheck/main.go:162`（默认旧侧文件 `main.go`）、`main.go:39-45`、`main.go:48-68`、`main.go:222-229`、`main.go:296-298`、`AGENTS.md:42`

证据（实跑）：
- 按 `AGENTS.md:42` 的文档命令 `go run ./tools/equivcheck 09cd978 25e9271 --renamed` 直接失败：
  `git show 09cd978:main.go: exit status 128`，因为拆包之后仓库根已经没有 `main.go`。
- 补上 `--old-file cmd/type/main.go` 后输出：
  `09cd978:cmd/type/main.go → 25e9271 (12 个 .go 文件): 旧侧函数总数 1`，
  结论是 `函数体归一化后逐字节一致: 0`、`差异: main`。
- 而该 rev 的业务函数总数是 54（`cmd/type/*.go` 非测试文件逐个统计：main 1、typing 12、win32_clipboard 13、
  win32_keyboard 10、win32_window 8、win32_webview2 3、win32_instance 2、win32_msgbox 1 等），
  新侧也是 54。也就是说这次“重构等价性验证”实际只比了 1 个函数。
- 同一工具对 25e9271 → 19b22d6 同样只比了 `main` 一个函数（输出“逐字节一致: 1”）。
- 另外它会报重名警告：`devMode(cmd/type/devserver_dev.go:23 与 cmd/type/devserver_prod.go)`，
  因为工具不理解构建标签，两个互斥文件被当成重名，只比了其中之一（这里是 `devserver_prod.go`）。

影响：工具本身没有说谎（“旧侧函数总数 1”打印在第一行，重名也只比后者并给了警告），
但对照着 AGENTS.md 使用、只看“一致/差异”两行结论的人来说，很容易把“1 个函数一致”读成“这次重构等价”。
`--old-file` 只接受单个文件的限制，决定了它在“多文件 → 多文件”的拆包类重构里天然覆盖不到大部分代码。

建议：
1. `AGENTS.md:42` 的命令补上实际可用形式（`--old-file cmd/type/main.go`），并写明“只比对旧侧单个文件里的函数”；
2. 让工具打印“新侧函数总数”，并在旧侧函数数明显少于新侧时给一条醒目警告（例如“旧侧仅 1/54，覆盖不足”）；
3. 可选：`--old-file` 支持多个路径或直接接收一个 rev 的完整文件清单，让拆包类重构能全量比对。

### F10 [严重度: 吹毛求疵] wmcharprobe 的 close 模式可以一次关掉所有匹配窗口

位置：`tools/wmcharprobe/main.go:155-178`（匹配逻辑）、`main.go:381-386`（close 模式）、`main.go:304-307`

证据：
- `findTargets` 用 `strings.Contains(title, sub)` 匹配所有可见顶层窗口。
- `close` 模式对每个命中窗口 `PostMessageW(WM_CLOSE)`，没有确认、没有干跑、没有二次校验。
- 传入空字符串作为标题子串时 `strings.Contains(title, "")` 恒为真，会把 WM_CLOSE 发给当前桌面上的每一个可见顶层窗口
  （有未保存内容的程序会弹保存对话框）。
- 另外 `main.go:305` 的用法提示写的是 `<标题子串> [文本] [char|keys]`，漏了文件头（`main.go:12`）里列出的
  `direct` 与 `close`，与实现不一致。

影响：这是开发用工具、使用者是维护者本人，出问题的前提是主动传了 `close` 且子串过短或为空，所以概率低；
但后果是不可逆的窗口关闭动作，值得加一道最低限度的防护。

建议：`close` 模式拒绝长度 < 3 的子串并要求显式确认（或加 `--yes` / `--dry-run` 先列出将关闭的窗口）；
顺手把 `main.go:305` 的用法提示与文件头对齐。

### F11 [严重度: 吹毛求疵] gen_icon.py 的头部注释仍描述已被取代的 version.rc / windres

位置：`scripts/gen_icon.py:4-5`

证据：注释写 `assets/icon.jpg (322×322 源图) → assets/icon.ico 圆角多尺寸应用图标
(version.rc 引用, windres 编译进 exe; 窗口图标经 SHGetFileInfoW 从 exe 提取)`，
而 `assets/` 目录下只有 `icon.ico`、`icon.jpg`、`screenshot-dark.png`、`screenshot-light.png`，
没有 `version.rc`；`tools/mkres/main.go:3` 与 `AGENTS.md:138` 都写明资源已改由 Go 的 mkres 生成。

影响：只有文档准确性，但这条注释是图标管线唯一的说明，会让人以为还要改 .rc。属文档同步类缺陷。

建议：把该行改为“由 tools/mkres 在构建期写进 version_<arch>.syso”。

### F12 [严重度: 吹毛求疵] .gitignore 未忽略发布打包产物

位置：`.gitignore:1-38`、`.github/workflows/release.yml:94-95`

证据：`release.yml:94-95` 把 `Type-$env:version-windows-$arch.zip` 写在仓库根；
`.gitignore` 里有 `*.exe`（`build/<arch>/Type.exe` 因此被忽略）但没有 `*.zip`，也没有 `build/`，
全文件 38 行内没有 zip 相关规则。

影响：在本地复现发布步骤（或本地跑 release 的打包段）后，`git status` 会多出两个 10MB 级 zip，
误提交一次就是两个二进制进库。CI 里因为工作区随手丢弃，不构成问题。

建议：加 `Type-*-windows-*.zip` 与 `build/`。

### F13 [严重度: 吹毛求疵] 图标资产没有漂移守卫

位置：`.github/workflows/ci.yml`（无 uv/python 步骤）、`README.md:205-206`、`assets/icon.ico`

证据：`README.md:205-206` 写明“更换 assets/icon.jpg 后执行 uv run scripts/gen_icon.py，产物字节级可复现”，
但 `ci.yml` 里没有任何 Python/uv 步骤，也没有把 `assets/icon.ico` 与 `assets/icon.jpg` + 脚本的产物做比对。
`assets/icon.ico` 是入库文件，构建期由 mkres 原样嵌入（`tools/mkres/main.go:123-136`），
`tools/pecheck` 只检查 RT_GROUP_ICON 存在且非空（`tools/pecheck/main.go:177-182`），不管内容新旧。

影响：与前端产物漂移同一类问题，只是概率与影响都低得多（图标换了忘重生成，发出去的是旧图标）。
考虑到在 CI 里引入 uv 与网络下载的成本高于收益，这条不一定要修。

建议：至少让 README 明确“icon.ico 以入库版本为准，gen_icon.py 只在更换 icon.jpg 时手工执行”，
并把这条与前端产物的漂移检查区别开；如果哪天图标改动频繁，再考虑加 CI 校验。

### F14 [严重度: 吹毛求疵] workflow_dispatch 的版本号输入直接插值进 PowerShell

位置：`.github/workflows/release.yml:17-21`、`release.yml:50`

证据：`$tag = 'v' + ('${{ inputs.version }}'.TrimStart('v', 'V'))`，输入值被原样拼进脚本字符串。
`workflow_dispatch` 需要仓库写权限，所以这不是权限提升问题，但输入里出现单引号会破坏脚本语法，
在“版本号一致性检查”之前就产生难以理解的报错。

影响：低。只是多一个不必要的注入口，且拼错的版本号会以晦涩的方式失败。

建议：按 GitHub 的推荐做法把输入经环境变量传入（`env: INPUT_VERSION: ${{ inputs.version }}`，脚本里读 `$env:INPUT_VERSION`），
并在插值前用与 ci.yml 同款正则校验一遍。
（另：两个触发源（tag push 与手动触发）没有 `concurrency` 归并，理论上可以并发跑两次 Release。
失败方式是 `gh release create` 报“已存在”，会失败着停下而不是静默出错，故未单列。）

### F15 [严重度: 吹毛求疵] mkres 的版本正则比 pecheck 的解析器松

位置：`tools/mkres/main.go:53`、`main.go:68-70`、`main.go:174-183`；`tools/pecheck/main.go:187-205`

证据：
- `mkres` 的 `versionRe` 是 `^[\d.]+(?:-[0-9A-Za-z.]+)?$`，`1..5` 这种空段也合法。
- 实测 `go run ./tools/mkres -version "1..5" -out "$env:TEMP\mkres-probe2"` exit 0，
  真的写出了两个 231830 字节的 .syso。
- `pecheck` 的 `fileVersion` 用 `strconv.Atoi` 逐段解析，且有段数上限，遇到 `1..5` 会报错。

影响：畸形版本号会在 mkres 阶段被放过，但下游一定会兜住（`build.ps1:67-69` 的 FileVersion 读回比较，
以及 `pecheck`），所以最终仍然是响亮的构建失败，不是静默错误。属于纵深防御的小毛刺。

建议：把 mkres 的校验换成与 pecheck 同源的解析（逐段 Atoi + 段数上限），让错误信息在最早的环节出现；
或者干脆两边共用一个小函数。

## 已核查、未发现问题的项

以下都做了实测，结论是当前状态正常，列出来是为了让后续复核不必重跑：

1. **前端产物漂移检查有效，不误报也不漏报**（`ci.yml:59-81`）。
   在 `%TEMP%` 的 frontend 副本里重建：产物 SHA256 `5B715CD1...8855E` 与仓库入库文件完全一致，
   `git hash-object --path=internal/web/dist/index.html` 得到 `0cc9796c...f3c0fe`，与 `HEAD:` 的 blob 相同，逐字节相同。
   把 `src/main.ts` 加一行 `console.log('drift-probe-marker')` 后重建，blob 变成 `3978c4cb...77af98`，
   与 HEAD 不同，说明“改了源码忘提交产物”确实会被抓住。
   把入库文件转成 CRLF 后（81860 字节 vs 81842 字节）用同样的 `--path` 哈希仍得到 `0cc9796c...f3c0fe`，
   证实 `git hash-object --path=` 的行尾归一确实按 `.gitattributes` 生效，Windows 检出不会误报。
2. **`npm --prefix frontend run build` 与 `Push-Location frontend; npm run build` 等价**。
   用临时包实测：两种方式的脚本 `cwd` 都是包目录、`PATH` 首项都是 `<包目录>\node_modules\.bin`，
   唯一差别是 `INIT_CWD`（根目录 vs 包目录），本仓库没有任何脚本读它。
3. **漂移检查所用的命令真的带类型检查**。`frontend/package.json:10` 是
   `vue-tsc -p tsconfig.json --noEmit && vite build`；在副本里注入 `const bad: number = "x"` 后
   `npm run build` exit 2 并报 TS2322，说明 `ci.yml:67` 的那条命令每次都跑了类型检查。
4. **Node 版本来源与 lockfile**：`.node-version` 内容为 `24`（3 字节，含换行），
   `ci.yml:27` 用 `node-version-file: .node-version`，`ci.yml:29` 的 cache 路径
   `frontend/package-lock.json` 存在；本机 `node -v` 为 v24.14.0，与 24 同主版本。
   `ci.yml:56` 用的是 `npm ci` 而不是 `npm install`。
5. **Go 基线全绿**：`gofmt -l ./cmd ./internal ./tools` 输出为空；
   `go vet ./...` exit 0；`go test -count=1 ./...` exit 0
   （`internal/typing` 2.556s、`internal/win32` 0.690s，其余为 no test files）。
6. **`go 1.26.5` 合法**。go 指令自 Go 1.21 起允许补丁号，`go vet`/`go test` 在本机 go1.27.1 下正常。
   效果是工具链下限抬到 1.26.5（`GOTOOLCHAIN=auto` 时旧版本会自动下载），
   CI 按 `go-version-file: go.mod` 装 1.26.5，与本机 1.27.1 不同，但没有不变量依赖 Go 产物字节，无实际风险。
7. **`pecheck` 的失败信息可诊断**（实跑 4 种失败）：架构不符报
   `PE 机器类型 = 0x8664, want 0xAA64 (arm64): 产物架构不对`；版本不符报
   `版本资源 FileVersion = [1 5 5 0], want [9 9 9 0]`；缺参数报 `三个参数都必填 (用法见文件头注释)`；
   都对 exe 的现有产物（仓库根 `Type.exe`，版本 1.5.5）跑通了 happy path：
   `pecheck: Type.exe 校验通过 (架构 amd64, 版本 1.5.5; 图标/版本/manifest 均已链入)`。
8. **arm64 的资源读回校验没有被漏掉**：`ci.yml:136-137` 在 amd64/arm64 矩阵内对每个架构都跑 pecheck，
   矩阵 `fail-fast: false`（`ci.yml:105`），两个架构互不遮蔽。
9. **THIRD_PARTY_NOTICES.md 与实际进 exe 的组件一致**。
   `go list -deps ./cmd/type` 的外部包只有 go-webview2（含 pkg/edge、webviewloader）、go-winloader、
   `golang.org/x/sys/windows`，与声明表（`THIRD_PARTY_NOTICES.md:8-11`）一一对应；
   winres 作为构建期工具也在 `:15` 单独说明。`nfnt/resize`、`x/image` 只进构建工具，不进产品。
10. **供应链触发面干净**：两个 workflow 都没有 `pull_request_target`，没有使用 `secrets`，
    发布用的令牌是 `github.token`（`release.yml:105`）；`release.yml:24-25` 的 `contents: write` 是
    建标签与建 Release 的最小必要权限。
11. **缓存配置正确**：`setup-go` 都是 `cache: true` + `go-version-file: go.mod`；
    `setup-node` 的 `cache: npm` 配了正确的 lockfile 路径；build 作业不需要 Node，也没有 setup-node，一致。
12. **Python 侧单一来源一致**：`pyproject.toml` 要求 `pillow==12.3.0`、`.python-version` 为 `3.12`、
    `uv.lock` 中 pillow 为 `12.3.0`，与 `requires-python = ">=3.12"` 相符。
13. **`internal/web/web.go`、`cmd/type/devserver_prod.go`、`devserver_dev.go` 无构建链缺陷**：
    `go:embed dist/index.html`（`web.go:9-10`）与 `.gitignore` 的例外规则一致；
    `devserver_prod.go:1` 的构建约束 `windows && (amd64 || arm64) && !dev` 使正式产物不含
    dev server 与调试开关，与 AGENTS.md 的说明一致。
14. **`build.ps1` 的错误处理是 fail-fast 的**。`build.ps1:10` 设了 `$ErrorActionPreference = "Stop"`；
    原生命令不会因非零退出而抛异常，但每一处都显式比对并抛错：`npm install`(`:40`)、`npm run build`(`:43`)、
    mkres(`:52`)、`go build`(`:54`)、pecheck(`:71`)。重复运行也是幂等的（版本替换与资源生成都是覆盖写）。
    逐行检查未发现只在 PowerShell 5.1 或只在 7 上可用的构造；带路径的变量都已加引号
    （`:29`、`:31`、`:35`），其余是相对路径，含空格/中文的仓库路径理论上可用，但本次没有在那种路径下实跑
    （不执行 build.ps1 是硬约束），见“待确认”。
15. **架构矩阵与产品约束一致**：`ci.yml:106-107` 是 amd64/arm64 两个架构，与 AGENTS.md 的
    “产品仅支持 windows && (amd64 || arm64)、386 刻意禁止”相符，且 `fail-fast: false` 让一个架构失败
    不影响另一个的诊断信息。CI 没有上传产物，但失败路径都把关键值打进了日志
    （`pecheck` 会打印实测值与期望值，漂移检查会打印入库哈希、重建哈希与 node/vite 版本，`ci.yml:73-79`），
    本次未发现需要额外 upload-artifact 才能定位的检查。
16. **go.sum 存在且构建会校验模块哈希**（`go.sum`，4288 字节；`go build` / `go test` 对参与构建的模块
    强制比对 go.sum，缺失或篡改会直接失败）。未覆盖的部分是“整洁性”：CI 没有 `go mod verify`，
    也没有 `go mod tidy -diff`，所以 go.mod 里多余的 indirect 依赖或 go.sum 里过期的条目不会被发现。
    这属于可选的加固（一行命令即可加进 verify 作业），当前没有实际缺陷，故未列为 finding。
    同理，`gofmt` + `go vet` 之外没有引入 staticcheck/gosec 之类工具；本次审计没有发现具体缺陷是它们能兜住的，
    故不建议仅为形式而加。

## 待确认

1. **CI runner 是否预装 C 编译器**，这决定 `-race` 能不能加。`go test -race` 要求 cgo：
   本机在 `CGO_ENABLED=0` 下报 `go: -race requires cgo`，设 `CGO_ENABLED=1` 后（本机有 MinGW gcc 15.2.0）
   `go test -race -count=1 ./internal/typing` 通过，用时 3.712s。若 GitHub 的 windows-latest 没有 gcc，
   加 `-race` 会引入对 C 工具链的依赖，与“构建链不含 C 编译器”的前提冲突（只影响测试步骤，不影响产品构建）。
   建议先确认 runner 环境再决定；本次未在 CI 上实测。
2. **仓库默认的 workflow 权限是 read 还是 read-write**（Settings → Actions → Workflow permissions）。
   这决定 F7 里“缺 permissions”的实际风险等级：默认为只读时，问题只是写法不够显式。
3. **`assets/icon.ico` 是否与当前 `assets/icon.jpg` + `scripts/gen_icon.py` 一致**。
   复现需要执行 `uv run scripts/gen_icon.py`，它会重写入库的 `assets/icon.ico`，超出本次只读审计范围，故未执行；
   README 声称的“字节级可复现”本次未独立验证。
4. **升级 `x/sys` 到 v0.44.0+ / `x/image` 到 v0.41.0+ 是否有行为变化**。
   本次只做了 `go list -m -u all`（可以看到目标版本）与 govulncheck 可达性分析，没有真的升级后回归。
5. **发布 zip 是否字节可复现**。`Compress-Archive` 会把文件时间戳写进 zip 条目，本次没有在 CI 上重跑发布流程，
   未验证两个架构的 zip 是否能稳定复现（项目也未声称这一点）。
6. **`build.ps1` 在“路径含空格或中文”的检出目录下能否跑通**。静态检查未发现未加引号的路径变量，
   但实跑会写出 `Type.exe` 与 `internal/web/dist/index.html`，超出只读审计范围，故未验证。
7. **build.ps1 的并发运行**。同一工作区里同时跑两次会在 `cmd/type/version_<arch>.syso` 与根目录 `Type.exe`
   上互相覆盖，没有锁；未实测，且同一工作区并发构建本来不是受支持的用法，故未列为 finding。

## 本次执行的验证命令与结果

| 命令 | 结果 |
|---|---|
| 读 `scripts/build.ps1` 前 3 字节 | `EF BB BF`（完整开头 `EF BB BF 23 20 62 75 69`），BOM 完好 |
| `gofmt -l ./cmd ./internal ./tools` | 输出为空 |
| `go vet ./...` | exit 0 |
| `go test -count=1 ./...` | exit 0（typing 2.556s / win32 0.690s） |
| `go list -m -u all` | 有网络，列出可用更新：`x/sys v0.5.0 → v0.48.0`、`x/image v0.12.0 → v0.46.0` 等 |
| `go run golang.org/x/vuln/cmd/govulncheck@latest [-show verbose] ./...` | 可达 0 条；import 到的包 2 条（GO-2026-5031 x/image@v0.12.0、GO-2026-5024 x/sys@v0.5.0）；require 的模块 9 条（全属 x/image@v0.12.0）；原始输出逐字节引用见 F3 |
| `go list -deps ./cmd/type` 与 `./tools/mkres ./tools/pecheck` | 产品只含 go-webview2 / go-winloader / x/sys；工具含 winres / resize / x/image/bmp |
| 副本内 `npm run build` 后比对 | 与入库产物逐字节一致（blob `0cc9796c...`）；改动源码后 blob 变化 |
| 副本内注入类型错误后 `npm run build` | exit 2，TS2322，确认 vue-tsc 在跑 |
| 临时包实测 `npm --prefix sub run X` 与 `cd sub; npm run X` | cwd 与 PATH 首项相同，仅 INIT_CWD 不同 |
| `npm ci`（package.json 1.5.5 + lock 1.3.4） | exit 0，不校验根包版本字段 |
| 两次同目录 `go build -ldflags="-H windowsgui -s -w"` | SHA256 相同；PE TimeDateStamp = 0 |
| 同源加 `-trimpath` 构建 | 哈希不同，体积 3631616 → 3621888；未 trimpath 的产物内嵌 9 处 `D:/Projects/Type` |
| `go test -c` 对 `./cmd/type`（arm64 与 amd64） | 均 exit 0 且 `[no test files]`，不产出文件 |
| `go test -c -o <dir> ./internal/typing ./internal/win32`（arm64） | exit 0，产出 4534272 / 4737536 字节测试二进制 |
| `go run ./tools/pecheck`（4 种输入） | 1 次通过、3 次按预期失败，错误信息可定位 |
| `go run ./tools/mkres -version "bad"` / `"1..5"` | 前者 exit 1 且不写文件；后者 exit 0 并写出两个 .syso |
| `go run ./tools/equivcheck 09cd978 25e9271 --renamed [--old-file cmd/type/main.go]` | 默认命令报 `git show 09cd978:main.go: exit status 128`；补 --old-file 后“旧侧函数总数 1”（实际 54） |
| `go test -race`（CGO_ENABLED=1，本机 MinGW gcc） | `internal/typing` 通过，3.712s；CGO_ENABLED=0 时报 `-race requires cgo` |

审计人：audit-build-ci（team task `task-3`）
