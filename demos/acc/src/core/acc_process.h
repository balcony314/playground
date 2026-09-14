#ifndef ACC_PROCESS_H
#define ACC_PROCESS_H

#include <signal.h>
#include <stdint.h>
#include <sys/types.h>

#include "core/acc_core.h"

/* 进程表容量 */
#define ACC_MAX_PROCESSES 64
/* 无效 pid 标记（空槽/失败返回） */
#define ACC_INVALID_PID (-1)
/* respawn 退避间隔：50ms 起步、逐次倍增、1000ms 封顶（防死亡循环） */
#define ACC_RESPAWN_DELAY_MIN_MS 50
#define ACC_RESPAWN_DELAY_MAX_MS 1000

/* acc_spawn_process 的 respawn 参数语义 */
#define ACC_PROCESS_NORESPAWN (-1) /* 新槽位，退出后不重生 */
#define ACC_PROCESS_RESPAWN   (-2) /* 新槽位，异常退出后由 master 重生 */

/* 进程角色（acc_process 取值）：信号 handler 据此区分语义 */
#define ACC_PROCESS_MASTER 1
#define ACC_PROCESS_WORKER 2

typedef void (*acc_spawn_proc_pt)(acc_context_t *ctx, void *data);

/* 进程表项：master 维护；fork 后 worker 继承副本，仅用于通道 fd 管理
 * （保留其他槽位 master 端 fd 副本，供 CLOSE_CHANNEL 命令按 slot 关闭） */
typedef struct {
    pid_t pid;               /* ACC_INVALID_PID = 空槽 */
    int channel[2];          /* [0] master 侧，[1] worker 侧；-1 = 未打开 */
    acc_spawn_proc_pt proc;  /* 重生时重新执行的入口 */
    void *data;              /* proc 的透传参数 */
    char name[16];           /* 日志用进程名 */
    unsigned respawn:1;      /* 异常退出后由 master 重生 */
    unsigned just_spawn:1;   /* 刚生成：首轮命令下发跳过（raw 语义） */
    unsigned detached:1;     /* 预留 */
    unsigned exiting:1;      /* master 已向其发出退出命令 */
    unsigned exited:1;       /* 已被 waitpid 回收 */
    int64_t respawn_at_ms;   /* 允许下次 respawn 的时刻（MONOTONIC 毫秒） */
    int respawn_delay_ms;    /* respawn 退避间隔（ACC_RESPAWN_DELAY_* 之间倍增） */
} acc_process_t;

extern acc_process_t acc_processes[ACC_MAX_PROCESSES];
extern int acc_last_process; /* 已用槽位数 */
extern int acc_process_slot; /* worker 自身槽位（spawn 时设置） */
extern int acc_channel;      /* worker 自身通道 fd（channel[1]），-1 = 无 */
extern int acc_process;      /* 当前进程角色（ACC_PROCESS_*） */

/* 信号标志：handler 内写、主循环读；volatile sig_atomic_t 保证原子可见 */
extern volatile sig_atomic_t acc_reap;
extern volatile sig_atomic_t acc_quit;
extern volatile sig_atomic_t acc_terminate;
extern volatile sig_atomic_t acc_reconfigure;

/* 生成子进程：socketpair 通道 + fork；子进程完成通道 fd 卫生后执行
 * proc(ctx, data) 并 _exit。成功返回子 pid，失败 ACC_INVALID_PID。
 * respawn >= 0 表示复用指定槽位（重生路径），否则按参数语义取新槽位 */
pid_t acc_spawn_process(acc_context_t *ctx, acc_spawn_proc_pt proc,
                        void *data, const char *name, int respawn);

/* 安装信号表：SIGTERM/SIGINT→terminate、SIGQUIT→quit、SIGCHLD→reap、
 * SIGHUP→reconfigure、SIGALRM→仅唤醒、SIGPIPE→SIG_IGN（写半关连接不炸进程）。
 * 成功返回 0，失败 -1 */
int acc_init_signals(void);

/* 按 slot 关闭本进程持有的 master 侧通道 fd 副本（CLOSE_CHANNEL 命令落地） */
void acc_process_close_master_channel(int slot);

#endif /* ACC_PROCESS_H */
