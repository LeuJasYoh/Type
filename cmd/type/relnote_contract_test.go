//go:build windows && (amd64 || arm64)

// 发布说明契约: "改版本号必须连发布说明一起写"从打标签时刻提前到每次提交 ——
// release.yml 闸门①只在发版时才红, 但这类遗忘在本地 go test 就该抓住。
// 判据与 release.yml 那段同款(存在 + 含 Type-<版本>-windows-); 大小写这里更严一档,
// 那边 Select-String 默认不敏感 —— 只会更严, 不会漏拦。

package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// releaseNotesMismatchFor 检查指定发布说明与版本号是否同步, 返回空串表示通过。
// 拆出路径参数是为了让"文件在、包名没跟着改"这条红路也能被用例钉住
func releaseNotesMismatchFor(notes, ver string) string {
	data, err := os.ReadFile(notes)
	if err != nil {
		return "缺少 " + notes + ": 改版本号必须同一次提交新增 release-notes/v" + ver + ".md, 结构照抄上一版(下载表格+本次变化)"
	}
	if !strings.Contains(string(data), "Type-"+ver+"-windows-") {
		return notes + " 里没有出现 Type-" + ver + "-windows-: 说明里的包名与实际产物对不上(防复制上一版忘改版本号)"
	}
	return ""
}

// releaseNotesMismatch 按版本号拼出仓库里的发布说明路径
func releaseNotesMismatch(ver string) string {
	return releaseNotesMismatchFor(filepath.Join("..", "..", "release-notes", "v"+ver+".md"), ver)
}

// TestReleaseNotesMatchVersion 主用例: main.go 的版本号必须有配得上的发布说明
func TestReleaseNotesMatchVersion(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("读取 cmd/type/main.go 失败: %v", err)
	}
	ms := regexp.MustCompile(versionPattern).FindAllStringSubmatch(string(src), -1)
	if len(ms) != 1 {
		t.Fatalf("main.go 里 var version = 的声明应恰好 1 处, 实际 %d 处: 提取值不可信", len(ms))
	}
	if p := releaseNotesMismatch(ms[0][1]); p != "" {
		t.Fatal(p)
	}
}

// TestReleaseNotesGateRejectsMismatch 反例自检: 缺文件、包名没跟着改这两条红路
// 都必须真的报错 —— 否则主用例只证明"当前恰好对得上", 证明不了闸门会红
func TestReleaseNotesGateRejectsMismatch(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("写临时说明 %s 失败: %v", name, err)
		}
		return p
	}

	good := write("v1.0.0.md", "| `Type-1.0.0-windows-amd64.zip` |")
	if p := releaseNotesMismatchFor(good, "1.0.0"); p != "" {
		t.Errorf("配套正常的说明被判成不同步: %s", p)
	}
	old := write("v1.0.1.md", "| `Type-1.0.0-windows-amd64.zip` |")
	if p := releaseNotesMismatchFor(old, "1.0.1"); p == "" {
		t.Error("说明里只有旧版本包名却判为通过: 复制上一版忘改的红路没被守住")
	}
	if p := releaseNotesMismatchFor(filepath.Join(dir, "v9.9.9.md"), "9.9.9"); p == "" {
		t.Error("发布说明缺失却判为通过: 缺文件的红路没被守住")
	}
}
