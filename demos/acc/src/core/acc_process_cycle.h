#ifndef ACC_PROCESS_CYCLE_H
#define ACC_PROCESS_CYCLE_H

#include "core/acc_core.h"

/* master 主循环：阻塞信号后 sigsuspend 等待，派发 reap/respawn、
 * quit（优雅）、terminate（限时强杀）、reconfigure（仅记日志）；
 * 内部 exit，不返回 */
void acc_master_process_cycle(acc_context_t *ctx);

/* worker 入口（acc_spawn_process 的 proc）：worker init → 事件主循环
 * → 退出清理。acc_worker_quit 置位（通道命令或信号）后优雅退出 */
void acc_worker_process_cycle(acc_context_t *ctx, void *data);

/* worker 单轮事件循环：时间缓存刷新 → accept 互斥 trylock（持锁则
 * POST_EVENTS 模式）→ epoll_wait(500ms) → 兜底放锁 → posted 队列
 * → 超时清扫 */
void acc_process_events_and_timers(acc_context_t *ctx);

#endif /* ACC_PROCESS_CYCLE_H */
