// equivcheck — 重构等价性验证: 逐函数比对重构前后的函数体。
// 用 go/parser 取顶层函数(旧实现用正则扫行 + 找行首 "}" 收尾, 遇到函数内的
// 行首大括号就提前截断, 历史 rev 上只能抽出 2 个函数, 结论不可用),
// 函数体按 token 序列比对(字面量保留原文, 只忽略 token 之间的空白):
// 纯搬移的函数应完全一致,
// 被机械变换(改名/方法化/接口调用替换)的函数会列入差异清单, 供人工逐条定位。
//
// 用法:
//
//	go run ./tools/equivcheck <旧rev> <新rev> [选项]
//
//	--old-file <路径>   旧侧源文件路径 (默认 main.go; 结构整理前的历史 rev 用根路径)
//	--renames <路径>    改名映射 JSON ({"旧函数名": "新函数名"}), 默认与本工具同目录的 renames.json
//	--renamed           启用改名映射比对接口化之后的提交
//
// 新侧文件清单不在工具里写死: 直接用 git ls-tree 取该 rev 的全部 .go 文件
// (跳过 _test.go 与 tools/ 下的工具), 因此仓库布局变化后无需同步维护清单。
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

var funcRe = regexp.MustCompile(`^func (?:\([^)]*\) )?([A-Za-z_][A-Za-z0-9_]*)\(`)

func gitShow(rev, path string) (string, error) {
	out, err := exec.Command("git", "show", rev+":"+path).Output()
	if err != nil {
		return "", fmt.Errorf("git show %s:%s: %w", rev, path, err)
	}
	return string(out), nil
}

// gitGoFiles 列出该 rev 中参与比对的 .go 文件
func gitGoFiles(rev string) ([]string, error) {
	out, err := exec.Command("git", "ls-tree", "-r", "--name-only", rev).Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-tree %s: %w", rev, err)
	}
	var files []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasSuffix(line, ".go") || strings.HasSuffix(line, "_test.go") {
			continue
		}
		if strings.HasPrefix(filepath.ToSlash(line), "tools/") {
			continue // 工具自身不属于被验证的业务代码
		}
		files = append(files, line)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s 中未找到可比对的 .go 文件", rev)
	}
	return files, nil
}

// extract 解析源码并返回 {函数名: 函数体文本}。
// 函数体不含签名, 方法化(仅 receiver 变化)不会产生差异
func extract(src string) (map[string]string, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "src.go", src, parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("解析源码失败: %w", err)
	}
	funcs := make(map[string]string)
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue // 无函数体的声明(如汇编桩)不参与比对
		}
		// 用 fset 的偏移切出函数体原文: 只取 { 与 } 之间的内容
		start := fset.Position(fd.Body.Lbrace).Offset
		end := fset.Position(fd.Body.Rbrace).Offset
		if start < 0 || end > len(src) || end <= start {
			continue
		}
		funcs[fd.Name.Name] = src[start:end]
	}
	return funcs, nil
}

// normalize 把函数体化成 token 序列, 作为比对的基准。
//
// 旧实现直接删掉函数体里的全部空白, 那会把字符串字面量内部的空格一并删掉,
// 于是 "剩余 N 秒" 与 "剩余N秒" 被判成完全一致 —— 而界面文案恰恰是本仓库
// 最不该悄悄变动的东西(冻结文案)。改为按 token 比对: 字符串/字符/数字字面量
// 保留原文, 只有 token 之间的空白被丢弃。注释仍按旧规则去掉空白: 注释不影响
// 行为, 而多行块注释的缩进会随 gofmt 变动, 不能算成差异。
func normalize(src string) (string, error) {
	var firstErr error
	var s scanner.Scanner
	fset := token.NewFileSet()
	file := fset.AddFile("body.go", fset.Base(), len(src))
	s.Init(file, []byte(src), func(_ token.Position, msg string) {
		if firstErr == nil {
			firstErr = errors.New(msg)
		}
	}, scanner.ScanComments)

	var b strings.Builder
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.SEMICOLON && lit == "\n" {
			continue // 换行自动插入的分号等价于空白: 留下它会把纯排版调整算成差异
		}
		if tok == token.COMMENT {
			lit = commentNorm.Replace(lit)
		}
		b.WriteString(tok.String())
		if lit != "" {
			b.WriteByte(' ')
			b.WriteString(lit)
		}
		b.WriteByte('\n')
	}
	if firstErr != nil {
		return "", firstErr
	}
	return b.String(), nil
}

// commentNorm 注释文本内的空白: 与旧实现一致地忽略(见 normalize 注释)
var commentNorm = strings.NewReplacer(" ", "", "\t", "", "\r", "", "\n", "")

// origin 记录函数来自哪个文件的第几行, 供差异清单定位
type origin struct {
	file string
	line int
}

func list(items []string) string {
	if len(items) == 0 {
		return "无"
	}
	return strings.Join(items, ", ")
}

