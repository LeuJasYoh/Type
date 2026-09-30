// ═══ 测试期的模块解析钩子 ═══════════════════════════════
// src/ 里的相对导入是按打包器(moduleResolution: bundler)写的省略扩展名形式,
// 而 Node 自己的 ESM 解析要求写全扩展名 —— 直接跑会 ERR_MODULE_NOT_FOUND。
// 这里在解析失败时补一个 .ts 再试一次, 只影响测试进程, 不改产品源码。
//
// 用法: node --import ./test/ts-resolve.mjs --test test/*.test.ts

import { registerHooks } from 'node:module';

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier.startsWith('.') && !/\.[cm]?[jt]s$/.test(specifier)) {
      try {
        return nextResolve(specifier + '.ts', context);
      } catch {
        // 不是 TS 文件, 交给默认解析
      }
    }
    return nextResolve(specifier, context);
  },
});
