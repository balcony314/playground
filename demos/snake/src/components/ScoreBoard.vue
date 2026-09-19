<script setup lang="ts">
defineProps<{
  score: number
  highScore: number
}>()
</script>

<template>
  <div class="score-board">
    <div class="score-item">
      <span class="score-label">得分</span>
      <span class="score-value">{{ score }}</span>
    </div>
    <div class="score-item">
      <span class="score-label">最高分</span>
      <span class="score-value high">{{ highScore }}</span>
    </div>
  </div>
</template>

<style scoped>
.score-board {
  display: flex;
  gap: 24px;
  justify-content: center;
  margin-bottom: 16px;
}

.score-item {
  display: flex;
  flex-direction: column;
  align-items: center;
  min-width: 96px;
  padding: 8px 20px;
  border-radius: 10px;
  background: rgba(148, 163, 184, 0.08);
}

.score-label {
  font-size: 13px;
  color: #94a3b8;
}

.score-value {
  font-size: 26px;
  font-weight: 700;
  color: #e2e8f0;
  font-variant-numeric: tabular-nums;
}

.score-value.high {
  color: #fbbf24;
}
</style>
