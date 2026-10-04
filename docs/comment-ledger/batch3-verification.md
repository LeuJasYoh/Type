# 核验：阶段 2 第 3 批（internal/win32）注释重写

- 核验对象：`internal/win32/{win32_window,win32_clipboard,win32_keyboard,win32,win32_instance,win32_msgbox,win32_webview2,win32_test}.go`（`win32.go` 相对 HEAD 无改动，一并纳入比对）。
- 核验人：agent `ledger-app`（独立核验，未参与本批任何文件的重写）；日期 2026-10-04；基线 HEAD。
- 结论：**只动注释通过；三条红线全部保留；7 条新指针全部解析成功；命令全绿。信息零丢失有 1 条实质未通过（W-035 的"文档依据"推导无下落）与 6 条轻微压缩，均可由 Lead 一句话决定是否补回。**

## 1. 只动注释（通过）

方法（等价于 go/scanner 去 COMMENT 后逐 token 比对）：对每个文件，用字符级状态机剥掉 `//` 与 `/* */`（正确跳过 `"`、`'`、反引号原始串与转义），保留字面量原文，再逐行比较 HEAD 与工作区；剥离后残行完全相同即等价于"token 流与字面量原文一致"。

| 文件 | 剥离后代码行（HEAD = 工作区） | 结果 |
|---|---|---|
| win32_window.go | 355 = 355 | PASS |
| win32_clipboard.go | 239 = 239 | PASS |
| win32_keyboard.go | 115 = 115 | PASS |
| win32.go | 55 = 55 | PASS |
| win32_instance.go | 46 = 46 | PASS |
| win32_msgbox.go | 26 = 26 | PASS |
| win32_webview2.go | 43 = 43 | PASS |
| win32_test.go | 521 = 521 | PASS |

辅助证据：`gofmt -l internal/win32` 为空；`comment_contract_test.go` 的注释闸门（无块注释、长度预算、裸引用基线、台账）随 `go test ./cmd/type/` 通过。改动量 118+/220-，全部落在注释行。

## 2. 三条红线（全部保留）

| 红线 | 位置（工作区） | 判定 |
|---|---|---|
| W-036 "事后 `GetLastError` 被运行时清零(实测恒为 0)" | internal/win32/win32_instance.go:63-65 | 保留（仅行文微调） |
| W-015 `^uintptr(n)` 负常量族（取反不是取负；13→-14、33→-34） | internal/win32/win32_window.go:437-438；另见 327（GWL_STYLE = -16）、352-353（HWND_TOPMOST/NOTOPMOST） | 保留 |
| W-005 `AdjustWindowRectExForDpi` 五参数（只传 4 个 → 寄存器残留，实测 600×400 摆成 1540×941） | internal/win32/win32_window.go:250-258 | 保留（逐字含 1540×941） |
| win32_test.go 的 A/B 取证说明（阳性对照 + 清位复刻，缺一不可） | internal/win32/win32_test.go:619-627（① 阳性对照 640-645） | 保留（压缩为 4 行 + 指针，机制细节在 docs/invariants.md:188-195） |

## 3. 两处需验真

### 3.1 `initialWindowSize`：`workH` → `workW` 的纠错**正确**

代码是 `func initialWindowSize(workW, workH int)`，第 102 行 `_ = workW`（注释：宽度由高度推出），`workH` 在第 103、112 行实际参与计算。原注释"workH 用不上时忽略"与代码相反；新注释"workW 刻意不用"与代码一致。**此纠错必须保留。**

### 3.2 新写入的冻结格式指针（7 条，全部解析成功）

