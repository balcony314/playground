#include "acc_buffer.h"

#include <errno.h>
#include <openssl/ssl.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>

/* 单个节点默认容量（字节） */
#define ACC_BUF_NODE_CAP 4096

/* 链表节点：data[head..tail) 为有效数据区 */
struct buf_node {
    struct buf_node *next;
    unsigned char *data;
    size_t cap, head, tail;
};

/* 写缓冲：单链表 + 当前总长 + 总容量上限 */
struct buf_s {
    size_t len;         /* 当前缓冲字节数 */
    size_t total_limit; /* 总容量上限 */
    struct buf_node *head, *tail;
};

/* 新建节点：容量 = max(现有总长/2, 需求 n)，且不低于默认 4096；失败返回 NULL */
static struct buf_node *buf_node_new(const buf_t *b, size_t need)
{
    size_t cap = b->len / 2;
    if (need > cap)
        cap = need;
    if (cap < ACC_BUF_NODE_CAP)
        cap = ACC_BUF_NODE_CAP;

    struct buf_node *node = calloc(1, sizeof(*node));
    if (!node)
        return NULL;
    node->data = malloc(cap);
    if (!node->data) {
        free(node);
        return NULL;
    }
    node->cap = cap;
    return node;
}

buf_t *acc_buf_create(size_t total_limit)
{
    buf_t *b = calloc(1, sizeof(*b));
    if (!b)
        return NULL;
    b->total_limit = total_limit;
    return b;
}

void acc_buf_destroy(buf_t *b)
{
    if (!b)
        return;
    struct buf_node *n = b->head;
    while (n) {
        struct buf_node *next = n->next;
        free(n->data);
        free(n);
        n = next;
    }
    free(b);
}

int acc_buf_push_data(buf_t *b, const void *data, size_t n)
{
    if (!b || (!data && n > 0))
        return ACC_BUF_ERR;
    /* 上限保护：超限则一个字节都不入队（先单独判 n，避免 len+n 回绕） */
    if (n > b->total_limit || b->len + n > b->total_limit)
        return ACC_BUF_LIMIT;

    const unsigned char *p = data;
    while (n > 0) {
        struct buf_node *node = b->tail;
        size_t room = node ? node->cap - node->tail : 0;
        if (room == 0) {
            node = buf_node_new(b, n);
            if (!node)
                return ACC_BUF_ERR; /* 分配失败：已入队部分保留 */
            if (b->tail)
                b->tail->next = node;
            else
                b->head = node;
            b->tail = node;
            room = node->cap - node->tail;
        }
        size_t chunk = n < room ? n : room;
        memcpy(node->data + node->tail, p, chunk);
        node->tail += chunk;
        p += chunk;
        n -= chunk;
        b->len += chunk;
    }
    return ACC_BUF_OK;
}

size_t acc_buf_len(const buf_t *b)
{
    return b ? b->len : 0;
}

int acc_buf_write_to_fd(buf_t *b, int fd)
{
    if (!b)
        return ACC_BUF_ERR;
    while (b->head) {
        struct buf_node *node = b->head;
        if (node->head < node->tail) {
            ssize_t nw = send(fd, node->data + node->head,
                              node->tail - node->head, MSG_NOSIGNAL);
            if (nw < 0) {
                if (errno == EAGAIN || errno == EWOULDBLOCK)
                    return ACC_BUF_EAGAIN; /* 保留节点进度，稍后续写 */
                return ACC_BUF_ERR;
            }
            node->head += (size_t)nw;
            b->len -= (size_t)nw;
        }
        /* 节点数据写完即释放 */
        if (node->head == node->tail) {
            b->head = node->next;
            if (!b->head)
                b->tail = NULL;
            free(node->data);
            free(node);
        }
    }
    return ACC_BUF_OK;
}

int acc_buf_write_to_ssl(buf_t *b, struct ssl_st *ssl)
{
    SSL *s = (SSL *)ssl;
    if (!b || !s)
        return ACC_BUF_ERR;
    while (b->head) {
        struct buf_node *node = b->head;
        if (node->head < node->tail) {
            /* 默认模式下 SSL_write 成功即写入全部请求字节；
             * WANT_READ/WANT_WRITE 返回时一个字节都未写，
             * OpenSSL 要求重试沿用相同指针与长度——节点保留
             * head 进度恰好满足，直接整段重试即可 */
            int nw = SSL_write(s, node->data + node->head,
                               (int)(node->tail - node->head));
            if (nw <= 0) {
                int err = SSL_get_error(s, nw);
                if (err == SSL_ERROR_WANT_READ ||
                    err == SSL_ERROR_WANT_WRITE)
                    /* WANT_READ 同为 TLS1.2 再协商场景（对称留白）：
                     * 正常路径不可达，残余数据留待下次 EPOLLOUT/读事件
                     * 续写；再协商卡死无事件时由超时清扫兜底回收 */
                    return ACC_BUF_EAGAIN; /* 保留节点进度，稍后续写 */
                return ACC_BUF_ERR;
            }
            node->head += (size_t)nw;
            b->len -= (size_t)nw;
        }
        /* 节点数据写完即释放 */
        if (node->head == node->tail) {
            b->head = node->next;
            if (!b->head)
                b->tail = NULL;
            free(node->data);
            free(node);
        }
    }
    return ACC_BUF_OK;
}
