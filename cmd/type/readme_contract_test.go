//go:build windows && (amd64 || arm64)

// README 项目结构树的守卫: 树上写着的每个文件都得真有, 产品代码目录里的每个文件
// 都得在树上出现。手工同步必然漂移(上一次拆分就漏了三个新文件), 交给闸门来记

package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// treeFileExts 参与双向核对的扩展名。刻意不含 .exe / .syso: 它们是构建产物,
// 在 CI 的干净检出里不存在, 拿它们做存在性断言只会假红
var treeFileExts = map[string]bool{
	".go": true, ".ts": true, ".vue": true, ".mjs": true,
	".css": true, ".html": true, ".json": true, ".ps1": true,
}

// normalizeNewlines 归一换行: 仓库里存 LF, 而 `* text=auto` 会让 Windows 上的检出
// 变成 CRLF(CI 的 runner 就是), 拿 "\n" 去匹配多行字面量在那里一律落空
func normalizeNewlines(src string) string {
	return strings.ReplaceAll(src, "\r\n", "\n")
}

// readmeTreeBlock 取 README 里那段目录树(以 "Type/" 开头的围栏代码块)
func readmeTreeBlock(src string) string {
	src = normalizeNewlines(src)
	head := "```\nType/\n"
	i := strings.Index(src, head)
	if i < 0 {
		return ""
	}
	rest := src[i+len(head):]
	if j := strings.Index(rest, "```"); j >= 0 {
		return rest[:j]
	}
	return ""
}

// treeEntryNames 取树里每个条目的名字: "├── xxx ← 说明" 与 "└── xxx/" 里的 xxx
func treeEntryNames(block string) []string {
	var out []string
	for _, line := range strings.Split(block, "\n") {
		rest, ok := "", false
		for _, m := range []string{"├── ", "└── "} {
			if i := strings.Index(line, m); i >= 0 {
				rest, ok = line[i+len(m):], true
				break
			}
		}
		if !ok {
			continue
		}
		name := strings.TrimSpace(rest)
		// 截掉 "← 说明" 与 "go.mod / go.sum" 这类并列写法, 只留第一个名字
		if i := strings.Index(name, " "); i >= 0 {
			name = name[:i]
		}
		name = strings.TrimSuffix(name, "/")
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// repoBaseNames 全仓文件名(不含目录)的集合, 供"树上的文件是不是真有"核对
func repoBaseNames(t *testing.T) map[string]bool {
	t.Helper()
	root := filepath.Join("..", "..")
	skip := map[string]bool{".git": true, "node_modules": true, ".venv": true, ".workbuddy": true}
	have := make(map[string]bool)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if skip[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		have[d.Name()] = true
		return nil
	})
	if err != nil {
		t.Fatalf("遍历仓库失败: %v", err)
	}
	return have
}

// 归一换行不能去掉: 本地(仓库存 LF)跑得过的解析器, 在 CI 的 CRLF 检出下会一条
// 条目都解析不出来 —— 2026-10 的 CI 失败就是这么来的(两个测试同时红)
func TestTreeBlockParserHandlesCRLF(t *testing.T) {
	const block = "```\nType/\n├── cmd/type/\n│   ├── main.go   ← 装配\n│   └── contract_test.go\n```"
	if got := len(treeEntryNames(readmeTreeBlock(block))); got != 3 {
		t.Errorf("LF 下解析出 %d 条, want 3", got)
	}
	crlf := strings.ReplaceAll(block, "\n", "\r\n")
	if got := len(treeEntryNames(readmeTreeBlock(crlf))); got != 3 {
		t.Errorf("CRLF 下解析出 %d 条, want 3: 换行没归一", got)
	}
}

// 树上写着的每个文件都得真有: 文件改名或删掉之后, 树上的那行就成了谎话
func TestReadmeStructureTreeFilesExist(t *testing.T) {
	block := readmeTreeBlock(repoFile(t, "README.md"))
	names := treeEntryNames(block)
	if len(names) == 0 {
		t.Fatal("README 里没解析出目录树条目: 围栏或缩进格式变了, 这条闸门要跟着改")
	}
	have := repoBaseNames(t)
	for _, name := range names {
		// 树上有时写相对路径(如 "dist/index.html"), 取末段才是文件名
		name = filepath.Base(name)
		if !treeFileExts[filepath.Ext(name)] {
			continue
		}
		if !have[name] {
			t.Errorf("README 结构树里的 %s 在仓库中不存在: 文件被改名或删了", name)
		}
	}
}

// 产品代码目录里的每个文件都要在树上出现: 新增文件忘了补树, 这里先红
// (覆盖范围与 .gitattributes 的语言构成口径一致: 只算产品代码)
func TestReadmeStructureTreeCoversProductFiles(t *testing.T) {
	block := readmeTreeBlock(repoFile(t, "README.md"))
	root := filepath.Join("..", "..")

	// 逐个目录要覆盖的文件: 目录 -> 是否递归
	dirs := []struct {
		parts     []string
		recursive bool
	}{
		{[]string{"cmd", "type"}, false},
		{[]string{"internal"}, true},
		{[]string{"frontend", "src"}, true},
		{[]string{"tools"}, false},
		{[]string{"assets"}, false},
	}
	for _, d := range dirs {
		base := filepath.Join(append([]string{root}, d.parts...)...)
		err := filepath.WalkDir(base, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if e.IsDir() {
				if !d.recursive && path != base {
					return fs.SkipDir
				}
				return nil
			}
			name := e.Name()
			// .syso 是构建期生成的资源(不入库), 不要求进树
			if filepath.Ext(name) == ".syso" {
				return nil
			}
			if !strings.Contains(block, name) {
				rel, _ := filepath.Rel(root, path)
				t.Errorf("README 结构树里没有 %s: 新增的文件要一并补进树", filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s 失败: %v", filepath.Join(d.parts...), err)
		}
	}
	// 嵌入产物单独点名: 它在 internal/web/dist 下, 而那个目录只有它入库
	if html := filepath.Join(root, "internal", "web", "dist", "index.html"); true {
		if _, err := os.Stat(html); err == nil && !strings.Contains(block, "index.html") {
			t.Error("README 结构树里没有 internal/web/dist/index.html")
		}
	}
}
