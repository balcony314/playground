#ifndef ACC_CONNECTION_H
#define ACC_CONNECTION_H

#include <openssl/ssl.h>
#include <pthread.h>
#include <stdint.h>
#include <sys/types.h>
#include <time.h>

#include "event/acc_event.h"
#include "util/acc_buffer.h"
#include "util/acc_list.h"

/* 结构 tag 与 acc_epoll.h/acc_event.h 的前向声明一致（C11 重复 typedef 合法） */
typedef struct acc_connection_s acc_connection_t;

/* 传输层返回语义（对齐 raw 的 ADS_ERR/ADS_AGAIN）：
 * >= 0 数据字节数；-1 硬错误（调用方应关闭连接）；-2 非阻塞暂不可继续 */
#define ACC_CONN_ERR   (-1)
#define ACC_CONN_AGAIN (-2)

/* 全局上下文/监听对象前向声明：本头文件不反向依赖 core */
struct acc_context;
struct acc_listening;

/* 连接对象（纯传输层）：预分配于连接池，经 data 字段串空闲链 */
struct acc_connection_s {
    int fd;               /* 连接 fd，空闲为 -1 */
    SSL *ssl;             /* NULL = 明文 */
    unsigned ssl_connected:1; /* TLS 握手是否完成 */
    acc_event_t *read, *write; /* 池内配对事件（读/写各一） */
    ssize_t (*recv)(struct acc_connection_s *, void *, size_t);
    ssize_t (*send)(struct acc_connection_s *, void *, size_t);
    /* 读缓冲：应用层半包粘包处理，可 realloc，上限 conf.max_read_buffer_size */
    unsigned char *readbuf;
    uint32_t readbufsize, readlen, readoffset;
    buf_t *write_buf;     /* 链表写缓冲：延迟到 EPOLLOUT 冲刷 */
    /* 写路径互斥：线程池 send_publish 与事件循环 flush/写事件增删/关闭复位
     * 之间串行化。池内存常驻 worker 生命周期，指针永不悬垂；
     * con_id/reuse_count 快照校验须在锁内做（消除 TOCTOU） */
    pthread_mutex_t buf_mu;
    uint64_t con_id, reuse_count; /* con_id 池内单调；reuse_count 跨线程代际校验 */
    time_t timestamp;     /* 最近活跃时间：超时清扫依据 */
    void *data;           /* 应用层会话（→ acc_mqtt_session_t）；空闲链串联复用 */
    struct acc_list inused_queue; /* 挂 ctx->inused_connection 队列 */
};

/* 连接池：预分配 connections + 等长配对的 read/write 事件数组，串空闲链。
 * 成功返回 0，参数非法或分配失败返回 -1 */
int acc_create_connections_pool(struct acc_context *ctx);

/* 从空闲链取一个连接：摘链、分配单调 con_id、默认 recv/send、挂 inused 链尾。
 * 空闲链为空返回 NULL */
acc_connection_t *acc_get_connection(struct acc_context *ctx);

/* 归还连接到空闲链头部：reuse_count++、摘 inused 链、释放读写缓冲、
 * 清理事件位（active/ready/posted）、fd 置 -1 */
void acc_free_connection(struct acc_context *ctx, acc_connection_t *c);

/* 关闭连接：epoll 注销 → 应用层回调（ctx->on_conn_close 且 c->data 非空）→
 * close fd / SSL → 归还空闲链。红线：不触碰其他在队 posted 事件 */
void acc_close_connection(struct acc_context *ctx, acc_connection_t *c);

/* 超时清扫：全量扫描 inused 链，timestamp 距今超过 conf.client_timeout
 * 的逐个 close，每轮最多 64 个（残余下轮继续）；活跃时间戳刷新不重排
 * 链表，故不能在首个未过期处提前停止 */
void acc_traversal_expired_connection(struct acc_context *ctx);

/* 传输收发：明文 recv/send(MSG_NOSIGNAL) 与 TLS 对应实现。
 * TLS recv 在握手未完成时先推进握手 */
ssize_t acc_unix_recv(acc_connection_t *c, void *buf, size_t size);
ssize_t acc_ssl_recv(acc_connection_t *c, void *buf, size_t size);
ssize_t acc_conn_socket_send(acc_connection_t *c, void *buf, size_t n);
ssize_t acc_conn_ssl_send(acc_connection_t *c, void *buf, size_t n);

/* 非阻塞 TLS 握手推进（SSL_do_handshake）：
 * 完成返回 0 并置 ssl_connected、摘除握手期注册的写事件；
 * WANT_READ/WANT_WRITE 调整 epoll 注册后返回 ACC_CONN_AGAIN(-2)；
 * 硬错误返回 ACC_CONN_ERR(-1) */
ssize_t acc_ssl_handshake(acc_connection_t *c);

/* 下行写入口：数据拷贝进 write_buf 并注册写事件（EPOLLOUT），成功返回 0。
 * write_buf 未创建时按 conf.max_read_buffer_size（0 则回退默认 1MB）创建 */
int acc_conn_write(struct acc_context *ctx, acc_connection_t *c,
                   const void *data, size_t n);

/* 冲刷写缓冲到 fd/SSL：写空返回 ACC_BUF_OK 并摘除写事件；
 * EAGAIN 返回 ACC_BUF_EAGAIN（保留进度待下次）；错误返回 ACC_BUF_ERR */
int acc_conn_flush(acc_connection_t *c);

/* accept 批处理：ev->data 为监听对象，循环 accept4(SOCK_NONBLOCK) 直到 EAGAIN */
void acc_event_accept(acc_event_t *ev);

/* 连接事件整组注册/注销（实现于本模块，声明见 event/acc_epoll.h） */
int acc_epoll_add_connection(acc_connection_t *c);
void acc_epoll_del_connection(acc_connection_t *c);

#endif /* ACC_CONNECTION_H */