| 文件 | 指针目标 | 文件存在 | 小节名命中 |
|---|---|---|---|
| win32_clipboard.go | docs/invariants.md「其它不变量」 | ✓ | ✓ |
| win32_instance.go | docs/invariants.md「其它不变量」 | ✓ | ✓ |
| win32_keyboard.go | docs/invariants.md「文本直投（v1.5.0，WM_CHAR 文本层注入）」 | ✓ | ✓ |
| win32_test.go | docs/invariants.md「其它不变量」 | ✓ | ✓ |
| win32_window.go | docs/invariants.md「其它不变量」 | ✓ | ✓ |
| win32_window.go | docs/invariants.md「文本直投（v1.5.0，WM_CHAR 文本层注入）」 | ✓ | ✓ |
| win32_window.go | docs/invariants.md「焦点锁定与漂移防护（v1.5.3）」 | ✓ | ✓ |

既有裸引用 1 条：`win32_webview2.go:24` 的 `见 docs/behavior-contract.md)`，与 `comment_contract_test.go` 的裸引用基线（该文件 1 条）一致，非本批引入。本批未新增裸引用。

## 4. 逐文件"说法 → 下落"审查

判据：注释里被删/改写的每条说法，都要能在"文件内其他行 / docs 小节 / 用例 / ledger W-*"里找到等价落点；找不到即记为丢失。已读 docs/invariants.md 的相关小节：241-245、180-204、205-240、298-321、322-364、375-379、124-157、28-62。

| 文件 | 主要改写 | 下落 |
|---|---|---|
| win32_clipboard.go | HoldsText 三个落点与"旧签名把歧义解到掩盖失败那一边"→指针；Snapshot 的两处易误读→指针；skippableFormat 三类排除压缩 | docs/invariants.md:322-335（HoldsText，含三个落点）、336-343（Snapshot 不完整判据）、351-356（跳过的格式族与判据） |
| win32_instance.go | mutexAlreadyHeld 的文档推导→指针；claimInstanceMutex 的"名字注入 + 模态框"保留；instanceMutexName 两条理由保留 | docs/invariants.md:357-364 覆盖两个错误码与后果；**"文档并未逐字写这一支、由 MUTEX_ALL_ACCESS/NULL 推得"这段推导 docs 中没有 → 丢失（见第 5 节 U-1）**；模态框与 PID 名字留在代码内 |
| win32_keyboard.go | SendText 实测数据→指针；sendCharUnitsViaWMChar 退化与全角盲区→指针 | docs/invariants.md:124-134（实测：逐字落盘/零 keydown/零配对；wmcharprobe 证据）、150-154（退化 + 全角标点盲区 + 入队计数判据） |
| win32_msgbox.go | 文件头压缩 | 无删除；"只走 user32、不经过 WebView2"与用途保留 |
| win32_test.go | clipboardExclusive 本机跳过/CI 红、TestClaimInstanceMutex、TestMutexAlreadyHeld→指针、TestInitialWindowSize 算式注释、TestWindowSizeIsFixed A/B | docs:324-335/357-364（单实例）、188-195（A/B）；算式可由 docs:181-184 的 49% 与 6:5 推出；轻微压缩见第 5 节 |
| win32_webview2.go | 文件头与 WebView2Available 压缩 | docs/invariants.md:298-321（三道闸门、官方建议、预检不等于创建成功） |
| win32_window.go | ScaledForDPI、scaledByDPI、roundDiv、dpiForWindow、上下限、PlanForDisplay、内容缩放、windowRectForClient、SetWindowClientRect、样式位、取证手段、focusedHWND、Sample 均压缩或改指针 | docs:241-245（取整/clientLogicalSize）、282-288（DPI manifest）、180-204（尺寸与 SetWindowClientRect 加固）、205-240（内容缩放）、146-149+39-40（focusedHWND）、30-33+49-52（Sample 一次读全/标题缓存）、188-195（样式位与 A/B）；`^uintptr` 与五参数两处红线留在原函数内 |

## 5. 未通过项与建议

