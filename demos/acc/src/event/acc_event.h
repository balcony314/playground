#ifndef ACC_EVENT_H
#define ACC_EVENT_H

#include "util/acc_list.h" /* 跨层引用统一 src 根相对路径 */

/* 事件对象：连接 fd 的读/写各一个，监听 fd、唤醒 channel 等独立 fd 一个 */
typedef struct acc_event_s acc_event_t;

/* 事件就绪后的回调：worker 线程内执行（立即派发或 posted 队列出队时） */
typedef void (*acc_event_handler_pt)(acc_event_t *ev);

/* 连接对象前向声明：事件层仅持有指针，完整定义在 acc_connection.h（Task 10） */
struct acc_connection_s;
typedef struct acc_connection_s acc_connection_t;

struct acc_event_s {
    void *data;                /* 归属者自用：连接/监听/channel 对象指针 */
    acc_event_handler_pt handler;
    struct acc_list queue;     /* posted 链表节点 */
    struct acc_event_s *peer;  /* 对端事件：连接 read/write 互指，standalone 为 NULL */
    unsigned write:1;          /* 1=写事件，0=读事件 */
    unsigned active:1;         /* 已注册进 epoll */
    unsigned ready:1;          /* epoll_wait 已报告就绪 */
    unsigned posted:1;         /* 已挂入 posted 延迟队列 */
    unsigned conn:1;           /* 连接事件：IN/OUT 共用一次注册，data.ptr 挂 c->read */
    unsigned accept:1;         /* 监听 accept 事件：accept 互斥持锁窗口内优先派发 */
    int fd;                    /* 冗余存储的 fd，便于 epoll 注册 */
};

/* posted 延迟执行队列：ACC_POST_EVENTS 模式下就绪事件先进此队列，
 * 由 worker 在拿锁窗口外统一执行，避免 handler 在事件循环内长耗时 */
extern struct acc_list acc_posted_events;

/* 投入 posted 队列（尾插保序）；已 posted 的事件不重复入队 */
void acc_event_post(acc_event_t *ev);

/* 从 posted 队列摘除并清 posted 位（事件归属者销毁前必须调用，防悬垂） */
void acc_event_delete_posted(acc_event_t *ev);

/* 逐个出队执行：摘节点、清 posted 位后调 handler；handler 可安全重投自身 */
void acc_event_process_posted(struct acc_list *queue);

/* 仅派发 posted 队列中 accept 位为 1 的事件（摘节点+清 posted+调 handler），
 * 其余事件原样留在队列。accept 互斥模式下供持锁窗口内先行处理，
 * 让 accept 尽快完成以缩短持锁时间（nginx posted_accept 语义） */
void acc_event_process_posted_accept(void);

/* accept 互斥标志：多 worker 争抢监听 fd 的惊群抑制开关（Task 10+ 接线） */
extern int acc_use_accept_mutex;
extern int acc_accept_mutex_held;
extern int acc_accept_disabled;

#endif /* ACC_EVENT_H */
