//go:build windows && (amd64 || arm64)

// tools/wmcharprobe 的 direct 模式镜像产品的文本直投算法, 而它不进产品链路、也不
// 被产品的编译器管到: 产品侧改了分支, 探针照旧跑、照旧"实测通过", 结论却是拿旧
// 算法测出来的。这条闸门只钉"两边仍有同名的两个分支、Esc 先于回车", 更深的一致性
// 仍靠人 —— 它是弱闸门, 别把它当成"算法相同"的证明。
// 见 docs/invariants.md「文本直投（v1.5.0，WM_CHAR 文本层注入）」

package main

import (
	"strings"
	"testing"
)

// probeDirectBody 取探针里 sendDirect 的函数体: 从 func 那行起, 到行首的 } 为止
func probeDirectBody(src string) string {
	// 归一换行: Windows 上的检出是 CRLF, 拿 "\n}\n" 去找函数体末尾会落空
	src = strings.ReplaceAll(src, "\r\n", "\n")
	i := strings.Index(src, "func sendDirect(")
	if i < 0 {
		return ""
	}
	rest := src[i:]
	if j := strings.Index(rest, "\n}\n"); j >= 0 {
		return rest[:j]
	}
	return rest
}

func TestWmcharprobeMirrorsTextDirectBranches(t *testing.T) {
	probe := repoFile(t, "tools", "wmcharprobe", "main.go")
	// 探针必须自称镜像产品算法: 不自称的那天, 这条闸门的前提就不成立了
	if !strings.Contains(probe, "镜像产品") {
		t.Error("wmcharprobe 的文件头不再声称镜像产品算法: 要么补回来, 要么改这条闸门")
	}
	body := probeDirectBody(probe)
	if body == "" {
		t.Fatal("wmcharprobe 里找不到 sendDirect: 探针改了结构, 这条闸门要跟着改")
	}
	// 两个分支与产品对齐: 换行走真按键且 Esc 先于回车, 其余字符走文本层
	if !strings.Contains(body, `r == '\n'`) {
		t.Error("sendDirect 没有换行分支: 产品对 '\\n' 走 Esc + 回车, 探针会测出另一种结果")
	}
	if esc, ret := strings.Index(body, "VK_ESCAPE"), strings.Index(body, "VK_RETURN"); esc < 0 || ret < 0 || esc > ret {
		t.Error("sendDirect 的换行分支不是 Esc 先于回车: 与产品的 sendEscaped 不一致(弹窗关不掉)")
	}
	if !strings.Contains(body, "sendRuneMsg") {
		t.Error("sendDirect 的非换行字符没走 WM_CHAR(sendRuneMsg): 与产品的 SendText 不一致")
	}

	// 产品侧那两个分支也要还在 —— 它们一旦改掉, 上面几条的"一致"就成了过时的对照
	product := packageGoSource(t, "internal", "typing")
	for _, need := range []string{`r.textDirect && ru == '\n'`, "s.injector.SendText(ru)"} {
		if !strings.Contains(product, need) {
			t.Errorf("internal/typing 里找不到 %q: 文本直投的分支变了, 探针与这条闸门都要跟着改", need)
		}
	}
}
