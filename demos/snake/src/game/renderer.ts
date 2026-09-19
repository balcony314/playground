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
