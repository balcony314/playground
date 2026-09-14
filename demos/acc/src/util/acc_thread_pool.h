#ifndef ACC_THREAD_POOL_H
#define ACC_THREAD_POOL_H

#include <pthread.h>

/* 任务处理函数：data 为提交方附带的上下文指针 */
typedef void (*acc_tp_handler_t)(void *data);

/* 环形队列任务槽：处理函数 + 上下文 */
typedef struct {
    acc_tp_handler_t handler;
    void *data;
} acc_tp_task_t;

/* 固定容量线程池：init 时分配工作线程数组与环形任务队列 */
typedef struct {
    pthread_t *threads;       /* 工作线程句柄数组（init 分配） */
    int nthreads;             /* 已成功创建的工作线程数 */
    acc_tp_task_t *slot;      /* 环形队列槽位数组（init 分配） */
    int queue_size;           /* 队列容量（槽数） */
    int head;                 /* 队首下标（worker 取任务处） */
    int count;                /* 当前排队任务数 */
    int busy;                 /* 已取出且尚未执行完的任务数 */
    int shutting_down;        /* 销毁标志：置 1 后 worker 清完队列退出 */
    pthread_mutex_t mu;       /* 保护以上全部共享字段 */
    pthread_cond_t not_full;  /* 队列非满：worker 取走任务时唤醒 */
    pthread_cond_t not_empty; /* 队列非空：提交时唤醒 worker；全执行完唤醒 drain */
} acc_thread_pool_t;

/* 创建 nthreads 个工作线程与 queue_size 深的任务队列，成功返回 0；
 * 参数非法或资源分配失败返回 -1（已创建的线程会被回收） */
int acc_thread_pool_init(acc_thread_pool_t *tp, int nthreads, int queue_size);

/* 销毁线程池：唤醒并 join 全部工作线程（销毁前已提交的任务会执行完），
 * 释放队列与线程数组；调用方应先 drain 以确保任务清空 */
void acc_thread_pool_destroy(acc_thread_pool_t *tp);

/* 提交任务：入队并唤醒一个 worker，成功返回 0；
 * 队列满或正在销毁时返回 -1，不阻塞调用方 */
int acc_thread_pool_post(acc_thread_pool_t *tp, acc_tp_handler_t h, void *data);

/* 当前排队任务数（不含已取出执行中的），供指标埋点读取；
 * tp 为 NULL 返回 -1。内部持锁读取（锁操作对逻辑只读对象是惯例豁免） */
int acc_thread_pool_pending(const acc_thread_pool_t *tp);

/* 等待队列清空且所有已取出任务执行完毕（用于测试/优雅退出）；
 * timeout_ms 内未完成返回 -1，否则返回 0 */
int acc_thread_pool_drain(acc_thread_pool_t *tp, int timeout_ms);

#endif /* ACC_THREAD_POOL_H */
