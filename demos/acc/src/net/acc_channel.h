#ifndef ACC_CHANNEL_H
#define ACC_CHANNEL_H

#include <signal.h>
#include <stdint.h>
#include <sys/types.h>

#include "event/acc_event.h"

/* master ↔ worker 进程通道命令 */
#define ACC_CMD_OPEN_CHANNEL  1 /* 向 worker 移交监听/通道 fd（SCM_RIGHTS） */
#define ACC_CMD_CLOSE_CHANNEL 2 /* 关闭指定 worker 的通道 fd（ch.fd 携带） */
#define ACC_CMD_QUIT          3 /* 优雅退出 */
#define ACC_CMD_TERMINATE     4 /* 立即退出 */

/* 通道命令结构：经 socketpair sendmsg/recvmsg 传递，fd 字段为 SCM_RIGHTS 载体 */
typedef struct {
    uint32_t command; /* ACC_CMD_* */
    pid_t pid;        /* 命令来源/目标进程 */
    int slot;         /* worker 进程表槽位 */
    int fd;           /* 接收方向：从 cmsg 取回的 fd；无则为 -1 */
} acc_channel_t;

/* 写一条命令；fd_to_pass >= 0 时经 SCM_RIGHTS 附带传递该 fd。
 * 成功 0；EAGAIN -2（非阻塞通道暂满）；错误 -1 */
int acc_write_channel(int fd, acc_channel_t *ch, int fd_to_pass);

/* 读一条命令：从 cmsg 取回的 fd 存入 ch->fd（无 cmsg 时置 -1）。
 * 成功 0；EAGAIN -2；错误（含对端关闭、报文不完整）-1 */
int acc_read_channel(int fd, acc_channel_t *ch);

/* 通道读事件回调：循环读命令直到 EAGAIN。
 * QUIT/TERMINATE 置 acc_worker_quit；CLOSE_CHANNEL 按槽位关闭本进程
 * 持有的 master 端通道 fd 副本；OPEN_CHANNEL 登记新 worker 的 master
 * 端 fd 副本（SCM_RIGHTS） */
void acc_channel_handler(acc_event_t *ev);

/* worker 退出标志：通道命令或信号置位，worker 主循环检测后退出 */
extern volatile sig_atomic_t acc_worker_quit;

#endif /* ACC_CHANNEL_H */
