#ifndef ACC_BUFFER_H
#define ACC_BUFFER_H

#include <stddef.h>

/* 返回码 */
#define ACC_BUF_OK     0  /* 成功 */
#define ACC_BUF_EOF   -1  /* 结束（预留） */
#define ACC_BUF_EAGAIN -2 /* 非阻塞写暂时不可写，稍后重试 */
#define ACC_BUF_LIMIT -3  /* 超过总容量上限，未入队任何数据 */
#define ACC_BUF_ERR   -4  /* 一般错误（含内存分配失败） */

/* 写缓冲（链表实现，结构定义见 acc_buffer.c） */
typedef struct buf_s buf_t;

/* 创建总容量上限为 total_limit 字节的写缓冲，失败返回 NULL */
buf_t *acc_buf_create(size_t total_limit);

/* 销毁缓冲并释放所有未发送数据 */
void acc_buf_destroy(buf_t *b);

/* 拷贝 n 字节入链；若 b->len + n 超过 total_limit 则返回 ACC_BUF_LIMIT 且不入队任何数据 */
int acc_buf_push_data(buf_t *b, const void *data, size_t n);

/* 当前缓冲内的字节数 */
size_t acc_buf_len(const buf_t *b);

/* 非阻塞冲刷到 fd：逐节点 send(MSG_NOSIGNAL)；写完的节点立即释放。
 * EAGAIN/EWOULDBLOCK → ACC_BUF_EAGAIN（保留进度，稍后续写）；
 * 其他错误 → ACC_BUF_ERR；全部写完 → ACC_BUF_OK 且 len==0 */
int acc_buf_write_to_fd(buf_t *b, int fd);

/* 非阻塞冲刷到 SSL：语义同 acc_buf_write_to_fd（SSL_write 逐节点推进，
 * WANT_READ/WANT_WRITE → ACC_BUF_EAGAIN；其余错误 → ACC_BUF_ERR） */
struct ssl_st;
int acc_buf_write_to_ssl(buf_t *b, struct ssl_st *ssl);

#endif /* ACC_BUFFER_H */
