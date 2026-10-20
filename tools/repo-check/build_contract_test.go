package repocheck

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// versionPattern 是构建链共用的版本号提取正则(逐字): 必须锚定行首 var version = 且
// 大小写敏感 —— version 是常见词, 无锚点的子串匹配会命中注释里的同形写法, 或将来
// 某个 minXxxVersion = "…"; 下游(标签闸门/资源/包名/package.json)全用这个提取值。
const versionPattern = `(?m)^\s*var\s+version\s*=\s*"([\d.]+(?:-[0-9A-Za-z.]+)?)"`

// versionCountGuard 是各调用点都有的"恰好一处"断言: 匹配到 0 处或多处时取到的
// 值就不可信, 宁可当场失败。
const versionCountGuard = "Count -ne 1"

// releaseBuildCmd 发布构建命令行的公共前缀, 各处必须同款。三个参数都不会让
// go build 失败, 所以只能靠比对来守: -trimpath 少了产物嵌着构建机绝对路径;
// -H windowsgui 少了产物带控制台黑框; -s -w 少了带符号表、体积明显变大。
const releaseBuildCmd = `go build -trimpath -ldflags="-H windowsgui -s -w"`

// codeLines 去掉整行注释后返回剩下的行。断言必须落在代码行上: 只说"文件里出现过
// 某段文本"会被注释满足（独立验证实测过), 那就又变成一条假报警器了。
func codeLines(src string) []string {
	var out []string
	for _, line := range strings.Split(normalizeNewlines(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		out = append(out, line)
	}
	return out
}

// buildCommandLines 抽出"真正要执行的 go build 命令行": 注释已去掉, 且整行行首
// 就是 go build。用行首判定是为了不把 throw "go build 失败" 这类语句算成构建命令。
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

// TestVersionPatternSingleSource 各处版本号提取正则必须逐字相同, 且都带
// "恰好一处"的断言与大小写敏感开关。
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

// TestReleaseBuildCommandSingleSource 各处发布构建命令行必须同款, 且都构建
// ./cmd/type 这个包; 另外每一处 go build 都必须带 -trimpath。
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
// Windows PowerShell 5.1 对无 BOM 的 .ps1 按系统 ANSI 解码, 中文注释的尾字节会
// 吃掉换行, 把下一行代码并进注释变成死代码。
func TestBuildScriptKeepsUTF8BOM(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "build.ps1"))
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
