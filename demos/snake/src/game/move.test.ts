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
