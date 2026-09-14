/* master/worker 进程主循环：master 以 sigsuspend 派发信号驱动
 * respawn/优雅退出/强杀；worker 完成 init（RLIMIT/日志/epoll/连接池/
 * 线程池/路由表/MQTT 接线/SSL/监听与通道事件注册）后进入事件主循环。
 * 对齐 raw 的 ads_process_cycle.c（剥离 zookeeper/redis 等业务外围） */
#include "acc_process_cycle.h"

#include <errno.h>
#include <signal.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>
#include <sys/resource.h>
#include <sys/time.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

#include <openssl/ssl.h>

#include "acc_process.h"
#include "event/acc_epoll.h"
#include "mqtt/acc_mqtt_router.h"
#include "mqtt/acc_mqtt_session.h"
#include "net/acc_channel.h"
#include "net/acc_connection.h"
#include "net/acc_listening.h"
#include "util/acc_log.h"
#include "util/acc_metrics.h"
#include "util/acc_mutex.h"
#include "util/acc_thread_pool.h"
#include "util/acc_time.h"

/* worker 事件循环的 epoll_wait 超时（毫秒）：兼顾退出响应与清扫精度 */
#define ACC_CYCLE_TIMER_MS 500
/* terminate 后给 worker 的优雅退出窗口（秒）：超窗升级 SIGKILL */
#define ACC_TERMINATE_GRACE_SEC 5
/* worker 退出前线程池排空等待上限（毫秒） */
#define ACC_POOL_DRAIN_MS 2000
/* 订阅路由表桶数（无对应配置项，取固定值） */
#define ACC_SUB_MAP_BUCKETS 1024
/* worker 内监听读事件数组容量（TCP/TLS 各一，预留余量） */
#define ACC_MAX_LISTEN_EVENTS 8

/* 监听读事件与命令通道读事件：worker 进程生命周期常驻 */
static acc_event_t g_listen_events[ACC_MAX_LISTEN_EVENTS];
static int g_listen_events_n;
static acc_event_t g_channel_event;

static void acc_start_worker_processes(acc_context_t *ctx, int n);
static void acc_broadcast_channel_cmd(acc_context_t *ch_ctx,
                                      acc_channel_t *ch, int skip_slot);
static void acc_signal_worker_processes(acc_context_t *ctx, int signo);
static int acc_reap_children(acc_context_t *ctx);
static void acc_master_arm_timer(void);
static void acc_master_process_exit(acc_context_t *ctx);
static void acc_worker_process_init(acc_context_t *ctx, int worker);
static void acc_worker_process_exit(acc_context_t *ctx);
static int acc_trylock_accept_mutex(acc_context_t *ctx);
static int acc_enable_accept_events(acc_context_t *ctx);
static int acc_disable_accept_events(acc_context_t *ctx);
static int64_t acc_monotonic_ms(void);

/* 是否存在退避等待中的待重生槽位（acc_reap_children 每轮刷新），
 * 决定 master 是否需要周期唤醒定时器 */
static int g_pending_respawn;

/* master 主循环 -------------------------------------------------------------*/

void acc_master_process_cycle(acc_context_t *ctx)
{
    sigset_t set;
    int live;
    time_t term_start = 0;

    /* 阻塞全部信号：只在 sigsuspend 的临时掩码处原子地放开等待 */
    sigfillset(&set);
    if (sigprocmask(SIG_BLOCK, &set, NULL) == -1)
        ACC_LOGE("sigprocmask(SIG_BLOCK) 失败, errno=%d", errno);
    sigemptyset(&set);

    acc_process = ACC_PROCESS_MASTER;
    acc_start_worker_processes(ctx, ctx->conf.worker_process);
    live = acc_reap_children(ctx); /* 初始存活统计：spawn 全失败时为 0 */

    ACC_LOGI("master 运行, pid=%d, worker=%d", (int)getpid(),
             ctx->conf.worker_process);

    for ( ;; ) {
        acc_master_arm_timer(); /* 依退出标志/退避状态安排周期唤醒 */
        sigsuspend(&set);       /* 等待任一信号（SIGCHLD/SIGALRM/退出命令） */

        if (acc_reap)
            acc_reap = 0;
        /* 每次唤醒都重算：SIGALRM 唤醒需复查退避到期与退出进度 */
        live = acc_reap_children(ctx);

        if (!live && (acc_terminate || acc_quit))
            acc_master_process_exit(ctx);

        if (acc_terminate) {
            if (term_start == 0)
                term_start = time(NULL);
            if (time(NULL) - term_start > ACC_TERMINATE_GRACE_SEC)
                acc_signal_worker_processes(ctx, SIGKILL); /* 超窗强杀 */
            else
                acc_signal_worker_processes(ctx, SIGTERM); /* 幂等重发 */
            continue;
        }

        if (acc_quit) {
            acc_signal_worker_processes(ctx, SIGQUIT);
            /* master 侧关闭监听 fd（quit 期间不再 respawn，无继承需求） */
            for (acc_listening_t *ls = ctx->listenlist; ls != NULL;
                 ls = ls->next) {
                if (ls->fd != -1) {
                    close(ls->fd);
                    ls->fd = -1;
                }
            }
            continue;
        }

        if (acc_reconfigure) {
            acc_reconfigure = 0;
            ACC_LOGI("收到 SIGHUP：重新加载配置（非目标，忽略）");
        }
    }
}

