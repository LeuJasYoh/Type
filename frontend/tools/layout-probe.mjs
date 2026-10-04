// ═══ 版式几何探针 ═══════════════════════════════════════
// 用无头 Edge 逐档量真实几何, 而不是"看代码觉得对"。
// 页面配方与 docs/architecture.md「README 配图的复现方式」同一套: 取已构建的
// internal/web/dist/index.html(只读, 不修改产物), 复制到临时目录, 在 <head> 顶部插一段
// 打桩脚本(定义 startTyping / getTypingStatus 等五个绑定, 按 URL 的 scenario 返回固定
// 状态; 必须先于 Vue 的 module 脚本执行), 末尾插一段度量脚本(量完把几何写进
// <pre id="probe-metrics"> 的文本), 由本地 HTTP 服务提供。
// 几何取数走 --dump-dom(逐档校准视口), 截图走 --screenshot, 两条路的视口口径不同(见③)。
// 到不了的档位(小于系统最小窗口)如实报"到不了"并跳过, 不换宽度顶替、不中断其余档位。

// 用法:
//   node frontend/tools/layout-probe.mjs                             # 默认档位 × long 场景
//   node frontend/tools/layout-probe.mjs --sizes 648x540,576x480
//   node frontend/tools/layout-probe.mjs --scenario full             # 目标条 + 进度条(内容最多)
//   node frontend/tools/layout-probe.mjs --shot                      # 另存截图(默认 540×480 与 648×540)
//   node frontend/tools/layout-probe.mjs --out D:\tmp\probe          # 指定输出目录(默认 %TEMP%)
//
// 场景: long = 最长的一条冻结终态文案(错误态, 无目标条); full = 同一文案 + 运行态
//       (目标条 + 进度条, 内容最多); badge = 塞满文本并滚到底, 量字数徽标与末行的间距。

// ── 在这台机器上跑通要绕的三个坑(都实测过, 别再踩) ──
// ① **HTTP 服务与 Edge 必须在同一个事件循环里活着**。探针的本地服务就在本进程,
//    用 spawnSync 起 Edge 会锁住事件循环 → Edge 永远拿不到页面, --dump-dom 不返回,
//    表现是 status=null ETIMEDOUT 加一堆 task_manager 噪音。必须异步 spawn。
// ② **--window-size 量的是含窗口框的外框**(本机 +26×+93, 随系统缩放与浏览器版本变),
//    而且窗口有系统最小宽度(本机视口最窄 496px, 480 宽的档位到不了), 所以几何取数要
//    逐档校准: 量一次视口 → 把差额补进外框 → 再量, 通常第二趟就逐像素相等。别写死差额。

// ③ **--screenshot 与 --dump-dom 的视口不是一回事**: 截图模式下视口就等于
//    --window-size(实测 540×480 出图 1080×960@DSF2, 状态栏底边落在 480−12 处),
//    所以截图用目标尺寸本身, 不要用 ②校准后的外框尺寸。另外 --user-data-dir 必须
//    每次运行独立(共用会撞车, 得到退出码 21 或卡住), 收尾只按该 profile 路径精确
//    清理 msedge 进程, 不碰用户自己的浏览器。
//    Edge 万一不自己退出(某些环境下会挂住), runEdge 的超时会杀掉它, 已落盘的 DOM
//    照常解析 —— 出图/出 DOM 即收工, 不必等进程自己走。

import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const REPO = path.resolve(HERE, '..', '..');
const DIST = path.join(REPO, 'internal', 'web', 'dist', 'index.html');

const EDGE_CANDIDATES = [
  process.env.TYPE_PROBE_EDGE,
  'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  'C:\\Program Files\\Microsoft\\Edge\\Application\\msedge.exe',
].filter(Boolean);

// 冻结文案逐字取自 internal/typing/contract_test.go(msgFocusStayedOnSelf):
// 后端最长的一条终态文案。只用于量版式, 不写进 src
const LONG_MESSAGE = '未切换到目标窗口：倒计时结束时焦点仍在 Type，请重新启动后聚焦目标窗口';

const DEFAULT_SIZES = ['540x480', '576x480', '610x509', '648x540', '480x480'];
const DEFAULT_SHOT_SIZES = ['540x480', '648x540'];

