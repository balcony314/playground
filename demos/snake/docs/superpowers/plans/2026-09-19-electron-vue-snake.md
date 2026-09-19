# Electron + Vue 贪吃蛇 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 ts-demo 目录内实现 Electron + Vue 3 + Canvas 贪吃蛇(标准版:计分、最高分、暂停、速度递增)。

**Architecture:** 游戏核心逻辑(`engine.ts`)为无副作用纯函数,独立于渲染;Canvas 渲染器只读状态绘图;rAF 游戏循环按 `stepInterval` 固定步进驱动引擎;Vue 组件只做视图与输入。状态整体不可变替换。

**Tech Stack:** Electron + electron-vite + Vue 3(组合式 API)+ TypeScript(strict)+ Vitest。

**Spec:** `docs/superpowers/specs/2026-09-19-electron-vue-snake-design.md`

## Global Constraints

- TypeScript `strict: true`,所有代码与注释用简体中文
- 引擎纯函数不可变:任何 `step`/`enqueueDirection` 等调用不得修改传入状态
- 单测仅针对 `src/game/engine.ts`(spec 规定),其余任务以 `pnpm typecheck` + 手动运行验证
- 棋盘 20×20 格,每格 32px,画布 640×640;步进间隔公式 `max(60, 150 - floor(score/5)*10)` ms
- 现有 `app.ts` / `app.js` 与项目无关,保留不动、不纳入构建
- 依赖一律安装当前最新稳定版(不硬编码版本号);包管理器用 pnpm,不可用则回退 npm
- 提交信息格式 `<type>: <描述>`,不加 Co-Authored-By(用户全局禁用了署名)
- 窗口 720×820、`resizable: false`、隐藏菜单栏、`contextIsolation: true`、`nodeIntegration: false`
- 最高分 key 为 `snake.highScore`,localStorage 读写必须 try/catch 降级内存

---

### Task 1: 项目脚手架(Electron + Vue 可运行窗口)

**Files:**
- Create: `package.json`、`.gitignore`、`electron.vite.config.ts`
- Create: `tsconfig.json`、`tsconfig.node.json`、`tsconfig.web.json`、`src/env.d.ts`
- Create: `electron/main.ts`、`electron/preload.ts`
- Create: `src/index.html`、`src/main.ts`、`src/App.vue`(占位版)、`src/style.css`

**Interfaces:**
- Produces: 可运行的 Electron 开发环境;`pnpm dev` / `pnpm build` / `pnpm test` / `pnpm typecheck` 四个脚本;主进程加载渲染页的约定(`out/main/index.js` + preload `out/preload/index.js`)

- [ ] **Step 1: 写 package.json**

```json
{
  "name": "snake-game",
  "version": "0.1.0",
  "private": true,
  "type": "module",
  "main": "out/main/index.js",
  "scripts": {
    "dev": "electron-vite dev",
    "build": "electron-vite build",
    "test": "vitest run",
    "typecheck": "tsc --noEmit -p tsconfig.node.json && vue-tsc --noEmit -p tsconfig.web.json"
  }
}
```

- [ ] **Step 2: 写 .gitignore**

```
node_modules/
out/
dist/
*.log
```

- [ ] **Step 3: 安装依赖**

```bash
pnpm add vue
pnpm add -D electron electron-vite vite @vitejs/plugin-vue typescript vue-tsc vitest @types/node
```

- [ ] **Step 4: 写 electron.vite.config.ts**

```typescript
import { defineConfig } from 'electron-vite'
import vue from '@vitejs/plugin-vue'

export default defineConfig({
  main: {
    build: { lib: { entry: 'electron/main.ts' } },
  },
  preload: {
    build: { lib: { entry: 'electron/preload.ts' } },
  },
  renderer: {
    root: 'src',
    plugins: [vue()],
  },
})
```

- [ ] **Step 5: 写三个 tsconfig**

`tsconfig.json`:

```json
{
  "files": [],
  "references": [
    { "path": "./tsconfig.node.json" },
    { "path": "./tsconfig.web.json" }
  ]
}
```

`tsconfig.node.json`(主进程/ preload / Node 配置):

```json
{
  "compilerOptions": {
    "composite": true,
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "strict": true,
    "skipLibCheck": true,
    "noEmit": true,
    "types": ["node"]
  },
  "include": ["electron/**/*.ts", "electron.vite.config.ts"]
}
```

`tsconfig.web.json`(渲染进程):

```json
{
  "compilerOptions": {
    "composite": true,
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "strict": true,
    "skipLibCheck": true,
    "noEmit": true,
    "jsx": "preserve",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "types": ["vite/client"]
  },
  "include": ["src/**/*.ts", "src/**/*.d.ts", "src/**/*.vue"]
}
```

`src/env.d.ts`:

```typescript
/// <reference types="vite/client" />

declare module '*.vue' {
  import type { DefineComponent } from 'vue'
  const component: DefineComponent<object, object, unknown>
  export default component
}
```

- [ ] **Step 6: 写主进程与预加载**

`electron/main.ts`:

```typescript
import { app, BrowserWindow } from 'electron'
import path from 'node:path'

function createWindow(): void {
  const win = new BrowserWindow({
    width: 720,
    height: 820,
    resizable: false,
    autoHideMenuBar: true,
    webPreferences: {
      preload: path.join(__dirname, '../preload/index.js'),
      contextIsolation: true,
      nodeIntegration: false,
    },
  })

  if (process.env.ELECTRON_RENDERER_URL) {
    void win.loadURL(process.env.ELECTRON_RENDERER_URL)
  } else {
    void win.loadFile(path.join(__dirname, '../renderer/index.html'))
  }
}

app.whenReady().then(() => {
  createWindow()
  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) createWindow()
  })
}).catch((err: unknown) => {
  console.error('窗口创建失败:', err)
  app.exit(1)
})

app.on('window-all-closed', () => {
  if (process.platform !== 'darwin') app.quit()
})
```

`electron/preload.ts`:

```typescript
import { contextBridge } from 'electron'

// 本游戏不需要主进程 API,仅保留最小桥接占位
contextBridge.exposeInMainWorld('electron', { platform: process.platform })
```

- [ ] **Step 7: 写渲染进程入口与占位 App**

`src/index.html`:

```html
<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <title>贪吃蛇</title>
  </head>
  <body>
    <div id="app"></div>
    <script type="module" src="/main.ts"></script>
  </body>
</html>
```

