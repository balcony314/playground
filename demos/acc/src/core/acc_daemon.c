/* 守护进程化：对齐 raw 的 ads_daemon.c（保留 chdir("/")，路径绝对化
 * 责任归调用方，见 main 的 acc_path_to_absolute 预处理） */
#include "acc_daemon.h"

#include <fcntl.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

int acc_daemon(void)
{
    switch (fork()) {
    case -1:
        return -1;
    case 0:
        break; /* 子进程继续 */
    default:
        _exit(0); /* 前台原进程退出：调用 shell 立即拿回控制权 */
    }

    if (setsid() == -1)
        return -1;

    umask(0);
    if (chdir("/") == -1) /* 释放原工作目录占用：相对路径自此失效 */
        return -1;

    int fd = open("/dev/null", O_RDWR);
    if (fd == -1)
        return -1;
    if (dup2(fd, STDIN_FILENO) == -1 || dup2(fd, STDOUT_FILENO) == -1 ||
        dup2(fd, STDERR_FILENO) == -1)
        return -1;
    if (fd > STDERR_FILENO)
        close(fd);
    return 0;
}
