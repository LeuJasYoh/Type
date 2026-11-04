module github.com/LeuJasYoh/type

go 1.27.3

require (
	github.com/egoist/mygo v0.7.4
	github.com/tc-hib/winres v0.3.1
)

// Type 自带一份精简过的 mygo: 窗口壳还要它, 但框架里 Type 用不到的子系统
// (TS 生成器、更新器、HTTP 栈等)在本仓 third_party/mygo 里已经删掉。
// 它是独立模块, 因此不进根的 go test ./... 与 repo-check 的扫描范围。
// 升级上游时: 覆盖 third_party/mygo 再重放本仓的删改, 见 docs/tooling.md「自养的精简框架」
replace github.com/egoist/mygo => ./third_party/mygo

require (
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/nfnt/resize v0.0.0-20180221191011-83c6a9932646 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)
