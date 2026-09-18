// mkres 生成 Windows 资源目标文件 (.syso): 应用图标 + 版本信息。
//
// 取代此前的 windres + assets/version.rc —— 构建链因此不再需要 MinGW/binutils。
// 资源构成与 .rc 时代保持一致:
//   - 图标组沿用原名 "IDI_ICON1"(.rc 里该符号未被任何头文件定义, windres 按
//     "名字"记录; 这里显式同名, 资源树与旧产物一致), 图标图像直通 .ico 原始
//     字节(含 PNG 压缩的 256 尺寸), 不做解码重编码。图像条目语言为 neutral(0)、
//     图标组为 0x0409——winres 的分配方式, Windows 按语言回退取用, 与旧产物等价
//   - 版本资源 id=1, 语言 0x0409(en-US), FileOS/FileFlags/FILETYPE 由 winres 按
//     VOS_NT_WINDOWS32 / 0x3f / VFT_APP 填充, 与旧 .rc 的取值一致
//
// 用法:
//
//	go run ./tools/mkres -version 1.5.1 -icon assets/icon.ico -out cmd/type/version
//
// 产出 <out>_amd64.syso 与 <out>_arm64.syso: go build 按目标架构各取所需,
// 因此 arm64 构建能拿到同架构的资源对象(.syso 不入库, 见 .gitignore)。
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
	// iconName 旧 .rc 里的图标资源名(见文件头说明)
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
	ico, err := winres.LoadICO(f)
	if err != nil {
		return nil, fmt.Errorf("读取图标 %s: %w", iconPath, err)
	}

	rs := &winres.ResourceSet{}
	if err := rs.SetIconTranslation(winres.Name(iconName), langID, ico); err != nil {
		return nil, err
	}
	rs.SetVersionInfo(vi)
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
