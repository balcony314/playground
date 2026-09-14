#ifndef ACC_EPOLL_H
#define ACC_EPOLL_H

#include <stdint.h>
#include <sys/epoll.h>

#include "acc_event.h"

/* 事件方向：取值即 epoll 事件掩码，注册时直接使用 */
#define ACC_EVENT_READ  (EPOLLIN | EPOLLRDHUP)
#define ACC_EVENT_WRITE EPOLLOUT

/* process_events 标志：就绪事件投 posted 队列延迟执行，而非立即回调 */
#define ACC_POST_EVENTS 1u

/* 创建 epoll 实例与就绪数组，初始化 posted 队列；成功返回 0，失败返回 -1。
 * 重复调用会先释放旧实例 */
int acc_epoll_init(void);

/* 关闭 epoll 实例并释放就绪数组，posted 队列复位为空。
 * 前置契约：调用方须先排空 posted 队列（acc_event_process_posted），
 * 否则残留事件的 posted 位失真，重新 init 后无法再次入队 */
void acc_epoll_done(void);

/* 注册/注销事件（standalone 或连接事件）。
 * 同 fd 对侧事件已 active 时用 EPOLL_CTL_MOD 合并掩码，否则 EPOLL_CTL_ADD；
 * 成功返回 0，失败返回 -1 */
int acc_epoll_add_event(acc_event_t *ev, int event);
int acc_epoll_del_event(acc_event_t *ev, int event);

/* 连接事件整组注册/注销：fd 只注册一次，data.ptr 挂 c->read，
 * IN/OUT 掩码按 c->read/c->write 的 active 位合成。
 * 实现归 acc_connection.c（Task 10），此处仅声明 */
int acc_epoll_add_connection(acc_connection_t *c);
void acc_epoll_del_connection(acc_connection_t *c);

/* 等待并派发就绪事件：timer 为 epoll_wait 超时毫秒数。
 * 返回本次就绪的 fd 数；EINTR 返回 0，其余错误返回 -1。
 * flags 含 ACC_POST_EVENTS 时就绪事件入 posted 队列，否则立即调 handler */
int acc_epoll_process_events(int timer, uint32_t flags);

#endif /* ACC_EPOLL_H */