const STUB = `<script>(function(){
  // 量的是静态几何: 关掉所有过渡与动画, 否则进度条/目标条的入场动画会让量到的
  // 高度停在中间值(README 配图配方里同样这么做)。!important 压得住产物里的样式
  document.head.insertAdjacentHTML('beforeend', '<style>*,*::before,*::after{transition:none!important;animation:none!important}</style>');
  var scenario = new URLSearchParams(location.search).get('scenario') || 'long';
  var LONG = ${JSON.stringify(LONG_MESSAGE)};
  var status = scenario === 'full'
    ? { phase: 'typing', message: LONG, progress: 45, secondsLeft: 0, targetWindow: '记事本 - 未命名' }
    : { phase: 'error', message: LONG, progress: -1, secondsLeft: 0, targetWindow: '' };
  window.startTyping = function () { return Promise.resolve(''); };
  window.cancelTyping = function () { return Promise.resolve(''); };
  window.toggleTopmost = function () { return Promise.resolve(false); };
  window.getTopmost = function () { return Promise.resolve(false); };
  window.getTypingStatus = function () { return Promise.resolve(status); };
})();</script>`;

const COLLECTOR = `<script>(function(){
  var scenario = new URLSearchParams(location.search).get('scenario') || 'long';
  var r2 = function (v) { return Math.round(v * 100) / 100; };
  var rect = function (el) {
    var b = el.getBoundingClientRect();
    return { top: r2(b.top), bottom: r2(b.bottom), left: r2(b.left), right: r2(b.right), width: r2(b.width), height: r2(b.height) };
  };
  var px = function (el, prop) { return parseFloat(getComputedStyle(el)[prop]) || 0; };

  // 细节取值: 参与 --ui-scale 的那些应随档位等比变化, 不参与的那些(边框宽度、
  // 字号、字距、过渡时长)在任何档位都必须一字不差
  function tokens(app, iw, sb, ta) {
    var q = function (sel) { return document.querySelector(sel); };
    var btn = q('.btn-primary');
    var pill = q('.pill-toggle');
    var icon = q('.icon-btn');
    var pin = q('.pin-btn');
    var sliderRow = q('.delay-slider-row');
    var label = q('.section-label');
    var caption = q('.caption-title');
    var play = q('.btn-primary svg');
    var raw = function (el, prop) { return el ? getComputedStyle(el)[prop] : null; };
    var box = function (el) { if (!el) return null; var b = el.getBoundingClientRect(); return r2(b.width) + '×' + r2(b.height); };
    return {
      appPadding: raw(app, 'paddingTop') + '/' + raw(app, 'paddingLeft'),
      appGap: raw(app, 'rowGap'),
      inputWrapRadius: raw(iw, 'borderTopLeftRadius'),
      btnSize: box(btn), btnRadius: raw(btn, 'borderTopLeftRadius'), btnPadX: raw(btn, 'paddingLeft'),
      pillSize: box(pill), pillPadX: raw(pill, 'paddingLeft'),
      iconBtnSize: box(icon), pinBtnPad: raw(pin, 'paddingTop') + '/' + raw(pin, 'paddingLeft'),
      statusRadius: raw(sb, 'borderTopLeftRadius'),
      sliderRowGap: raw(sliderRow, 'columnGap'),
      playIcon: box(play),
      // 以下四项永不参与缩放, 换档位应完全不变
      inputWrapBorder: raw(iw, 'borderTopWidth'),
      statusFontSize: raw(document.querySelector('.status-text'), 'fontSize'),
      labelFontSize: raw(label, 'fontSize'),
      textareaFontSize: raw(ta, 'fontSize'),
      captionLetterSpacing: raw(caption, 'letterSpacing'),
      btnTransition: raw(btn, 'transitionDuration'),
    };
  }

  function prepare() {
    if (scenario !== 'badge') return;
    var ta = document.querySelector('#textInput');
    if (!ta) return;
    ta.value = Array.from({ length: 9 }, function (_, i) { return '第' + (i + 1) + '行: 版式度量用的占位文本, 用来把输入区填满'; }).join('\\n');
    ta.dispatchEvent(new Event('input', { bubbles: true }));
    ta.scrollTop = ta.scrollHeight;
  }

  function measure() {
    var doc = document.documentElement;
    var app = document.querySelector('.app-container');
    var iw = document.querySelector('.input-wrap');
    var sb = document.querySelector('.status-bar');
    var st = document.querySelector('.status-text');
    var block = document.querySelector('.status-block');
    var actions = document.querySelector('.actions-row');
    var ta = document.querySelector('#textInput');
    var badge = document.querySelector('.char-count');
    var appRect = app.getBoundingClientRect();
    var padBottom = px(app, 'paddingBottom');
    var blockRect = block.getBoundingClientRect();
    var stLine = px(st, 'lineHeight') || 1;
    var data = {
      scenario: scenario,
      viewport: {
        innerWidth: innerWidth, innerHeight: innerHeight,
        clientWidth: doc.clientWidth, clientHeight: doc.clientHeight,
        devicePixelRatio: devicePixelRatio,
      },
      uiScale: getComputedStyle(doc).getPropertyValue('--ui-scale').trim(),
      documentElement: {
        scrollHeight: doc.scrollHeight, clientHeight: doc.clientHeight,
        scrollWidth: doc.scrollWidth, clientWidth: doc.clientWidth,
      },
      body: {
        scrollHeight: document.body.scrollHeight, clientHeight: document.body.clientHeight,
        scrollWidth: document.body.scrollWidth, clientWidth: document.body.clientWidth,
      },
      appContainer: {
        scrollHeight: app.scrollHeight, clientHeight: app.clientHeight,
        scrollWidth: app.scrollWidth, clientWidth: app.clientWidth,
        overflowY: getComputedStyle(app).overflowY,
        paddingTop: px(app, 'paddingTop'), paddingBottom: padBottom,
        paddingLeft: px(app, 'paddingLeft'), paddingRight: px(app, 'paddingRight'),
        gap: px(app, 'rowGap'),
      },
      inputWrap: rect(iw),
      statusBar: rect(sb),
      statusText: { rect: rect(st), lines: Math.round(st.getBoundingClientRect().height / stLine) },
      statusBlock: rect(block),
      actionsRow: rect(actions),
      // 状态块底边到内容区底边(含 padding)的距离: 0 = 贴底
      statusBlockToContentBottom: r2(appRect.bottom - padBottom - blockRect.bottom),
      // 按钮行底边到状态块顶边的空白: 余量归输入框之后这里应该只剩一个 gap
      actionsToStatusGap: r2(blockRect.top - actions.getBoundingClientRect().bottom),
      // 字数徽标顶边与 textarea 内容区底边的间距: >=0 说明徽标没压住最后一行
      badgeGap: (function () {
        if (!badge || !ta) return null;
        var taRect = ta.getBoundingClientRect();
        return r2(badge.getBoundingClientRect().top - (taRect.bottom - px(ta, 'paddingBottom')));
      })(),
      inputScrollable: iw.scrollHeight > iw.clientHeight,
      tokens: tokens(app, iw, sb, ta),
    };
    var pre = document.getElementById('probe-metrics');
    if (!pre) {
      pre = document.createElement('pre');
      pre.id = 'probe-metrics';
      // fixed 而不是 absolute: absolute 以初始包含块定位, 一行不换行的长 JSON 会从
      // left:-9999px 一路伸到正坐标, 把 documentElement.scrollWidth 顶大(实测把
      // 648 档顶到 886), 看着像页面横向溢出 —— 那是探针自己制造的假报警。
      // fixed 的元素不进可滚动溢出区; max-width:0 + overflow:hidden 再兜一层
      pre.style.position = 'fixed';
      pre.style.left = '0';
      pre.style.top = '0';
      pre.style.maxWidth = '0';
      pre.style.overflow = 'hidden';
      document.body.appendChild(pre);
    }
    pre.textContent = JSON.stringify(data);
  }

  function kick() {
    prepare();
    // 只用定时器, 不用 requestAnimationFrame: 无头下没有合成帧时 rAF 可能永不触发
    setTimeout(measure, 0);
    setTimeout(measure, 300); // 第二拍兜底: 首拍早于 Vue 的异步状态同步时再量一次
    setTimeout(measure, 1200); // 第三拍: 万一 --virtual-time-budget 放大了节拍, 保证最后一拍是终值
  }
  if (document.readyState === 'complete') kick();
  else window.addEventListener('load', kick);
})();</script>`;

