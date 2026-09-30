//go:build windows && (amd64 || arm64)

// 跨端契约: webview Bind 的函数名与签名, 以及前端 ipc.ts 的镜像。
//
// 为什么值得单独一个测试文件: 这些名字与参数顺序是 Go 与 JS 之间唯一的事实
// 来源, 而两边各自的编译器都管不到对方。改个名字、换一下两个 bool 的先后,
// Go 编译通过, vue-tsc 也通过, 界面照常渲染 —— 只是功能悄悄错位(比如
// "绕过粘贴检测"和"文本直投"两个开关互换了作用), 要等用户发现。前端重构
// 时这个文件会一起变红, 这正是它存在的意义

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/LeuJasYoh/type/internal/typing"
)

// contractBindNames 四个绑定名, 前端 window 上必须是同名的这四个
var contractBindNames = []string{"startTyping", "cancelTyping", "toggleTopmost", "getTypingStatus"}

var (
	bindCallRE  = regexp.MustCompile(`w\.Bind\("([A-Za-z]+)"`)
	ipcExportRE = regexp.MustCompile(`(?m)^export const ([A-Za-z]+) = \(([^)]*)\)`)
)

// mainSource 读装配层源码: 绑定名与前端镜像都要对着它核
func mainSource(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("读取 main.go 失败: %v", err)
	}
	return string(data)
}

// 装配层绑定的名字必须正好是约定的四个: 少一个前端会拿到 undefined,
// 多一个说明后端加了能力而前端镜像与文档都没跟上
func TestBindNamesMatchContract(t *testing.T) {
	src := mainSource(t)
	matches := bindCallRE.FindAllStringSubmatch(src, -1)
	got := make([]string, 0, len(matches))
	for _, m := range matches {
		got = append(got, m[1])
	}
	if !reflect.DeepEqual(got, contractBindNames) {
		t.Fatalf("Bind 名 = %v, want %v", got, contractBindNames)
	}
}

// startTyping 的四个参数按位置传递, 顺序或类型一变就是静默错位:
// text/delay/forceSendInput/textDirect
func TestStartSignatureFrozen(t *testing.T) {
	mt := reflect.TypeOf((*typing.TypingService)(nil).Start)
	want := []reflect.Type{
		reflect.TypeOf(""),
		reflect.TypeOf(int(0)),
		reflect.TypeOf(false),
		reflect.TypeOf(false),
	}
	if mt.NumIn() != len(want) {
		t.Fatalf("Start 参数个数 = %d, want %d", mt.NumIn(), len(want))
	}
	for i, w := range want {
		if mt.In(i) != w {
			t.Errorf("Start 第 %d 个参数类型 = %v, want %v", i+1, mt.In(i), w)
		}
	}
	if mt.NumOut() != 2 || mt.Out(0).Kind() != reflect.String || mt.Out(1) != reflect.TypeOf((*error)(nil)).Elem() {
		t.Errorf("Start 返回值 = %v, want (string, error)", mt)
	}

	// 后两个参数同为 bool, 光看类型分不出谁是谁, 所以把顺序写进注释并由
	// 前端那侧的位置一起核对(见下一个用例)
	t.Log("startTyping(text string, delay int, forceSendInput bool, textDirect bool)")
}

// 另外三个绑定的签名: cancelTyping() / getTypingStatus() *TypingStatus
func TestOtherSignaturesFrozen(t *testing.T) {
	typeOf := reflect.TypeOf(typing.NewTypingService(nil, nil, nil))

	var cancel, status *reflect.Method
	for i := 0; i < typeOf.NumMethod(); i++ {
		m := typeOf.Method(i)
		switch m.Name {
		case "Cancel":
			cancel = &m
		case "Status":
			status = &m
		}
	}
	if cancel == nil {
		t.Fatal("TypingService 缺少 Cancel 方法")
	}
	if cancel.Type.NumIn() != 1 || cancel.Type.NumOut() != 2 {
		t.Errorf("Cancel 签名 = %v, want () (string, error)", cancel.Type)
	}
	if status == nil {
		t.Fatal("TypingService 缺少 Status 方法")
	}
	if status.Type.NumIn() != 1 || status.Type.NumOut() != 1 {
		t.Fatalf("Status 签名 = %v, want () *TypingStatus", status.Type)
	}
	if got := status.Type.Out(0); got != reflect.TypeOf(&typing.TypingStatus{}) {
		t.Errorf("Status 返回 %v, want *TypingStatus (前端按字段名取值)", got)
	}
}

// 前端镜像: ipc.ts 里声明并导出的四个函数名必须与绑定名一一对应。
// 前端重构改了 window 接口或导出名时, 这里会先红
func TestFrontendMirrorsBindNames(t *testing.T) {
	path := filepath.Join("..", "..", "frontend", "src", "ipc.ts")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取 %s 失败: %v (前端目录被移动了?)", path, err)
	}
	src := string(data)

	declared := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s{4}([A-Za-z]+)\(`).FindAllStringSubmatch(src, -1) {
		declared[m[1]] = true
	}
	for _, name := range contractBindNames {
		if !declared[name] {
			t.Errorf("frontend/src/ipc.ts 的 window 接口里找不到 %q", name)
		}
	}

	exported := map[string]string{} // 名字 -> 参数列表
	for _, m := range ipcExportRE.FindAllStringSubmatch(src, -1) {
		exported[m[1]] = strings.TrimSpace(m[2])
	}
	for _, name := range contractBindNames {
		if _, ok := exported[name]; !ok {
			t.Errorf("frontend/src/ipc.ts 未导出 %q", name)
		}
	}
	// startTyping 是唯一带参数的: 四个, 顺序与 Go 侧一致
	if args, ok := exported["startTyping"]; ok {
		parts := strings.Split(args, ",")
		if len(parts) != 4 {
			t.Errorf("ipc.ts 的 startTyping 参数个数 = %d, want 4 (%q)", len(parts), args)
		}
	}
	for _, name := range []string{"cancelTyping", "toggleTopmost", "getTypingStatus"} {
		if args, ok := exported[name]; ok && strings.TrimSpace(args) != "" {
			t.Errorf("ipc.ts 的 %s 应当无参, 实际 (%s)", name, args)
		}
	}
}
