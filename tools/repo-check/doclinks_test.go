package repocheck

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// 跨文件指针的冻结格式: 见 docs/<文件名>.md「<小节标题>」。
// 先用只认路径的正则抓出所有 docs/ 引用，再要求全部命中冻结格式 —— 裸引用不是"宽松",
// 是整条逃检; 只数个数也漏掉另一头(指向不存在的文档, 计数不变)。
var (
	docRefAnyRE    = regexp.MustCompile(`见[ \t\x{3000}]+(docs/[A-Za-z0-9._/-]+\.md)`)
	docRefStrictRE = regexp.MustCompile(`见[ \t\x{3000}]+(docs/[A-Za-z0-9._/-]+\.md)「([^」]+)」`)
	mdLinkRE       = regexp.MustCompile(`\]\(([^()\s#]+)(?:#[^()\s]*)?\)`)
)

// headingExists 小节名必须是文档里的一个 Markdown 标题。允许标题以引用名开头:
// 「焦点锁定与漂移防护」命中「焦点锁定与漂移防护（v1.5.3）」;
// 「行为契约（冻结，改动需双端同步）」命中同名标题。
func headingExists(doc, section string) bool {
	for _, line := range strings.Split(normalizeNewlines(doc), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "#") {
			continue
		}
		title := strings.TrimSpace(strings.TrimLeft(line, "#"))
		if title == section || strings.HasPrefix(title, section) {
			return true
		}
	}
	return false
}

// TestDocSectionReferencesResolve 源码注释里的跨文件指向必须真的指得到。
// 2026-10 把 AGENTS.md 拆成 docs/ 之后, 这些指针是后来者唯一的线索: 文档再拆、
// 小节改名, 就会变成空指针而没有任何环节会失败。
func TestDocSectionReferencesResolve(t *testing.T) {
	root := repoRoot(t)
	checked := 0
	for rel, src := range sourceFiles(t) {
		all := len(docRefAnyRE.FindAllString(src, -1))
		strictMatches := docRefStrictRE.FindAllStringSubmatch(src, -1)
		if bare := all - len(strictMatches); bare > 0 {
			t.Errorf("%s 有 %d 条裸引用: 改写成「见 docs/某文档.md「某小节」」这种格式", rel, bare)
		}
		for _, m := range strictMatches {
			checked++
			docPath, section := m[1], m[2]
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(docPath)))
			if err != nil {
				t.Errorf("%s 指向 %s, 但该文件不存在", rel, docPath)
				continue
			}
			if !headingExists(string(data), section) {
				t.Errorf("%s 指向 %s「%s」, 但那里没有这一节的标题(改名了? 拆走了?)", rel, docPath, section)
			}
		}
	}
	// 文档之间用同样格式写的指针也要能解析(不要求文档正文也守冻结格式)
	for _, path := range markdownFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		for _, m := range docRefAnyRE.FindAllStringSubmatch(string(data), -1) {
			full := filepath.Join(root, filepath.FromSlash(m[1]))
			if _, err := os.Stat(full); err != nil {
				t.Errorf("%s 指向 %s, 但该文件不存在", filepath.ToSlash(rel), m[1])
			}
		}
	}
	if checked == 0 {
		t.Error("一条跨文件文档指向都没扫到: 正则或目录列表过期了?")
	}
	t.Logf("已核对 %d 条跨文件文档指向", checked)
}

// TestMarkdownLinksResolve 仓库内 markdown 的相对链接必须指向真实存在的文件。
// 范围是 docs/、release-notes/、tools/ 下的 md 与根目录 README/AGENTS。
// 跳出仓库的相对路径(如 README 里 GitHub 语义的 ../../issues)不检查。
func TestMarkdownLinksResolve(t *testing.T) {
	root := repoRoot(t)
	checked := 0
	for _, path := range markdownFiles(t) {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		dir := filepath.Dir(path)
		for _, m := range mdLinkRE.FindAllStringSubmatch(normalizeNewlines(string(data)), -1) {
			target := m[1]
			if strings.Contains(target, "://") || strings.HasPrefix(target, "mailto:") {
				continue
			}
			if strings.HasPrefix(target, "/") {
				continue // GitHub 站点绝对路径, 不是仓库内文件
			}
			resolved := filepath.Clean(filepath.Join(dir, filepath.FromSlash(target)))
			inRepo, rerr := filepath.Rel(root, resolved)
			if rerr != nil || strings.HasPrefix(inRepo, "..") {
				continue // 跳出仓库的相对链接按 GitHub 网页语义处理
			}
			checked++
			if _, serr := os.Stat(resolved); serr != nil {
				t.Errorf("%s 的链接 %s 指向不存在的文件", filepath.ToSlash(rel), target)
			}
		}
	}
	if checked == 0 {
		t.Error("一条本地 markdown 链接都没扫到: 正则或扫描范围过期了?")
	}
	t.Logf("已核对 %d 条本地 markdown 链接", checked)
}
