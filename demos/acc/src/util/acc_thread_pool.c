#include "acc_thread_pool.h"

#include <errno.h>
#include <stdlib.h>
#include <time.h>

/* 计算 pthread_cond_timedwait 用的绝对超时时刻：now + timeout_ms */
static void tp_deadline(struct timespec *ts, int timeout_ms)
{
    clock_gettime(CLOCK_REALTIME, ts);
    ts->tv_sec += timeout_ms / 1000;
    ts->tv_nsec += (long)(timeout_ms % 1000) * 1000000L;
    if (ts->tv_nsec >= 1000000000L) {
        ts->tv_sec++;
        ts->tv_nsec -= 1000000000L;
    }
}

/* 工作线程主循环：取任务执行；销毁时把剩余任务清完再退出 */
static void *tp_worker(void *arg)
{
    acc_thread_pool_t *tp = arg;
    for (;;) {
        pthread_mutex_lock(&tp->mu);
        while (tp->count == 0 && !tp->shutting_down)
            pthread_cond_wait(&tp->not_empty, &tp->mu);
        if (tp->count == 0) { /* 销毁中且已无排队任务 */
            pthread_mutex_unlock(&tp->mu);
            break;
        }
        acc_tp_task_t task = tp->slot[tp->head];
        tp->head = (tp->head + 1) % tp->queue_size;
        tp->count--;
        tp->busy++;
        pthread_cond_signal(&tp->not_full);
        pthread_mutex_unlock(&tp->mu);

        task.handler(task.data);

        pthread_mutex_lock(&tp->mu);
        tp->busy--;
        if (tp->count == 0)
            pthread_cond_broadcast(&tp->not_empty); /* 通知 drain */
        pthread_mutex_unlock(&tp->mu);
    }
    return NULL;
}

int acc_thread_pool_init(acc_thread_pool_t *tp, int nthreads, int queue_size)
{
    if (!tp || nthreads <= 0 || queue_size <= 0)
        return -1;
    tp->threads = calloc((size_t)nthreads, sizeof(pthread_t));
    tp->slot = calloc((size_t)queue_size, sizeof(acc_tp_task_t));
    if (!tp->threads || !tp->slot) {
        free(tp->threads);
        free(tp->slot);
        tp->threads = NULL;
        tp->slot = NULL;
        return -1;
    }
    tp->nthreads = 0;
    tp->queue_size = queue_size;
    tp->head = 0;
    tp->count = 0;
    tp->busy = 0;
    tp->shutting_down = 0;
    pthread_mutex_init(&tp->mu, NULL);
    pthread_cond_init(&tp->not_full, NULL);
    pthread_cond_init(&tp->not_empty, NULL);

    for (int i = 0; i < nthreads; i++) {
        if (pthread_create(&tp->threads[i], NULL, tp_worker, tp) != 0) {
            /* 部分创建失败：置销毁标志并复用 destroy 回收已建线程与资源 */
            pthread_mutex_lock(&tp->mu);
            tp->shutting_down = 1;
            pthread_cond_broadcast(&tp->not_empty);
            pthread_mutex_unlock(&tp->mu);
            acc_thread_pool_destroy(tp);
            return -1;
        }
        tp->nthreads++;
    }
    return 0;
}

int acc_thread_pool_post(acc_thread_pool_t *tp, acc_tp_handler_t h, void *data)
{
    if (!tp || !h)
        return -1;
    pthread_mutex_lock(&tp->mu);
    if (tp->shutting_down || tp->count == tp->queue_size) {
        pthread_mutex_unlock(&tp->mu);
        return -1; /* 满队/销毁中：不阻塞，直接拒绝 */
    }
    acc_tp_task_t *t = &tp->slot[(tp->head + tp->count) % tp->queue_size];
    t->handler = h;
    t->data = data;
    tp->count++;
    pthread_cond_signal(&tp->not_empty);
    pthread_mutex_unlock(&tp->mu);
    return 0;
}

/* 当前排队任务数：读 count 需持锁（与 worker 取任务互斥保证一致性）；
 * pthread_mutex_lock 形参非 const，对逻辑只读对象做显式去 const */
int acc_thread_pool_pending(const acc_thread_pool_t *tp)
{
    int n = -1;

    if (tp == NULL)
        return -1;
    pthread_mutex_lock((pthread_mutex_t *)&tp->mu);
    n = tp->count;
    pthread_mutex_unlock((pthread_mutex_t *)&tp->mu);
    return n;
}

int acc_thread_pool_drain(acc_thread_pool_t *tp, int timeout_ms)
{
    if (!tp || timeout_ms < 0)
        return -1;
    struct timespec dl;
    tp_deadline(&dl, timeout_ms);
    pthread_mutex_lock(&tp->mu);
    int rc = 0;
    while (tp->count > 0 || tp->busy > 0) {
        if (pthread_cond_timedwait(&tp->not_empty, &tp->mu, &dl) == ETIMEDOUT) {
            rc = -1;
            break;
        }
    }
    pthread_mutex_unlock(&tp->mu);
    return rc;
}

void acc_thread_pool_destroy(acc_thread_pool_t *tp)
{
    if (!tp)
        return;
    pthread_mutex_lock(&tp->mu);
    tp->shutting_down = 1;
    pthread_cond_broadcast(&tp->not_empty);
    pthread_cond_broadcast(&tp->not_full);
    pthread_mutex_unlock(&tp->mu);

    for (int i = 0; i < tp->nthreads; i++)
        pthread_join(tp->threads[i], NULL);

    pthread_cond_destroy(&tp->not_empty);
    pthread_cond_destroy(&tp->not_full);
    pthread_mutex_destroy(&tp->mu);
    free(tp->slot);
    free(tp->threads);
    tp->slot = NULL;
    tp->threads = NULL;
    tp->nthreads = 0;
}