/* 周期唤醒定时器：terminate/quit 复查进度与重发命令（just_spawn 首轮
 * 被跳过的槽位靠它下轮补发），respawn 退避到期重试；无事则停表 */
static void acc_master_arm_timer(void)
{
    struct itimerval itv;
    memset(&itv, 0, sizeof(itv));

    if (acc_terminate || acc_quit) {
        itv.it_interval.tv_sec = 1;
        itv.it_value.tv_sec = 1;
    } else if (g_pending_respawn) {
        itv.it_interval.tv_usec = 50 * 1000; /* 退避粒度 50ms */
        itv.it_value.tv_usec = 50 * 1000;
    }
    if (setitimer(ITIMER_REAL, &itv, NULL) == -1)
        ACC_LOGE("setitimer 失败, errno=%d", errno);
}

static int64_t acc_monotonic_ms(void)
{
    struct timespec ts;
    if (clock_gettime(CLOCK_MONOTONIC, &ts) == -1)
        return 0;
    return (int64_t)ts.tv_sec * 1000 + ts.tv_nsec / 1000000;
}

/* 启动 n 个 worker：spawn 后向既有 worker 广播新通道的 master 端 fd */
static void acc_start_worker_processes(acc_context_t *ctx, int n)
{
    for (int i = 0; i < n; i++) {
        if (acc_spawn_process(ctx, acc_worker_process_cycle,
                              (void *)(intptr_t)i, "worker process",
                              ACC_PROCESS_RESPAWN) == ACC_INVALID_PID) {
            ACC_LOGE("启动 worker %d 失败", i);
            continue;
        }

        acc_channel_t ch;
        memset(&ch, 0, sizeof(ch));
        ch.command = ACC_CMD_OPEN_CHANNEL;
        ch.pid = acc_processes[acc_process_slot].pid;
        ch.slot = acc_process_slot;
        ch.fd = acc_processes[acc_process_slot].channel[0];
        acc_broadcast_channel_cmd(ctx, &ch, acc_process_slot);
    }
}

/* 向全部存活进程广播通道命令（skip_slot 通常为新 spawn 的槽位自身） */
static void acc_broadcast_channel_cmd(acc_context_t *ch_ctx,
                                      acc_channel_t *ch, int skip_slot)
{
    (void)ch_ctx; /* 通道 fd 全在进程表内，无需上下文 */

    for (int i = 0; i < acc_last_process; i++) {
        if (i == skip_slot || acc_processes[i].pid == ACC_INVALID_PID ||
            acc_processes[i].channel[0] == -1)
            continue;
        if (acc_write_channel(acc_processes[i].channel[0], ch, ch->fd) != 0)
            ACC_LOGW("广播通道命令失败, slot=%d command=%u", i, ch->command);
    }
}

/* 向全部 worker 下发退出指令：SIGQUIT/SIGTERM 走通道命令（写失败以
 * kill 信号兜底），SIGKILL 直接 kill */
