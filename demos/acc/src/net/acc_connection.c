/* 连接层：预分配连接池、收发原语、写缓冲集成、accept 批处理与超时清扫。
 * 对齐 raw 的 ads_connection.c / ads_epoll_event.c（event_accept 部分），
 * 剥离业务字段与线程锁（acc 为单 worker 事件循环 + 线程池转发模型） */
#include "acc_connection.h"

#include <errno.h>
#include <netinet/in.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include "core/acc_core.h"
#include "event/acc_epoll.h"
#include "net/acc_listening.h"
#include "util/acc_log.h"
#include "util/acc_time.h"

/* conf.max_read_buffer_size 未配置（0）时写缓冲回退上限：1MB */
#define ACC_CONN_WRITE_BUF_DEFAULT (1u << 20)
/* 超时清扫每轮最多关闭的连接数 */
#define ACC_EXPIRED_CLEAN_BATCH 64

/* 连接 id 计数器：池生命周期内单调递增，create_pool 归零 */
static uint64_t g_con_id;
/* 池所属上下文：acc_event_accept 等无 ctx 形参的回调路径取用 */
static acc_context_t *g_conn_ctx;

/* 连接池 --------------------------------------------------------------------*/

int acc_create_connections_pool(acc_context_t *ctx)
{
    if (ctx == NULL || ctx->connections_n <= 0)
        return -1;

    /* 重复创建时先释放旧池（测试/重启用） */
    free(ctx->connections);
    free(ctx->read_events);
    free(ctx->write_events);

    g_con_id = 0;
    g_conn_ctx = ctx;

    ctx->connections = calloc((size_t)ctx->connections_n,
                              sizeof(acc_connection_t));
    ctx->read_events = calloc((size_t)ctx->connections_n, sizeof(acc_event_t));
    ctx->write_events = calloc((size_t)ctx->connections_n, sizeof(acc_event_t));
    if (ctx->connections == NULL || ctx->read_events == NULL ||
        ctx->write_events == NULL) {
        free(ctx->connections);
        free(ctx->read_events);
        free(ctx->write_events);
        ctx->connections = NULL;
        ctx->read_events = NULL;
        ctx->write_events = NULL;
        g_conn_ctx = NULL;
        return -1;
    }

    /* 自高向低串空闲链：取出顺序为 connections[0..n)，data 字段兼任链指针 */
    acc_connection_t *next = NULL;
    for (int i = ctx->connections_n - 1; i >= 0; i--) {
        acc_connection_t *c = &ctx->connections[i];
        c->fd = -1;
        c->read = &ctx->read_events[i];
        c->write = &ctx->write_events[i];
        c->recv = acc_unix_recv;
        c->send = acc_conn_socket_send;
        c->data = next; /* 取出时由 get_connection 覆写为 NULL */
        c->con_id = 0;
        c->reuse_count = 0;
        pthread_mutex_init(&c->buf_mu, NULL); /* 写路径互斥（Task 12 锁纪律） */
        acc_list_init(&c->inused_queue);
        acc_list_init(&c->read->queue);
        acc_list_init(&c->write->queue);
        next = c;
    }
    ctx->free_connections = next;
    ctx->free_connections_n = ctx->connections_n;
    acc_list_init(&ctx->inused_connection);
    return 0;
}

acc_connection_t *acc_get_connection(acc_context_t *ctx)
{
    acc_connection_t *c = ctx->free_connections;
    if (c == NULL)
        return NULL;

    ctx->free_connections = c->data;
    ctx->free_connections_n--;

    g_con_id++;
    c->con_id = g_con_id;
    c->timestamp = acc_time_now();
    c->data = NULL; /* 覆写空闲链串联指针 */
    c->recv = acc_unix_recv; /* 默认明文收发，accept 按监听类型覆写 */
    c->send = acc_conn_socket_send;
    /* 挂 inused 链尾：保活跃时间序，供超时清扫从头扫描 */
    acc_list_add_tail(&c->inused_queue, &ctx->inused_connection);
    return c;
}

