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
