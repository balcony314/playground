#ifndef ACC_CONF_H
#define ACC_CONF_H

#include <stddef.h>
#include <unistd.h> /* 提供方(测试/调用方)直接使用 unlink 等声明 */

#define ACC_CONF_PATH_MAX 256   /* 路径类配置项缓冲区长度 */
#define ACC_CONF_INCLUDE_MAX_DEPTH 4 /* include 嵌套最大深度 */

/* 服务端配置项（spec 第 6 节），全部为定长 POD，便于整体清零/填充 */
typedef struct acc_conf {
    int log_level;                          /* 日志级别 */
    char log_path[ACC_CONF_PATH_MAX];       /* 日志目录 */
    int enable_daemon;                      /* 是否守护进程化（0/1） */
    int worker_process;                     /* worker 进程数 */
    int connections_per_worker;             /* 单 worker 最大连接数 */
    char server[ACC_CONF_PATH_MAX];         /* 监听地址 */
    int tcp_port;                           /* TCP 监听端口 */
    int tls_port;                           /* TLS 监听端口 */
    char cert_file[ACC_CONF_PATH_MAX];      /* TLS 证书文件 */
    char key_file[ACC_CONF_PATH_MAX];       /* TLS 私钥文件 */
    int accept_mutex;                       /* 是否启用 accept 互斥锁（0/1） */
    int accept_mutex_delay_ms;              /* 抢锁失败后的重试延迟（毫秒） */
    int client_timeout;                     /* 客户端空闲超时（秒） */
    unsigned max_read_buffer_size;          /* 单连接最大读缓冲（字节） */
    int max_open_fd;                        /* 进程最大打开 fd 数 */
    int thread_pool_thread_num;             /* 线程池线程数 */
    int thread_pool_queue_size;             /* 线程池任务队列长度 */
    char metrics_dir[ACC_CONF_PATH_MAX];    /* 指标输出目录 */
} acc_conf_t;

/* 命令表项：一个配置键对应一个解析函数与目标地址；dflt 为默认值文本 */
typedef struct acc_conf_cmd {
    const char *name;                       /* 配置键名 */
    int (*parse)(const char *v, void *addr, int cap); /* 解析回调，0 成功 / -1 非法 */
    void *addr;                             /* 写入地址（conf 字段） */
    int cap;                                /* 字符串缓冲区长度，其余类型传 0 */
    const char *dflt;                       /* 默认值文本（经 parse 回填） */
} acc_conf_cmd_t;

/* 运行时构建命令表：逐项绑定 &conf->field 地址，避免 offsetof 可读性问题 */
void acc_conf_init_cmds(const acc_conf_t *conf, acc_conf_cmd_t *cmds, int *ncmds);

/* 加载配置：先填默认值再解析文件；失败返回 -1 并向 stderr 打印文件与行号 */
int acc_conf_load(acc_conf_t *conf, const char *file);

/* 类型解析器：均返回 0 成功 / -1 非法；addr 为 NULL 时仅做合法性校验 */
int acc_conf_parse_int(const char *v, void *addr, int cap);
int acc_conf_parse_bool(const char *v, void *addr, int cap);
int acc_conf_parse_string(const char *v, void *addr, int cap);
int acc_conf_parse_memsize(const char *v, void *addr, int cap); /* 512/k/m/g → 字节 */
int acc_conf_parse_time(const char *v, void *addr, int cap);    /* 10s/5m/2h → 秒 */

#endif /* ACC_CONF_H */
