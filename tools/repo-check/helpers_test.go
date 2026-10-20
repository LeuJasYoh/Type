package repocheck

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRoot 返回仓库根。测试进程的工作目录是 tools/repo-check。
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("定位仓库根失败: %v", err)
	}
	return root
}

// repoFile 读仓库内文本文件（路径相对仓库根，逐段传入）。
func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{repoRoot(t)}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v (文件被移动了?)", strings.Join(parts, "/"), err)
	}
	return string(data)
}

// normalizeNewlines 归一换行: 仓库存 LF, Windows 上的检出可能是 CRLF。
func normalizeNewlines(src string) string {
	return strings.ReplaceAll(src, "\r\n", "\n")
}

// sourceDirs 参与治理扫描的源码目录。
var sourceDirs = [][]string{
	{"cmd"}, {"internal"}, {"tools"},
	{"frontend", "src"}, {"frontend", "test"}, {"frontend", "tools"},
}

// sourceExts 参与注释类检查的源码扩展名。
var sourceExts = map[string]bool{".go": true, ".ts": true, ".vue": true, ".mjs": true}

// sourceFiles 返回 相对路径(斜杠形式) -> 文件内容。
func sourceFiles(t *testing.T) map[string]string {
	t.Helper()
	root := repoRoot(t)
	out := make(map[string]string)
	for _, parts := range sourceDirs {
		base := filepath.Join(append([]string{root}, parts...)...)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", ".venv", ".git", ".workbuddy":
					return fs.SkipDir
				}
				return nil
			}
			if !sourceExts[filepath.Ext(path)] {
				return nil
			}
			data, rerr := os.ReadFile(path)
			if rerr != nil {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return nil
			}
			out[filepath.ToSlash(rel)] = string(data)
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s 失败: %v", filepath.Join(parts...), err)
		}
	}
	if len(out) == 0 {
		t.Fatal("一个源码文件都没扫到: 目录列表过期了?")
	}
	return out
}

// markdownFiles 返回参与文档检查的 md 文件绝对路径。范围是 docs/、release-notes/、
// tools/ 下的 md 加上根目录的 README.md 与 AGENTS.md。
func markdownFiles(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	var files []string
	walk := func(parts ...string) {
		base := filepath.Join(append([]string{root}, parts...)...)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", ".venv", ".git", ".workbuddy":
					return fs.SkipDir
				}
				return nil
			}
			if filepath.Ext(path) == ".md" {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s 失败: %v", filepath.Join(parts...), err)
		}
	}
	walk("docs")
	walk("release-notes")
	walk("tools")
	files = append(files, filepath.Join(root, "README.md"), filepath.Join(root, "AGENTS.md"))
	return files
}