`src/main.ts`:

```typescript
import { createApp } from 'vue'
import App from './App.vue'
import './style.css'

createApp(App).mount('#app')
```

`src/App.vue`(占位版,Task 10 替换):

```vue
<template>
  <main class="app">
    <h1>🐍 贪吃蛇</h1>
  </main>
</template>
```

`src/style.css`:

```css
* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

body {
  background: #020617;
  color: #e2e8f0;
  font-family: 'Segoe UI', system-ui, -apple-system, sans-serif;
  user-select: none;
}

.app {
  display: flex;
  flex-direction: column;
  align-items: center;
  padding-top: 24px;
}
```

- [ ] **Step 8: 验证开发环境可启动**

Run: `pnpm typecheck`,Expected: 无错误
Run: 后台启动 `pnpm dev`,等待输出出现 `ready`(electron-vite dev server 就绪)且无报错,然后停止进程
Expected: Electron 窗口弹出显示 "🐍 贪吃蛇" 标题

- [ ] **Step 9: 提交**

```bash
git add package.json pnpm-lock.yaml .gitignore electron.vite.config.ts tsconfig.json tsconfig.node.json tsconfig.web.json src/ electron/
git commit -m "feat: electron-vite + vue 项目脚手架"
```

---

### Task 2: Vitest 配置与共享类型常量

**Files:**
- Create: `vitest.config.ts`、`src/game/types.ts`
- Test: `src/game/types.test.ts`

**Interfaces:**
- Produces(后续所有任务依赖,签名逐字使用):
  - `type Direction = 'up' | 'down' | 'left' | 'right'`
  - `interface Point { x: number; y: number }`
  - `type GameStatus = 'ready' | 'running' | 'paused' | 'over'`
  - `interface GameState { snake: Point[]; food: Point; direction: Direction; pendingDirections: Direction[]; score: number; status: GameStatus; stepInterval: number }`
  - 常量:`GRID_SIZE = 20`、`CELL_SIZE = 32`、`CANVAS_SIZE = 640`、`INITIAL_STEP_INTERVAL = 150`、`MIN_STEP_INTERVAL = 60`、`STEP_DECREMENT = 10`、`SCORES_PER_SPEEDUP = 5`

- [ ] **Step 1: 写 vitest.config.ts**

```typescript
import { defineConfig } from 'vitest/config'

export default defineConfig({
  test: {
    include: ['src/game/**/*.test.ts'],
  },
})
```

- [ ] **Step 2: 写失败测试 src/game/types.test.ts**

```typescript
import { describe, it, expect } from 'vitest'
import {
  GRID_SIZE,
  CELL_SIZE,
  CANVAS_SIZE,
  INITIAL_STEP_INTERVAL,
  MIN_STEP_INTERVAL,
  STEP_DECREMENT,
  SCORES_PER_SPEEDUP,
} from './types'

describe('游戏常量', () => {
  it('棋盘 20×20 格,每格 32px,画布 640×640', () => {
    expect(GRID_SIZE).toBe(20)
    expect(CELL_SIZE).toBe(32)
    expect(CANVAS_SIZE).toBe(GRID_SIZE * CELL_SIZE)
  })

  it('速度参数:初始 150ms、下限 60ms、每档减 10ms、每 5 分一档', () => {
    expect(INITIAL_STEP_INTERVAL).toBe(150)
    expect(MIN_STEP_INTERVAL).toBe(60)
    expect(STEP_DECREMENT).toBe(10)
    expect(SCORES_PER_SPEEDUP).toBe(5)
  })
})
```

- [ ] **Step 3: 运行确认失败**

Run: `pnpm test`
Expected: FAIL(`Cannot find module './types'`)

- [ ] **Step 4: 写 src/game/types.ts**

```typescript
/** 移动方向 */
export type Direction = 'up' | 'down' | 'left' | 'right'

/** 网格坐标(0 起始,x 向右 y 向下) */
export interface Point {
  x: number
  y: number
}

/** 游戏状态机:就绪 → 运行 ⇄ 暂停;任何状态 → 结束;结束 → 重开(运行) */
export type GameStatus = 'ready' | 'running' | 'paused' | 'over'

/** 完整游戏状态(不可变:每次变更整体替换) */
export interface GameState {
  /** 蛇身,snake[0] 为头 */
  snake: Point[]
  food: Point
  /** 当前实际移动方向(step 时从队列头取出) */
  direction: Direction
  /** 输入缓冲队列,最多 2 个,防止同帧双转与 180° 掉头 */
  pendingDirections: Direction[]
  score: number
  status: GameStatus
  /** 当前步进间隔 ms */
  stepInterval: number
}

export const GRID_SIZE = 20
export const CELL_SIZE = 32
export const CANVAS_SIZE = GRID_SIZE * CELL_SIZE
export const INITIAL_STEP_INTERVAL = 150
export const MIN_STEP_INTERVAL = 60
export const STEP_DECREMENT = 10
export const SCORES_PER_SPEEDUP = 5
```

- [ ] **Step 5: 运行确认通过**

Run: `pnpm test`
Expected: PASS(2 个测试)

- [ ] **Step 6: 提交**

```bash
git add vitest.config.ts src/game/
git commit -m "feat: vitest 配置与游戏共享类型常量"
```

---

### Task 3: 引擎 —— 初始状态与状态转换

**Files:**
- Create: `src/game/engine.ts`
- Test: `src/game/state.test.ts`

**Interfaces:**
- Consumes: Task 2 的 `GameState`、`INITIAL_STEP_INTERVAL`
- Produces:
  - `createInitialState(): GameState`
  - `startGame(state: GameState): GameState`(ready → running)
  - `togglePause(state: GameState): GameState`(running ⇄ paused)
  - `resetGame(state: GameState): GameState`(任意 → 初始且立即 running)

- [ ] **Step 1: 写失败测试 src/game/state.test.ts**

