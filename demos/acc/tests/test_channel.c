#include "acc_test.h"
#include "acc_channel.h"
#include <sys/wait.h>
#include <sys/socket.h>
#include <unistd.h>
#include <fcntl.h>

ACC_TEST(channel_pass_command_and_fd)
{
    int sv[2];
    ACC_ASSERT_EQ(socketpair(AF_UNIX, SOCK_STREAM, 0, sv), 0);
    int payload_fd = open("/dev/null", O_RDONLY);

    pid_t pid = fork();
    ACC_ASSERT(pid >= 0);
    if (pid == 0) { /* 子：收 */
        close(sv[0]);
        fcntl(sv[1], F_SETFL, O_NONBLOCK);
        acc_channel_t ch;
        /* 轮询等待对端写入 */
        for (int i = 0; i < 100; i++) {
            int rc = acc_read_channel(sv[1], &ch);
            if (rc == 0) {
                _exit(ch.command == ACC_CMD_OPEN_CHANNEL &&
                      ch.slot == 3 && ch.fd >= 0 ? 0 : 1);
            }
            usleep(10000);
        }
        _exit(2);
    }
    close(sv[1]);
    acc_channel_t ch = { .command = ACC_CMD_OPEN_CHANNEL, .pid = getpid(),
                         .slot = 3, .fd = 0 };
    ACC_ASSERT_EQ(acc_write_channel(sv[0], &ch, payload_fd), 0);
    int st = 0;
    waitpid(pid, &st, 0);
    ACC_ASSERT_EQ(WEXITSTATUS(st), 0);
    close(sv[0]); close(payload_fd);
}

ACC_TEST_MAIN();
