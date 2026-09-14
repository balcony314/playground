#ifndef ACC_METRICS_H
#define ACC_METRICS_H

#include <stdint.h>

/* 初始化指标导出：dir 为空串时禁用（gauge/count/write 全部 no-op）；
 * 目录不存在则逐级创建，创建失败记 WARN 并降级为禁用（不阻断启动）；
 * 重复 init 会重置全部指标与节流时钟 */
void acc_metrics_init(const char *dir);

/* 记录 gauge（同名覆盖）；name 过长或槽位满时静默丢弃（埋点均为
 * 短字面量，属不可达防御） */
void acc_metrics_gauge(const char *name, int64_t v);

/* counter 累加 delta（同名相加） */
void acc_metrics_count(const char *name, int64_t delta);

/* 快照写盘：输出 Prometheus 文本到 <dir>/acc-<pid>.prom（临时文件 +
 * rename 原子替换，抓取方不会读到半个文件）；距上次成功写盘不足 5s
 * 跳过（CLOCK_MONOTONIC 计时）；禁用或未 init 时 no-op */
void acc_metrics_write(void);

#endif /* ACC_METRICS_H */