```typescript
import { describe, it, expect } from 'vitest'
import { createInitialState, startGame, togglePause, resetGame } from './engine'

describe('createInitialState', () => {
  it('蛇初始长度 3,居中,头在右侧,向右移动', () => {
    const s = createInitialState()
    expect(s.snake).toEqual([{ x: 11, y: 10 }, { x: 10, y: 10 }, { x: 9, y: 10 }])
    expect(s.direction).toBe('right')
    expect(s.score).toBe(0)
    expect(s.status).toBe('ready')
    expect(s.pendingDirections).toEqual([])
    expect(s.stepInterval).toBe(150)
  })

  it('初始食物不与蛇身重叠', () => {
    const s = createInitialState()
    const onSnake = s.snake.some(seg => seg.x === s.food.x && seg.y === s.food.y)
    expect(onSnake).toBe(false)
  })
})

describe('状态转换', () => {
  it('startGame: ready → running', () => {
    expect(startGame(createInitialState()).status).toBe('running')
  })

  it('startGame 对非 ready 状态无副作用(返回原引用)', () => {
    const running = startGame(createInitialState())
    expect(startGame(running)).toBe(running)
  })

  it('togglePause: running → paused → running', () => {
    const running = startGame(createInitialState())
    expect(togglePause(running).status).toBe('paused')
    expect(togglePause(togglePause(running)).status).toBe('running')
  })

  it('togglePause 对 ready / over 无效', () => {
    const ready = createInitialState()
    expect(togglePause(ready)).toBe(ready)
    const over = { ...createInitialState(), status: 'over' as const }
    expect(togglePause(over)).toBe(over)
  })

  it('resetGame: 回到初始且立即 running', () => {
    const over = { ...createInitialState(), status: 'over' as const, score: 99 }
    const next = resetGame(over)
    expect(next.status).toBe('running')
    expect(next.score).toBe(0)
    expect(next.snake).toHaveLength(3)
    expect(next.stepInterval).toBe(150)
  })
})
```

- [ ] **Step 2: 运行确认失败**

Run: `pnpm test`
Expected: FAIL(`Cannot find module './engine'`)

- [ ] **Step 3: 写 src/game/engine.ts**

```typescript
import type { GameState } from './types'
import { INITIAL_STEP_INTERVAL } from './types'

/** 创建初始状态:蛇 3 节居中(第 10 行,列 9/10/11,头在 11 列),向右 */
export function createInitialState(): GameState {
  return {
    snake: [
      { x: 11, y: 10 },
      { x: 10, y: 10 },
      { x: 9, y: 10 },
    ],
    food: { x: 5, y: 10 },
    direction: 'right',
    pendingDirections: [],
    score: 0,
    status: 'ready',
    stepInterval: INITIAL_STEP_INTERVAL,
  }
}

/** 开始游戏:仅 ready → running,其余返回原状态 */
export function startGame(state: GameState): GameState {
  return state.status === 'ready' ? { ...state, status: 'running' } : state
}

/** 暂停/恢复:仅 running ⇄ paused,其余返回原状态 */
export function togglePause(state: GameState): GameState {
  if (state.status === 'running') return { ...state, status: 'paused' }
  if (state.status === 'paused') return { ...state, status: 'running' }
  return state
}

/** 重开:丢弃旧状态,回到初始并立即进入 running */
export function resetGame(_state: GameState): GameState {
  return { ...createInitialState(), status: 'running' }
}
```

- [ ] **Step 4: 运行确认通过**

Run: `pnpm test`
Expected: PASS(state.test.ts 全部 + types.test.ts)

- [ ] **Step 5: 提交**

```bash
git add src/game/
git commit -m "feat: 引擎初始状态与状态转换纯函数"
```

---

### Task 4: 引擎 —— step 移动与方向队列

**Files:**
- Modify: `src/game/engine.ts`(追加)
- Test: `src/game/move.test.ts`

**Interfaces:**
- Consumes: Task 3 的 `createInitialState`
- Produces:
  - `step(state: GameState): GameState`(本任务版本:仅移动,碰撞与吃食物在 Task 5/6 加入)
  - `enqueueDirection(state: GameState, dir: Direction): GameState`

- [ ] **Step 1: 写失败测试 src/game/move.test.ts**

```typescript
import { describe, it, expect } from 'vitest'
import { createInitialState, step, enqueueDirection } from './engine'
import type { GameState } from './types'

/** 构造 running 状态的测试辅助 */
function runningState(overrides: Partial<GameState> = {}): GameState {
  return { ...createInitialState(), status: 'running', ...overrides }
}

describe('step 基础移动', () => {
  it('前进一步:头更新、尾移除、长度不变', () => {
    const next = step(runningState())
    expect(next.snake).toEqual([{ x: 12, y: 10 }, { x: 11, y: 10 }, { x: 10, y: 10 }])
    expect(next.direction).toBe('right')
  })

  it('step 不可变:不修改传入的状态对象', () => {
    const s = runningState()
    const before = JSON.parse(JSON.stringify(s)) as GameState
    step(s)
    expect(s).toEqual(before)
  })

  it('非 running 状态调用 step 返回原引用', () => {
    const s = createInitialState() // ready
    expect(step(s)).toBe(s)
  })

  it('一帧最多消费一个方向:队列 [down, left] 本帧只转向 down', () => {
    let s = runningState()
    s = enqueueDirection(s, 'down')
    s = enqueueDirection(s, 'left')
    const next = step(s)
    expect(next.direction).toBe('down')
    expect(next.pendingDirections).toEqual(['left'])
  })

  it('队列空时沿用当前方向', () => {
    const next = step(runningState())
    expect(next.direction).toBe('right')
    expect(next.pendingDirections).toEqual([])
  })
})

describe('enqueueDirection 方向队列', () => {
  it('合法转向入队', () => {
    expect(enqueueDirection(runningState(), 'up').pendingDirections).toEqual(['up'])
  })

  it('与当前方向相同:忽略(返回原引用)', () => {
    const s = runningState()
    expect(enqueueDirection(s, 'right')).toBe(s)
  })

  it('与当前方向相反(180° 掉头):忽略', () => {
    const s = runningState() // 向右
    expect(enqueueDirection(s, 'left')).toBe(s)
  })

  it('与队尾相同或相反:忽略', () => {
    let s = enqueueDirection(runningState(), 'up')
    expect(enqueueDirection(s, 'up')).toBe(s) // 同向
    expect(enqueueDirection(s, 'down')).toBe(s) // 反向(相对队尾 up)
  })

  it('队列已满 2 个:忽略新方向', () => {
    let s = runningState()
    s = enqueueDirection(s, 'up')
    s = enqueueDirection(s, 'left')
    const next = enqueueDirection(s, 'down')
    expect(next).toBe(s)
    expect(next.pendingDirections).toEqual(['up', 'left'])
  })

  it('非 running 状态:忽略(返回原引用)', () => {
    const s = createInitialState() // ready
    expect(enqueueDirection(s, 'up')).toBe(s)
  })
})
```