function parseArgs(argv) {
  const opts = { sizes: DEFAULT_SIZES, shotSizes: DEFAULT_SHOT_SIZES, scenario: 'long', out: '', shot: false, edge: '', json: true };
  for (let i = 0; i < argv.length; i += 1) {
    const a = argv[i];
    if (a === '--sizes') opts.sizes = argv[++i].split(',').map((s) => s.trim()).filter(Boolean);
    else if (a === '--shot-sizes') opts.shotSizes = argv[++i].split(',').map((s) => s.trim()).filter(Boolean);
    else if (a === '--scenario') opts.scenario = argv[++i];
    else if (a === '--out') opts.out = argv[++i];
    else if (a === '--shot') opts.shot = true;
    else if (a === '--edge') opts.edge = argv[++i];
    else if (a === '--no-json') opts.json = false;
    else throw new Error('未知参数: ' + a);
  }
  return opts;
}

function findEdge(explicit) {
  const list = explicit ? [explicit, ...EDGE_CANDIDATES] : EDGE_CANDIDATES;
  for (const p of list) {
    try { if (fs.statSync(p).isFile()) return p; } catch { /* 继续找 */ }
  }
  throw new Error('找不到 msedge.exe, 用 --edge <路径> 指定');
}

function buildPage() {
  const html = fs.readFileSync(DIST, 'utf8');
  const head = html.indexOf('<head>');
  if (head < 0) throw new Error('产物里没有 <head>, 无法插入打桩脚本');
  const withStub = html.slice(0, head + 6) + STUB + html.slice(head + 6);
  const closeBody = withStub.lastIndexOf('</body>');
  const bodyAt = closeBody < 0 ? withStub.length : closeBody;
  return withStub.slice(0, bodyAt) + COLLECTOR + withStub.slice(bodyAt);
}

