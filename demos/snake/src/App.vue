<script setup lang="ts">
import { ref } from 'vue'
import ScoreBoard from './components/ScoreBoard.vue'
import GameCanvas from './components/GameCanvas.vue'
import { useHighScore } from './composables/useHighScore'

const score = ref(0)
const { highScore, submit } = useHighScore()

function onScore(n: number): void {
  score.value = n
}

function onGameOver(finalScore: number): void {
  submit(finalScore)
}
</script>

<template>
  <main class="app">
    <ScoreBoard :score="score" :high-score="highScore" />
    <GameCanvas @score="onScore" @game-over="onGameOver" />
    <p class="controls-hint">方向键 / WASD 移动 · 空格 暂停/继续 · 回车 重开</p>
  </main>
</template>

<style scoped>
.controls-hint {
  margin-top: 14px;
  font-size: 13px;
  color: #64748b;
}
</style>