- [ ] **Step 2: 运行确认失败**

Run: `pnpm test`
Expected: FAIL(`step is not a function` / `enqueueDirection is not a function`)

- [ ] **Step 3: 在 engine.ts 追加实现**

```typescript
import type { Direction, GameState, Point } from './types'

/** 各方向的位移增量 */
const DELTA: Record<Direction, Point> = {
  up: { x: 0, y: -1 },
  down: { x: 0, y: 1 },
  left: { x: -1, y: 0 },
  right: { x: 1, y: 0 },
}

/** 相反方向(用于禁止 180° 掉头) */
const OPPOSITE: Record<Direction, Direction> = {
  up: 'down',
  down: 'up',
  left: 'right',
  right: 'left',
}

/** 步进一次:消费队列头方向,蛇整体前移一格(碰撞/吃食物逻辑后续任务加入) */
export function step(state: GameState): GameState {
  if (state.status !== 'running') return state

  const direction = state.pendingDirections[0] ?? state.direction
  const pendingDirections = state.pendingDirections.slice(1)
  const delta = DELTA[direction]
  const head = state.snake[0]
  const nextHead: Point = { x: head.x + delta.x, y: head.y + delta.y }
  const snake = [nextHead, ...state.snake.slice(0, -1)]

  return { ...state, snake, direction, pendingDirections }
}

/** 方向入队:校验同向/反向/容量,合法则返回新状态 */
export function enqueueDirection(state: GameState, dir: Direction): GameState {
  if (state.status !== 'running') return state
  if (state.pendingDirections.length >= 2) return state
  const last = state.pendingDirections[state.pendingDirections.length - 1] ?? state.direction
  if (dir === last || dir === OPPOSITE[last]) return state
  return { ...state, pendingDirections: [...state.pendingDirections, dir] }
}
```

注意:engine.ts 文件顶部 import 合并为一条(含 `Point`),`DELTA`/`OPPOSITE` 常量放在函数之前。

- [ ] **Step 4: 运行确认通过**

Run: `pnpm test`
Expected: PASS(move.test.ts 全部通过,既有测试不回归)

- [ ] **Step 5: 提交**

```bash
git add src/game/
git commit -m "feat: 引擎移动步进与方向队列"
```

---

### Task 5: 引擎 —— 吃食物、计分与速度递增

**Files:**
- Modify: `src/game/engine.ts`(替换 `step`,追加辅助函数)
- Test: `src/game/food.test.ts`

**Interfaces:**
- Consumes: `GRID_SIZE`、`MIN_STEP_INTERVAL`、`INITIAL_STEP_INTERVAL`、`STEP_DECREMENT`、`SCORES_PER_SPEEDUP`(Task 2)
- Produces:
  - `step` 增强版:吃到食物 → 蛇 +1、score +1、食物重生(不落在蛇身上)、`stepInterval` 按公式重算
  - `calculateStepInterval(score: number): number`(导出以便测试)

- [ ] **Step 1: 写失败测试 src/game/food.test.ts**

```typescript
import { describe, it, expect } from 'vitest'
import { createInitialState, step, calculateStepInterval } from './engine'
import type { GameState } from './types'

function runningState(overrides: Partial<GameState> = {}): GameState {
  return { ...createInitialState(), status: 'running', ...overrides }
}

describe('吃食物', () => {
  it('吃到食物:长度 +1、分数 +1、食物换位', () => {
    const s = runningState({ food: { x: 12, y: 10 } }) // 头(11,10)向右的下一格
    const next = step(s)
    expect(next.snake).toHaveLength(4)
    expect(next.snake[0]).toEqual({ x: 12, y: 10 })
    expect(next.score).toBe(1)
    expect(next.food).not.toEqual({ x: 12, y: 10 })
  })

  it('新食物不落在蛇身上', () => {
    const s = runningState({ food: { x: 12, y: 10 } })
    const next = step(s)
    const onSnake = next.snake.some(seg => seg.x === next.food.x && seg.y === next.food.y)
    expect(onSnake).toBe(false)
  })

  it('未吃到食物:长度与分数不变、间隔不变', () => {
    const next = step(runningState())
    expect(next.snake).toHaveLength(3)
    expect(next.score).toBe(0)
    expect(next.stepInterval).toBe(150)
  })
})

describe('速度递增', () => {
  it('calculateStepInterval:每 5 分减 10ms,下限 60ms', () => {
    expect(calculateStepInterval(0)).toBe(150)
    expect(calculateStepInterval(4)).toBe(150)
    expect(calculateStepInterval(5)).toBe(140)
    expect(calculateStepInterval(9)).toBe(140)
    expect(calculateStepInterval(10)).toBe(130)
    expect(calculateStepInterval(45)).toBe(60)
    expect(calculateStepInterval(100)).toBe(60) // 触底
  })

  it('吃食物时间隔按新分数重算', () => {
    // 分数 4 → 5:floor(5/5)=1 档 → 140
    expect(step(runningState({ score: 4, food: { x: 12, y: 10 } })).stepInterval).toBe(140)
    // 分数 49 → 50:公式 50ms 触底 → 60
    expect(step(runningState({ score: 49, food: { x: 12, y: 10 } })).stepInterval).toBe(60)
  })
})
```

- [ ] **Step 2: 运行确认失败**

Run: `pnpm test`
Expected: FAIL(吃到食物的用例:长度仍为 3 / `calculateStepInterval is not a function`)

- [ ] **Step 3: 替换 engine.ts 中的 step 并追加辅助函数**

将 Task 4 的 `step` 整体替换为:

```typescript
/** 步进一次:消费队列头方向、判定吃食物;碰撞判定见 Task 6 */
export function step(state: GameState): GameState {
  if (state.status !== 'running') return state

  const direction = state.pendingDirections[0] ?? state.direction
  const pendingDirections = state.pendingDirections.slice(1)
  const delta = DELTA[direction]
  const head = state.snake[0]
  const nextHead: Point = { x: head.x + delta.x, y: head.y + delta.y }

  const ateFood = nextHead.x === state.food.x && nextHead.y === state.food.y
  const snake = ateFood ? [nextHead, ...state.snake] : [nextHead, ...state.snake.slice(0, -1)]
  const score = ateFood ? state.score + 1 : state.score
  const stepInterval = ateFood ? calculateStepInterval(score) : state.stepInterval

  if (!ateFood) {
    return { ...state, snake, direction, pendingDirections }
  }

  const free = freeCells(snake)
  if (free.length === 0) {
    // 棋盘被占满:通关,游戏结束
    return { ...state, snake, score, stepInterval, direction, pendingDirections, status: 'over' }
  }
  const food = free[randomIndex(free.length)]
  return { ...state, snake, food, score, stepInterval, direction, pendingDirections }
}
```

