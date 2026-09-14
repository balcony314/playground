#include "acc_test.h"
#include "acc_conf.h"

ACC_TEST(conf_defaults)
{
    acc_conf_t c;
    ACC_ASSERT_EQ(acc_conf_load(&c, "/nonexistent"), -1);
}

ACC_TEST(conf_parse_file)
{
    const char *path = "/tmp/acc_test.conf";
    FILE *f = fopen(path, "w");
    fputs("# comment\nlog_level 2\nenable_daemon yes\n"
          "tcp_port 2883\nclient_timeout 90s\nmax_read_buffer_size 4m\n"
          "[section ignored\nserver 0.0.0.0\n", f);
    fclose(f);

    acc_conf_t c;
    ACC_ASSERT_EQ(acc_conf_load(&c, path), 0);
    ACC_ASSERT_EQ(c.log_level, 2);
    ACC_ASSERT_EQ(c.enable_daemon, 1);
    ACC_ASSERT_EQ(c.tcp_port, 2883);
    ACC_ASSERT_EQ(c.client_timeout, 90);
    ACC_ASSERT_EQ(c.max_read_buffer_size, 4u * 1024 * 1024);
    ACC_ASSERT_STR_EQ(c.server, "0.0.0.0");
    ACC_ASSERT_EQ(c.worker_process, 2); /* 未设置的取默认 */
    unlink(path);
}

ACC_TEST(conf_parse_types)
{
    int i = 0; unsigned m = 0;
    ACC_ASSERT_EQ(acc_conf_parse_bool("yes", &i, 0), 0); ACC_ASSERT_EQ(i, 1);
    ACC_ASSERT_EQ(acc_conf_parse_bool("OFF", &i, 0), 0); ACC_ASSERT_EQ(i, 0);
    ACC_ASSERT_EQ(acc_conf_parse_memsize("2m", &m, 0), 0); ACC_ASSERT_EQ(m, 2u << 20);
    ACC_ASSERT_EQ(acc_conf_parse_time("1h30m", NULL, 0), -1); /* 复合不支持 */
    ACC_ASSERT_EQ(acc_conf_parse_time("5m", &i, 0), 0); ACC_ASSERT_EQ(i, 300);
}

ACC_TEST(conf_bad_line)
{
    const char *path = "/tmp/acc_test_bad.conf";
    FILE *f = fopen(path, "w");
    fputs("unknown_key 1\n", f);
    fclose(f);
    acc_conf_t c;
    ACC_ASSERT_EQ(acc_conf_load(&c, path), -1);
    unlink(path);
}

ACC_TEST_MAIN();
