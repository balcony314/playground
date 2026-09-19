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
