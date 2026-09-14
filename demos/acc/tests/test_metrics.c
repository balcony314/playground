/* acc_metrics 单测：临时目录写 .prom 文件断言内容、节流跳过、禁用 no-op */
#include "acc_test.h"
#include "acc_metrics.h"

#include <stdlib.h>
#include <sys/stat.h>
#include <sys/types.h>
#include <unistd.h>

/* 拼出当前进程的指标文件路径 */
static void metrics_file(char *buf, size_t cap, const char *dir)
{
    int w = snprintf(buf, cap, "%s/acc-%d.prom", dir, (int)getpid());
    if (w < 0 || (size_t)w >= cap)
        buf[0] = '\0'; /* 截断防御：后续 access/read 会失败并被断言捕获 */
}

/* 读整个小文件到 buf（含 '\0'），失败返回 NULL */
static char *read_file(const char *path, char *buf, size_t cap)
{
    FILE *fp = fopen(path, "r");
    if (fp == NULL)
        return NULL;
    size_t n = fread(buf, 1, cap - 1, fp);
    fclose(fp);
    buf[n] = '\0';
    return buf;
}

/* 在 buf 前缀里检查子串存在 */
static int has_str(const char *buf, const char *needle)
{
    return strstr(buf, needle) != NULL;
}

/* 基本读写：gauge 覆盖、counter 累加、.prom 文本含对应行 */
ACC_TEST(test_metrics_basic)
{
    char dir[] = "/tmp/acc_metrics_test_XXXXXX";
    char path[512], buf[4096];

    ACC_ASSERT(mkdtemp(dir) != NULL);
    acc_metrics_init(dir);

    acc_metrics_gauge("acc_test_gauge", 42);
    acc_metrics_count("acc_test_count", 3);
    acc_metrics_count("acc_test_count", 4);
    acc_metrics_write();

    metrics_file(path, sizeof(path), dir);
    ACC_ASSERT(read_file(path, buf, sizeof(buf)) != NULL);
    ACC_ASSERT(has_str(buf, "acc_test_gauge 42"));
    ACC_ASSERT(has_str(buf, "acc_test_count 7"));
    ACC_ASSERT(has_str(buf, "# TYPE acc_test_gauge gauge"));
    ACC_ASSERT(has_str(buf, "# TYPE acc_test_count counter"));

    unlink(path);
    rmdir(dir);
}

/* gauge 同名二次设置应覆盖旧值 */
ACC_TEST(test_metrics_gauge_overwrite)
{
    char dir[] = "/tmp/acc_metrics_test_XXXXXX";
    char path[512], buf[4096];

    ACC_ASSERT(mkdtemp(dir) != NULL);
    acc_metrics_init(dir);

    acc_metrics_gauge("acc_test_ow", 1);
    acc_metrics_gauge("acc_test_ow", 99);
    acc_metrics_write();

    metrics_file(path, sizeof(path), dir);
    ACC_ASSERT(read_file(path, buf, sizeof(buf)) != NULL);
    ACC_ASSERT(has_str(buf, "acc_test_ow 99"));
    ACC_ASSERT(!has_str(buf, "acc_test_ow 1\n"));

    unlink(path);
    rmdir(dir);
}

/* 节流：首次写盘后 5s 内第二次 write 不更新文件内容 */
ACC_TEST(test_metrics_throttle)
{
    char dir[] = "/tmp/acc_metrics_test_XXXXXX";
    char path[512], buf[4096];

    ACC_ASSERT(mkdtemp(dir) != NULL);
    acc_metrics_init(dir);

    acc_metrics_gauge("acc_test_th", 1);
    acc_metrics_write();
    metrics_file(path, sizeof(path), dir);
    ACC_ASSERT(read_file(path, buf, sizeof(buf)) != NULL);
    ACC_ASSERT(has_str(buf, "acc_test_th 1"));

    /* 改值后立即再写：距上次 <5s 应被节流跳过，文件保持旧值 */
    acc_metrics_gauge("acc_test_th", 2);
    acc_metrics_write();
    ACC_ASSERT(read_file(path, buf, sizeof(buf)) != NULL);
    ACC_ASSERT(has_str(buf, "acc_test_th 1"));
    ACC_ASSERT(!has_str(buf, "acc_test_th 2"));

    unlink(path);
    rmdir(dir);
}

/* 空串禁用：gauge/count/write 全部 no-op，不产生任何文件 */
ACC_TEST(test_metrics_disabled)
{
    char dir[] = "/tmp/acc_metrics_test_XXXXXX";
    char path[512];

    ACC_ASSERT(mkdtemp(dir) != NULL);
    acc_metrics_init(""); /* 先启用再切换禁用，覆盖重置路径 */
    acc_metrics_gauge("acc_test_dis", 1);
    acc_metrics_count("acc_test_dis", 1);
    acc_metrics_write();

    metrics_file(path, sizeof(path), dir);
    ACC_ASSERT(access(path, F_OK) != 0); /* 禁用：不写盘 */

    rmdir(dir);
}

/* init 自动逐级创建不存在的目录 */
ACC_TEST(test_metrics_mkdir)
{
    char dir[] = "/tmp/acc_metrics_test_XXXXXX";
    char deep[512], path[512], buf[4096];

    ACC_ASSERT(mkdtemp(dir) != NULL);
    snprintf(deep, sizeof(deep), "%s/a/b", dir);
    acc_metrics_init(deep);

    acc_metrics_gauge("acc_test_mkdir", 7);
    acc_metrics_write();

    metrics_file(path, sizeof(path), deep);
    ACC_ASSERT(read_file(path, buf, sizeof(buf)) != NULL);
    ACC_ASSERT(has_str(buf, "acc_test_mkdir 7"));

    unlink(path);
    rmdir(deep);
    snprintf(deep, sizeof(deep), "%s/a", dir);
    rmdir(deep);
    rmdir(dir);
}

ACC_TEST_MAIN()
