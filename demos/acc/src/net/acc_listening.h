#ifndef ACC_LISTENING_H
#define ACC_LISTENING_H

#include <openssl/ssl.h>

/* 监听 socket 类型：取值即 accept 后连接的传输形态 */
#define ACC_SOCK_TCP 0 /* 明文 TCP */
#define ACC_SOCK_TLS 1 /* TLS：监听对象持有 SSL_CTX，accept 时派生 SSL */

/* 连接对象前向声明：与 acc_event.h 保持同一 tag */
struct acc_connection_s;
typedef struct acc_connection_s acc_connection_t;

/* 全局上下文前向声明：本头文件不反向依赖 core */
struct acc_context;

/* 监听 socket：TCP/TLS 各一个，经 next 串成单链挂 ctx->listenlist */
typedef struct acc_listening {
    int fd;         /* 监听 fd，未打开为 -1 */
    int port;       /* 监听端口 */
    int socket_type;   /* ACC_SOCK_TCP / ACC_SOCK_TLS */
    SSL_CTX *ssl_ctx;  /* TLS 监听填 ctx->ssl_ctx，TCP 为 NULL */
    void (*handler)(acc_connection_t *c); /* 新连接回调（Task 12 填），NULL 跳过 */
    struct acc_listening *next;
} acc_listening_t;

/* 按 conf.server/tcp_port/tls_port 创建监听 socket 链（SO_REUSEADDR + O_NONBLOCK）。
 * tls_port 为 0 跳过 TLS 监听；TLS 监听要求 ctx->ssl_ctx 已初始化。
 * 成功返回 0，任一失败返回 -1（已创建的部分由调用方经 close 清理） */
int acc_create_listening_sockets(struct acc_context *ctx);

/* 关闭并释放全部监听 socket（worker/master 退出路径） */
void acc_close_listening_sockets(struct acc_context *ctx);

#endif /* ACC_LISTENING_H */
