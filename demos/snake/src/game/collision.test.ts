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
      direction: 'right',
    })
    const next = step(s)
    expect(next.status).toBe('running')
    expect(next.snake[0]).toEqual({ x: 3, y: 2 })
    expect(next.snake).toHaveLength(4)
  })

  it('蛇占满棋盘后吃到最后一格食物:通关 over,蛇长 400', () => {
    const cells: Point[] = []
    for (let y = 0; y < GRID_SIZE; y++) {
      for (let x = 0; x < GRID_SIZE; x++) cells.push({ x, y })
    }
    // 修正:snake[0] 为头,反转使头位于 (18,19)、唯一空格 (19,19)(原数组顺序头为 (0,0),与注释意图不符)
    const snake = cells.slice(0, GRID_SIZE * GRID_SIZE - 1).reverse()
    const s = runningState({ snake, food: { x: 19, y: 19 }, direction: 'right' })
    const next = step(s)
    expect(next.status).toBe('over')
    expect(next.snake).toHaveLength(GRID_SIZE * GRID_SIZE)
  })
})
