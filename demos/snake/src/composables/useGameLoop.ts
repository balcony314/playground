import { onBeforeUnmount } from 'vue'

/** 单帧时间增量上限 ms:后台/卡顿恢复时避免一次性补偿巨量步数 */
const MAX_FRAME_DELTA = 250

/**
 * 固定步进游戏循环:requestAnimationFrame + 时间累积器。
 * 到达步进间隔时调用 onTick(执行一次引擎 step);间隔通过 getInterval
 * 动态读取,以支持分数驱动的加速。组件卸载时自动停止。
 */
export function useGameLoop(onTick: () => void, getInterval: () => number) {
  let rafId = 0
  let lastTime = 0
  let elapsed = 0
  let running = false

  const frame = (now: number): void => {
    // 钳制单帧增量:rAF 在窗口最小化时暂停,恢复时的巨大 delta 不参与补偿
    elapsed += Math.min(now - lastTime, MAX_FRAME_DELTA)
    lastTime = now
    const interval = getInterval()
    // while:间隔很小(如切后台后回来)时最多连续补偿若干步,避免掉帧堆积
    while (running && elapsed >= interval && interval > 0) {
      elapsed -= interval
      onTick()
    }
    if (running) rafId = requestAnimationFrame(frame)
  }

  function start(): void {
    if (running) return
    running = true
    lastTime = performance.now()
    elapsed = 0
    rafId = requestAnimationFrame(frame)
  }

  function stop(): void {
    running = false
    cancelAnimationFrame(rafId)
  }

  onBeforeUnmount(stop)
  return { start, stop }
}
