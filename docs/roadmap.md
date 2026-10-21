# 发展规划（2026-10 起，滚动窗口）

本文件是**规划，不是契约**。与 README（面向用户）、docs/behavior-contract.md（冻结行为）冲突时，
以后两者为准。每个版本窗口结束时更新一次"下一步"，不追溯改历史结论。

## 一、这份文档解决什么

只回答三个问题：**下一步做什么、明确不做什么、怎么验收**。只写能开工的事；写不进去的、
还没想清楚的，留在本地草稿（.workbuddy/，已被 .gitignore 忽略，不入库）。

文档分四层：契约（behavior-contract / architecture / comment-style / build-and-release /
writing-style）、知识（invariants）、工具手册（tooling）、规划（本文件）。历史档案在 git 里，
工作区只在 docs/comment-ledger/README.md 留一个指针。

## 二、现状（2026-10-20 治理批次完成后）

| 指标 | 数值 | 口径 |
|---|---|---|
| 产品 Go | 2,349 行 / 94,402 B / 17 文件 | cmd + internal，排除 _test.go |
| Go 测试 | 4,550 行 / 13 文件（约 1.94 倍于产品代码） | cmd + internal 的 _test.go |
| 前端 | src 1,267 行（含 style.css 666 行）；测试 656 行 / 30 例 | frontend/src 与 frontend/test |
| Go 注释密度 | 产品 Go 581/2,349 = 24.7%；单块最长 10 行 | 不设注释总量上限，只限单块 ≤10 行 |
| 仓库治理闸门 | tools/repo-check 一个包、13 个用例 | 注释形态 / README 树 / 发布说明 / 文档指针与链接 |
| cmd/type 测试 | 15 例（跨端契约 14 + 探针 1） | 治理闸门已搬出 |
| 版本号提取 | 1 处实现（scripts/version.ps1） | build.ps1 与三个 workflow 共用 |
| CI | verify.yml 检查项唯一处；ci.yml 双架构发布构建 + amd64 跑 build.ps1 全链路 | 发版复用 verify.yml |
| 文档 | 契约 5 篇 + 知识 1 篇 + 工具手册 1 篇 + 规划 1 篇 + 档案指针 1 篇 | 具体字节数以现场统计为准，不写死在文档里 |
| 发布 | v1.7.3；27 个标签；release-notes 自 v1.5.3 起 13 份 | 更早版本说明见 GitHub Release/历史 |

工程质量水位（按本仓库自己的标准）：依赖方向单向且编译期核对接口签名；发布走 release.yml
且 needs: verify；版本号单一来源；每个踩过的坑写进 docs/invariants.md；失败路径不允许静默
（CI 里的 skip 必须红，剪贴板/注入失败必须可见）。

## 三、四条原则

1. **冻结文案与行为契约优先**：改代码前先读 docs/behavior-contract.md；可以新增文案，必须登记。
2. **小步提交、文档与代码同一次提交**：提交前对照 AGENTS.md 的「检查文档同步」表；不允许
   "下次再补"。
3. **不设会逼人删知识的数字指标**：注释与文档不设字数/占比硬上限（会逼人删有用信息）；
   只设"单块 ≤10 行""档案不进工作区""精确数字不写进散文"这类结构性规则。
4. **闸门守能机检的东西，内容正确性靠人工**：不要把"全绿"当成"文档写得对"；每轮改动后
   人工过一遍受影响的事实描述。

## 四、当前窗口：v1.7.2（仓库治理批次，2026-10-20 已发布）

本批次没有新增用户可见功能；用户可感知的变化只有两处，且都属于修复：剪贴板为空时中文输入
恢复走粘贴通道（设计原意）、失败终态在剪贴板恢复失败时多一句"原内容可能已丢失"。

已落地：

- 文档瘦身与分层：注释重写档案（122KB/9 文件）移出工作区，git 保留快照 a881117；
  invariants 45.9KB → 21.2KB 并按专题拆小节；architecture 18KB → 7.9KB，操作手册拆到
  tooling；本文件重写替代旧 30KB 草稿。
- 治理闸门搬家：六个文件型闸门从 cmd/type 搬到 tools/repo-check（1,406 行 → 一个包、13 例），
  合并两套文档指针检查，新增 markdown 链接存在性检查，删掉按文件基线的注释预算表。
- 构建链单一来源：版本提取收敛到 scripts/version.ps1；三个 workflow 与 build.ps1 共用；
  ci.yml 的 amd64 档执行一次完整 build.ps1；gofmt 检查补进程退出码。
- 失败路径修复：空剪贴板快照语义（并防住"只有位图格式时被当成空剪贴板清掉"的数据丢失）；
  5 处 t.Skip 统一为"本机跳过、CI 必红"；注入失败且恢复失败时上报剪贴板可能丢失；
  6 处 Bind error 走启动提示；等让位上界字段化（internal/typing 测试 8.4s → 1.5s）。
- 前端与契约：healPolling 不在页面隐藏时重启链；版式探针补齐第 6 个绑定；
  toggleTopmost/getTopmost 返回签名与 TypingStatus json tag 选项进闸门；删除与 tsconfig 重复的
  src/env.d.ts 并重建入库产物。