void acc_free_connection(acc_context_t *ctx, acc_connection_t *c)
{
    c->reuse_count++; /* 代际校验：转发任务投递前比对防错发 */
    acc_list_del(&c->inused_queue); /* 摘 inused 链（自环节点时为无害空操作） */

    if (c->write_buf != NULL) { /* 写缓冲释放，下次使用时按配置重建 */
        acc_buf_destroy(c->write_buf);
        c->write_buf = NULL;
    }
    if (c->readbuf != NULL) { /* 应用层读缓冲兜底释放，防回收路径泄漏 */
        free(c->readbuf);
        c->readbuf = NULL;
    }
    c->readbufsize = 0;
    c->readlen = 0;
    c->readoffset = 0;

    /* 事件位清理：仅清标志。红线（T9）：posted handler 语境禁止对其他
     * 在队事件调 acc_event_delete_posted——残留节点由 acc_event_process_posted
     * 出队时自行摘除，此处绝不动 queue 节点本身 */
    c->read->active = 0;
    c->read->ready = 0;
    c->read->posted = 0;
    c->write->active = 0;
    c->write->ready = 0;
    c->write->posted = 0;

    c->fd = -1;
    c->data = ctx->free_connections; /* 串回空闲链头部（LIFO） */
    ctx->free_connections = c;
    ctx->free_connections_n++;
}

void acc_close_connection(acc_context_t *ctx, acc_connection_t *c)
{
    if (ctx == NULL || c == NULL || c->fd < 0)
        return;

    /* 应用层清理先行（含路由表 del_conn）：不持 buf_mu——
     * 转发 worker 持桶读锁后再取 buf_mu，若此处先持 buf_mu 再等桶写锁
     * 会构成 AB-BA 死锁 */
    if (ctx->on_conn_close != NULL && c->data != NULL)
        ctx->on_conn_close(c); /* 应用层清理（Task 12 填），未设置跳过 */

    /* 持 buf_mu 串行化整个拆链/回收：与线程池 send_publish 的
     * 写缓冲 push / 写事件注册 / flush 互斥；free 内 reuse_count++ 使
     * 残留转发任务的旧快照立即失效 */
    pthread_mutex_lock(&c->buf_mu);

    /* 仅做 epoll 注销与事件位清理（红线：不触碰 posted 队列其他在队事件） */
    acc_epoll_del_connection(c);

    if (c->ssl != NULL) {
        if (c->ssl_connected) {
            SSL_set_quiet_shutdown(c->ssl, 1); /* 不等待对端 close_notify */
            SSL_shutdown(c->ssl);
        }
        SSL_free(c->ssl);
        c->ssl = NULL;
        c->ssl_connected = 0;
    }

    int fd = c->fd;
    c->fd = -1;
    if (close(fd) == -1)
        ACC_LOGE("close(fd=%d) 失败, errno=%d", fd, errno);

    acc_free_connection(ctx, c);

    pthread_mutex_unlock(&c->buf_mu);
}

void acc_traversal_expired_connection(acc_context_t *ctx)
{
    time_t now = acc_time_now();
    int count = 0;

    ACC_LIST_FOR_EACH_SAFE(q, n, &ctx->inused_connection) {
        if (count >= ACC_EXPIRED_CLEAN_BATCH)
            break; /* 单轮限量：残余下轮继续 */
        acc_connection_t *c = ACC_LIST_ENTRY(q, acc_connection_t, inused_queue);
        /* 全量扫描：活跃时间戳刷新（session 每包更新）不重排 inused 链，
         * 队首未过期不代表后继未过期 */
        if (now > c->timestamp &&
            now - c->timestamp > (time_t)ctx->conf.client_timeout) {
            acc_close_connection(ctx, c);
            count++;
        }
    }
}

/* 收发原语 ------------------------------------------------------------------*/

ssize_t acc_unix_recv(acc_connection_t *c, void *buf, size_t size)
{
    ssize_t n = recv(c->fd, buf, size, 0);
    if (n > 0)
        return n;
    if (n == 0)
        return 0; /* 对端关闭 */
    if (errno == EAGAIN || errno == EWOULDBLOCK || errno == EINTR)
        return ACC_CONN_AGAIN;
    ACC_LOGE("recv(fd=%d) 失败, errno=%d", c->fd, errno);
    return ACC_CONN_ERR;
}

ssize_t acc_conn_socket_send(acc_connection_t *c, void *buf, size_t n)
{
    ssize_t nw = send(c->fd, buf, n, MSG_NOSIGNAL);
    if (nw >= 0)
        return nw;
    if (errno == EAGAIN || errno == EWOULDBLOCK || errno == EINTR)
        return ACC_CONN_AGAIN;
    ACC_LOGE("send(fd=%d) 失败, errno=%d", c->fd, errno);
    return ACC_CONN_ERR;
}

