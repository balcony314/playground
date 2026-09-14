#ifndef ACC_DAEMON_H
#define ACC_DAEMON_H

/* 守护进程化：fork 脱离前台 + setsid 建会话 + umask(0) + chdir("/") +
 * 标准流重定向 /dev/null。成功返回 0，失败 -1（原进程退出，仅子进程返回）。
 * 注意：chdir("/") 后相对路径失效——调用前须先把日志目录等绝对化 */
int acc_daemon(void);

#endif /* ACC_DAEMON_H */