- **U-1（实质，建议处理）**：W-035 的"文档依据"推导被删除且 docs 无落点 —— 旧注释写"CreateMutexW 的文档并没有逐字写这一支，它只说明'名字命中已存在的对象时请求 MUTEX_ALL_ACCESS'以及'失败返回 NULL'；ACCESS_DENIED 是从这两句推得的（访问检查只在对象存在时才会做）"，新注释以"文档依据……见 docs/invariants.md「其它不变量」"代替。实际该小节（357-364）只记了两个错误码、NULL 语义与后果，**没有**这段推导；grep `MUTEX_ALL_ACCESS`/`逐字`/`推得`/`访问检查` 在 docs 下零命中，仅存在于 `docs/comment-ledger/win32.md:44`。故 ledger W-035 的"该节已记"与事实不符，指针存在轻微过度声明。建议（二选一，均由 Lead 决定）：① 往 docs/invariants.md「其它不变量」的单实例条目补一句推导与依据；② 在 `win32_instance.go` 的 `mutexAlreadyHeld` 注释里恢复半句（"文档未逐字写后一支，由 MUTEX_ALL_ACCESS 与失败返回 NULL 两句推得"）。实际风险低：操作结论（两个错误码都算"已被占用"）与后果在 docs 与单测里都在。
- **U-2（轻微，量化明细压缩）**：`win32_window.go` 上下限块删去纵向固定成本分项（标题栏 34 / 输入框 195 / 选项行 26 / 按钮行 36 / 状态栏 32 / 目标条 26）与"原先按 450 高定的"来历、"540 是文档化的默认宽度"；总数 420px、"600 高余量 146 / 540 高余量约 70、输入框约 300"与"480 是保守取值"都保留，docs:181-184 另有 49%/6:5/保守取值/446<470/37.5/496。分项数字可由 CSS 推出，判为可接受。
- **U-3（轻微，疑似纠错但需确认）**：`dpiForWindow` 删去"本机实测就是 100"，而 docs/invariants.md:244 写"本机实测 DPI 为 120"。两处本就矛盾，删除消除了一个可能错的值；建议 Lead 确认留档口径（若确有 100 DPI 机器，可并入 docs）。
- **U-4（轻微）**：`roundDiv` 删去"100 DPI 下 720 逻辑 → 750 物理、折回 720 看着对"的退化例子；docs:241-245 记了 125% 下 1024→1023 与 clientLogicalSize 历史。新写入的 `599 @120dpi: 舍入 749, 截断 748` 已按 `roundDiv` 实现核算正确。
- **U-5（轻微）**：样式位常量注释删去 go-webview2 源码出处（`style &^= (WSThickFrame|WSMaximizeBox)` + `SWP_FRAMECHANGED`）；"库会清"这一事实、`SWP_FRAMECHANGED` 的复刻与 A/B 判据分别在 docs:188-195 与 win32_test.go:622-627。
- **U-6（轻微）**：`win32_clipboard.go` 的 skippableFormat 删去"数据由持有方解释、形状不保证是内存块"的判据表述（改为后果）；判据本身在 docs:351-356（"判据是这块数据是不是普通内存块"）。
- **U-7（轻微）**：`win32_test.go` 的 TestInitialWindowSize 删去各档算式注释与"本机实测 125% 缩放下的逻辑工作区 1536×912"出处；期望值仍在用例内，比率在 docs。

## 6. 命令结果

| 命令 | 结果 |
|---|---|
| `go test -count=1 ./cmd/type/ ./internal/typing/` | `ok`（cmd/type 0.9s、internal/typing 7.9s），exit 0 |
| `go vet ./internal/win32/` | 无输出，exit 0 |
| `gofmt -l internal/win32` | 空 |

按要求未单独跑 `go test ./internal/win32/`（真实剪贴板/窗口），未跑 `-race`，未跑 build.ps1。

## 7. 给 Lead 的基线信息

8 个文件的"单块连续 `//` > 10 行"实际数**全部为 0**（`comment_contract_test.go` 现基线：clipboard 2、instance 1、keyboard 1、test 2、window 2）。`TestCommentBlockBudget` 已通过，可顺手把这些基线降为 0（不降等于放水）。各文件整行注释数（供对照）：window 130、test 101、clipboard 52、keyboard 35、instance 25、webview2 18、win32.go 11、msgbox 7。