static void acc_signal_worker_processes(acc_context_t *ctx, int signo)
{
    (void)ctx;

    acc_channel_t ch;
    memset(&ch, 0, sizeof(ch));
    ch.fd = -1;
    ch.command = (signo == SIGQUIT) ? ACC_CMD_QUIT
                 : (signo == SIGTERM) ? ACC_CMD_TERMINATE : 0;

    for (int i = 0; i < acc_last_process; i++) {
        if (acc_processes[i].pid == ACC_INVALID_PID)
            continue;
        if (acc_processes[i].just_spawn) {
            acc_processes[i].just_spawn = 0;
            continue;
        }
        if (acc_processes[i].exiting)
            continue; /* 已下发过：等其自然退出 */

        if (ch.command != 0) {
            if (acc_write_channel(acc_processes[i].channel[0], &ch, -1) == 0) {
                acc_processes[i].exiting = 1;
            } else if (kill(acc_processes[i].pid, signo) == 0) {
                acc_processes[i].exiting = 1; /* 通道异常：信号兜底 */
            } else if (errno == ESRCH) {
                acc_processes[i].exited = 1;
            }
        } else { /* SIGKILL */
            if (kill(acc_processes[i].pid, SIGKILL) == -1 && errno == ESRCH) {
                acc_processes[i].exited = 1;
                acc_processes[i].exiting = 0;
            } else {
                acc_processes[i].exiting = 1;
            }
        }
    }
}

/* 回收僵尸并按需重生（带指数退避）：返回是否仍有存活进程。
 * 死亡槽位统一转为空槽（pid=INVALID）+ respawn 位标记待重生，
 * 退避未到则登记 g_pending_respawn 由周期唤醒定时器下轮重试 */
static int acc_reap_children(acc_context_t *ctx)
{
    int live = 0;
    int64_t now = acc_monotonic_ms();

    g_pending_respawn = 0; /* 每轮重置：下面按实际待重生槽位刷新 */

    /* 先收割全部僵尸：退出码 2 为 worker init 致命错误，不再重生 */
    for ( ;; ) {
        int status;
        pid_t pid = waitpid(-1, &status, WNOHANG);
        if (pid == 0)
            break; /* 无更多僵尸 */
        if (pid == -1) {
            if (errno == EINTR)
                continue;
            break; /* ECHILD 等 */
        }

        for (int i = 0; i < acc_last_process; i++) {
            if (acc_processes[i].pid != pid)
                continue;
            acc_processes[i].exited = 1;
            if (WIFSIGNALED(status)) {
                ACC_LOGW("%s pid=%d 被信号 %d 终止", acc_processes[i].name,
                         (int)pid, WTERMSIG(status));
            } else {
                ACC_LOGI("%s pid=%d 退出, code=%d", acc_processes[i].name,
                         (int)pid, WEXITSTATUS(status));
                if (WEXITSTATUS(status) == 2)
                    acc_processes[i].respawn = 0; /* init 失败：重启无意义 */
            }
            break;
        }
    }

    for (int i = 0; i < acc_last_process; i++) {
        acc_process_t *p = &acc_processes[i];

        if (p->exited && p->pid != ACC_INVALID_PID) {
            /* 一次性收尾（通道两端任一未关才算未收尾，防重复广播）：
             * 关通道两端并通知其余 worker 释放该槽位 master 端 fd 副本 */
            if (p->channel[0] != -1 || p->channel[1] != -1) {
                if (p->channel[0] != -1) {
                    close(p->channel[0]);
                    p->channel[0] = -1;
                }
                if (p->channel[1] != -1) {
                    close(p->channel[1]);
                    p->channel[1] = -1;
                }
                acc_channel_t ch;
                memset(&ch, 0, sizeof(ch));
                ch.command = ACC_CMD_CLOSE_CHANNEL;
                ch.pid = p->pid;
                ch.slot = i;
                ch.fd = -1;
                acc_broadcast_channel_cmd(ctx, &ch, i);
            }

            /* 意外死亡（非命令退出且 master 未在退出流程）→ 登记退避待重生；
             * 否则清除 respawn 位，槽位彻底废弃 */
            if (p->respawn && !p->exiting && !acc_terminate && !acc_quit)
                p->respawn_at_ms = now + p->respawn_delay_ms;
            else
                p->respawn = 0;
            p->pid = ACC_INVALID_PID;
            p->exited = 0;
            p->exiting = 0;
        }

        if (p->pid != ACC_INVALID_PID) {
            live = 1; /* 正常运行或已下发退出命令尚未退 */
            continue;
        }

        if (!p->respawn)
            continue; /* 空槽或已废弃 */

        /* 退避未到：延后重生（下轮 sigsuspend/setitimer 唤醒再试） */
        if (now < p->respawn_at_ms) {
            g_pending_respawn = 1;
            continue;
        }

        if (acc_spawn_process(ctx, p->proc, p->data, p->name, i) ==
            ACC_INVALID_PID) {
            ACC_LOGE("重生 worker(slot=%d) 失败, %dms 后重试", i,
                     p->respawn_delay_ms);
            p->respawn_at_ms = now + p->respawn_delay_ms; /* 失败同样退避 */
            g_pending_respawn = 1;
            continue;
        }

        /* 重生成功：登记下一次退避间隔（倍增至封顶） */
        p->respawn_at_ms = now + p->respawn_delay_ms;
        p->respawn_delay_ms *= 2;
        if (p->respawn_delay_ms > ACC_RESPAWN_DELAY_MAX_MS)
            p->respawn_delay_ms = ACC_RESPAWN_DELAY_MAX_MS;

        acc_channel_t och;
        memset(&och, 0, sizeof(och));
        och.command = ACC_CMD_OPEN_CHANNEL;
        och.pid = p->pid;
        och.slot = i;
        och.fd = p->channel[0];
        acc_broadcast_channel_cmd(ctx, &och, i);
        live = 1;
    }
    return live;
}

