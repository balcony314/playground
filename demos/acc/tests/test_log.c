#include "acc_test.h"
#include "acc_log.h"
#include <dirent.h>
#include <fcntl.h>
#include <unistd.h>

/* 读取 dir 下第一个 log_* 文件全部内容到 out，返回长度，失败返回 -1 */
static int read_first_log(const char *dir, char *out, size_t n)
{
    char path[512];
    snprintf(path, sizeof(path), "%s", dir);
    DIR *d = opendir(dir);
    if (d == NULL)
        return -1;
    struct dirent *e;
    while ((e = readdir(d)) != NULL) {
        if (strncmp(e->d_name, "log_", 4) != 0)
            continue;
        snprintf(path, sizeof(path), "%s/%s", dir, e->d_name);
        closedir(d);
        int fd = open(path, O_RDONLY);
        if (fd < 0)
            return -1;
        int len = (int)read(fd, out, n - 1);
        close(fd);
        out[len > 0 ? len : 0] = '\0';
        return len;
    }
    closedir(d);
    return -1;
}

ACC_TEST(log_write_to_file)
{
    const char *dir = "/tmp/acc_test_log_1";
    char buf[4096] = {0};
    ACC_ASSERT_EQ(acc_log_init(dir, ACC_LOG_DEBUG), 0);
    ACC_LOGI("hello %s", "acc");
    ACC_LOGE("err %d", 42);
    acc_log_close();

    ACC_ASSERT(read_first_log(dir, buf, sizeof(buf)) > 0);
    ACC_ASSERT(strstr(buf, "hello acc") != NULL);
    ACC_ASSERT(strstr(buf, "[ERROR]") != NULL);
    ACC_ASSERT(strstr(buf, "err 42") != NULL);
}

ACC_TEST(log_level_filter)
{
    const char *dir = "/tmp/acc_test_log_2";
    char buf[4096] = {0};
    ACC_ASSERT_EQ(acc_log_init(dir, ACC_LOG_WARN), 0);
    ACC_LOGD("should-not-appear");
    ACC_LOGW("should-appear");
    acc_log_close();
    ACC_ASSERT(read_first_log(dir, buf, sizeof(buf)) > 0);
    ACC_ASSERT(strstr(buf, "should-not-appear") == NULL);
    ACC_ASSERT(strstr(buf, "should-appear") != NULL);
}

ACC_TEST_MAIN();
