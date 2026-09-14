/* 进程管理：spawn（socketpair 通道 + fork）、信号表与信号标志，
 * 对齐 raw 的 ads_process.c（剥离 os_signal_process 等运维外围） */
#include "acc_process.h"

#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/wait.h>
#include <unistd.h>

#include "net/acc_channel.h"
#include "util/acc_log.h"

/* 信号表项 */
typedef struct {
    int signo;
    const char *signame;
    void (*handler)(int);
} acc_signal_t;

static void acc_signal_handler(int signo);
static void acc_init_process_table(void);

acc_process_t acc_processes[ACC_MAX_PROCESSES];
int acc_last_process = 0;
int acc_process_slot = 0;
int acc_channel = -1;
int acc_process = 0;

volatile sig_atomic_t acc_reap = 0;
volatile sig_atomic_t acc_quit = 0;
volatile sig_atomic_t acc_terminate = 0;
volatile sig_atomic_t acc_reconfigure = 0;

/* 信号表：SIGPIPE 必须 SIG_IGN——SSL_write/socket 对端半关时内核发
 * SIGPIPE，默认动作会直接杀死 worker */
static acc_signal_t signals_tab[] = {
    { SIGHUP, "SIGHUP", acc_signal_handler },
    { SIGTERM, "SIGTERM", acc_signal_handler },
    { SIGQUIT, "SIGQUIT", acc_signal_handler },
    { SIGINT, "SIGINT", acc_signal_handler },
    { SIGALRM, "SIGALRM", acc_signal_handler }, /* 仅唤醒 sigsuspend */
    { SIGCHLD, "SIGCHLD", acc_signal_handler },
    { SIGPIPE, "SIGPIPE", SIG_IGN },
    { 0, NULL, NULL }
};

/* 进程表首用初始化：fd/pid 需显式 -1（BSS 清零的 0 是合法 fd/pid 语义干扰） */
static void acc_init_process_table(void)
{
    static int inited = 0;

    if (inited)
        return;
    for (int i = 0; i < ACC_MAX_PROCESSES; i++) {
        acc_processes[i].pid = ACC_INVALID_PID;
        acc_processes[i].channel[0] = -1;
        acc_processes[i].channel[1] = -1;
    }
    inited = 1;
}

int acc_init_signals(void)
{
    /* 调用点在 main：此后到达的信号按 master 语义处理
     * （worker 侧由 spawn 的子进程自行改写角色） */
    acc_process = ACC_PROCESS_MASTER;

    for (acc_signal_t *sig = signals_tab; sig->signo != 0; sig++) {
        struct sigaction sa;
        memset(&sa, 0, sizeof(sa));
        sa.sa_handler = sig->handler;
        sigemptyset(&sa.sa_mask);
        if (sigaction(sig->signo, &sa, NULL) == -1) {
            ACC_LOGE("sigaction(%s) 失败, errno=%d", sig->signame, errno);
            return -1;
        }
    }
    return 0;
}

/* 信号 handler：只写 volatile sig_atomic_t 标志（async-signal-safe） */
static void acc_signal_handler(int signo)
{
    switch (acc_process) {
    case ACC_PROCESS_MASTER:
        switch (signo) {
        case SIGQUIT:
            acc_quit = 1; /* 优雅退出 */
            break;
        case SIGTERM:
        case SIGINT:
            acc_terminate = 1; /* 快速退出 */
            break;
        case SIGHUP:
            acc_reconfigure = 1;
            break;
        case SIGCHLD:
            acc_reap = 1;
            break;
        default:
            break; /* SIGALRM：仅唤醒 sigsuspend（terminate 进度复查） */
        }
        break;
    case ACC_PROCESS_WORKER:
        switch (signo) {
        case SIGQUIT:
        case SIGTERM:
        case SIGINT:
            acc_worker_quit = 1; /* 统一并入优雅退出标志 */
            break;
        default:
            break;
        }
        break;
    default:
        break; /* 信号表安装前（角色未定）：忽略 */
    }
}