ssize_t acc_ssl_handshake(acc_connection_t *c)
{
    if (c->ssl == NULL)
        return ACC_CONN_ERR;

    int r = SSL_do_handshake(c->ssl);
    if (r == 1) {
        c->ssl_connected = 1;
        if (c->write->active) /* 握手期注册的写事件不再需要 */
            acc_epoll_del_event(c->write, ACC_EVENT_WRITE);
        return 0;
    }

    int err = SSL_get_error(c->ssl, r);
    if (err == SSL_ERROR_WANT_READ) {
        if (!c->read->active)
            acc_epoll_add_event(c->read, ACC_EVENT_READ);
        return ACC_CONN_AGAIN;
    }
    if (err == SSL_ERROR_WANT_WRITE) {
        if (!c->write->active)
            acc_epoll_add_event(c->write, ACC_EVENT_WRITE);
        return ACC_CONN_AGAIN;
    }
    ACC_LOGE("SSL 握手失败, ssl_err=%d, errno=%d", err, errno);
    return ACC_CONN_ERR;
}

ssize_t acc_ssl_recv(acc_connection_t *c, void *buf, size_t size)
{
    if (c->ssl == NULL)
        return ACC_CONN_ERR;

    if (!c->ssl_connected) { /* 未握手完成：先推进非阻塞握手 */
        ssize_t r = acc_ssl_handshake(c);
        if (r != 0)
            return r; /* AGAIN 等待下次事件；ERR 由上层关闭连接 */
    }

    int n = SSL_read(c->ssl, buf, (int)size);
    if (n > 0)
        return (ssize_t)n;

    int err = SSL_get_error(c->ssl, n);
    if (err == SSL_ERROR_WANT_READ || err == SSL_ERROR_WANT_WRITE) {
        /* 读路径的 WANT_WRITE 仅 TLS1.2 再协商中途出现，此处返回
         * AGAIN 却不注册写事件（已知留白，不改事件结构）：
         * - TLS1.3 已取消再协商，本端 min TLS1.2 且从不主动发起、
         *   亦不请求客户端证书（SSL_VERIFY_NONE），正常路径不可达；
         * - 若对端强行发起再协商导致阻塞无事件驱动，由超时清扫
         *   （conf.client_timeout）兜底回收，不崩、不死循环 */
        return ACC_CONN_AGAIN;
    }
    return ACC_CONN_ERR; /* 含对端正常关闭（0）与硬错误 */
}

ssize_t acc_conn_ssl_send(acc_connection_t *c, void *buf, size_t n)
{
    if (c->ssl == NULL || !c->ssl_connected)
        return ACC_CONN_ERR;

    int nw = SSL_write(c->ssl, buf, (int)n);
    if (nw > 0)
        return (ssize_t)nw;

    int err = SSL_get_error(c->ssl, nw);
    if (err == SSL_ERROR_WANT_READ || err == SSL_ERROR_WANT_WRITE) {
        /* 写路径的 WANT_READ 同为 TLS1.2 再协商场景（对称留白）：
         * 返回 AGAIN 依赖后续 EPOLLOUT/读事件重试，正常路径不可达
         * （本端不发起再协商、无客户端证书请求），异常卡死由超时
         * 清扫兜底回收 */
        return ACC_CONN_AGAIN;
    }
    return ACC_CONN_ERR;
}

/* 写缓冲集成 ----------------------------------------------------------------*/

int acc_conn_write(acc_context_t *ctx, acc_connection_t *c, const void *data,
                   size_t n)
{
    if (c->write_buf == NULL) {
        size_t limit = ctx->conf.max_read_buffer_size;
        if (limit == 0)
            limit = ACC_CONN_WRITE_BUF_DEFAULT; /* 未配置（测试/零配置）回退 */
        c->write_buf = acc_buf_create(limit);
        if (c->write_buf == NULL)
            return -1;
    }

    if (acc_buf_push_data(c->write_buf, data, n) != ACC_BUF_OK)
        return -1;

    /* 注册写事件（已注册时跳过，避免与读侧未注册时的重复 ADD 冲突）；
     * EPOLLOUT 就绪后由冲刷路径消费。accept 之外的独立注册路径
     * （测试/特殊连接）在此同步事件对象冗余的 fd */
    if (!c->write->active) {
        c->write->fd = c->fd;
        if (acc_epoll_add_event(c->write, ACC_EVENT_WRITE) == -1)
            return -1;
    }
    return 0;
}