追加(放在 `step` 之后):

```typescript
/** 步进间隔公式:每 SCORES_PER_SPEEDUP 分减 STEP_DECREMENT,下限 MIN_STEP_INTERVAL */
export function calculateStepInterval(score: number): number {
  return Math.max(
    MIN_STEP_INTERVAL,
    INITIAL_STEP_INTERVAL - Math.floor(score / SCORES_PER_SPEEDUP) * STEP_DECREMENT,
  )
}

/** 棋盘上未被蛇占据的格子 */
function freeCells(snake: Point[]): Point[] {
  const occupied = new Set(snake.map(seg => `${seg.x},${seg.y}`))
  const free: Point[] = []
  for (let y = 0; y < GRID_SIZE; y++) {
    for (let x = 0; x < GRID_SIZE; x++) {
      if (!occupied.has(`${x},${y}`)) free.push({ x, y })
    }
  }
  return free
}

/** [0, max) 内随机整数 */
function randomIndex(max: number): number {
  return Math.floor(Math.random() * max)
}
```

import 行需补充 `GRID_SIZE`、`MIN_STEP_INTERVAL`、`STEP_DECREMENT`、`SCORES_PER_SPEEDUP`。

- [ ] **Step 4: 运行确认通过**

Run: `pnpm test`
Expected: PASS(food.test.ts 全部 + 既有测试不回归)

- [ ] **Step 5: 提交**

```bash
git add src/game/
git commit -m "feat: 引擎吃食物计分与速度递增"
```

---

### Task 6: 引擎 —— 碰撞与游戏结束

**Files:**
- Modify: `src/game/engine.ts`(在 `step` 中插入碰撞判定)
- Test: `src/game/collision.test.ts`

**Interfaces:**
- Consumes: Task 5 的 `step`
- Produces: `step` 最终版 —— 撞墙/撞自身 → `status: 'over'`(蛇尾让位规则:未吃食物时尾格不算碰撞体)

- [ ] **Step 1: 写失败测试 src/game/collision.test.ts**

```typescript
import { describe, it, expect } from 'vitest'
import { createInitialState, step } from './engine'
import { GRID_SIZE } from './types'
import type { GameState, Point } from './types'

function runningState(overrides: Partial<GameState> = {}): GameState {
  return { ...createInitialState(), status: 'running', ...overrides }
}

describe('碰撞判定', () => {
  it('撞右墙:over', () => {
    const s = runningState({
      snake: [{ x: 19, y: 0 }, { x: 18, y: 0 }, { x: 17, y: 0 }],
      direction: 'right',
    })
    expect(step(s).status).toBe('over')
  })

  it('撞上墙:over', () => {
    const s = runningState({
      snake: [{ x: 0, y: 0 }, { x: 1, y: 0 }, { x: 2, y: 0 }],
      direction: 'left',
    })
    expect(step(s).status).toBe('over')
  })

  it('撞自身:over', () => {
    // 蛇绕圈,头(2,2)向下将进入身体(2,3)
    const s = runningState({
      snake: [{ x: 2, y: 2 }, { x: 2, y: 3 }, { x: 3, y: 3 }, { x: 3, y: 2 }, { x: 4, y: 2 }],
      direction: 'down',
    })
    expect(step(s).status).toBe('over')
  })

  it('尾格让位:未吃食物时,头进入即将空出的尾格不算碰撞', () => {
    // 蛇形四格,头向右 → 下一格是当前尾格(3,2);前进后尾移出,应存活
    const s = runningState({
      snake: [{ x: 2, y: 2 }, { x: 2, y: 3 }, { x: 3, y: 3 }, { x: 3, y: 2 }],
      direction: 'up',
    })
    // 头(2,2)向上到(2,1),不是尾格 —— 改用:头(2,2)向右到(3,2)= 尾格
    const s2 = runningState({
      snake: [{ x: 2, y: 2 }, { x: 2, y: 3 }, { x: 3, y: 3 }, { x: 3, y: 2 }],
      direction: 'right',
    })
    const next = step(s2)
    expect(next.status).toBe('running')
    expect(next.snake[0]).toEqual({ x: 3, y: 2 })
    expect(next.snake).toHaveLength(4)
    // s 变量仅为对照说明,不参与断言
    void s
  })

  it('蛇占满棋盘后吃到最后一格食物:通关 over,蛇长 400', () => {
    const cells: Point[] = []
    for (let y = 0; y < GRID_SIZE; y++) {
      for (let x = 0; x < GRID_SIZE; x++) cells.push({ x, y })
    }
    const snake = cells.slice(0, GRID_SIZE * GRID_SIZE - 1) // 头(18,19),唯一空格(19,19)
    const s = runningState({ snake, food: { x: 19, y: 19 }, direction: 'right' })
    const next = step(s)
    expect(next.status).toBe('over')
    expect(next.snake).toHaveLength(GRID_SIZE * GRID_SIZE)
  })
})
```

- [ ] **Step 2: 运行确认失败**

Run: `pnpm test`
Expected: FAIL(撞墙/撞自身用例:status 仍为 'running')

- [ ] **Step 3: 在 step 中插入碰撞判定**

在 Task 5 版 `step` 的 `const ateFood = ...` 一行**之前**插入:

```typescript
  // 碰撞判定:撞墙,或撞上身体(未吃食物时尾格即将让出,不计入碰撞体)
  const hitWall =
    nextHead.x < 0 || nextHead.x >= GRID_SIZE || nextHead.y < 0 || nextHead.y >= GRID_SIZE
  const willEat = nextHead.x === state.food.x && nextHead.y === state.food.y
  const body = willEat ? state.snake : state.snake.slice(0, -1)
  const hitSelf = body.some(seg => seg.x === nextHead.x && seg.y === nextHead.y)
  if (hitWall || hitSelf) {
    return { ...state, status: 'over' }
  }
```

同时把紧随其后的原 `const ateFood = ...` 行改为复用 `willEat`:

```typescript
  const ateFood = willEat
```

(即删除原来重复计算 `ateFood` 的那一行,改为 `const ateFood = willEat`。)

- [ ] **Step 4: 运行确认通过**

Run: `pnpm test`
Expected: PASS(collision.test.ts 全部 + 全部既有测试不回归)

- [ ] **Step 5: 提交**

```bash
git add src/game/
git commit -m "feat: 引擎碰撞判定与游戏结束"
```

---

### Task 7: Canvas 渲染器

**Files:**
- Create: `src/game/renderer.ts`

**Interfaces:**
- Consumes: Task 2 的 `GameState`、`CANVAS_SIZE`、`CELL_SIZE`、`GRID_SIZE`
- Produces(Task 10 的 GameCanvas 依赖):
  - `setupCanvas(canvas: HTMLCanvasElement): void` —— 按 devicePixelRatio 缩放画布(调用一次)
  - `drawGame(ctx: CanvasRenderingContext2D, state: GameState): void` —— 全量重绘
- 本任务无单测(spec 规定仅测引擎),验证方式:`pnpm typecheck`;视觉在 Task 10 手动验证

- [ ] **Step 1: 写 src/game/renderer.ts**

```typescript
import type { GameState, Point } from './types'
import { CANVAS_SIZE, CELL_SIZE, GRID_SIZE } from './types'

/** 按 devicePixelRatio 设置画布物理尺寸并缩放上下文,保证高分屏清晰(调用一次) */
export function setupCanvas(canvas: HTMLCanvasElement): void {
  const dpr = window.devicePixelRatio || 1
  canvas.width = CANVAS_SIZE * dpr
  canvas.height = CANVAS_SIZE * dpr
  const ctx = canvas.getContext('2d')
  if (ctx !== null) ctx.scale(dpr, dpr)
}

/** 全量重绘一帧:背景 → 网格 → 食物 → 蛇 */
export function drawGame(ctx: CanvasRenderingContext2D, state: GameState): void {
  drawBackground(ctx)
  drawGrid(ctx)
  drawFood(ctx, state.food)
  drawSnake(ctx, state.snake)
}

function drawBackground(ctx: CanvasRenderingContext2D): void {
  ctx.fillStyle = '#0f172a'
  ctx.fillRect(0, 0, CANVAS_SIZE, CANVAS_SIZE)
}

function drawGrid(ctx: CanvasRenderingContext2D): void {
  ctx.strokeStyle = 'rgba(148, 163, 184, 0.07)'
  ctx.lineWidth = 1
  ctx.beginPath()
  for (let i = 1; i < GRID_SIZE; i++) {
    const p = i * CELL_SIZE
    ctx.moveTo(p, 0)
    ctx.lineTo(p, CANVAS_SIZE)
    ctx.moveTo(0, p)
    ctx.lineTo(CANVAS_SIZE, p)
  }
  ctx.stroke()
}

function drawFood(ctx: CanvasRenderingContext2D, food: Point): void {
  const cx = food.x * CELL_SIZE + CELL_SIZE / 2
  const cy = food.y * CELL_SIZE + CELL_SIZE / 2
  ctx.fillStyle = '#ef4444'
  ctx.beginPath()
  ctx.arc(cx, cy, CELL_SIZE / 2 - 4, 0, Math.PI * 2)
  ctx.fill()
  ctx.strokeStyle = 'rgba(252, 165, 165, 0.9)'
  ctx.lineWidth = 2
  ctx.stroke()
}

function drawSnake(ctx: CanvasRenderingContext2D, snake: Point[]): void {
  snake.forEach((seg, i) => {
    // 头部亮绿,身体沿长度向深蓝渐变
    const t = snake.length === 1 ? 0 : i / (snake.length - 1)
    ctx.fillStyle = i === 0 ? '#34d399' : `hsl(${160 + t * 40}, 70%, ${55 - t * 20}%)`
    const pad = 2
    ctx.beginPath()
    ctx.roundRect(
      seg.x * CELL_SIZE + pad,
      seg.y * CELL_SIZE + pad,
      CELL_SIZE - pad * 2,
      CELL_SIZE - pad * 2,
      i === 0 ? 8 : 6,
    )
    ctx.fill()
  })
}
```

- [ ] **Step 2: 类型检查**

Run: `pnpm typecheck`
Expected: 无错误

- [ ] **Step 3: 提交**

```bash
git add src/game/renderer.ts
git commit -m "feat: canvas 渲染器(网格/食物/渐变蛇身/dpr 适配)"
```

---

### Task 8: 组合式函数 —— useGameLoop 与 useHighScore

**Files:**
- Create: `src/composables/useGameLoop.ts`、`src/composables/useHighScore.ts`

**Interfaces:**
- Produces(Task 9/10 依赖):
  - `useGameLoop(onTick: () => void, getInterval: () => number): { start: () => void; stop: () => void }`
  - `useHighScore(): { highScore: Ref<number>; submit: (score: number) => boolean }`
- 验证方式:`pnpm typecheck`(rAF 与 localStorage 不做单测,spec 规定)

- [ ] **Step 1: 写 src/composables/useGameLoop.ts**

```typescript
import { onBeforeUnmount } from 'vue'

/**
 * 固定步进游戏循环:requestAnimationFrame + 时间累积器。
 * 到达步进间隔时调用 onTick(执行一次引擎 step);间隔通过 getInterval
 * 动态读取,以支持分数驱动的加速。组件卸载时自动停止。
 */
export function useGameLoop(onTick: () => void, getInterval: () => number) {
  let rafId = 0
  let lastTime = 0
  let elapsed = 0
  let running = false

  const frame = (now: number): void => {
    elapsed += now - lastTime
    lastTime = now
    const interval = getInterval()
    // while:间隔很小(如切后台后回来)时最多连续补偿若干步,避免掉帧堆积
    while (running && elapsed >= interval && interval > 0) {
      elapsed -= interval
      onTick()
    }
    if (running) rafId = requestAnimationFrame(frame)
  }

  function start(): void {
    if (running) return
    running = true
    lastTime = performance.now()
    elapsed = 0
    rafId = requestAnimationFrame(frame)
  }

  function stop(): void {
    running = false
    cancelAnimationFrame(rafId)
  }

  onBeforeUnmount(stop)
  return { start, stop }
}
```

- [ ] **Step 2: 写 src/composables/useHighScore.ts**