static void acc_master_process_exit(acc_context_t *ctx)
{
    struct itimerval itv = {{0, 0}, {0, 0}};
    setitimer(ITIMER_REAL, &itv, NULL); /* 停周期唤醒定时器 */

    acc_close_listening_sockets(ctx);
    if (ctx->ssl_ctx != NULL) {
        SSL_CTX_free(ctx->ssl_ctx);
        ctx->ssl_ctx = NULL;
    }
    if (ctx->lockfd != -1) { /* close 同时隐式释放 accept 互斥记录锁 */
        close(ctx->lockfd);
        ctx->lockfd = -1;
    }
    ACC_LOGI("master 退出, pid=%d", (int)getpid());
    acc_log_close();
    exit(0);
}

/* worker 主循环 -------------------------------------------------------------*/

void acc_worker_process_cycle(acc_context_t *ctx, void *data)
{
    int worker = (int)(intptr_t)data;

    acc_worker_process_init(ctx, worker);

    for ( ;; ) {
        acc_process_events_and_timers(ctx);
        if (acc_worker_quit)
            break; /* 通道命令（QUIT/TERMINATE）或信号触发 */
    }
    acc_worker_process_exit(ctx);
}

static void acc_worker_process_init(acc_context_t *ctx, int worker)
{
    /* 解除 master 阻塞的信号：worker 依赖信号打断 epoll_wait 触发退出 */
    sigset_t set;
    sigemptyset(&set);
    if (sigprocmask(SIG_SETMASK, &set, NULL) == -1)
        ACC_LOGE("worker 解除信号阻塞失败, errno=%d", errno);

    /* fd 数上限：只收紧软限且不超过硬限 */
    if (ctx->conf.max_open_fd > 0) {
        struct rlimit rl;
        if (getrlimit(RLIMIT_NOFILE, &rl) == 0) {
            rlim_t want = (rlim_t)ctx->conf.max_open_fd;
            if (rl.rlim_max != RLIM_INFINITY && want > rl.rlim_max)
                want = rl.rlim_max;
            rl.rlim_cur = want;
            if (setrlimit(RLIMIT_NOFILE, &rl) != 0)
                ACC_LOGW("setrlimit(RLIMIT_NOFILE=%lu) 失败, errno=%d",
                         (unsigned long)want, errno);
        }
    }

    acc_time_init();
    acc_time_update();

    /* 日志重定向：与 master 各持独立句柄，避免跨小时滚动后写旧文件 */
    acc_log_close();
    if (acc_log_init(ctx->conf.log_path, ctx->conf.log_level) != 0)
        ACC_LOGW("worker 日志初始化失败: %s（转 stderr）",
                 ctx->conf.log_path);

    /* 指标导出：每 worker 独立 acc-<pid>.prom；目录为空串禁用；
     * 目录创建失败内部已降级（WARN + 禁用），不阻断 worker 启动 */
    acc_metrics_init(ctx->conf.metrics_dir);

    acc_quit = 0;
    acc_terminate = 0;
    acc_reconfigure = 0;
    acc_reap = 0;
    acc_worker_quit = 0;
    acc_use_accept_mutex = (ctx->conf.accept_mutex == 1);
    acc_accept_mutex_held = 0;
    acc_accept_disabled = 0;

    if (acc_epoll_init() != 0) {
        ACC_LOGE("worker epoll 初始化失败");
        exit(2);
    }

    ctx->connections_n = ctx->conf.connections_per_worker;
    if (acc_create_connections_pool(ctx) != 0) {
        ACC_LOGE("worker 连接池创建失败");
        exit(2);
    }

    if (acc_thread_pool_init(&ctx->thread_pool,
                             ctx->conf.thread_pool_thread_num,
                             ctx->conf.thread_pool_queue_size) != 0) {
        ACC_LOGE("worker 线程池创建失败");
        exit(2);
    }

    ctx->sub_map = acc_sub_map_create(ACC_SUB_MAP_BUCKETS);
    if (ctx->sub_map == NULL) {
        ACC_LOGE("订阅路由表创建失败");
        exit(2);
    }

    /* 应用层接线：连接关闭回调 + 上下文注入（session 经此取路由表/线程池） */
    ctx->on_conn_close = acc_mqtt_on_close;
    acc_mqtt_set_context(ctx);

    /* SSL 说明：master 阶段证书加载失败已降级（跳过 TLS 监听、保持明文），
     * worker 继承同一份 ctx->ssl_ctx——此处不再重试：证书文件在 master
     * 生命周期内不会自愈，重复加载只会刷屏；TLS 监听对象本就不存在 */

    /* 监听读事件：连接级 handler（ls->handler = acc_mqtt_init_connection）
     * 由 listening 层填好，经 acc_event_accept 收口调用 */
    g_listen_events_n = 0;
    for (acc_listening_t *ls = ctx->listenlist; ls != NULL; ls = ls->next) {
        if (g_listen_events_n >= ACC_MAX_LISTEN_EVENTS) {
            ACC_LOGE("监听 socket 数超过事件数组容量");
            exit(2);
        }
        acc_event_t *ev = &g_listen_events[g_listen_events_n++];
        memset(ev, 0, sizeof(*ev));
        ev->fd = ls->fd;
        ev->data = ls; /* acc_event_accept 经 data 取监听对象 */
        ev->handler = acc_event_accept;
        ev->accept = 1; /* accept 事件：互斥持锁窗口内优先派发 */
        acc_list_init(&ev->queue);
        if (acc_use_accept_mutex)
            continue; /* 互斥启用：延后到抢到锁的 enable 路径注册 */
        if (acc_epoll_add_event(ev, ACC_EVENT_READ) != 0) {
            ACC_LOGE("监听读事件注册失败, fd=%d", ls->fd);
            exit(2);
        }
    }

    /* master 命令通道读事件 */
    memset(&g_channel_event, 0, sizeof(g_channel_event));
    g_channel_event.fd = acc_channel;
    g_channel_event.handler = acc_channel_handler;
    acc_list_init(&g_channel_event.queue);
    if (acc_epoll_add_event(&g_channel_event, ACC_EVENT_READ) != 0) {
        ACC_LOGE("通道读事件注册失败, fd=%d", acc_channel);
        exit(2);
    }

    ACC_LOGI("worker %d 初始化完成, pid=%d", worker, (int)getpid());
}

