/* 极简 Prometheus 文本指标：进程内固定槽数值表，事件循环与池线程
 * 并发更新（mutex 保护），write 持锁快照后原子替换目标文件。
 * 节流采用 CLOCK_MONOTONIC（不受系统时间跳变影响） */
#include "acc_metrics.h"

#include <errno.h>
#include <inttypes.h>
#include <pthread.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <time.h>
#include <unistd.h>

#include "acc_log.h"
#include "acc_string.h"

/* 指标名最大长度（含结尾 '\0'） */
#define ACC_METRICS_NAME_MAX 64
/* 固定槽位上限：超出静默丢弃（本项目埋点 6 项，余量充足） */
#define ACC_METRICS_SLOTS 32
/* 写盘节流间隔（毫秒） */
#define ACC_METRICS_THROTTLE_MS 5000
/* 目录与文件路径缓冲 */
#define ACC_METRICS_PATH_MAX 512
/* 快照文本缓冲：每行（TYPE + 数值两行）< 96 字节 × 槽位定界 */
#define ACC_METRICS_TEXT_MAX (ACC_METRICS_SLOTS * 96)

/* 指标类型：gauge 覆盖写，counter 累加 */
enum acc_metric_type {
    ACC_METRIC_GAUGE = 0,
    ACC_METRIC_COUNTER = 1
};

typedef struct {
    char name[ACC_METRICS_NAME_MAX];
    int type;
    int64_t value;
} acc_metric_t;

static pthread_mutex_t g_mu = PTHREAD_MUTEX_INITIALIZER;
static acc_metric_t g_metrics[ACC_METRICS_SLOTS]; /* 固定槽数值表 */
static int g_nmetrics;                            /* 已占用槽位数 */
static char g_dir[ACC_METRICS_PATH_MAX];          /* 输出目录；空串表示禁用 */
static int64_t g_last_write_ms;                   /* 上次写盘时刻（MONOTONIC） */

/* CLOCK_MONOTONIC 毫秒（util 不依赖 core 的同名 static 辅助） */
static int64_t metrics_now_ms(void)
{
    struct timespec ts;

    if (clock_gettime(CLOCK_MONOTONIC, &ts) == -1)
        return 0;
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

/* mkdir -p：逐级创建目录，已存在视为成功（语义同 acc_log） */
static int metrics_mkdir_p(const char *dir)
{
    char tmp[ACC_METRICS_PATH_MAX];

    if (dir == NULL || dir[0] == '\0' || strlen(dir) >= sizeof(tmp))
        return -1;
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

void acc_metrics_init(const char *dir)
{
    char want[ACC_METRICS_PATH_MAX];

    pthread_mutex_lock(&g_mu);
    g_nmetrics = 0;
    memset(g_metrics, 0, sizeof(g_metrics));
    g_dir[0] = '\0';
    /* 初始时刻回拨一个节流窗：保证首次 write 立即落盘 */
    g_last_write_ms = metrics_now_ms() - ACC_METRICS_THROTTLE_MS;
    if (dir != NULL && dir[0] != '\0')
        acc_strlcpy(g_dir, dir, sizeof(g_dir));
    acc_strlcpy(want, g_dir, sizeof(want));
    pthread_mutex_unlock(&g_mu);

    /* 目录创建在锁外（文件系统操作可能较慢）：失败降级为禁用 */
    if (want[0] != '\0' && metrics_mkdir_p(want) != 0) {
        ACC_LOGW("指标目录创建失败, 指标导出禁用: %s", want);
        pthread_mutex_lock(&g_mu);
        g_dir[0] = '\0';
        pthread_mutex_unlock(&g_mu);
    }
}

/* 锁内查找/新建槽位并按类型更新 */
static void metrics_update(const char *name, int64_t v, int type)
{
    acc_metric_t *m = NULL;

    pthread_mutex_lock(&g_mu);
    if (g_dir[0] == '\0' || name == NULL ||
        strlen(name) >= ACC_METRICS_NAME_MAX) {
        pthread_mutex_unlock(&g_mu);
        return; /* 禁用或非法名：no-op */
    }
    for (int i = 0; i < g_nmetrics; i++) {
        if (strcmp(g_metrics[i].name, name) == 0) {
            m = &g_metrics[i];
            break;
        }
    }
    if (m == NULL && g_nmetrics < ACC_METRICS_SLOTS) {
        m = &g_metrics[g_nmetrics++];
        acc_strlcpy(m->name, name, sizeof(m->name));
        m->type = type;
        m->value = 0;
    }
    if (m != NULL) {
        if (type == ACC_METRIC_GAUGE)
            m->value = v;
        else
            m->value += v;
    }
    pthread_mutex_unlock(&g_mu);
}

void acc_metrics_gauge(const char *name, int64_t v)
{
    metrics_update(name, v, ACC_METRIC_GAUGE);
}

void acc_metrics_count(const char *name, int64_t delta)
{
    metrics_update(name, delta, ACC_METRIC_COUNTER);
}

void acc_metrics_write(void)
{
    char path[ACC_METRICS_PATH_MAX];
    char tmp[ACC_METRICS_PATH_MAX + 8]; /* 预留 ".tmp" 后缀空间 */
    char text[ACC_METRICS_TEXT_MAX];
    size_t off = 0;
    FILE *fp = NULL;

    pthread_mutex_lock(&g_mu);
    if (g_dir[0] == '\0') {
        pthread_mutex_unlock(&g_mu);
        return;
    }
    int64_t now = metrics_now_ms();
    if (now - g_last_write_ms < ACC_METRICS_THROTTLE_MS) {
        pthread_mutex_unlock(&g_mu);
        return; /* 节流窗口内：跳过本次快照 */
    }
    g_last_write_ms = now;

    /* 持锁快照：格式化与路径拼接在同一线性临界区内完成 */
    for (int i = 0; i < g_nmetrics; i++) {
        const acc_metric_t *m = &g_metrics[i];
        int w = snprintf(text + off, sizeof(text) - off,
                         "# TYPE %s %s\n%s %" PRId64 "\n", m->name,
                         (m->type == ACC_METRIC_GAUGE) ? "gauge"
                                                       : "counter",
                         m->name, m->value);
        if (w < 0 || (size_t)w >= sizeof(text) - off)
            break; /* 缓冲将尽：截断（槽位与行长定界下不可达） */
        off += (size_t)w;
    }
    int w = snprintf(path, sizeof(path), "%s/acc-%d.prom", g_dir,
                     (int)getpid());
    if (w < 0 || (size_t)w >= sizeof(path)) {
        pthread_mutex_unlock(&g_mu);
        return; /* 目录过长装不下文件名：跳过本轮 */
    }
    snprintf(tmp, sizeof(tmp), "%s.tmp", path);
    pthread_mutex_unlock(&g_mu);

    /* 锁外写盘 + 原子替换：失败保留旧文件，本轮按已写计（下窗重试） */
    fp = fopen(tmp, "w");
    if (fp == NULL) {
        ACC_LOGW("指标临时文件打开失败: %s", tmp);
        return;
    }
    if (fwrite(text, 1, off, fp) != off) {
        ACC_LOGW("指标快照写入失败: %s", tmp);
        fclose(fp);
        unlink(tmp);
        return;
    }
    fclose(fp);
    if (rename(tmp, path) != 0) {
        ACC_LOGW("指标文件替换失败: %s -> %s", tmp, path);
        unlink(tmp);
    }
}
