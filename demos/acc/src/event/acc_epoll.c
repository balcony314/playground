/* epoll 事件封装：事件的注册/注销、就绪派发与 posted 延迟执行队列。
 * 同一 fd 的读/写两个事件对象（conn:1 且 peer 互指）共用一次 epoll 注册，
 * data.ptr 固定挂读事件；standalone 事件（conn:0）独立注册挂自身 */
#include "acc_epoll.h"

#include <errno.h>
#include <stdlib.h>
#include <unistd.h>

#include "util/acc_log.h"

/* 单轮 epoll_wait 最多报告的就绪 fd 数 */
#define ACC_EPOLL_MAX_EVENTS 1024

/* posted 延迟执行队列：全局唯一，worker 排空后回到事件循环 */
struct acc_list acc_posted_events;

/* accept 互斥标志：worker init 依 conf.accept_mutex 启用 */
int acc_use_accept_mutex = 0;
int acc_accept_mutex_held = 0;
int acc_accept_disabled = 0;

/* epoll 实例 fd；-1 表示未初始化 */
static int ep = -1;
/* 就绪数组：init 分配，done 释放 */
static struct epoll_event *event_list = NULL;
/* 就绪数组容量 */
static int nevents = 0;

/* posted 队列 --------------------------------------------------------------*/

void acc_event_post(acc_event_t *ev)
{
    if (ev == NULL || ev->posted)
        return; /* 已在队列：防重复入队 */
    acc_list_add_tail(&ev->queue, &acc_posted_events); /* 尾插保 FIFO */
    ev->posted = 1;
}

void acc_event_delete_posted(acc_event_t *ev)
{
    if (ev == NULL || !ev->posted)
        return;
    acc_list_del(&ev->queue);
    ev->posted = 0;
}

void acc_event_process_posted(struct acc_list *queue)
{
    /* 禁令：handler 内禁止对其他仍在队列中的事件调用 acc_event_delete_posted。
     * 若被删节点恰为 SAFE 迭代器保存的 next，acc_list_del 使其自环，
     * 增量步 pos->next 永不回到队头 → 死循环、其余事件永不处理、worker 挂死。
     * 删除其他事件须推迟到 handler 返回之后，或仅置 ev->active = 0。 */
    ACC_LIST_FOR_EACH_SAFE(q, n, queue) {
        acc_event_t *ev = ACC_LIST_ENTRY(q, acc_event_t, queue);
        acc_list_del(q); /* 先摘节点并清 posted 位，再回调 */
        ev->posted = 0;
        ev->handler(ev); /* 重投自身安全：posted 已清，重投后本轮会继续处理 */
    }
}

void acc_event_process_posted_accept(void)
{
    struct acc_list *q, *n;

    /* 仅摘取 accept 事件，其余原样留队（普通队列稍后统一处理）。
     * 跨节点摘除对 SAFE 迭代安全：next 已先行保存 */
    for (q = acc_posted_events.next; q != &acc_posted_events; q = n) {
        n = q->next;
        acc_event_t *ev = ACC_LIST_ENTRY(q, acc_event_t, queue);
        if (!ev->accept)
            continue;
        acc_list_del(q);
        ev->posted = 0;
        ev->handler(ev);
    }
}

/* epoll 封装 ---------------------------------------------------------------*/

int acc_epoll_init(void)
{
    if (ep != -1) { /* 重复 init：先释放旧实例 */
        close(ep);
        ep = -1;
    }

    ep = epoll_create1(0);
    if (ep == -1) {
        ACC_LOGE("epoll_create1 失败, errno=%d", errno);
        return -1;
    }

    nevents = ACC_EPOLL_MAX_EVENTS;
    free(event_list);
    event_list = calloc((size_t)nevents, sizeof(struct epoll_event));
    if (event_list == NULL) {
        ACC_LOGE("分配就绪数组失败");
        close(ep);
        ep = -1;
        nevents = 0;
        return -1;
    }

    acc_list_init(&acc_posted_events);
    return 0;
}

void acc_epoll_done(void)
{
    if (ep != -1 && close(ep) == -1)
        ACC_LOGE("关闭 epoll fd 失败, errno=%d", errno);
    ep = -1;
    free(event_list);
    event_list = NULL;
    nevents = 0;
    acc_list_init(&acc_posted_events); /* 要求调用方先排空 posted 队列 */
}