void acc_process_events_and_timers(acc_context_t *ctx)
{
    uint32_t flags = 0;

    acc_time_update(); /* 日志/超时清扫共用的时间缓存 */

    if (acc_use_accept_mutex) {
        if (acc_accept_disabled > 0) {
            acc_accept_disabled--; /* 负载超标冷却：递减恢复 */
            /* 冷却期不再竞争：注销监听事件防电平触发空转唤醒
             * （事件未摘除时 listen fd 常备就绪，epoll_wait 会立即返回） */
            acc_disable_accept_events(ctx);
        } else {
            /* 锁文件异常也只影响 accept：不得跳过事件循环，
             * 否则通道命令（QUIT）收不到，worker 失去退出途径 */
            acc_trylock_accept_mutex(ctx);
        }
        if (acc_accept_mutex_held)
            flags |= ACC_POST_EVENTS; /* 持锁窗口内事件先入队延后派发 */
    }

    acc_epoll_process_events(ACC_CYCLE_TIMER_MS, flags);

    /* 时序（对齐 raw）：持锁窗口内先派发 accept 事件——posted 队列中
     * 仅摘取 accept 位事件（acc_event_accept 依赖 !held 守卫，锁外派发
     * 会被守卫跳过导致永不 accept），其余事件留在队列 */
    if (flags & ACC_POST_EVENTS)
        acc_event_process_posted_accept();

    /* 放锁清 held：无论本轮是否处理过 accept 都不能让锁跨轮滞留 */
    if (acc_accept_mutex_held) {
        acc_file_mutex_t m = { ctx->lockfd };
        acc_file_mutex_unlock(&m);
        acc_accept_mutex_held = 0;
    }

    /* 普通事件锁外统一处理：通道命令/连接读写不与 accept 争锁，
     * 保持 worker 间并发 */
    acc_event_process_posted(&acc_posted_events);

    /* 超时清扫：全量扫描（活跃时间戳刷新不重排 inused 链） */
    acc_traversal_expired_connection(ctx);

    /* 指标：每轮刷新连接 gauge，write 内部按 5s 节流快照写盘（禁用时 no-op） */
    acc_metrics_gauge("acc_connections",
                      ctx->connections_n - ctx->free_connections_n);
    acc_metrics_gauge("acc_free_connections", ctx->free_connections_n);
    acc_metrics_write();
}

