//go:build windows && (amd64 || arm64)

// 构建链契约: "必须多处同款"的东西交给测试记住, 别靠人的记性
//
// 为什么值得单独一个文件: 版本号提取正则写在四处(scripts/build.ps1 与三个
// workflow), 发布构建命令行写在三处, 而它们之间此前没有任何检查保证一致。
// 改一处漏一处时本地毫无症状: 漏掉的是发布产物里的东西(比如 -trimpath 少了一处,
// 构建机的绝对路径就进了 exe), 只有人肉去 exe 里搜路径才看得见。
//
// 这些断言不追求"覆盖全部构建约定", 只追求一条: 声明为同款的东西, 真的逐字同款。
// 与本目录的 contract_test.go 同一套口径(读仓库内文本文件, 路径相对 cmd/type)
//
// 它是**逐字**比较, 因此不吃"行为等价的改写": 把某处的正则拆成两行、把
// `$m.Count -ne 1` 写成 `1 -ne $m.Count`、给命令加个反引号续行, 都会变红。
// 这是刻意的取舍(四处本来就要求同款), 代价是"四处一起改写"时要顺手改掉这里的
// 常量 —— 下面那两个常量就是这套约定的唯一权威副本

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// versionPattern 是四处共用的版本号提取正则(逐字), 必须锚定行首的
// `var version =` 且大小写敏感: version 是常见词, 无锚点的子串匹配会命中注释里的
// `// version = "x"` 或将来某个 `minXxxVersion = "…"`, 而下游(标签闸门、资源、
// 包名、package.json)全用这个提取值, 取错值会一路错到底
const versionPattern = `(?m)^\s*var\s+version\s*=\s*"([\d.]+(?:-[0-9A-Za-z.]+)?)"`

// versionCountGuard 是四处都有的"恰好一处"断言: 上面那条正则若匹配到 0 处或
// 多处, 取到的值就不可信, 宁可当场失败
const versionCountGuard = "Count -ne 1"

// releaseBuildCmd 是发布构建命令行的公共前缀。三处必须同款:
//   - -trimpath: 少了它, 产物里嵌着构建机的绝对路径(既漏环境信息, 也让
//     "同样源码编出同样文件"做不到);
//   - -H windowsgui: 少了它产物带控制台黑框;
//   - -s -w: 少了它带符号表, 体积明显变大。
//
// 三者都不会让 go build 失败, 所以只能靠比对来守
const releaseBuildCmd = `go build -trimpath -ldflags="-H windowsgui -s -w"`

// codeLines 去掉整行注释后返回剩下的行。
//
// 断言必须落在代码行上: 只说"文件里出现过某段文本"会被注释满足 —— 独立验证
// 实测过这条(把真正的命令里的 -trimpath 删掉, 在旁边补一条写着同款字面量的
// 注释, 只看 Contains 的断言照样绿), 那就又变成一条假报警器了
func codeLines(src string) []string {
	var out []string
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// buildCommandLines 抽出"真正要执行的 go build 命令行": 注释已去掉, 且整行
// 行首就是 go build。用行首判定是为了不把 `throw "go build 失败"` 这类只是提到
// 该词的语句算成构建命令
func buildCommandLines(src string) []string {
	var out []string
	for _, line := range codeLines(src) {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "go build ") {
			out = append(out, trimmed)
		}
	}
	return out
}

// TestVersionPatternSingleSource 四处版本号提取正则必须逐字相同, 且都带
// "恰好一处"的断言与大小写敏感开关
func TestVersionPatternSingleSource(t *testing.T) {
	files := [][]string{
		{"scripts", "build.ps1"},
		{".github", "workflows", "verify.yml"},
		{".github", "workflows", "ci.yml"},
		{".github", "workflows", "release.yml"},
	}
	for _, parts := range files {
		src := strings.Join(codeLines(repoFile(t, parts...)), "\n")
		name := strings.Join(parts, "/")
		if !strings.Contains(src, versionPattern) {
			t.Errorf("%s 里的版本号提取正则与约定不一致(注释里出现不算):\n  want 含 %s", name, versionPattern)
		}
		if !strings.Contains(src, "-CaseSensitive") {
			t.Errorf("%s 的版本号提取没有 -CaseSensitive: 大小写不敏感会命中同形词", name)
		}
		if !strings.Contains(src, versionCountGuard) {
			t.Errorf("%s 缺少 %q 这条断言: 匹配到 0 处或多处时取到的值不可信", name, versionCountGuard)
		}
	}
}

// TestReleaseBuildCommandSingleSource 三处发布构建命令行必须同款, 且都构建
// ./cmd/type 这个包; 另外每一处 go build 都必须带 -trimpath(新加一条不带它的
// 构建命令, 也会在这里暴露)
func TestReleaseBuildCommandSingleSource(t *testing.T) {
	files := [][]string{
		{"scripts", "build.ps1"},
		{".github", "workflows", "ci.yml"},
		{".github", "workflows", "release.yml"},
	}
	for _, parts := range files {
		src := repoFile(t, parts...)
		name := strings.Join(parts, "/")
		cmds := buildCommandLines(src)
		if len(cmds) == 0 {
			t.Errorf("%s 里找不到 go build 命令行(注释与提到该词的语句不算)", name)
			continue
		}
		hasRelease := false
		for _, cmd := range cmds {
			if !strings.Contains(cmd, "-trimpath") {
				t.Errorf("%s 里的 go build 没有 -trimpath: %s", name, cmd)
			}
			if strings.Contains(cmd, releaseBuildCmd) {
				hasRelease = true
				if !strings.Contains(cmd, "./cmd/type") {
					t.Errorf("%s 的发布构建没有指向 ./cmd/type: %s", name, cmd)
				}
			}
		}
		if !hasRelease {
			t.Errorf("%s 里没有同款的发布构建命令行:\n  want 含 %s", name, releaseBuildCmd)
		}
	}
}

// TestBuildScriptKeepsUTF8BOM scripts/build.ps1 的首三字节必须是 UTF-8 BOM。
//
// Windows PowerShell 5.1 对无 BOM 的 .ps1 按系统 ANSI(中文系统是 GBK)解码,
// 中文注释的尾字节会吃掉换行, 把下一行代码并进注释变成死代码 —— ProductVersion
// 的同步曾因此静默失效。verify.yml 里有一条同款检查(量首三字节), 这里再钉一条
// 本地的: 改写脚本之后立刻就能发现, 不必等 CI
func TestBuildScriptKeepsUTF8BOM(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "build.ps1"))
	if err != nil {
		t.Fatalf("读取 scripts/build.ps1 失败: %v", err)
	}
	if !bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		head := raw
		if len(head) > 3 {
			head = head[:3]
		}
		t.Errorf("scripts/build.ps1 的 BOM 丢了: 首三字节 = % X, want EF BB BF", head)
	}
}
