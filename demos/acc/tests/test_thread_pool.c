#include "acc_test.h"
#include "acc_thread_pool.h"

#include <pthread.h>
#include <sched.h>

static pthread_mutex_t cnt_mu = PTHREAD_MUTEX_INITIALIZER;
static int cnt = 0;

static void bump(void *arg)
{
    (void)arg;
    pthread_mutex_lock(&cnt_mu);
    cnt++;
    pthread_mutex_unlock(&cnt_mu);
}

ACC_TEST(thread_pool_executes_all)
{
    acc_thread_pool_t tp;
    ACC_ASSERT_EQ(acc_thread_pool_init(&tp, 4, 128), 0);
    for (int i = 0; i < 1000; i++)
        while (acc_thread_pool_post(&tp, bump, NULL) != 0)
            sched_yield(); /* 满队背压：post 语义为满即拒 -1，等消费后重试 */
    ACC_ASSERT_EQ(acc_thread_pool_drain(&tp, 5000), 0);
    ACC_ASSERT_EQ(cnt, 1000);
    acc_thread_pool_destroy(&tp);
}

ACC_TEST_MAIN();
