#include "acc_test.h"
#include "acc_buffer.h"
#include <sys/socket.h>
#include <unistd.h>

ACC_TEST(buf_push_len_limit)
{
    buf_t *b = acc_buf_create(1024);
    ACC_ASSERT(b != NULL);
    ACC_ASSERT_EQ(acc_buf_push_data(b, "abc", 3), ACC_BUF_OK);
    ACC_ASSERT_EQ(acc_buf_len(b), 3);
    ACC_ASSERT_EQ(acc_buf_push_data(b, "x", 2000), ACC_BUF_LIMIT);
    ACC_ASSERT_EQ(acc_buf_len(b), 3); /* 失败不入队 */
    acc_buf_destroy(b);
}

ACC_TEST(buf_write_to_socketpair)
{
    int sv[2];
    ACC_ASSERT_EQ(socketpair(AF_UNIX, SOCK_STREAM, 0, sv), 0);
    buf_t *b = acc_buf_create(64 * 1024);
    char big[10000];
    memset(big, 'A', sizeof(big));
    ACC_ASSERT_EQ(acc_buf_push_data(b, big, sizeof(big)), ACC_BUF_OK);

    int rc = acc_buf_write_to_fd(b, sv[0]);
    if (rc == ACC_BUF_EAGAIN) /* socketpair 缓冲充足场景一般一次写完 */
        rc = acc_buf_write_to_fd(b, sv[0]);
    ACC_ASSERT_EQ(rc, ACC_BUF_OK);
    ACC_ASSERT_EQ(acc_buf_len(b), 0);

    char rb[sizeof(big)];
    ACC_ASSERT_EQ(read(sv[1], rb, sizeof(rb)), (int64_t)sizeof(big));
    ACC_ASSERT_EQ(memcmp(rb, big, sizeof(big)), 0);
    close(sv[0]); close(sv[1]);
    acc_buf_destroy(b);
}

ACC_TEST_MAIN();
