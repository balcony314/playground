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
