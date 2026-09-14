#include "acc_time.h"

#include <stdio.h>
#include <time.h>

/* 缓存的当前秒值与分解时间；单线程事件循环内使用，无需加锁 */
static time_t g_now;
static struct tm g_tm;

/* 取当前时间并用 localtime_r 填充分解时间 */
void acc_time_update(void)
{
    g_now = time(NULL);
    localtime_r(&g_now, &g_tm);
}

/* 进程启动时刷新一次缓存 */
void acc_time_init(void)
{
    acc_time_update();
}

/* 返回缓存的秒值 */
time_t acc_time_now(void)
{
    return g_now;
}

/* 按缓存时间格式化为 "YYYY-MM-DD HH:MM:SS"；缓冲不足或失败时置空串 */
void acc_time_fmt(char *buf, size_t n)
{
    if (buf == NULL || n == 0)
        return;

    if (strftime(buf, n, "%Y-%m-%d %H:%M:%S", &g_tm) == 0)
        buf[0] = '\0';
}