function parseSize(s) {
  const m = /^(\d+)x(\d+)$/.exec(s);
  if (!m) throw new Error('档位写法应为 宽x高, 收到: ' + s);
  return { label: s, width: Number(m[1]), height: Number(m[2]) };
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

// PNG 头里读尺寸(IHDR 的宽高在第 16..23 字节): 用来核实截图视口确实是目标尺寸
function pngSize(file) {
  const b = fs.readFileSync(file);
  return { w: b.readUInt32BE(16), h: b.readUInt32BE(20) };
}

// 收尾: 只杀"命令行里带着本次运行那个唯一 profile 目录"的 msedge —— 用户自己的
// 浏览器、以及并发的另一次探针运行都不受影响(路径经环境变量传入, 不插值进脚本)
function killProfileProcesses(profileDir) {
  const script = 'Get-CimInstance Win32_Process -Filter "Name=\'msedge.exe\'" | '
    + 'Where-Object { $_.CommandLine -and $_.CommandLine.Contains($env:PROBE_PROFILE_DIR) } | '
    + 'ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }';
  spawnSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', script], {
    env: { ...process.env, PROBE_PROFILE_DIR: profileDir },
    stdio: 'ignore',
    windowsHide: true,
  });
}

// 异步 spawn + stdout/stderr 直接落文件描述符:
//   · 不能用 spawnSync —— HTTP 服务在本进程里, 阻塞事件循环会让 Edge 拿不到响应;
//   · 不用管道 —— 沙箱受限时管道会 EPERM, 文件重定向不受影响;
//   · 到点没退出就杀掉, 但已落盘的 DOM 仍然可用(某些环境下 Edge 会挂住不自己退)
function runEdge(edge, args, stdoutFile, stderrFile, timeoutMs) {
  return new Promise((resolve) => {
    const out = fs.openSync(stdoutFile, 'w');
    const err = fs.openSync(stderrFile, 'w');
    let closed = false;
    const finish = (result) => {
      if (closed) return;
      closed = true;
      fs.closeSync(out);
      fs.closeSync(err);
      resolve(result);
    };
    const child = spawn(edge, args, { stdio: ['ignore', out, err], windowsHide: true });
    const timer = setTimeout(() => { child.__timedOut = true; child.kill(); }, timeoutMs);
    child.on('close', (code, signal) => { clearTimeout(timer); finish({ status: code, signal, timedOut: Boolean(child.__timedOut) }); });
    child.on('error', (e) => { clearTimeout(timer); finish({ status: null, signal: null, error: e, timedOut: false }); });
  });
}

