/* main 组装：参数解析 → 配置加载 → 日志（路径绝对化）→ accept 互斥锁
 * → 服务初始化（SSL_CTX 降级 + 监听 socket）→ 信号表 → 守护进程化（可选）
 * → master 主循环（不返回） */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include "core/acc_core.h"
#include "core/acc_daemon.h"
#include "core/acc_process.h"
#include "core/acc_process_cycle.h"
#include "util/acc_conf.h"
#include "util/acc_log.h"
#include "util/acc_mutex.h"

static void usage(const char *prog)
{
    fprintf(stderr, "Usage: %s -c <config file>\n", prog);
    fprintf(stderr, "  -c  配置文件路径（必填）\n");
    fprintf(stderr, "  -h  打印本帮助\n");
}

int main(int argc, char **argv)
{
    const char *conffile = NULL;
    int opt;

    opterr = 0;
    while ((opt = getopt(argc, argv, "c:h")) != -1) {
        switch (opt) {
        case 'c':
            conffile = optarg;
            break;
        case 'h':
            usage(argv[0]);
            return 0;
        default:
            usage(argv[0]);
            return 1;
        }
    }
    if (conffile == NULL) {
        usage(argv[0]);
        return 1;
    }

    acc_context_t ctx;
    memset(&ctx, 0, sizeof(ctx));
    ctx.lockfd = -1;

    if (acc_conf_load(&ctx.conf, conffile) != 0) {
        fprintf(stderr, "配置加载失败: %s\n", conffile);
        usage(argv[0]);
        return 1;
    }
    snprintf(ctx.conffile, sizeof(ctx.conffile), "%s", conffile);

    /* 守护进程化会 chdir("/")：日志与指标目录先行绝对化，
     * 否则 master/worker 其后的相对路径全部失效 */
    if (acc_path_to_absolute(ctx.conf.log_path,
                             sizeof(ctx.conf.log_path)) != 0)
        fprintf(stderr, "警告：日志目录绝对化失败, 继续用原路径: %s\n",
                ctx.conf.log_path);
    if (acc_path_to_absolute(ctx.conf.metrics_dir,
                             sizeof(ctx.conf.metrics_dir)) != 0)
        fprintf(stderr, "警告：指标目录绝对化失败, 继续用原路径: %s\n",
                ctx.conf.metrics_dir);

    if (acc_log_init(ctx.conf.log_path, ctx.conf.log_level) != 0) {
        fprintf(stderr, "日志初始化失败: %s\n", ctx.conf.log_path);
        return 1;
    }

    /* accept 互斥锁文件：与配置文件同目录 acc.lock（open O_CREAT） */
    if (ctx.conf.accept_mutex) {
        char lockpath[ACC_CONF_PATH_MAX + 32];
        const char *slash = strrchr(conffile, '/');
        if (slash != NULL)
            snprintf(lockpath, sizeof(lockpath), "%.*s/acc.lock",
                     (int)(slash - conffile), conffile);
        else
            snprintf(lockpath, sizeof(lockpath), "acc.lock");

        acc_file_mutex_t m;
        if (acc_file_mutex_create(&m, lockpath) != 0) {
            ACC_LOGE("创建 accept 互斥锁文件失败: %s", lockpath);
            acc_log_close();
            return 1;
        }
        ctx.lockfd = m.fd;
    }

    if (acc_server_init(&ctx) != 0) {
        ACC_LOGE("服务初始化失败（监听 socket）");
        acc_server_shutdown(&ctx);
        acc_log_close();
        return 1;
    }

    if (acc_init_signals() != 0) {
        ACC_LOGE("信号初始化失败");
        acc_server_shutdown(&ctx);
        acc_log_close();
        return 1;
    }

    if (ctx.conf.enable_daemon && acc_daemon() != 0) {
        ACC_LOGE("守护进程化失败");
        acc_server_shutdown(&ctx);
        acc_log_close();
        return 1;
    }

    acc_master_process_cycle(&ctx); /* 内部 exit，不返回 */
    return 0;
}
