import { ref } from 'vue'
import type { Ref } from 'vue'

const STORAGE_KEY = 'snake.highScore'

/** 读取持久化的最高分;存储不可用时返回 0(内存降级) */
function loadHighScore(): number {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (raw === null) return 0
    const n = Number(raw)
    return Number.isFinite(n) && n >= 0 ? Math.floor(n) : 0
  } catch {
    return 0
  }
}

/**
 * 最高分管理:读取 localStorage,游戏结束时 submit 提交分数。
 * 写入失败(隐私模式等)时仅在内存中生效,不抛错。
 */
export function useHighScore(): { highScore: Ref<number>; submit: (score: number) => boolean } {
  const highScore = ref(loadHighScore())

  function submit(score: number): boolean {
    if (!Number.isFinite(score) || score < 0) return false
    if (score <= highScore.value) return false
    highScore.value = score
    try {
      localStorage.setItem(STORAGE_KEY, String(score))
    } catch {
      // 存储不可用:保留内存值即可
    }
    return true
  }

  return { highScore, submit }
}
