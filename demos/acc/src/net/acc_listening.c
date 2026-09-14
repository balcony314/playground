/* 监听 socket 管理：按 conf 创建 TCP/TLS 双监听（对齐 raw 的
 * ads_create_listening_sockets，剥离业务与地址记录字段） */
#include "acc_listening.h"

#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <netinet/in.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include "core/acc_core.h"
/* net→mqtt 的依赖仅此一处：监听 handler 固定填 MQTT 接入初始化，
 * 新连接的业务初始化（session/读写 handler）由此进入 */
#include "mqtt/acc_mqtt_session.h"
#include "util/acc_log.h"

/* 监听积压队列长度（对齐 raw 的 LISTEN_BACKLOG） */
#define ACC_LISTEN_BACKLOG 511

/* 解析 conf.server 为 IPv4 地址：空/"*"/"0.0.0.0" 通配，"localhost" 回环，
 * 其余按点分地址解析；成功返回 0，非法地址返回 -1 */
static int listening_resolve_server(const char *server, struct in_addr *addr)
{
    if (server[0] == '\0' || strcmp(server, "*") == 0 ||
        strcmp(server, "0.0.0.0") == 0) {
        addr->s_addr = htonl(INADDR_ANY);
        return 0;
    }
    if (strcmp(server, "localhost") == 0) {
        addr->s_addr = htonl(INADDR_LOOPBACK);
        return 0;
    }
    if (inet_pton(AF_INET, server, addr) != 1) {
        ACC_LOGE("非法监听地址 \"%s\"", server);
        return -1;
    }
    return 0;
}

/* 创建并监听一个 socket（SO_REUSEADDR + O_NONBLOCK + bind + listen），
 * 成功返回 fd，失败返回 -1 */
static int listening_open_socket(const struct sockaddr_in *sa)
{
    int fd = socket(AF_INET, SOCK_STREAM, 0);
    if (fd == -1) {
        ACC_LOGE("socket() 失败, errno=%d", errno);
        return -1;
    }

    int reuse = 1;
    if (setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &reuse, sizeof(reuse)) == -1) {
        ACC_LOGE("setsockopt(SO_REUSEADDR) 失败, errno=%d", errno);
        close(fd);
        return -1;
    }

    int flags = fcntl(fd, F_GETFL);
    if (flags == -1 || fcntl(fd, F_SETFL, flags | O_NONBLOCK) == -1) {
        ACC_LOGE("fcntl(O_NONBLOCK) 失败, errno=%d", errno);
        close(fd);
        return -1;
    }

    if (bind(fd, (const struct sockaddr *)sa, sizeof(*sa)) == -1) {
        ACC_LOGE("bind(port=%u) 失败, errno=%d", ntohs(sa->sin_port), errno);
        close(fd);
        return -1;
    }

    if (listen(fd, ACC_LISTEN_BACKLOG) == -1) {
        ACC_LOGE("listen() 失败, errno=%d", errno);
        close(fd);
        return -1;
    }
    return fd;
}

/* 创建单个监听对象并头插进 ctx->listenlist，成功返回 0 */
static int create_one_listening(acc_context_t *ctx, int port, int socket_type)
{
    struct sockaddr_in sa;
    memset(&sa, 0, sizeof(sa));
    sa.sin_family = AF_INET;
    sa.sin_port = htons((uint16_t)port);
    if (listening_resolve_server(ctx->conf.server, &sa.sin_addr) != 0)
        return -1;

    int fd = listening_open_socket(&sa);
    if (fd == -1)
        return -1;

    acc_listening_t *ls = calloc(1, sizeof(*ls));
    if (ls == NULL) {
        ACC_LOGE("分配监听对象失败");
        close(fd);
        return -1;
    }
    ls->fd = fd;
    ls->port = port;
    ls->socket_type = socket_type;
    ls->ssl_ctx = (socket_type == ACC_SOCK_TLS) ? ctx->ssl_ctx : NULL;
    ls->handler = acc_mqtt_init_connection; /* 新连接固定走 MQTT 接入 */
    ls->next = ctx->listenlist;
    ctx->listenlist = ls;
    return 0;
}

int acc_create_listening_sockets(acc_context_t *ctx)
{
    if (ctx == NULL)
        return -1;

    if (ctx->conf.tcp_port > 0 &&
        create_one_listening(ctx, ctx->conf.tcp_port, ACC_SOCK_TCP) != 0)
        return -1;

    if (ctx->conf.tls_port > 0) { /* 0 = 不启用 TLS 监听 */
        if (ctx->ssl_ctx == NULL) {
            /* 证书缺失等降级：放弃 TLS 监听，明文端口继续服务 */
            ACC_LOGW("tls_port=%d 但 SSL_CTX 未初始化, 跳过 TLS 监听",
                     ctx->conf.tls_port);
            return 0;
        }
        if (create_one_listening(ctx, ctx->conf.tls_port, ACC_SOCK_TLS) != 0)
            return -1;
    }
    return 0;
}

void acc_close_listening_sockets(acc_context_t *ctx)
{
    if (ctx == NULL)
        return;
    acc_listening_t *ls = ctx->listenlist;
    while (ls != NULL) {
        acc_listening_t *next = ls->next;
        if (ls->fd != -1) {
            close(ls->fd);
            ls->fd = -1;
        }
        free(ls);
        ls = next;
    }
    ctx->listenlist = NULL;
}