/* epoll 注册锚点：连接事件 data.ptr 固定挂 c->read（写事件经 peer 找到），
 * standalone 事件挂自身 */
static acc_event_t *ep_anchor(acc_event_t *ev)
{
    if (ev->conn && ev->write)
        return ev->peer;
    return ev;
}

int acc_epoll_add_event(acc_event_t *ev, int event)
{
    struct epoll_event ee;
    acc_event_t *other;
    uint32_t events, prev;
    int op;

    if (ev == NULL || ep == -1)
        return -1;

    if (event == ACC_EVENT_READ) {
        other = ev->peer; /* 对侧写事件（standalone 为 NULL） */
        prev = ACC_EVENT_WRITE;
    } else {
        other = ev->peer; /* 对侧读事件（standalone 为 NULL） */
        prev = ACC_EVENT_READ;
    }
    events = (uint32_t)event;

    if (other != NULL && other->active) {
        /* 对侧已注册：MOD 合并双向掩码，维持同一次注册 */
        op = EPOLL_CTL_MOD;
        events |= prev;
    } else {
        op = EPOLL_CTL_ADD;
    }

    ee.events = events;
    ee.data.ptr = ep_anchor(ev);
    if (epoll_ctl(ep, op, ev->fd, &ee) == -1) {
        ACC_LOGE("epoll_ctl(add op=%d fd=%d) 失败, errno=%d",
                 op, ev->fd, errno);
        return -1;
    }
    ev->active = 1;
    return 0;
}

int acc_epoll_del_event(acc_event_t *ev, int event)
{
    struct epoll_event ee;
    acc_event_t *other;
    uint32_t prev;
    int op;

    if (ev == NULL || ep == -1)
        return -1;

    if (event == ACC_EVENT_READ) {
        other = ev->peer;
        prev = ACC_EVENT_WRITE; /* 缩掩码后保留对侧的方向 */
    } else {
        other = ev->peer;
        prev = ACC_EVENT_READ;
    }

    if (other != NULL && other->active) {
        /* 对侧仍在注册：MOD 缩掉本侧掩码，保留对侧 */
        op = EPOLL_CTL_MOD;
        ee.events = prev;
        ee.data.ptr = ep_anchor(ev);
    } else {
        /* 无人订阅：整体注销 */
        op = EPOLL_CTL_DEL;
        ee.events = 0;
        ee.data.ptr = NULL;
    }

    if (epoll_ctl(ep, op, ev->fd, &ee) == -1) {
        ACC_LOGE("epoll_ctl(del op=%d fd=%d) 失败, errno=%d",
                 op, ev->fd, errno);
        return -1;
    }
    ev->active = 0;
    return 0;
}

int acc_epoll_process_events(int timer, uint32_t flags)
{
    acc_event_t *rev, *wev;
    int events, i;

    if (ep == -1 || event_list == NULL)
        return -1;

    events = epoll_wait(ep, event_list, nevents, timer);
    if (events == -1) {
        if (errno == EINTR)
            return 0; /* 被信号打断：本轮视作无就绪 */
        ACC_LOGE("epoll_wait 失败, errno=%d", errno);
        return -1;
    }

    for (i = 0; i < events; i++) {
        acc_event_t *ev = event_list[i].data.ptr;
        uint32_t revents = event_list[i].events;

        if (ev == NULL)
            continue;

        /* 连接事件：data.ptr 挂 c->read，经 peer 取写事件；
         * standalone：data.ptr 即事件自身，按 write 位区分方向 */
        if (ev->conn) {
            rev = ev;
            wev = ev->peer;
        } else if (ev->write) {
            rev = NULL;
            wev = ev;
        } else {
            rev = ev;
            wev = NULL;
        }

        if ((revents & EPOLLOUT) && wev != NULL && wev->active) {
            wev->ready = 1;
            if (flags & ACC_POST_EVENTS)
                acc_event_post(wev);
            else
                wev->handler(wev);
        }

        /* 读侧同时覆盖 ERR/HUP：异常经读 handler 收口关闭 */
        if ((revents & (EPOLLIN | EPOLLRDHUP | EPOLLERR | EPOLLHUP))
            && rev != NULL && rev->active) {
            rev->ready = 1;
            if (flags & ACC_POST_EVENTS)
                acc_event_post(rev);
            else
                rev->handler(rev);
        }
    }

    return events;
}