```typescript
import { ref } from 'vue'
import type { Ref } from 'vue'

const STORAGE_KEY = 'snake.highScore'

/** 读取持久化的最高分;存储不可用时返回 0(内存降级) */
function loadHighScore(): number {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw === null) return 0
    const n = Number(raw)
    return Number.isFinite(n) && n >= 0 ? Math.floor(n) : 0
  } catch {
    return 0
  }
}

/**
 * 最高分管理:读取 localStorage,游戏结束时 submit 提交分数。
 * 写入失败(隐私模式等)时仅在内存中生效,不抛错。
 */
export function useHighScore(): { highScore: Ref<number>; submit: (score: number) => boolean } {
  const highScore = ref(loadHighScore())

  function submit(score: number): boolean {
    if (score <= highScore.value) return false
    highScore.value = score
    try {
      localStorage.setItem(STORAGE_KEY, String(score))
    } catch {
      // 存储不可用:保留内存值即可
    }
    return true
  }

  return { highScore, submit }
}
```

- [ ] **Step 3: 类型检查**

Run: `pnpm typecheck`
Expected: 无错误

- [ ] **Step 4: 提交**

```bash
git add src/composables/
git commit -m "feat: 游戏循环与最高分组合式函数"
```

---

### Task 9: UI 组件 —— ScoreBoard 与 GameOverlay

**Files:**
- Create: `src/components/ScoreBoard.vue`、`src/components/GameOverlay.vue`

**Interfaces:**
- Produces(Task 10 依赖):
  - `ScoreBoard` props:`score: number`、`highScore: number`
  - `GameOverlay` props:`status: GameStatus`、`score: number`;emit:`primary`(开始/继续/重开按钮点击)
- 验证方式:`pnpm typecheck`;视觉在 Task 10 手动验证

- [ ] **Step 1: 写 src/components/ScoreBoard.vue**

```vue
<script setup lang="ts">
defineProps<{
  score: number
  highScore: number
}>()
</script>

<template>
  <div class="score-board">
    <div class="score-item">
      <span class="score-label">得分</span>
      <span class="score-value">{{ score }}</span>
    </div>
    <div class="score-item">
      <span class="score-label">最高分</span>
      <span class="score-value high">{{ highScore }}</span>
    </div>
  </div>
</template>

<style scoped>
.score-board {
  display: flex;
  gap: 24px;
  justify-content: center;
  margin-bottom: 16px;
}

.score-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  min-width: 96px;
  padding: 8px 20px;
  border-radius: 10px;
  background: rgba(148, 163, 184, 0.08);
}

.score-label {
  font-size: 13px;
  color: #94a3b8;
}

.score-value {
  font-size: 26px;
  font-weight: 700;
  color: #e2e8f0;
  font-variant-numeric: tabular-nums;
}

.score-value.high {
  color: #fbbf24;
}
</style>
```

- [ ] **Step 2: 写 src/components/GameOverlay.vue**

```vue
<script setup lang="ts">
import type { GameStatus } from '../game/types'

defineProps<{
  status: GameStatus
  score: number
}>()

const emit = defineEmits<{ primary: [] }>()
</script>

<template>
  <div v-if="status !== 'running'" class="overlay">
    <template v-if="status === 'ready'">
      <h1 class="overlay-title">🐍 贪吃蛇</h1>
      <p class="overlay-hint">方向键 / WASD 移动 · 空格暂停</p>
      <button class="overlay-btn" @click="emit('primary')">开始游戏(空格)</button>
    </template>
    <template v-else-if="status === 'paused'">
      <h1 class="overlay-title">⏸ 已暂停</h1>
      <button class="overlay-btn" @click="emit('primary')">继续(空格)</button>
    </template>
    <template v-else>
      <h1 class="overlay-title">💀 游戏结束</h1>
      <p class="overlay-hint">本局得分 {{ score }}</p>
      <button class="overlay-btn" @click="emit('primary')">再来一局(回车)</button>
    </template>
  </div>
</template>

<style scoped>
.overlay {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16px;
  background: rgba(2, 6, 23, 0.72);
  border-radius: 12px;
  backdrop-filter: blur(2px);
}

.overlay-title {
  font-size: 34px;
  color: #f1f5f9;
}

.overlay-hint {
  font-size: 15px;
  color: #94a3b8;
}

.overlay-btn {
  padding: 10px 28px;
  font-size: 16px;
  color: #052e16;
  background: #34d399;
  border: none;
  border-radius: 8px;
  cursor: pointer;
}

.overlay-btn:hover {
  background: #6ee7b7;
}
</style>
```

- [ ] **Step 3: 类型检查**

Run: `pnpm typecheck`
Expected: 无错误

- [ ] **Step 4: 提交**

```bash
git add src/components/
git commit -m "feat: 计分板与游戏覆盖层组件"
```

---

### Task 10: 组装 —— GameCanvas、App 与完整手动验证

**Files:**
- Create: `src/components/GameCanvas.vue`
- Modify: `src/App.vue`(替换占位版)

**Interfaces:**
- Consumes: 引擎全部函数(Task 3-6)、`setupCanvas`/`drawGame`(Task 7)、`useGameLoop`/`useHighScore`(Task 8)、`ScoreBoard`/`GameOverlay`(Task 9)
- Produces: 完整可玩的游戏。`GameCanvas` emits:`score [score: number]`、`gameOver [score: number]`

- [ ] **Step 1: 写 src/components/GameCanvas.vue**

