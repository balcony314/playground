#include "acc_mutex.h"

#include <errno.h>
#include <fcntl.h>
#include <string.h>
#include <unistd.h>

/* 对整个文件设置/释放记录锁：type 为 F_WRLCK 或 F_UNLCK */
static int file_lock(int fd, short type)
{
    struct flock fl;
    memset(&fl, 0, sizeof(fl));
    fl.l_type = type;
    fl.l_whence = SEEK_SET;
    fl.l_start = 0;
    fl.l_len = 0; /* 0 = 从起点到文件末尾（含后续增长） */
    return fcntl(fd, F_SETLK, &fl);
}

int acc_file_mutex_create(acc_file_mutex_t *m, const char *path)
{
    if (!m || !path)
        return -1;
    m->fd = open(path, O_RDWR | O_CREAT, 0644);
    return m->fd >= 0 ? 0 : -1;
}

int acc_file_mutex_trylock(acc_file_mutex_t *m)
{
    if (!m || m->fd < 0)
        return -1;
    if (file_lock(m->fd, F_WRLCK) == 0)
        return 0;
    /* 记录锁在被占用时 errno 为 EACCES 或 EAGAIN */
    return (errno == EACCES || errno == EAGAIN) ? 1 : -1;
}

int acc_file_mutex_unlock(acc_file_mutex_t *m)
{
    if (!m || m->fd < 0)
        return -1;
    return file_lock(m->fd, F_UNLCK) == 0 ? 0 : -1;
}

void acc_file_mutex_destroy(acc_file_mutex_t *m)
{
    if (!m)
        return;
    if (m->fd >= 0)
        close(m->fd);
    m->fd = -1;
}
