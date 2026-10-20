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

// TestVersionScriptIsSingleSource 版本号提取只有一处实现: scripts/version.ps1。
// 其余位置只允许 dot-source 它并调用 Get-TypeVersion; 任何地方再手写一份提取正则,
// 都会在下一次改规则时静默走散(这正是以前四处副本的教训)。
func TestVersionScriptIsSingleSource(t *testing.T) {
	const pattern = `(?m)^\s*var\s+version\s*=\s*"([\d.]+(?:-[0-9A-Za-z.]+)?)"`
	files := [][]string{
		{"scripts", "version.ps1"},
		{"scripts", "build.ps1"},
		{".github", "workflows", "verify.yml"},
		{".github", "workflows", "ci.yml"},
		{".github", "workflows", "release.yml"},
	}
	withPattern := 0
	for _, parts := range files {
		name := strings.Join(parts, "/")
		src := repoFile(t, parts...)
		if strings.Contains(src, pattern) {
			withPattern++
			if name != "scripts/version.ps1" {
				t.Errorf("%s 里又写了一份版本号提取正则: 应改为 dot-source scripts/version.ps1 再调用 Get-TypeVersion", name)
			}
			continue
		}
		if name == "scripts/version.ps1" {
			t.Errorf("scripts/version.ps1 里找不到版本号提取正则")
			continue
		}
		if !strings.Contains(src, "version.ps1") || !strings.Contains(src, "Get-TypeVersion") {
			t.Errorf("%s 没有 dot-source scripts/version.ps1 并调用 Get-TypeVersion", name)
		}
	}
	if withPattern != 1 {
		t.Errorf("版本号提取正则出现了 %d 处, want 恰好 1 处(scripts/version.ps1)", withPattern)
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

// TestScriptsKeepUTF8BOM scripts/build.ps1 与 scripts/version.ps1 的首三字节必须是
// UTF-8 BOM。Windows PowerShell 5.1 对无 BOM 的 .ps1 按系统 ANSI 解码, 中文注释的
// 尾字节会吃掉换行, 把下一行代码并进注释变成死代码。
func TestScriptsKeepUTF8BOM(t *testing.T) {
	for _, name := range []string{"build.ps1", "version.ps1"} {
		raw, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", name))
		if err != nil {
			t.Fatalf("读取 scripts/%s 失败: %v", name, err)
		}
		if !bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
			head := raw
			if len(head) > 3 {
				head = head[:3]
			}
			t.Errorf("scripts/%s 的 BOM 丢了: 首三字节 = % X, want EF BB BF", name, head)
		}
	}
}
