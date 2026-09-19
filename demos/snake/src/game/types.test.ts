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
