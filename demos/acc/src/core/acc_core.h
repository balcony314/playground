#ifndef ACC_CORE_H
#define ACC_CORE_H

#include <stddef.h>

#include <openssl/ssl.h>

#include "event/acc_event.h"
#include "net/acc_connection.h"
#include "net/acc_listening.h"
#include "util/acc_conf.h"
#include "util/acc_list.h"
#include "util/acc_thread_pool.h"

/* 配置文件路径缓冲区长度（与 util/acc_conf.h 保持一致，此处兜底定义） */
#ifndef ACC_CONF_PATH_MAX
#define ACC_CONF_PATH_MAX 256
#endif

/* 订阅路由表前向声明：core 不依赖 mqtt 头 */
struct acc_sub_map;

/* 全局上下文：master 创建并 fork 继承，worker 侧填充连接池/线程池等运行态 */
typedef struct acc_context {
    acc_conf_t conf;      /* 服务配置（解析 conf 文件所得） */
    acc_listening_t *listenlist; /* 监听 socket 链 */
    acc_connection_t *connections;     /* worker 内预分配连接池 */
    int connections_n;                /* 池容量 */
    acc_connection_t *free_connections; /* 空闲链头（经 c->data 串联） */
    int free_connections_n;           /* 空闲连接数 */
    struct acc_list inused_connection; /* 活跃连接链（按活跃时间，供超时清扫） */
    acc_event_t *read_events, *write_events; /* 与连接池等长配对的事件数组 */
    acc_thread_pool_t thread_pool; /* 转发卸载线程池（值类型） */
    struct acc_sub_map *sub_map;   /* 订阅路由表 */
    void (*on_conn_close)(struct acc_connection_s *c); /* 连接关闭回调，worker init 填，NULL 跳过 */
    SSL_CTX *ssl_ctx; /* TLS 服务上下文（tls_port>0 时初始化） */
    int lockfd;       /* accept 互斥锁文件 fd */
    char conffile[ACC_CONF_PATH_MAX]; /* 启动指定的配置文件路径 */
} acc_context_t;

/* 初始化 TLS 服务上下文：TLS_server_method + min TLS1.2 + 证书/私钥加载。
 * 成功写入 ctx->ssl_ctx 返回 0；失败返回 -1（ctx->ssl_ctx 保持原值，
 * 调用方降级为仅明文监听，不退出进程） */
int acc_init_ssl_ctx(acc_context_t *ctx);

/* master 侧服务初始化：SSL_CTX（可降级）+ 监听 socket 链；失败返回 -1 */
int acc_server_init(acc_context_t *ctx);

/* master 侧服务清理：关监听链 + 释放 SSL_CTX */
void acc_server_shutdown(acc_context_t *ctx);

/* 原地路径绝对化：已是绝对路径或为空则原样返回 0；相对路径用 getcwd
 * 拼接（目录可不存在）。成功 0，缓冲不足或取 cwd 失败 -1（path 不动） */
int acc_path_to_absolute(char *path, size_t cap);

#endif /* ACC_CORE_H */
