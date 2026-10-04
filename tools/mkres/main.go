// mkres 生成 Windows 资源目标文件 (.syso): 应用图标 + 版本信息 + DPI 感知 manifest。
// 取代 windres + assets/version.rc, 构建链因此不含 C 编译器:
// 见 docs/architecture.md「工具链分界（勿混用）」。
//
// 用法:
//
//	go run ./tools/mkres -version 1.5.1 -icon assets/icon.ico -out cmd/type/version
//
// 产出 <out>_amd64.syso 与 <out>_arm64.syso: go build 按目标架构各取所需(.syso 不入库)。
package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

// 版本资源的静态串, 原样搬自被删除的 assets/version.rc
const (
	companyName  = "LeuJasYoh"
	fileDesc     = "Type - Keyboard Input Simulator"
	internalName = "Type"
	legalCopy    = "Copyright (C) 2025-2026 LeuJasYoh"
	origFilename = "Type.exe"
	productName  = "Type"
)

const (
	// langID 对应旧 .rc 的 Translation 0x409, 1200
	langID = 0x409
	// iconName 沿用旧 .rc 的图标组名 "IDI_ICON1": 该符号未被任何头文件定义, 旧 windres
	// 按"名字"记录, 显式同名才能让资源树与旧产物一致
	iconName = "IDI_ICON1"
)

// versionRe 与 build.ps1 / ci.yml 同款: 数字部分 + 可选预发布后缀
var versionRe = regexp.MustCompile(`^[\d.]+(?:-[0-9A-Za-z.]+)?$`)

func main() {
	ver := flag.String("version", "", "版本号, 如 1.5.1 或 1.5.0-rc.1 (必填; 单一来源是 cmd/type/main.go)")
	iconPath := flag.String("icon", "assets/icon.ico", "图标文件路径")
	out := flag.String("out", "cmd/type/version", "输出前缀, 实际写 <out>_amd64.syso 与 <out>_arm64.syso")
	flag.Parse()

	if err := run(*ver, *iconPath, *out); err != nil {
		fmt.Fprintln(os.Stderr, "mkres:", err)
		os.Exit(1)
	}
}

func run(ver, iconPath, out string) error {
	if !versionRe.MatchString(ver) {
		return fmt.Errorf("版本号 %q 不合法", ver)
	}

	rs, err := buildResourceSet(ver, iconPath)
	if err != nil {
		return err
	}

	// 清掉 windres 时代的旧产物(同名无架构后缀): 它不入库, 但从旧版本 checkout
	// 升级上来的工作区里会残留, 被 go build 一并链接后 amd64 产物会带两份资源
	if legacy := out + ".syso"; fileExists(legacy) {
		if err := os.Remove(legacy); err != nil {
			return err
		}
		fmt.Println("  已清理 windres 旧产物:", legacy)
	}

	for _, a := range []struct {
		suffix string
		arch   winres.Arch
	}{
		{"amd64", winres.ArchAMD64},
		{"arm64", winres.ArchARM64},
	} {
		name := out + "_" + a.suffix + ".syso"
		if err := writeSyso(rs, name, a.arch); err != nil {
			return err
		}
		fmt.Println("  资源已生成:", name)
	}
	return nil
}

func buildResourceSet(ver, iconPath string) (*winres.ResourceSet, error) {
	// 版本资源 id=1、语言 0x0409(en-US); FileOS/FileFlags/FILETYPE 由 winres 按
	// VOS_NT_WINDOWS32 / 0x3f / VFT_APP 填充 —— 与旧 .rc 的取值一致
	vi := version.Info{}
	vi.Type = version.App
	for _, kv := range [][2]string{
		{version.CompanyName, companyName},
		{version.FileDescription, fileDesc},
		{version.InternalName, internalName},
		{version.LegalCopyright, legalCopy},
		{version.OriginalFilename, origFilename},
		{version.ProductName, productName},
	} {
		if err := vi.Set(langID, kv[0], kv[1]); err != nil {
			return nil, err
		}
	}
	// 数字版本(资源固定区)与字符串版本都要写。这两个方法会填进已存在的语言表,
	// 所以必须在 Set(...) 之后调用, 否则字符串会落进独立的 neutral 表,
	// 产出两个翻译块
	vi.SetFileVersion(fileVersion(ver))
	vi.SetProductVersion(ver)

	f, err := os.Open(iconPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// LoadICO 直通 .ico 原始字节(含 PNG 压缩的 256 尺寸), 不做解码重编码
	ico, err := winres.LoadICO(f)
	if err != nil {
		return nil, fmt.Errorf("读取图标 %s: %w", iconPath, err)
	}

	rs := &winres.ResourceSet{}
	// 图标组语言取 langID, 图像条目由 winres 记为 neutral(0) —— Windows 按语言回退取用,
	// 资源树与旧产物等价
	if err := rs.SetIconTranslation(winres.Name(iconName), langID, ico); err != nil {
		return nil, err
	}
	rs.SetVersionInfo(vi)
	// DPI 感知由 manifest 声明(id=1, 语言 0x0409, 而非运行时 API): manifest 在进程启动前
	// 生效, 也是微软推荐的做法; 缺了它, 缩放非 100% 的显示器上整窗会被位图拉伸发虚 ——
	// 见 docs/invariants.md「其它不变量」。
	// permonitorv2 带 system 回退(系统读不懂前者的取值时退化为系统级感知); ExecutionLevel
	// 必须是 asInvoker —— 本程序按设计以普通权限运行, 提权会改变 UIPI 判定, 使"目标窗口
	// 拒绝了模拟按键"这条提示失真。
	// 生成的 manifest 还会带 winres 默认的 supportedOS 声明(win7~win10); 本程序只用
	// Win10+ 才有的 API, 这些声明不影响实际可运行范围
	rs.SetManifest(winres.AppManifest{
		ExecutionLevel: winres.AsInvoker,
		DPIAwareness:   winres.DPIPerMonitorV2,
	})
	return rs, nil
}

func writeSyso(rs *winres.ResourceSet, name string, arch winres.Arch) error {
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	if err := rs.WriteObject(f, arch); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// fileVersion 把版本串化成资源固定区的四位数字形式:
// "1.5.1" -> "1.5.1.0", "1.5.0-rc.1" -> "1.5.0.0"(预发布后缀不能进数字字段),
// 与 build.ps1 对 FILEVERSION 的既有规则一致
func fileVersion(ver string) string {
	base := ver
	if i := strings.IndexByte(base, '-'); i >= 0 {
		base = base[:i] // 先剥后缀再切分: "1.5.0-rc.1" 按点切会多出一段
	}
	parts := strings.Split(base, ".")
	for len(parts) < 4 {
		parts = append(parts, "0")
	}
	return strings.Join(parts[:4], ".")
}
