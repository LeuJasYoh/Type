// pecheck — 构建产物读回校验(构建/CI 用, 不进产品链路)
//
// 图标、版本信息与 DPI manifest 由 tools/mkres 在构建期生成 .syso, 而 .syso
// 不入库: go build 在它缺失或架构不匹配时不会失败, 产物只是悄悄少了图标、
// 版本号与 manifest —— 直到用户看到空白图标、属性页版本号为空, 或在高分屏上
// 整个窗口发虚才暴露。本工具直接读 exe 的 PE 头与资源节, 把"该在的东西在
// 不在"变成一条会失败的检查; CI 的构建矩阵与本地发版验收共用同一份判据
// (build.ps1 末尾的版本读回是它的 PowerShell 前身, 只查 FileVersion)。
//
// 用法:
//
//	go run ./tools/pecheck -exe Type.exe -version 1.5.3 -arch amd64
//
// 校验项:
//   - PE 机器类型与 -arch 一致(amd64 = 0x8664, arm64 = 0xAA64)
//   - 版本资源: FileVersion 数字段 = 版本号数字部分(补足四位), 字符串表中的
//     FileVersion / ProductVersion 与版本号一致
//   - manifest 资源: dpiAwareness 为 permonitorv2,system(高分屏不糊)、
//     requestedExecutionLevel 为 asInvoker(本程序按设计以普通权限运行)
//   - 图标资源: RT_GROUP_ICON 存在且非空。不数图标帧数 —— 那是
//     assets/icon.ico 的属性, 重新生成图标就会变, 与"资源有没有链进 exe"无关
package main

import (
	"bytes"
	"debug/pe"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

func main() {
	exe := flag.String("exe", "", "待校验的 exe 路径(必填)")
	ver := flag.String("version", "", "期望版本号, 如 1.5.3 或 1.5.0-rc.1(必填, 单一来源是 cmd/type/main.go)")
	arch := flag.String("arch", "", "期望架构: amd64 或 arm64(必填)")
	flag.Parse()

	if err := run(*exe, *ver, *arch); err != nil {
		fmt.Fprintln(os.Stderr, "pecheck:", err)
		os.Exit(1)
	}
	fmt.Printf("pecheck: %s 校验通过 (架构 %s, 版本 %s; 图标/版本/manifest 均已链入)\n", *exe, *arch, *ver)
}

// machineByArch PE 的 Machine 字段值(winnt.h 的 IMAGE_FILE_MACHINE_*)
var machineByArch = map[string]uint16{
	"amd64": pe.IMAGE_FILE_MACHINE_AMD64,
	"arm64": pe.IMAGE_FILE_MACHINE_ARM64,
}

func run(path, ver, arch string) error {
	if path == "" || ver == "" || arch == "" {
		return errors.New("三个参数都必填 (用法见文件头注释)")
	}

	machine, err := peMachine(path)
	if err != nil {
		return err
	}
	want, ok := machineByArch[arch]
	if !ok {
		return fmt.Errorf("未知架构 %q (可用: amd64, arm64)", arch)
	}
	if machine != want {
		return fmt.Errorf("PE 机器类型 = 0x%04X, want 0x%04X (%s): 产物架构不对", machine, want, arch)
	}

	rs, err := loadResources(path)
	if err != nil {
		return err
	}
	if err := checkVersion(rs, ver); err != nil {
		return err
	}
	if err := checkManifest(rs); err != nil {
		return err
	}
	return checkIcon(rs)
}

// peMachine 读 COFF 头的 Machine 字段
func peMachine(path string) (uint16, error) {
	f, err := pe.Open(path)
	if err != nil {
		return 0, fmt.Errorf("打开 PE %s: %w", path, err)
	}
	defer f.Close()
	return f.FileHeader.Machine, nil
}

func loadResources(path string) (*winres.ResourceSet, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rs, err := winres.LoadFromEXE(f)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 的资源节: %w", path, err)
	}
	return rs, nil
}

// firstResource 取某类型下第一个非空资源的数据。
// 按类型遍历而不是按 (id, 语言) 取: mkres 写入的语言/ID 组合不是本工具
// 需要复述的知识, 少一处会在改资源生成时失同步的常量
func firstResource(rs *winres.ResourceSet, typeID winres.Identifier) []byte {
	var data []byte
	rs.WalkType(typeID, func(_ winres.Identifier, _ uint16, d []byte) bool {
		if len(d) > 0 {
			data = d
			return false // 命中即停
		}
		return true
	})
	return data
}

