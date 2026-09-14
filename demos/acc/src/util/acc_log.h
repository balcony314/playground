#ifndef ACC_LOG_H
#define ACC_LOG_H

/* 日志级别：数值即严重度，低于 min_level 的消息被过滤 */
enum acc_log_level {
    ACC_LOG_DEBUG = 0,
    ACC_LOG_INFO  = 1,
    ACC_LOG_WARN  = 2,
    ACC_LOG_ERROR = 3
};

/* 初始化：逐级创建目录（mkdir -p 语义），打开当前小时日志文件；失败返回 -1 */
int acc_log_init(const char *dir, int min_level);

/* 写一条日志；未初始化（或已关闭）时输出到 stderr，min_level 视为 0 */
void acc_log_write(int level, const char *file, int line, const char *fmt, ...);

/* 关闭日志文件并重置状态，之后可重新 acc_log_init */
void acc_log_close(void);

/* 分级宏：自动携带文件名与行号；## 兼容无可变参数的调用 */
#define ACC_LOGD(fmt, ...) \
    acc_log_write(ACC_LOG_DEBUG, __FILE__, __LINE__, fmt, ##__VA_ARGS__)
#define ACC_LOGI(fmt, ...) \
    acc_log_write(ACC_LOG_INFO, __FILE__, __LINE__, fmt, ##__VA_ARGS__)
#define ACC_LOGW(fmt, ...) \
    acc_log_write(ACC_LOG_WARN, __FILE__, __LINE__, fmt, ##__VA_ARGS__)
#define ACC_LOGE(fmt, ...) \
    acc_log_write(ACC_LOG_ERROR, __FILE__, __LINE__, fmt, ##__VA_ARGS__)

#endif /* ACC_LOG_H */