int acc_conn_flush(acc_connection_t *c)
{
    buf_t *b = c->write_buf;
    if (b == NULL)
        return ACC_BUF_OK;

    int r = (c->ssl != NULL) ? acc_buf_write_to_ssl(b, c->ssl)
                             : acc_buf_write_to_fd(b, c->fd);
    if (r == ACC_BUF_OK && c->write->active)
        acc_epoll_del_event(c->write, ACC_EVENT_WRITE); /* 写空：摘 EPOLLOUT */
    return r; /* EAGAIN 保留剩余进度，待下次写就绪续传 */
}

/* 事件接线 ------------------------------------------------------------------*/

int acc_epoll_add_connection(acc_connection_t *c)
{
    acc_event_t *rev = c->read, *wev = c->write;

    rev->conn = 1;
    wev->conn = 1;
    rev->write = 0;
    wev->write = 1; /* ep_anchor 依 write 位定位锚点（data.ptr 固定挂读事件） */
    rev->data = c;
    wev->data = c;
    rev->peer = wev;
    wev->peer = rev;
    rev->fd = c->fd;
    wev->fd = c->fd;

    return acc_epoll_add_event(rev, ACC_EVENT_READ);
}

void acc_epoll_del_connection(acc_connection_t *c)
{
    if (c->read->active)
        acc_epoll_del_event(c->read, ACC_EVENT_READ);
    if (c->write->active) /* 先删读再删写：中间态经 MOD 收窄掩码 */
        acc_epoll_del_event(c->write, ACC_EVENT_WRITE);
}

void acc_event_accept(acc_event_t *ev)
{
    if (ev == NULL || ev->data == NULL)
        return;

    acc_listening_t *ls = ev->data;
    acc_context_t *ctx = g_conn_ctx;
    if (ctx == NULL)
        return;

    /* 启用 accept 互斥但未持锁：本 worker 本轮不处理监听事件 */
    if (acc_use_accept_mutex && !acc_accept_mutex_held)
        return;

    ev->ready = 0;

    for ( ;; ) {
        struct sockaddr_in sa;
        socklen_t slen = sizeof(sa);
        int s = accept4(ls->fd, (struct sockaddr *)&sa, &slen, SOCK_NONBLOCK);
        if (s == -1) {
            if (errno != EAGAIN && errno != EWOULDBLOCK)
                ACC_LOGE("accept4 失败, errno=%d", errno); /* EMFILE 等：结束本批 */
            break;
        }

        acc_connection_t *c = acc_get_connection(ctx);
        if (c == NULL) {
            ACC_LOGW("连接池已满, 丢弃新连接"); /* 优雅降级：进程不退出 */
            close(s);
            continue;
        }

        c->fd = s;
        c->timestamp = acc_time_now();
        c->data = NULL;
        if (ls->socket_type == ACC_SOCK_TLS) {
            c->recv = acc_ssl_recv;
            c->send = acc_conn_ssl_send;
            c->ssl_connected = 0;
            /* 在 accept 即派生 SSL：握手由首次读事件推进；
             * SSL_new 失败保持 ssl=NULL，首次 recv 报错走关闭路径 */
            c->ssl = SSL_new(ls->ssl_ctx);
            if (c->ssl != NULL) {
                SSL_set_fd(c->ssl, s);
                SSL_set_accept_state(c->ssl);
            }
        } else {
            c->recv = acc_unix_recv;
            c->send = acc_conn_socket_send;
        }

        if (acc_epoll_add_connection(c) == -1) {
            ACC_LOGE("连接事件注册失败, fd=%d", s);
            acc_close_connection(ctx, c);
            break;
        }

        if (ls->handler != NULL)
            ls->handler(c); /* listening 层填的 acc_mqtt_init_connection */
    }

    /* 批次收尾：负载超限置位（worker cycle 每轮递减消费） */
    acc_accept_disabled = ctx->connections_n / 8 - ctx->free_connections_n;

    /* 放锁职责归调用方事件循环（acc_process_events_and_timers 的
     * 持锁窗口末尾统一执行）：此处若放锁，同轮 posted 的后续 accept
     * 事件会被下方 !held 守卫跳过、普通事件提前脱离持锁语义 */
}