function edgeFailure(r, stderrFile) {
  const tail = fs.readFileSync(stderrFile, 'utf8').split(/\r?\n/).filter(Boolean).slice(-6).join('\n');
  return `status=${r.status} signal=${r.signal} timeout=${r.timedOut} error=${r.error ? r.error.message : ''}\n${tail}`;
}

function readMetrics(stdoutFile) {
  const raw = fs.readFileSync(stdoutFile, 'utf8');
  const m = /<pre id="probe-metrics"[^>]*>([\s\S]*?)<\/pre>/.exec(raw);
  if (!m) throw new Error('DOM 里没有找到 #probe-metrics(页面可能没跑起来)');
  return JSON.parse(m[1].replace(/&amp;/g, '&').replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&quot;/g, '"'));
}

// 逐档校准: 量一次视口, 把与目标的差额补进 --window-size 再量, 直到逐像素相等。
// 差额(窗口框)只与系统缩放/浏览器版本有关, 与档位无关, 所以会把已知差额当起点
async function measureAtViewport(edge, common, baseUrl, outDir, opts, target, attempt) {
  const dumpFile = path.join(outDir, `dom-${opts.scenario}-${target.label}-try${attempt}.html`);
  const errFile = path.join(outDir, `edge-${opts.scenario}-${target.label}-try${attempt}.err`);
  const args = [...common, `--window-size=${target.winW},${target.winH}`, '--dump-dom', baseUrl];
  const r = await runEdge(edge, args, dumpFile, errFile, 60000);
  const m = readMetrics(dumpFile); // 超时被杀也照样解析: DOM 可能已经写完了
  if (r.status !== 0 && !r.timedOut) throw new Error(`Edge 在 ${target.label} 档未正常退出: ${edgeFailure(r, errFile)}`);
  m.windowSize = { w: target.winW, h: target.winH };
  m.viewportTarget = { w: target.width, h: target.height };
  m.calibrationTries = attempt;
  return m;
}

