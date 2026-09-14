/* master/worker 进程通道：socketpair + SCM_RIGHTS 传命令与 fd，
 * 对齐 raw 的 ads_channel.c（剥离进程表登记，归 Task 11） */
#include "acc_channel.h"

#include <errno.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/uio.h>
#include <unistd.h>

#include "core/acc_process.h" /* 进程表：CLOSE/OPEN_CHANNEL 的 fd 登记 */
#include "net/acc_connection.h" /* ACC_CONN_AGAIN 返回码 */
#include "util/acc_log.h"

/* worker 退出标志：通道命令或信号置位，worker 主循环检测后退出 */
volatile sig_atomic_t acc_worker_quit = 0;

/* cmsg 缓冲：恰好容纳一个 int fd 的 SCM_RIGHTS 控制报文 */
typedef union {
    struct cmsghdr cm;
    char space[CMSG_SPACE(sizeof(int))];
} acc_cmsg_buf_t;

int acc_write_channel(int fd, acc_channel_t *ch, int fd_to_pass)
{
    struct iovec iov[1];
    struct msghdr msg;
    acc_cmsg_buf_t cbuf;

    memset(&msg, 0, sizeof(msg));
    iov[0].iov_base = (void *)ch;
    iov[0].iov_len = sizeof(*ch);
    msg.msg_iov = iov;
    msg.msg_iovlen = 1;

    if (fd_to_pass >= 0) {
        memset(&cbuf, 0, sizeof(cbuf));
        cbuf.cm.cmsg_len = CMSG_LEN(sizeof(int));
        cbuf.cm.cmsg_level = SOL_SOCKET;
        cbuf.cm.cmsg_type = SCM_RIGHTS;
        /* memcpy 写入规避严格别名问题（对齐 raw 注记） */
        memcpy(CMSG_DATA(&cbuf.cm), &fd_to_pass, sizeof(int));
        msg.msg_control = &cbuf;
        msg.msg_controllen = sizeof(cbuf);
    }

    if (sendmsg(fd, &msg, MSG_NOSIGNAL) == -1) {
        if (errno == EAGAIN || errno == EWOULDBLOCK)
            return ACC_CONN_AGAIN; /* 非阻塞通道暂满，稍后重试 */
        ACC_LOGE("sendmsg(fd=%d) 失败, errno=%d", fd, errno);
        return -1;
    }
    return 0;
}

int acc_read_channel(int fd, acc_channel_t *ch)
{
    struct iovec iov[1];
    struct msghdr msg;
    acc_cmsg_buf_t cbuf;

    memset(&msg, 0, sizeof(msg));
    iov[0].iov_base = (void *)ch;
    iov[0].iov_len = sizeof(*ch);
    msg.msg_iov = iov;
    msg.msg_iovlen = 1;
    msg.msg_control = &cbuf;
    msg.msg_controllen = sizeof(cbuf);

    ssize_t n = recvmsg(fd, &msg, 0);
    if (n == -1) {
        if (errno == EAGAIN || errno == EWOULDBLOCK)
            return ACC_CONN_AGAIN; /* 暂无命令 */
        ACC_LOGE("recvmsg(fd=%d) 失败, errno=%d", fd, errno);
        return -1;
    }
    if (n == 0)
        return -1; /* 对端关闭 */
    if ((size_t)n < sizeof(*ch) || (msg.msg_flags & (MSG_TRUNC | MSG_CTRUNC)))
        return -1; /* 报文不完整或控制数据被截断 */

    /* 遍历 cmsg 链取回 SCM_RIGHTS 传来的 fd（边界由 CMSG 宏保证） */
    ch->fd = -1;
    for (struct cmsghdr *cm = CMSG_FIRSTHDR(&msg); cm != NULL;
         cm = CMSG_NXTHDR(&msg, cm)) {
        if (cm->cmsg_level == SOL_SOCKET && cm->cmsg_type == SCM_RIGHTS &&
            cm->cmsg_len >= CMSG_LEN(sizeof(int))) {
            memcpy(&ch->fd, CMSG_DATA(cm), sizeof(int));
        }
    }
    return 0;
}

void acc_channel_handler(acc_event_t *ev)
{
    for ( ;; ) {
        acc_channel_t ch;
        int rc = acc_read_channel(ev->fd, &ch);
        if (rc == ACC_CONN_AGAIN)
            return; /* 已排空 */
        if (rc != 0) {
            ACC_LOGE("读取通道命令失败, fd=%d", ev->fd);
            return;
        }

        switch (ch.command) {
        case ACC_CMD_QUIT:
        case ACC_CMD_TERMINATE:
            acc_worker_quit = 1;
            break;
        case ACC_CMD_CLOSE_CHANNEL:
            /* 某槽位 worker 已退出：关闭本进程持有的该槽位 master 端
             * fd 副本（经 OPEN_CHANNEL 登记或 fork 继承），防泄漏 */
            acc_process_close_master_channel(ch.slot);
            break;
        case ACC_CMD_OPEN_CHANNEL:
            /* 新 worker 上线：登记其通道 master 端 fd 副本（SCM_RIGHTS
             * 取回于 ch.fd），供后续 CLOSE_CHANNEL 按 slot 关闭。
             * 覆写前先关旧值：CLOSE_CHANNEL 丢失时防 fd 泄漏 */
            if (ch.fd >= 0 && ch.slot >= 0 && ch.slot < ACC_MAX_PROCESSES) {
                acc_process_close_master_channel(ch.slot);
                acc_processes[ch.slot].channel[0] = ch.fd;
            }
            break;
        default:
            ACC_LOGW("未知通道命令 %u", ch.command);
            break;
        }
    }
}
