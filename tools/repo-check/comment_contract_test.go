package repocheck

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// longCommentMax 单块连续整行注释的行数上限（docs/comment-style.md「长度预算」）。
const longCommentMax = 10

// TestNoBlockCommentsInGo Go 源码里不许有块注释。
//
// 理由不是审美: 构建契约与 README 树的源码解析只剥整行 //, 块注释会被当成代码
// 参与字面量断言。判定走 go/parser, 别用字符串找 "/*": 那会连字符串字面量里的
// 斜杠星号一起命中。
func TestNoBlockCommentsInGo(t *testing.T) {
	for rel, src := range sourceFiles(t) {
		if !strings.HasSuffix(rel, ".go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, rel, src, parser.ParseComments)
		if err != nil {
			t.Errorf("%s 解析失败: %v", rel, err)
			continue
		}
		for _, cg := range f.Comments {
			if !strings.HasPrefix(cg.List[0].Text, "/*") {
				continue
			}
			t.Errorf("%s:%d 出现块注释: 改用整行 //（docs/comment-style.md「长度预算」）",
				rel, fset.Position(cg.Pos()).Line)
		}
	}
}

// longLineComments 数一段源码里超过 max 行的连续整行 // 注释块
func longLineComments(src string, max int) int {
	run, count := 0, 0
	for _, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			run++
			continue
		}
		if run > max {
			count++
		}
		run = 0
	}
	if run > max {
		count++
	}
	return count
}

// longBlockComments 数超过 max 行的 /* */ 块(前端用; Go 里它们已被上面那条禁用)。
// 只认行首起头的块: 行中起头的那种(代码后跟 /*) 结构上就写不长, 暂不计
func longBlockComments(src string, max int) int {
	run, count := 0, 0
	in := false
	for _, line := range strings.Split(src, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case !in && strings.HasPrefix(trimmed, "/*"):
			if strings.Contains(trimmed[2:], "*/") {
				continue
			}
			in, run = true, 1
		case in:
			run++
			if strings.Contains(trimmed, "*/") {
				in = false
				if run > max {
					count++
				}
			}
		}
	}
	if in && run > max {
		count++
	}
	return count
}

// TestCommentBlockBudget 注释块长度预算: 单块连续注释不许超过 longCommentMax 行。
// 旧的"按文件基线表"已删除: 基线全为 0 之后, 键只剩仪式感; 现在任何被扫到的文件
// 出现超额块都直接报错。
func TestCommentBlockBudget(t *testing.T) {
	for rel, src := range sourceFiles(t) {
		var got int
		if strings.HasSuffix(rel, ".go") {
			got = longLineComments(src, longCommentMax)
		} else {
			got = longLineComments(src, longCommentMax) + longBlockComments(src, longCommentMax)
		}
		if got > 0 {
			t.Errorf("%s 有 %d 个超过 %d 行的注释块: 上移 docs 或拆成多块（docs/comment-style.md「长度预算」）",
				rel, got, longCommentMax)
		}
	}
}

// TestCommentStyleSpecRegistered 规范文件存在, 且以链接形式登记进了 AGENTS.md。
// 光有路径字符串不够: 规矩写在 AGENTS.md 之外而不进文档地图, 下场就是没人读得到
func TestCommentStyleSpecRegistered(t *testing.T) {
	if len(repoFile(t, "docs", "comment-style.md")) == 0 {
		t.Fatal("docs/comment-style.md 是空的")
	}
	if !strings.Contains(repoFile(t, "AGENTS.md"), "](docs/comment-style.md)") {
		t.Error("AGENTS.md 的文档地图里没有指向 docs/comment-style.md 的链接")
	}
}
