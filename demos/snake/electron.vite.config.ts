import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'electron-vite'
import vue from '@vitejs/plugin-vue'

// 配置文件为 ESM,自行推导 __dirname
const configDir = path.dirname(fileURLToPath(import.meta.url))

export default defineConfig({
  main: {
    // 输出 out/main/index.js,与 package.json 的 main 字段保持一致
    build: {
      lib: { entry: 'electron/main.ts' },
      rollupOptions: { output: { entryFileNames: 'index.js' } },
    },
  },
  preload: {
    // 输出 CJS 格式的 out/preload/index.js:
    // 沙箱渲染进程(默认开启)仅支持 CJS 预加载脚本
    build: {
      lib: { entry: 'electron/preload.ts', formats: ['cjs'] },
      rollupOptions: { output: { entryFileNames: 'index.js' } },
    },
  },
  renderer: {
    root: 'src',
    // electron-vite 默认在 src/renderer/ 下查找 index.html,
    // 本项目入口为 src/index.html,需显式指定
    build: { rollupOptions: { input: path.resolve(configDir, 'src/index.html') } },
    plugins: [vue()],
  },
})
