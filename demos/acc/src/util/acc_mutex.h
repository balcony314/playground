#ifndef ACC_MUTEX_H
#define ACC_MUTEX_H

/* 跨进程互斥文件锁：持有打开的锁文件 fd，通过 fcntl 记录锁实现互斥 */
typedef struct {
    int fd;
} acc_file_mutex_t;

/* 打开（不存在则创建）锁文件并记录 fd，成功返回 0 */
int acc_file_mutex_create(acc_file_mutex_t *m, const char *path);

/* 非阻塞尝试对整个文件加写锁（fcntl F_SETLK）：
 * 成功 0；锁被其他进程持有返回 1；其他错误返回 -1 */
int acc_file_mutex_trylock(acc_file_mutex_t *m);

/* 释放写锁，成功返回 0 */
int acc_file_mutex_unlock(acc_file_mutex_t *m);

/* 关闭锁文件 fd（close 同时隐式释放本进程在该文件上的锁） */
void acc_file_mutex_destroy(acc_file_mutex_t *m);

#endif /* ACC_MUTEX_H */