发版记录：已按补丁号 v1.7.2 发布（2026-10-20），发布说明见 release-notes/v1.7.2.md；
流程按 docs/build-and-release.md 执行（改版本号 → 写发布说明 → 构建产物 → 提交推送 → 打标签）。
补丁跟进：v1.7.3（2026-10-21）只恢复 v1.7.2 批次中误删的 11 条状态机用例并补 1 条失败路径用例，
程序行为与 v1.7.2 完全一致，发布说明见 release-notes/v1.7.3.md。

发布前验收（已通过）：

- gofmt 空、go vet 全绿、go test -race ./... 全绿（含 tools/repo-check 与 win32 真实用例）；
- 前端 typecheck / format:check / test 全绿；scripts/build.ps1 在本机与 CI 都绿；
- docs 内 markdown 链接与代码里的 92 条文档指针 0 失效；
- git 工作区除本次提交外无未跟踪文件（.workbuddy/ 已忽略）。

## 五、之后的窗口（维护期）

下一窗口起只做三类事：**缺陷修复、文档/工具链维护、可测性**。用户可见的新能力不在本窗口内，
真要开新功能，先写清行为契约、补用例、再动代码。

候选 backlog（按价值排序，未承诺；都是工程侧，不需要产品决策）：

| # | 事项 | 理由 | 规模 |
|---|---|---|---|
| 1 | 文档指针细化：把仍指向父节「其它不变量」的 29 处 Go 注释与 6 处前端注释改到具体小节 | 拆节后父节仍是入口，但精确指路能减少二次搜索 | S |
| 2 | win32 副作用外移：PromptWebView2Unusable 的 os.Exit、GuardSingleInstance 的模态框改成返回 error，由 cmd/type 决定退出与文案 | 平台层可测、装配层职责完整 | S/M |
| 3 | Status() 返回值化、viewfit.Fixed 导出面收敛 | 去掉共享可变状态与测试专用导出 | S |
| 4 | App.vue 纯函数测试：iconSize / charCount / 置顶回读 | 当前 0 行为覆盖，抽成纯函数后零成本可测 | S |
| 5 | 极端小屏的 PlanForDisplay 整窗越界边界 | 现在只夹客户区；补一条边界用例即可 | XS |
| 6 | 依赖与 CI 治理：dependabot（go/npm/actions）评估、actions 是否固定 SHA | 减少手工升级与供应链风险 | M |
| 7 | release-notes 历史缺口：写一句"v1.5.3 前无说明文件" | 避免"每版都有说明"的误读 | XS |
| 8 | 语言构成与精确数字：需要时由脚本现场统计，不写进文档 | 已从 architecture 删除会漂的字节表 | XS |
| 9 | 文档地图闸门：新增 docs/*.md 必须在 AGENTS 文档地图登记 | 防止再出现"没人读得到"的文档 | S |

## 六、明确不做

- 不做功能扩张：不新增注入通道、不改界面布局、不引入新的运行时依赖或 cgo；
- 不再来一轮"注释重写"（历史证明会再生成一批一次性档案）；注释只做局部增删；
- 不加"注释行占比上限""文档字数上限"这类数字闸门；
- 不把 internal/web/dist/index.html 改成 CI 生成（会牺牲"无 Node 也能 go build"，且漂移检查失去锚点）；
- 不恢复跨显示器 DPI 跟随（用户明确不要，理由见 docs/invariants.md「窗口尺寸与 DPI」）；
- 不为减少测试行数删测试；测试只搬位置、合并重复、删过时基线。

## 七、维护规则

- 每次提交前走 AGENTS.md 的「检查文档同步」表；行为契约、README、发布说明、docs 与代码
  同一次提交。
- 规划窗口内新增的"以后要做"只进本文件；想不清楚的留在本地草稿，不入库。
- 版本号涨法按 docs/build-and-release.md：用户能看出"多了一样东西"进次版本号，纯修复进补丁号。
- 发布说明与代码同一次提交，打 v<版本> 标签即由 CI 完成构建、校验与发布。

## 八、度量口径（现场可复核）

    gofmt -l ./cmd ./internal ./tools
    go vet ./...
    go test -count=1 -race ./...
    go test -count=1 ./tools/repo-check
    cd frontend; npm run typecheck; npm run format:check; npm test
    Get-ChildItem docs -Recurse -File -Filter *.md | Measure-Object Length -Sum
    Select-String -Path docs/invariants.md -Pattern '^#{1,4}\s'

数字型陈述默认不写进文档（写过就会漂）：本文件的指标表只在窗口更新时重算一次，
与代码注释/其他文档冲突时，以现场命令结果为准。

## 九、更新记录

| 日期 | 变更 |
|---|---|
| 2026-10-18 | 初稿（基于三份侦察与一次对抗式审校，未入库） |
| 2026-10-20 | 重写：治理批次落地后的现状、v1.7.2 发版建议、维护期 backlog 与明确不做清单 |
| 2026-10-20 | v1.7.2 按补丁号发布（纯修复与内部治理，无新增功能） |
| 2026-10-21 | v1.7.3 发布：恢复误删的 11 条状态机用例（程序行为与 v1.7.2 一致） |
