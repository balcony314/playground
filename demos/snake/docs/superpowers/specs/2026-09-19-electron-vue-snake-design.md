# Electron + Vue 贪吃蛇游戏 设计文档

日期:2026-09-19
状态:已确认

## 概述

在 `/home/ywdxz/code/test/ts-demo` 目录内搭建 Electron + Vue 3 贪吃蛇游戏(标准版):Canvas 渲染、键盘操控、计分与最高分持久化、速度递增、暂停/重开。

## 技术栈

- **框架**:Electron + Vue 3(组合式 API)+ TypeScript
- **脚手架**:electron-vite(方案 A,已确认)
- **测试**:Vitest(仅针对纯逻辑 `engine.ts`)
- **包管理**:pnpm(若不可用则回退 npm)

## 目录结构

```
ts-demo/
├── electron/
│   ├── main.ts          # 主进程:创建 BrowserWindow(720×820,禁用菜单栏)
│   └── preload.ts       # 预加载脚本(contextIsolation)
├── src/
│   ├── game/
│   │   ├── engine.ts    # 纯逻辑:蛇状态、移动、碰撞、食物生成、计分
│   │   ├── renderer.ts  # Canvas 绘制:网格、蛇身、食物
│   │   └── types.ts     # 共享类型(Direction、Point、GameState 等)
│   ├── composables/
│   │   └── useGameLoop.ts  # rAF + 时间累积器,固定步进调用 engine.step
│   ├── components/
│   │   ├── GameCanvas.vue   # 画布 + 键盘监听(方向队列)
│   │   ├── ScoreBoard.vue   # 当前分 / 最高分展示
│   │   └── GameOverlay.vue  # 开始 / 暂停 / 结束覆盖层
│   ├── App.vue
│   ├── main.ts
│   └── style.css       # 全局样式(深色主题)
├── package.json
├── electron.vite.config.ts
├── tsconfig.json / tsconfig.node.json
├── vitest.config.ts
└── docs/superpowers/specs/  # 本文档
```

现有 `app.ts` / `app.js` 与本项目无关,保留不动。

## 核心机制

### 游戏状态(engine.ts,纯函数)

```typescript
interface GameState {
  snake: Point[]            // 蛇身,snake[0] 为头
  food: Point               // 食物坐标
  direction: Direction      // 当前实际移动方向
  pendingDirections: Direction[]  // 输入方向队列(最多缓存 2 个)
  score: number
  status: 'ready' | 'running' | 'paused' | 'over'
  stepInterval: number      // 当前步进间隔 ms
}
```

- `createInitialState(): GameState` — 蛇初始长度 3,居中(第 10 行,列 9/10/11,头在右侧),向右移动
- `step(state): GameState` — 不可变:始终返回新状态,不修改入参
- `enqueueDirection(state, dir): GameState` — 入队校验:与队尾(或当前方向)相同或相反则忽略

### 数据流

```
键盘 → enqueueDirection(方向队列,防同帧双转/180°掉头)
     → 游戏循环按 stepInterval 触发 step()
     → 新 GameState → renderer.draw(state)
     → status/score 变化 → Vue 响应式更新 ScoreBoard / Overlay
```

### 游戏循环(useGameLoop.ts)

- `requestAnimationFrame` + 时间累积器:`elapsed >= stepInterval` 时调用一次 `step`
- rAF 回调内不直接改 Vue 响应式状态,通过回调返回新 state,由组件统一提交,避免每帧触发无关更新

### 速度递增

- 初始 150ms,每得 5 分减 10ms,下限 60ms:`stepInterval = max(60, 150 - floor(score/5)*10)`

### 碰撞与结算

- 撞墙(越界)或撞自身 → `status: 'over'`
- 吃到食物:蛇长 +1、score +1、重新生成食物(随机且不落在蛇身上)
- 蛇占满棋盘(无处置放食物)→ 视为通关,`status: 'over'`

### 键盘(在渲染进程 window 上监听)

- 方向键 / WASD:转向(running 状态)
- 空格:开始 / 暂停 / 恢复
- 回车:结束后重开
- 监听在组件卸载时移除;阻止方向键/空格默认滚动行为

### 最高分

- key:`snake.highScore`,localStorage 读写均 try/catch(隐私模式/存储被禁时降级为内存变量)
- 仅在 status 变为 `over` 时读取比较并写入

## 渲染(renderer.ts)

- 画布 20×20 格,每格 32px(640×640),devicePixelRatio 适配保证清晰
- 网格:深色背景 + 微弱网格线
- 蛇:头部高亮,身体圆角矩形,颜色沿长度渐变
- 食物:红色圆点,带浅色描边
- 每帧根据 state 全量重绘(无脏矩形优化,规模不需要)

## 窗口(electron/main.ts)

- 720×820,`resizable: false`,隐藏菜单栏,`contextIsolation: true`、`nodeIntegration: false`
- 开发模式加载 dev server URL,生产加载打包产物(electron-vite 默认行为)

## 错误处理

- localStorage 读写:try/catch + 内存降级
- 主进程窗口创建失败:打印错误日志并退出非零码
- 游戏逻辑为纯函数,无 I/O 异常面

## 测试计划(Vitest,仅 engine.ts)

- 初始状态:蛇长度、位置、方向、分数
- 移动:正常前进一步蛇头更新、蛇尾移除
- 吃食物:长度 +1、分数 +1、食物换位且不在蛇身上
- 碰撞:撞墙 over、撞自身 over
- 方向队列:同向忽略、反向忽略、合法转向生效、一帧最多消费一个方向
- 速度:分数跨 5 分档时间隔递减、不低于 60ms
- 不可变性:step 不修改原状态对象

覆盖率目标:engine.ts 100% 分支覆盖。

## 非目标(YAGNI)

- 音效、主题切换、难度选择、穿墙模式(增强版内容,本次不做)
- 移动端/触屏支持
- 打包安装包(electron-builder 配置),仅保证 dev 运行与 build 产物正常
- E2E 测试
