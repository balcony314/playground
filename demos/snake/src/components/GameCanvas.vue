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
