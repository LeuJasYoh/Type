// equivcheck — 重构等价性验证: 逐函数比对重构前后的函数体。
// 纯搬移的函数应完全一致; 被机械变换(改名/方法化/接口调用替换)的函数会列入
// 差异清单, 供人工逐条定位。
//
// 用法: go run ./tools/equivcheck <旧rev> <新rev> [--old-file <路径>] [--renamed] [--renames <JSON>]
// --old-file 默认 cmd/type/main.go; --renames 默认与本工具同目录的 renames.json
//
// 边界(别拿"逐字节一致"当零行为变化的唯一证据)见 docs/invariants.md「其它不变量」
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

// gitGoFiles 列出该 rev 中参与比对的 .go 文件。清单现取(不写死), 布局变化无需同步
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

// extract 解析源码并返回 {函数键: 函数体文本}。函数体不含签名, 方法化(仅 receiver
// 变化)不会产生差异。视野边界(常量/签名/字段都看不见)见 docs/invariants.md「其它不变量」
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
		funcs[funcKey(fd)] = src[start:end]
	}
	return funcs, nil
}

// funcKey 函数在比对表里的键: 带 receiver 的方法用 "Receiver.名字"。
// 只用裸函数名时, 不同结构体的同名方法会互相覆盖, 被覆盖的那个永远不参与比对。
// 同名但构建标签互斥的文件(devserver_dev.go 与 devserver_prod.go)仍会判重名,
// 由末尾的"新侧重名函数"一行提示出来
func funcKey(fd *ast.FuncDecl) string {
	name := fd.Name.Name
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return name
	}
	// receiver 可能是 *T / T / T[P] / *T[P]: 剥掉指针与类型参数, 取类型名
	t := fd.Recv.List[0].Type
	for {
		switch x := t.(type) {
		case *ast.StarExpr:
			t = x.X
			continue
		case *ast.IndexExpr:
			t = x.X
			continue
		case *ast.IndexListExpr:
			t = x.X
			continue
		}
		break
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name + "." + name
	}
	return name // 认不出的 receiver 形状: 退回裸名, 不硬凑一个会走散的键
}

// normalize 把函数体化成 token 序列, 作为比对的基准: 字面量保留原文, 只有 token
// 之间的空白被丢弃。别改回"删掉全部空白" —— 那会把字符串字面量内部的空格一并删掉,
// 冻结文案会被误判成完全一致(理由见 docs/invariants.md「其它不变量」)。
// 注释仍按旧规则忽略空白: 多行块注释的缩进会随 gofmt 变动, 不能算成差异
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
	// 默认旧侧路径要跟当前布局一致(代码搬进 cmd/type 之后, 写 "main.go" 会让默认用法
	// 直接死在 git show 上); 旧侧一次只读一个文件, 多文件重构要逐个 --old-file
	oldFile := "cmd/type/main.go"
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
		fmt.Fprintf(os.Stderr, "旧侧 %s@%s 读取失败: %v\n", oldFile, oldRev, err)
		fmt.Fprintln(os.Stderr, "提示: --old-file 指定旧侧路径(默认 cmd/type/main.go); 旧侧一次只比一个文件,")
		fmt.Fprintln(os.Stderr, "      重构跨了多个文件时要逐个跑(例: --old-file internal/typing/typing.go)")
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

// bodyLine 返回函数声明所在行(用于差异定位), 找不到时返回 0。
// 键可能带 "Receiver." 前缀, 比对行首的函数名时只取最后一段
func bodyLine(src, key string) int {
	name := key
	if i := strings.LastIndex(key, "."); i >= 0 {
		name = key[i+1:]
	}
	for i, line := range strings.Split(src, "\n") {
		if m := funcRe.FindStringSubmatch(line); m != nil && m[1] == name {
			return i + 1
		}
	}
	return 0
}
