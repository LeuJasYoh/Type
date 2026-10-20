// Package repocheck 是仓库治理闸门，只存在于测试里。
//
// 这些检查与产品行为无关：核对注释形态、README 结构树、发布说明、文档指针与
// markdown 链接。放在独立包里而不是 cmd/type 的测试里，是为了让"改一句文档"
// 不必编译带 WebView2 依赖的装配层，也让治理规则只有一个入口：
//
//	go test ./tools/repo-check
package repocheck