async function measureCalibrated(edge, common, baseUrl, outDir, opts, target) {
  let m = null;
  for (let attempt = 1; attempt <= 4; attempt += 1) {
    m = await measureAtViewport(edge, common, baseUrl, outDir, opts, target, attempt);
    const dw = target.width - m.viewport.clientWidth;
    const dh = target.height - m.viewport.clientHeight;
    if (dw === 0 && dh === 0) return m;
    target.winW += dw;
    target.winH += dh;
  }
  throw new Error(`${target.label} 档四次校准后视口仍是 ${m.viewport.clientWidth}×${m.viewport.clientHeight}`
    + `(目标 ${target.width}×${target.height}; 窗口有系统最小尺寸, 太小的档位到不了)`);
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));
  const edge = findEdge(opts.edge);
  const outDir = opts.out || path.join(os.tmpdir(), 'type-layout-probe');
  const pageDir = path.join(outDir, 'page');
  // profile 目录按进程唯一: 两次探针并发时共用同一个 profile 会让 Edge 直接失败
  // (启动撞击, 实测), 用完连目录一起清掉
  const profileDir = path.join(outDir, `profile-${process.pid}-${Date.now()}`);
  fs.mkdirSync(pageDir, { recursive: true });
  fs.mkdirSync(profileDir, { recursive: true });
  const pageFile = path.join(pageDir, 'index.html');
  fs.writeFileSync(pageFile, buildPage(), 'utf8');

  const server = http.createServer((req, res) => {
    if (req.url === '/favicon.ico') { res.writeHead(204).end(); return; }
    const body = fs.readFileSync(pageFile);
    res.writeHead(200, { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store' });
    res.end(body);
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = server.address().port;
  const baseUrl = `http://127.0.0.1:${port}/?scenario=${encodeURIComponent(opts.scenario)}`;

  const common = [
    '--headless=new',
    '--disable-gpu',
    '--no-first-run',
    '--no-default-browser-check',
    '--disable-extensions',
    '--user-data-dir=' + profileDir,
    '--virtual-time-budget=3000',
  ];

  const rows = [];
  const rawMetrics = [];
  const calibrated = new Map();
  let windowChrome = null; // 首次量到的"外框 - 视口"差额, 后续档位直接当起点
  try {
    for (const label of opts.sizes) {
      const target = parseSize(label);
      target.winW = target.width;
      target.winH = target.height;
      if (windowChrome) {
        target.winW = target.width + windowChrome.dw;
        target.winH = target.height + windowChrome.dh;
      }
      let m = null;
      try {
        m = await measureCalibrated(edge, common, baseUrl, outDir, opts, target);
      } catch (e) {
        // 到不了的档位(小于系统最小窗口)如实登记, 不换宽度顶替, 也不中断其余档位
        rows.push({ label: `${target.width}×${target.height}`, viewport: '到不了: ' + e.message });
        continue;
      }
      windowChrome = { dw: m.windowSize.w - m.viewport.clientWidth, dh: m.windowSize.h - m.viewport.clientHeight };
      calibrated.set(label, m.windowSize);
      rawMetrics.push(m);
      rows.push({
        label: `${target.width}×${target.height}`,
        viewport: `${m.viewport.clientWidth}×${m.viewport.clientHeight}`,
        scale: m.uiScale,
        docScroll: `${m.documentElement.scrollHeight} / ${m.documentElement.clientHeight}`,
        docScrollW: `${m.documentElement.scrollWidth} / ${m.documentElement.clientWidth}`,
        appScroll: `${m.appContainer.scrollHeight} / ${m.appContainer.clientHeight}`,
        appScrollW: `${m.appContainer.scrollWidth} / ${m.appContainer.clientWidth}`,
        inputWrapH: m.inputWrap.height,
        statusBarH: m.statusBar.height,
        statusLines: m.statusText.lines,
        statusTextW: m.statusText.rect.width,
        blockToBottom: m.statusBlockToContentBottom,
        actionsGap: m.actionsToStatusGap,
        badgeGap: m.badgeGap,
      });
    }

    console.log(`\n场景 scenario=${opts.scenario}  页面=${pageFile}`);
    console.log('视口已逐像素校准(window 外框 → 视口 的差额: '
      + (windowChrome ? `+${windowChrome.dw}×+${windowChrome.dh}` : '未测到'));
    console.table(rows);
    if (opts.json) {
      const jsonFile = path.join(outDir, `metrics-${opts.scenario}.json`);
      fs.writeFileSync(jsonFile, JSON.stringify(rawMetrics, null, 2), 'utf8');
      console.log('原始几何: ' + jsonFile);
    }

    if (opts.shot) {
      // 截图走的是另一条路: --screenshot 下视口就等于 --window-size(实测: 窗口
      // 540×480 出图 1080×960@DSF2, 状态栏底边落在 480-12 处), 而 --dump-dom 下
      // 视口比窗口小一个框(见上)。所以截图用目标尺寸本身, 不用校准后的外框尺寸。
      for (const label of opts.shotSizes) {
        const target = parseSize(label);
        const png = path.join(outDir, `type-${opts.scenario}-${label}.png`);
        const args = [...common, '--force-device-scale-factor=2', `--window-size=${target.width},${target.height}`, `--screenshot=${png}`, baseUrl];
        const r = await runEdge(edge, args, path.join(outDir, 'edge-shot.log'), path.join(outDir, 'edge-shot.err'), 60000);
        if (r.status !== 0 && !r.timedOut) throw new Error(`截图在 ${label} 档失败: ${edgeFailure(r, path.join(outDir, 'edge-shot.err'))}`);
        if (!fs.existsSync(png)) throw new Error(`截图 ${png} 没落盘`);
        const size = pngSize(png);
        if (size.w !== target.width * 2 || size.h !== target.height * 2) {
          throw new Error(`截图 ${png} 是 ${size.w}×${size.h}, 期望 ${target.width * 2}×${target.height * 2}(说明截图视口 ≠ 窗口尺寸)`);
        }
        console.log(`截图: ${png} (${size.w}×${size.h}, ${fs.statSync(png).size} 字节)`);
      }
    }
  } finally {
    // 无论成败都收掉本次运行起的 Edge 与它的 profile 目录, 不给下一次留撞击点
    killProfileProcesses(profileDir);
    try {
      fs.rmSync(profileDir, { recursive: true, force: true });
    } catch {
      // 句柄还没释放时留着, 反正在临时目录, 不影响结论
    }
    server.close();
  }
}

await main();
