#include "acc_test.h"
#include "acc_mutex.h"

#include <sys/wait.h>
#include <unistd.h>

ACC_TEST(file_mutex_excludes_across_fork)
{
    const char *path = "/tmp/acc_test_flock";
    acc_file_mutex_t m;
    ACC_ASSERT_EQ(acc_file_mutex_create(&m, path), 0);
    ACC_ASSERT_EQ(acc_file_mutex_trylock(&m), 0);

    pid_t pid = fork();
    ACC_ASSERT(pid >= 0);
    if (pid == 0) {
        acc_file_mutex_t m2;
        if (acc_file_mutex_create(&m2, path) != 0) _exit(2);
        _exit(acc_file_mutex_trylock(&m2) == 0 ? 1 : 0); /* 应锁失败 */
    }
    int st = 0;
    waitpid(pid, &st, 0);
    ACC_ASSERT_EQ(WEXITSTATUS(st), 0);

    ACC_ASSERT_EQ(acc_file_mutex_unlock(&m), 0);
    acc_file_mutex_destroy(&m);
    unlink(path);
}

ACC_TEST_MAIN();
