import {
  GRID_SIZE,
  INITIAL_STEP_INTERVAL,
  MIN_STEP_INTERVAL,
  SCORES_PER_SPEEDUP,
  STEP_DECREMENT,
  type Direction,
  type GameState,
  type Point,
} from './types'

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

/** 步进一次:消费队列头方向、判定碰撞与吃食物 */
export function step(state: GameState): GameState {
  if (state.status !== 'running') return state

  const direction = state.pendingDirections[0] ?? state.direction
  const pendingDirections = state.pendingDirections.slice(1)
  const delta = DELTA[direction]
  const head = state.snake[0]
  const nextHead: Point = { x: head.x + delta.x, y: head.y + delta.y }

  // 碰撞判定:撞墙,或撞上身体(未吃食物时尾格即将让出,不计入碰撞体)
  const hitWall =
    nextHead.x < 0 || nextHead.x >= GRID_SIZE || nextHead.y < 0 || nextHead.y >= GRID_SIZE
  const willEat = nextHead.x === state.food.x && nextHead.y === state.food.y
  const body = willEat ? state.snake : state.snake.slice(0, -1)
  const hitSelf = body.some(seg => seg.x === nextHead.x && seg.y === nextHead.y)
  if (hitWall || hitSelf) {
    return { ...state, status: 'over' }
  }

  const ateFood = willEat
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

/** 方向入队:校验同向/反向/容量,合法则返回新状态 */
export function enqueueDirection(state: GameState, dir: Direction): GameState {
  if (state.status !== 'running') return state
  if (state.pendingDirections.length >= 2) return state
  const last = state.pendingDirections[state.pendingDirections.length - 1] ?? state.direction
  if (dir === last || dir === OPPOSITE[last]) return state
  return { ...state, pendingDirections: [...state.pendingDirections, dir] }
}