static int acc_trylock_accept_mutex(acc_context_t *ctx)
{
    acc_file_mutex_t m = { ctx->lockfd };

    int rc = acc_file_mutex_trylock(&m);
    if (rc == 1) {
        /* 其他 worker 持有：注销本侧监听事件，避免未持锁的空唤醒
         * （handler 侧另有 !held 防护兜底） */
        acc_disable_accept_events(ctx);
        return 0;
    }
    if (rc != 0)
        return -1; /* fcntl 错误 */

    if (acc_accept_mutex_held)
        return 0; /* 本 worker 已持有（fcntl 记录锁对同进程幂等） */

    if (acc_enable_accept_events(ctx) != 0) {
        acc_file_mutex_unlock(&m);
        return -1;
    }
    acc_accept_mutex_held = 1;
    return 0;
}

static int acc_enable_accept_events(acc_context_t *ctx)
{
    (void)ctx; /* 事件对象在本模块静态数组内 */

    for (int i = 0; i < g_listen_events_n; i++) {
        if (g_listen_events[i].active)
            continue; /* 幂等：上轮持锁注册后未失锁注销，跳过防 EEXIST */
        if (acc_epoll_add_event(&g_listen_events[i], ACC_EVENT_READ) != 0)
            return -1;
    }
    return 0;
}

static int acc_disable_accept_events(acc_context_t *ctx)
{
    (void)ctx;

    for (int i = 0; i < g_listen_events_n; i++) {
        if (g_listen_events[i].active &&
            acc_epoll_del_event(&g_listen_events[i], ACC_EVENT_READ) != 0)
            return -1;
    }
    return 0;
}

static void acc_worker_process_exit(acc_context_t *ctx)
{
    ACC_LOGI("worker 退出清理开始, pid=%d", (int)getpid());

    /* 1. 线程池排空（带超时）并销毁：转发任务引用的连接仍存活 */
    acc_thread_pool_drain(&ctx->thread_pool, ACC_POOL_DRAIN_MS);
    acc_thread_pool_destroy(&ctx->thread_pool);

    /* 2. 排空 posted 队列（acc_epoll_done 的前置契约） */
    acc_event_process_posted(&acc_posted_events);

    /* 3. 摘除通道读事件并关闭通道 */
    if (g_channel_event.active)
        acc_epoll_del_event(&g_channel_event, ACC_EVENT_READ);
    if (acc_channel != -1) {
        close(acc_channel);
        acc_channel = -1;
    }

    /* 4. 摘除监听事件、关监听链与全部活跃连接 */
    for (int i = 0; i < g_listen_events_n; i++) {
        if (g_listen_events[i].active)
            acc_epoll_del_event(&g_listen_events[i], ACC_EVENT_READ);
    }
    acc_close_listening_sockets(ctx);
    for (int i = 0; i < ctx->connections_n; i++) {
        if (ctx->connections[i].fd != -1)
            acc_close_connection(ctx, &ctx->connections[i]);
    }

    /* 5. 销毁 epoll 实例 */
    acc_epoll_done();

    /* 6. 释放连接池与订阅路由表（含线程池已无任务后的静态自洽清理） */
    free(ctx->connections);
    free(ctx->read_events);
    free(ctx->write_events);
    ctx->connections = NULL;
    ctx->read_events = NULL;
    ctx->write_events = NULL;
    acc_sub_map_destroy(ctx->sub_map);
    ctx->sub_map = NULL;

    if (ctx->ssl_ctx != NULL) {
        SSL_CTX_free(ctx->ssl_ctx);
        ctx->ssl_ctx = NULL;
    }

    acc_log_close();
    exit(0);
}