pid_t acc_spawn_process(acc_context_t *ctx, acc_spawn_proc_pt proc,
                        void *data, const char *name, int respawn)
{
    acc_init_process_table();

    int s;
    if (respawn >= 0) {
        s = respawn; /* 重生路径：复用指定槽位 */
    } else {
        for (s = 0; s < acc_last_process; s++) {
            if (acc_processes[s].pid == ACC_INVALID_PID)
                break;
        }
    }
    if (s >= ACC_MAX_PROCESSES) {
        ACC_LOGE("进程数超过上限 %d", ACC_MAX_PROCESSES);
        return ACC_INVALID_PID;
    }

    if (socketpair(AF_UNIX, SOCK_STREAM, 0, acc_processes[s].channel) == -1) {
        ACC_LOGE("socketpair 失败, errno=%d", errno);
        return ACC_INVALID_PID;
    }

    /* 两侧非阻塞（worker 侧 handler 依赖 EAGAIN 排空语义）+ CLOEXEC */
    for (int i = 0; i < 2; i++) {
        int fd = acc_processes[s].channel[i];
        int flags = fcntl(fd, F_GETFL);
        if (flags == -1 ||
            fcntl(fd, F_SETFL, flags | O_NONBLOCK) == -1 ||
            fcntl(fd, F_SETFD, FD_CLOEXEC) == -1) {
            ACC_LOGE("设置通道 fd 属性失败, errno=%d", errno);
            close(acc_processes[s].channel[0]);
            close(acc_processes[s].channel[1]);
            acc_processes[s].channel[0] = -1;
            acc_processes[s].channel[1] = -1;
            return ACC_INVALID_PID;
        }
    }

    /* fork 前设置：子进程经内存副本获得自身通道 fd 与槽位 */
    acc_channel = acc_processes[s].channel[1];
    acc_process_slot = s;

    pid_t pid = fork();
    switch (pid) {
    case -1:
        ACC_LOGE("fork 失败, errno=%d", errno);
        close(acc_processes[s].channel[0]);
        close(acc_processes[s].channel[1]);
        acc_processes[s].channel[0] = -1;
        acc_processes[s].channel[1] = -1;
        return ACC_INVALID_PID;

    case 0:
        /* 子进程 fd 卫生：关自己槽位的 master 端与其他 worker 的
         * channel[1]；保留其他槽位 channel[0] 副本供 CLOSE_CHANNEL 关闭 */
        for (int n = 0; n < acc_last_process; n++) {
            if (n == s || acc_processes[n].pid == ACC_INVALID_PID)
                continue;
            if (acc_processes[n].channel[1] != -1) {
                close(acc_processes[n].channel[1]);
                acc_processes[n].channel[1] = -1;
            }
        }
        if (acc_processes[s].channel[0] != -1) {
            close(acc_processes[s].channel[0]);
            acc_processes[s].channel[0] = -1;
        }
        acc_process = ACC_PROCESS_WORKER;
        proc(ctx, data);
        _exit(0); /* proc 返回即异常路径：不得落入父进程逻辑 */

    default:
        break;
    }

    /* 父进程登记 */
    acc_processes[s].pid = pid;
    acc_processes[s].exited = 0;
    acc_processes[s].exiting = 0;
    acc_processes[s].just_spawn = 1; /* 首轮命令下发跳过（signal_worker 清零） */
    if (respawn < 0) { /* 新槽位才登记入口（复用槽位保留原值） */
        acc_processes[s].proc = proc;
        acc_processes[s].data = data;
        snprintf(acc_processes[s].name, sizeof(acc_processes[s].name), "%s",
                 name);
        acc_processes[s].respawn = (respawn == ACC_PROCESS_RESPAWN) ? 1 : 0;
        acc_processes[s].detached = 0;
        acc_processes[s].respawn_at_ms = 0; /* 无退避限制 */
        acc_processes[s].respawn_delay_ms = ACC_RESPAWN_DELAY_MIN_MS;
    }
    if (s == acc_last_process)
        acc_last_process++;

    ACC_LOGI("spawn %s pid=%d slot=%d", acc_processes[s].name, (int)pid, s);
    return pid;
}

void acc_process_close_master_channel(int slot)
{
    if (slot < 0 || slot >= ACC_MAX_PROCESSES)
        return;

    if (acc_processes[slot].channel[0] != -1) {
        close(acc_processes[slot].channel[0]);
        acc_processes[slot].channel[0] = -1;
    }
}
