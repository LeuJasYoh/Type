// equivcheck — 重构等价性验证: 逐函数比对重构前后的函数体。
// 按 gofmt 布局提取顶层函数, 函数体空白归一化后逐字节比对;
// 纯搬移的函数应完全一致, 被机械变换(改名/方法化/接口调用替换)的
// 函数会列入差异清单, 供人工逐条定位。
//
// 用法: go run ./tools/equivcheck <旧rev> <新rev> [--renamed]
//
//	--renamed 启用接口化改名映射(自由函数→方法), 用于比对接口化之后的提交
package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

// 新侧(当前布局)包含旧 main.go 全部函数的文件清单;
// 比对更早历史提交时旧侧仍读根路径 main.go, 不受布局调整影响
var newFiles = []string{
	"cmd/type/main.go", "cmd/type/typing.go", "cmd/type/win32.go",
	"cmd/type/win32_keyboard.go", "cmd/type/win32_clipboard.go", "cmd/type/win32_window.go",
}

// 接口化提交后的更名: 自由函数 → 方法 (仅签名与调用点变化)
var renames = map[string]string{
	"clipboardSetText":      "SetText",
	"clipboardGetText":      "GetText",
	"clipboardSnapshot":     "Snapshot",
	"restoreSnapshotRaw":    "RestoreSnapshotRaw",
	"sendRune":              "SendRune",
	"sendCtrlV":             "SendPaste",
	"foregroundWindowTitle": "Title",
}

var funcRe = regexp.MustCompile(`^func (?:\([^)]*\) )?([A-Za-z_][A-Za-z0-9_]*)\(`)

func gitShow(rev, path string) (string, error) {
	out, err := exec.Command("git", "show", rev+":"+path).Output()
	if err != nil {
		return "", fmt.Errorf("git show %s:%s: %w", rev, path, err)
	}
	return string(out), nil
}

// extract 提取顶层函数, 返回 {函数名: 函数体(首个 { 之后的部分)}。
// 函数体不含签名, 方法化(仅 receiver 变化)不会产生差异
func extract(src string) map[string]string {
	lines := strings.Split(src, "\n")
	funcs := make(map[string]string)
	for i := 0; i < len(lines); i++ {
		m := funcRe.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		var text string
		trimmed := strings.TrimRight(lines[i], " \t\r")
		if strings.Contains(lines[i], "{") && strings.HasSuffix(trimmed, "}") {
			text = lines[i] // 单行函数: gofmt 不会把 } 换到行首
		} else {
			j := i
			for j < len(lines) && strings.TrimRight(lines[j], " \t\r") != "}" {
				j++
			}
			if j >= len(lines) {
				break // 文件异常截断
			}
			text = strings.Join(lines[i:j+1], "\n")
			i = j
		}
		brace := strings.Index(text, "{")
		funcs[m[1]] = text[brace+1:]
	}
	return funcs
}

// norm 去除全部空白, 使比对只关心 token 序列
func norm(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func list(items []string) string {
	if len(items) == 0 {
		return "无"
	}
	return strings.Join(items, ", ")
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "用法: go run ./tools/equivcheck <旧rev> <新rev> [--renamed]")
		os.Exit(2)
	}
	oldRev, newRev := os.Args[1], os.Args[2]
	useRenames := false
	for _, a := range os.Args[3:] {
		if a == "--renamed" {
			useRenames = true
		}
	}

	oldSrc, err := gitShow(oldRev, "main.go")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	old := extract(oldSrc)

	merged := make(map[string]string)
	for _, f := range newFiles {
		src, err := gitShow(newRev, f)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for k, v := range extract(src) {
			merged[k] = v
		}
	}

	var same, diff, missing []string
	for name, body := range old {
		nn := name
		if useRenames {
			if r, ok := renames[name]; ok {
				nn = r
			}
		}
		nb, ok := merged[nn]
		switch {
		case !ok:
			missing = append(missing, name)
		case norm(body) == norm(nb):
			same = append(same, name)
		default:
			diff = append(diff, name)
		}
	}
	sort.Strings(same)
	sort.Strings(diff)
	sort.Strings(missing)

	fmt.Printf("%s → %s: 旧侧函数总数 %d\n", oldRev, newRev, len(old))
	fmt.Printf("  函数体归一化后逐字节一致: %d\n", len(same))
	fmt.Printf("  函数体存在差异(需逐条定位为机械变换): %s\n", list(diff))
	fmt.Printf("  新侧缺失: %s\n", list(missing))
}