const usage = `用法: go run ./tools/equivcheck <旧rev> <新rev> [--old-file <路径>] [--renames <路径>] [--renamed]`

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	oldRev, newRev := os.Args[1], os.Args[2]
	oldFile := "main.go"
	renamesFile := defaultRenamesPath()
	useRenames := false
	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--renamed":
			useRenames = true
		case "--old-file":
			if i+1 >= len(os.Args) {
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
			i++
			oldFile = os.Args[i]
		case "--renames":
			if i+1 >= len(os.Args) {
				fmt.Fprintln(os.Stderr, usage)
				os.Exit(2)
			}
			i++
			renamesFile = os.Args[i]
			useRenames = true
		default:
			fmt.Fprintf(os.Stderr, "未知选项: %s\n%s\n", os.Args[i], usage)
			os.Exit(2)
		}
	}

	// ── 旧侧: 单个文件 ──
	oldSrc, err := gitShow(oldRev, oldFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	old, err := extract(oldSrc)
	if err != nil {
		fmt.Fprintf(os.Stderr, "旧侧 %s: %v\n", oldFile, err)
		os.Exit(1)
	}

	// ── 新侧: 该 rev 的全部业务 .go 文件 ──
	files, err := gitGoFiles(newRev)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	merged := make(map[string]string)
	where := make(map[string]origin)
	var dupes []string
	for _, f := range files {
		src, err := gitShow(newRev, f)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		funcs, err := extract(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "新侧 %s: %v\n", f, err)
			os.Exit(1)
		}
		for name, body := range funcs {
			if prev, exists := where[name]; exists {
				dupes = append(dupes, fmt.Sprintf("%s(%s:%d 与 %s)", name, prev.file, prev.line, f))
			}
			merged[name] = body
			where[name] = origin{file: f, line: bodyLine(src, name)}
		}
	}

	renames := map[string]string{}
	if useRenames {
		if data, err := os.ReadFile(renamesFile); err == nil {
			if err := json.Unmarshal(data, &renames); err != nil {
				fmt.Fprintf(os.Stderr, "改名映射 %s 解析失败: %v\n", renamesFile, err)
				os.Exit(1)
			}
		} else if !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		} else {
			fmt.Fprintf(os.Stderr, "提示: 改名映射 %s 不存在, 按无映射比对\n", renamesFile)
		}
	}
	if useRenames && len(renames) == 0 {
		fmt.Fprintf(os.Stderr, "警告: 已请求 --renamed 但映射为空 (%s), 接口化改名会全部落入缺失清单\n", renamesFile)
	}

	// ── 比对 ──
	var same, diff, missing []string
	for name, body := range old {
		target := name
		if renamed, ok := renames[name]; ok {
			target = renamed
		}
		nb, ok := merged[target]
		if !ok {
			missing = append(missing, name)
			continue
		}
		// 两侧归一化失败宁可报错退出: 静默比对半个 token 流会把差异漏成"一致"
		oldNorm, err := normalize(body)
		if err != nil {
			fmt.Fprintf(os.Stderr, "旧侧 %s 归一化失败: %v\n", name, err)
			os.Exit(1)
		}
		newNorm, err := normalize(nb)
		if err != nil {
			fmt.Fprintf(os.Stderr, "新侧 %s 归一化失败: %v\n", target, err)
			os.Exit(1)
		}
		if oldNorm == newNorm {
			same = append(same, name)
		} else {
			diff = append(diff, name)
		}
	}
	sort.Strings(same)
	sort.Strings(diff)
	sort.Strings(missing)
	sort.Strings(dupes)

	fmt.Printf("%s:%s → %s (%d 个 .go 文件): 旧侧函数总数 %d\n",
		oldRev, oldFile, newRev, len(files), len(old))
	fmt.Printf("  函数体归一化后逐字节一致: %d\n", len(same))
	fmt.Printf("  函数体存在差异(需逐条定位为机械变换): %s\n", list(diff))
	fmt.Printf("  新侧缺失: %s\n", list(missing))
	for _, name := range diff {
		target := name
		if renamed, ok := renames[name]; ok {
			target = renamed
		}
		// 定位用新侧函数名: 接口化改名后旧名在新侧并不存在
		fmt.Printf("    - %s → %s\n", name, where[target].file)
	}
	if len(dupes) > 0 {
		fmt.Printf("  注意: 新侧重名函数(仅比对了后者): %s\n", list(dupes))
	}
	if useRenames {
		fmt.Printf("  已启用改名映射 %s (%d 条)\n", renamesFile, len(renames))
	}
}

// defaultRenamesPath 改名映射的默认位置: 与本工具同目录。
// 工具的工作目录是仓库根, 而映射文件是工具的私有数据, 故按源文件位置定位
func defaultRenamesPath() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "renames.json"
	}
	return filepath.Join(filepath.Dir(file), "renames.json")
}

// bodyLine 返回函数声明所在行(用于差异定位), 找不到时返回 0
func bodyLine(src, name string) int {
	for i, line := range strings.Split(src, "\n") {
		if m := funcRe.FindStringSubmatch(line); m != nil && m[1] == name {
			return i + 1
		}
	}
	return 0
}
