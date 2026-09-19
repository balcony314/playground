<script setup lang="ts">
import type { GameStatus } from '../game/types'

defineProps<{
  status: GameStatus
  score: number
}>()

const emit = defineEmits<{ primary: [] }>()
</script>

<template>
  <div v-if="status !== 'running'" class="overlay">
    <template v-if="status === 'ready'">
      <h1 class="overlay-title">🐍 贪吃蛇</h1>
      <p class="overlay-hint">方向键 / WASD 移动 · 空格暂停</p>
      <button class="overlay-btn" @click="emit('primary')">开始游戏(空格)</button>
    </template>
    <template v-else-if="status === 'paused'">
      <h1 class="overlay-title">⏸ 已暂停</h1>
      <button class="overlay-btn" @click="emit('primary')">继续(空格)</button>
    </template>
    <template v-else-if="status === 'over'">
      <h1 class="overlay-title">💀 游戏结束</h1>
      <p class="overlay-hint">本局得分 {{ score }}</p>
      <button class="overlay-btn" @click="emit('primary')">再来一局(回车)</button>
    </template>
  </div>
</template>

<style scoped>
.overlay {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 16px;
  background: rgba(2, 6, 23, 0.72);
  border-radius: 12px;
  backdrop-filter: blur(2px);
}

.overlay-title {
  font-size: 34px;
  color: #f1f5f9;
}

.overlay-hint {
  font-size: 15px;
  color: #94a3b8;
}

.overlay-btn {
  padding: 10px 28px;
  font-size: 16px;
  color: #052e16;
  background: #34d399;
  border: none;
  border-radius: 8px;
  cursor: pointer;
}

.overlay-btn:hover {
  background: #6ee7b7;
}
</style>
