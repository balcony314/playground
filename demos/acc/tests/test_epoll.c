#include "acc_test.h"
#include "acc_epoll.h"
#include "acc_event.h"
#include <sys/socket.h>
#include <unistd.h>
#include <fcntl.h>

static int got_read_calls = 0;
static void on_read(acc_event_t *ev)
{
    got_read_calls++;
    char b[16];
    ssize_t n = read(ev->fd, b, sizeof(b)); /* 消费数据，避免水平触发重复就绪 */
    (void)n;
}

ACC_TEST(epoll_dispatches_read)
{
    ACC_ASSERT_EQ(acc_epoll_init(), 0);
    int sv[2];
    ACC_ASSERT_EQ(socketpair(AF_UNIX, SOCK_STREAM, 0, sv), 0);
    fcntl(sv[0], F_SETFL, O_NONBLOCK);

    int before = got_read_calls; /* 用例间共享计数器：相对断言 */
    acc_event_t ev = {0};
    ev.fd = sv[0];
    ev.handler = on_read;
    ACC_ASSERT_EQ(acc_epoll_add_event(&ev, ACC_EVENT_READ), 0);

    ACC_ASSERT_EQ(write(sv[1], "x", 1), 1);
    ACC_ASSERT_EQ(acc_epoll_process_events(1000, 0), 1); /* 返回就绪事件数 */
    ACC_ASSERT_EQ(got_read_calls, before + 1);

    ACC_ASSERT_EQ(acc_epoll_del_event(&ev, ACC_EVENT_READ), 0);
    acc_epoll_done();
    close(sv[0]); close(sv[1]);
}

ACC_TEST(epoll_post_events_deferred)
{
    /* 同上，flags=ACC_POST_EVENTS 时 handler 不立即调用，
       随后 acc_event_process_posted(&acc_posted_events) 才调用 */
    ACC_ASSERT_EQ(acc_epoll_init(), 0);
    int sv[2];
    socketpair(AF_UNIX, SOCK_STREAM, 0, sv);
    fcntl(sv[0], F_SETFL, O_NONBLOCK);

    static acc_event_t ev;
    ev.fd = sv[0];
    ev.handler = on_read;
    acc_epoll_add_event(&ev, ACC_EVENT_READ);

    int before = got_read_calls;
    ACC_ASSERT_EQ(write(sv[1], "y", 1), 1);
    ACC_ASSERT_EQ(acc_epoll_process_events(1000, ACC_POST_EVENTS), 1);
    ACC_ASSERT_EQ(got_read_calls, before); /* 未立即执行 */
    acc_event_process_posted(&acc_posted_events);
    ACC_ASSERT_EQ(got_read_calls, before + 1);

    acc_epoll_del_event(&ev, ACC_EVENT_READ);
    acc_epoll_done();
    close(sv[0]); close(sv[1]);
}

ACC_TEST_MAIN();