func checkVersion(rs *winres.ResourceSet, ver string) error {
	wantFixed, err := fileVersion(ver)
	if err != nil {
		return err
	}
	raw := firstResource(rs, winres.RT_VERSION)
	if raw == nil {
		return errors.New("版本资源缺失: .syso 没被链接进 exe? (先跑 tools/mkres 生成, 且架构要与目标一致)")
	}
	info, err := version.FromBytes(raw)
	if err != nil {
		return fmt.Errorf("解析版本资源: %w", err)
	}
	if info.FileVersion != wantFixed {
		return fmt.Errorf("版本资源 FileVersion = %v, want %v", info.FileVersion, wantFixed)
	}
	table := info.Table().GetMainTranslation()
	if table == nil {
		return errors.New("版本资源缺少字符串表")
	}
	// mkres 写入的两条字符串: FileVersion 是补足四位的数字形式, ProductVersion 是完整版本号
	if got := table[version.FileVersion]; got != fixedString(wantFixed) {
		return fmt.Errorf("版本资源 FileVersion 字符串 = %q, want %q", got, fixedString(wantFixed))
	}
	if got := table[version.ProductVersion]; got != ver {
		return fmt.Errorf("版本资源 ProductVersion 字符串 = %q, want %q", got, ver)
	}
	return nil
}

// checkManifest 校验 DPI 感知与执行级别声明。缺 DPI 声明的后果见
// win32_window.go 的 scaledForDPI: 缩放非 100% 的显示器上整窗被位图拉伸
func checkManifest(rs *winres.ResourceSet) error {
	raw := firstResource(rs, winres.RT_MANIFEST)
	if raw == nil {
		return errors.New("manifest 资源缺失: 进程会失去 PerMonitorV2 DPI 感知, 高分屏上整个窗口发虚")
	}
	// 资源里是 UTF-8 XML(可能带结尾 NUL): 去掉 NUL 并压掉空白后再找, 不
	// 依赖 winres 的换行/缩进细节
	xml := string(bytes.ReplaceAll(raw, []byte{0}, nil))
	compact := strings.Join(strings.Fields(xml), "")
	for _, want := range []string{
		">permonitorv2,system<", // dpiAwareness 的取值(带 system 回退)
		`level="asInvoker"`,     // 普通权限运行: 提权会改变 UIPI 判定, 使既有的失败提示失真
	} {
		if !strings.Contains(compact, want) {
			return fmt.Errorf("manifest 中未找到 %s: 资源内容与 tools/mkres 的声明不一致", want)
		}
	}
	return nil
}

func checkIcon(rs *winres.ResourceSet) error {
	if data := firstResource(rs, winres.RT_GROUP_ICON); data == nil {
		return errors.New("图标资源(RT_GROUP_ICON)缺失或为空: assets/icon.ico 没被 mkres 写进 .syso?")
	}
	return nil
}

// fileVersion 把版本号化成资源固定区的四位数字形式:
// "1.5.3" -> [1 5 3 0], "1.5.0-rc.1" -> [1 5 0 0]
// (预发布后缀不能进数字字段; 与 tools/mkres、build.ps1 同一规则)
func fileVersion(ver string) ([4]uint16, error) {
	base := ver
	if i := strings.IndexByte(base, '-'); i >= 0 {
		base = base[:i] // 先剥后缀再切分: "1.5.0-rc.1" 按点切会多出一段
	}
	parts := strings.Split(base, ".")
	if len(parts) > 4 {
		return [4]uint16{}, fmt.Errorf("版本号 %q 的段数超过 4", ver)
	}
	var out [4]uint16
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || n > 0xFFFF {
			return [4]uint16{}, fmt.Errorf("版本号 %q 的第 %d 段无法解析", ver, i+1)
		}
		out[i] = uint16(n)
	}
	return out, nil
}

func fixedString(v [4]uint16) string {
	return fmt.Sprintf("%d.%d.%d.%d", v[0], v[1], v[2], v[3])
}