```vue
<script setup lang="ts">
import { onBeforeUnmount, onMounted, shallowRef, watch } from 'vue'
import {
  createInitialState,
  step,
  enqueueDirection,
  startGame,
  togglePause,
  resetGame,
} from '../game/engine'
import { drawGame, setupCanvas } from '../game/renderer'
import { useGameLoop } from '../composables/useGameLoop'
import type { Direction } from '../game/types'
import GameOverlay from './GameOverlay.vue'

const emit = defineEmits<{
  score: [score: number]
  gameOver: [score: number]
}>()

const canvasRef = shallowRef<HTMLCanvasElement | null>(null)
const state = shallowRef(createInitialState())
let lastStatus = state.value.status

// 游戏循环:到步进点执行一次引擎 step(间隔动态读取,支持加速)
const { start: startLoop, stop: stopLoop } = useGameLoop(
  () => {
    state.value = step(state.value)
  },
  () => state.value.stepInterval,
)

function draw(): void {
  const canvas = canvasRef.value
  if (canvas === null) return
  const ctx = canvas.getContext('2d')
  if (ctx === null) return
  drawGame(ctx, state.value)
}

// 状态整体替换后:重绘 + 上报分数;进入 over 时停循环并上报最终分
watch(state, (s) => {
  draw()
  emit('score', s.score)
  if (lastStatus !== 'over' && s.status === 'over') {
    emit('gameOver', s.score)
    stopLoop()
  }
  lastStatus = s.status
})

function turn(dir: Direction): void {
  state.value = enqueueDirection(state.value, dir)
}

function onSpace(): void {
  const s = state.value
  if (s.status === 'ready') {
    state.value = startGame(s)
    startLoop()
  } else if (s.status === 'running') {
    state.value = togglePause(s)
    stopLoop()
  } else if (s.status === 'paused') {
    state.value = togglePause(s)
    startLoop()
  }
}

function onEnter(): void {
  if (state.value.status !== 'over') return
  state.value = resetGame(state.value)
  lastStatus = 'running'
  startLoop()
}

/** 覆盖层按钮:over 时重开,其余等同空格 */
function onPrimary(): void {
  if (state.value.status === 'over') onEnter()
  else onSpace()
}

function onKeyDown(e: KeyboardEvent): void {
  const actions: Record<string, () => void> = {
    ArrowUp: () => turn('up'),
    KeyW: () => turn('up'),
    ArrowDown: () => turn('down'),
    KeyS: () => turn('down'),
    ArrowLeft: () => turn('left'),
    KeyA: () => turn('left'),
    ArrowRight: () => turn('right'),
    KeyD: () => turn('right'),
    Space: onSpace,
    Enter: onEnter,
  }
  const action = actions[e.code]
  if (action === undefined) return
  e.preventDefault() // 阻止方向键/空格滚动页面
  action()
}

onMounted(() => {
  const canvas = canvasRef.value
  if (canvas !== null) setupCanvas(canvas)
  window.addEventListener('keydown', onKeyDown)
  draw()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeyDown)
  stopLoop()
})
</script>

<template>
  <div class="canvas-wrap">
    <!-- 物理尺寸由 setupCanvas 按 dpr 设置,CSS 尺寸固定 640 -->
    <canvas ref="canvasRef" class="game-canvas" />
    <GameOverlay :status="state.status" :score="state.score" @primary="onPrimary" />
  </div>
</template>

<style scoped>
.canvas-wrap {
  position: relative;
}

.game-canvas {
  display: block;
  width: 640px;
  height: 640px;
  border-radius: 12px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
}
</style>
```

- [ ] **Step 2: 替换 src/App.vue**

```vue
<script setup lang="ts">
import { ref } from 'vue'
import ScoreBoard from './components/ScoreBoard.vue'
import GameCanvas from './components/GameCanvas.vue'
import { useHighScore } from './composables/useHighScore'

const score = ref(0)
const { highScore, submit } = useHighScore()

function onScore(n: number): void {
  score.value = n
}

function onGameOver(finalScore: number): void {
  submit(finalScore)
}
</script>

<template>
  <main class="app">
    <ScoreBoard :score="score" :high-score="highScore" />
    <GameCanvas @score="onScore" @game-over="onGameOver" />
    <p class="controls-hint">方向键 / WASD 移动 · 空格 暂停/继续 · 回车 重开</p>
  </main>
</template>

<style scoped>
.controls-hint {
  margin-top: 14px;
  font-size: 13px;
  color: #64748b;
}
</style>
```

- [ ] **Step 3: 类型检查与全量测试**

Run: `pnpm typecheck`,Expected: 无错误
Run: `pnpm test`,Expected: PASS(全部)

- [ ] **Step 4: 启动游戏手动验证**

Run: `pnpm dev`(前台运行,让用户试玩)

请用户按清单验证(每项打勾):
1. 窗口弹出,显示计分板 + 画布 + "开始游戏" 覆盖层
2. 空格开始,蛇向右移动,方向键/WASD 可转向;连按两键不 180° 掉头
3. 吃到食物:蛇变长、得分 +1、食物换位
4. 空格暂停/继续正常,覆盖层文案正确
5. 撞墙或撞自己:游戏结束覆盖层显示本局得分
6. 回车重开正常;故意输一局后最高分更新,重启应用(`pnpm dev`)后最高分仍在
7. 得分超过 5 分后蛇明显加速

- [ ] **Step 5: 提交**

```bash
git add src/components/GameCanvas.vue src/App.vue
git commit -m "feat: 组装游戏画布与主界面,游戏完整可玩"
```

---

### Task 11: 生产构建与最终验收

**Files:**
- 无新文件;验证与收尾

**Interfaces:**
- Consumes: 全部前置任务

- [ ] **Step 1: 全量测试与类型检查**

Run: `pnpm test && pnpm typecheck`
Expected: 全部 PASS、无类型错误

- [ ] **Step 2: 生产构建**

Run: `pnpm build`
Expected: 构建成功,生成 `out/main/index.js`、`out/preload/index.js`、`out/renderer/`

- [ ] **Step 3: 验证构建产物可运行(可选,若环境支持)**

Run: `npx electron out/main/index.js`
Expected: 窗口正常打开且游戏可玩(与 dev 模式表现一致)

- [ ] **Step 4: 收尾提交(如有零星修正)**

```bash
git status # 确认无遗漏文件;如有修正:
git add -A
git commit -m "chore: 构建验证与收尾"
```

---

## 自审记录

- **Spec 覆盖**:目录结构(Task 1/7/8/9/10)、引擎纯函数与不可变(Task 3-6 + 测试)、方向队列防掉头(Task 4)、速度公式(Task 5)、碰撞含尾格让位与满盘通关(Task 6)、localStorage try/catch 降级(Task 8)、键盘映射与 preventDefault(Task 10)、dpr 适配(Task 7)、窗口参数与主进程错误退出(Task 1)、测试计划各项(Task 3-6 测试逐一对应)——均已覆盖。
- **占位符扫描**:无 TBD/TODO;所有代码步骤给出完整代码。
- **类型一致性**:`GameState` 字段、`step`/`enqueueDirection`/`setupCanvas`/`drawGame`/`useGameLoop`/`useHighScore`/emit 名称在定义与消费任务间逐一核对一致;Task 6 的 `willEat` 复用避免了与 `ateFood` 的重复计算。
