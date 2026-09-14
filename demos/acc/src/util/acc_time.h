#ifndef ACC_TIME_H
#define ACC_TIME_H

#include <stddef.h>
#include <time.h>

/* 初始化缓存：取当前时间并填充分解时间，进程启动时调用一次 */
void acc_time_init(void);

/* 刷新缓存（秒值 + struct tm），事件循环每轮调用，避免各处重复 syscall */
void acc_time_update(void);

/* 返回缓存秒值 */
time_t acc_time_now(void);

/* 按缓存时间格式化 "YYYY-MM-DD HH:MM:SS"；缓冲不足时置空串 */
void acc_time_fmt(char *buf, size_t n);

#endif /* ACC_TIME_H */
