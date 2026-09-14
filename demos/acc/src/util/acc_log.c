#include "acc_log.h"

#include <errno.h>
#include <pthread.h>
#include <stdarg.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

#include "acc_time.h"

/* 日志路径最大长度 */
#define ACC_LOG_PATH_MAX 512
/* 单条消息缓冲大小 */
#define ACC_LOG_BUF_SIZE 4096

static pthread_mutex_t g_lock = PTHREAD_MUTEX_INITIALIZER;
static char g_dir[ACC_LOG_PATH_MAX];        /* 日志目录 */
static char g_name[ACC_LOG_PATH_MAX];       /* 当前日志文件完整路径 */
static int g_min_level = 0;                 /* 最低输出级别 */
static FILE *g_fp = NULL;                   /* 当前日志文件句柄；NULL 表示未初始化 */

/* 级别转字符串（补齐到 5 字符由格式符完成） */
static const char *acc_log_level_str(int level)
{
    switch (level) {
    case ACC_LOG_DEBUG: return "DEBUG";
    case ACC_LOG_INFO:  return "INFO";
    case ACC_LOG_WARN:  return "WARN";
    default:            return "ERROR";
    }
}

/* mkdir -p：逐级创建目录，已存在视为成功 */
static int acc_log_mkdir_p(const char *dir)
{
    if (dir == NULL || dir[0] == '\0' || strlen(dir) >= ACC_LOG_PATH_MAX)
        return -1;

    char tmp[ACC_LOG_PATH_MAX];
    snprintf(tmp, sizeof(tmp), "%s", dir);
    for (char *p = tmp + 1; *p != '\0'; p++) {
        if (*p != '/')
            continue;
        *p = '\0';
        if (mkdir(tmp, 0755) != 0 && errno != EEXIST)
            return -1;
        *p = '/';
    }
    if (mkdir(tmp, 0755) != 0 && errno != EEXIST)
        return -1;
    return 0;
}

/* 按缓存时间构建当前小时日志文件名 dir/log_<YYYY-MM-DD>T<HH>，截断视为失败 */
static int acc_log_build_name(char *name, size_t n)
{
    struct tm tm_now;
    time_t now = acc_time_now();
    localtime_r(&now, &tm_now);
    int w = snprintf(name, n, "%s/log_%04d-%02d-%02dT%02d", g_dir,
                     tm_now.tm_year + 1900, tm_now.tm_mon + 1,
                     tm_now.tm_mday, tm_now.tm_hour);
    return (w > 0 && (size_t)w < n) ? 0 : -1;
}

/* 锁内调用：跨小时则切换文件；返回当前可写句柄，异常时尽量沿用旧文件 */
static FILE *acc_log_roll_locked(void)
{
    char name[ACC_LOG_PATH_MAX];
    if (acc_log_build_name(name, sizeof(name)) != 0)
        return g_fp;
    if (g_fp != NULL && strcmp(name, g_name) == 0)
        return g_fp;

    FILE *fp = fopen(name, "a");
    if (fp == NULL)
        return g_fp;                        /* 打不开新文件时沿用旧文件，不丢句柄 */
    if (g_fp != NULL)
        fclose(g_fp);
    g_fp = fp;
    snprintf(g_name, sizeof(g_name), "%s", name);
    return g_fp;
}

/* 组装并写出一行：时间 [级别] [pid] 文件:行 消息 */
static void acc_log_emit(FILE *fp, int level, const char *file, int line,
                         const char *msg)
{
    char ts[32];
    acc_time_fmt(ts, sizeof(ts));
    fprintf(fp, "%s [%-5.5s] [pid %d] %s:%d %s\n",
            ts, acc_log_level_str(level), (int)getpid(), file, line, msg);
}

int acc_log_init(const char *dir, int min_level)
{
    if (dir == NULL || dir[0] == '\0')
        return -1;
    if (acc_log_mkdir_p(dir) != 0)
        return -1;

    char name[ACC_LOG_PATH_MAX];
    pthread_mutex_lock(&g_lock);
    acc_time_init();                        /* 保证缓存时间有效（幂等） */
    int rc = -1;
    int w = snprintf(g_dir, sizeof(g_dir), "%s", dir);
    if (w > 0 && (size_t)w < sizeof(g_dir) &&
        acc_log_build_name(name, sizeof(name)) == 0) {
        FILE *fp = fopen(name, "a");
        if (fp != NULL) {
            if (g_fp != NULL)
                fclose(g_fp);
            g_fp = fp;
            snprintf(g_name, sizeof(g_name), "%s", name);
            g_min_level = min_level;
            rc = 0;
        }
    }
    pthread_mutex_unlock(&g_lock);
    return rc;
}

void acc_log_write(int level, const char *file, int line, const char *fmt, ...)
{
    char msg[ACC_LOG_BUF_SIZE];
    va_list ap;

    if (fmt == NULL)
        return;
    va_start(ap, fmt);
    vsnprintf(msg, sizeof(msg), fmt, ap);
    va_end(ap);
    if (file == NULL)
        file = "?";

    pthread_mutex_lock(&g_lock);
    if (level < g_min_level) {              /* 低于阈值直接丢弃 */
        pthread_mutex_unlock(&g_lock);
        return;
    }
    FILE *out = (g_fp != NULL) ? acc_log_roll_locked() : NULL;
    if (out != NULL) {
        acc_log_emit(out, level, file, line, msg);
        fflush(out);
    } else {
        acc_time_init();                    /* 未初始化路径保证时间戳可用 */
        acc_log_emit(stderr, level, file, line, msg);
    }
    pthread_mutex_unlock(&g_lock);
}

void acc_log_close(void)
{
    pthread_mutex_lock(&g_lock);
    if (g_fp != NULL) {
        fclose(g_fp);
        g_fp = NULL;
    }
    g_name[0] = '\0';
    g_dir[0] = '\0';
    g_min_level = 0;                        /* 恢复未初始化语义 */
    pthread_mutex_unlock(&g_lock);
}
