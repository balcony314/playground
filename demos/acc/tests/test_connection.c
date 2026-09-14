#include "acc_test.h"
#include "acc_connection.h"
#include "acc_core.h"
#include "acc_epoll.h"
#include <sys/socket.h>
#include <string.h>
#include <unistd.h>

static acc_context_t ctx; /* 静态置零，手工填池 */

static void setup_ctx(void)
{
    memset(&ctx, 0, sizeof(ctx));
    ACC_ASSERT_EQ(acc_epoll_init(), 0); /* acc_conn_write 注册写事件需要 ep fd */
    ctx.connections_n = 4;
    acc_list_init(&ctx.inused_connection);
    ACC_ASSERT_EQ(acc_create_connections_pool(&ctx), 0);
    ACC_ASSERT_EQ(ctx.free_connections_n, 4);
}

ACC_TEST(conn_pool_get_free)
{
    setup_ctx();
    acc_connection_t *c = acc_get_connection(&ctx);
    ACC_ASSERT(c != NULL);
    ACC_ASSERT_EQ(ctx.free_connections_n, 3);
    ACC_ASSERT(!acc_list_empty(&ctx.inused_connection));
    ACC_ASSERT_EQ(c->con_id, 1);
    acc_free_connection(&ctx, c);
    ACC_ASSERT_EQ(ctx.free_connections_n, 4);
    ACC_ASSERT(acc_list_empty(&ctx.inused_connection));
}

ACC_TEST(conn_recv_send_bytes)
{
    setup_ctx();
    int sv[2];
    socketpair(AF_UNIX, SOCK_STREAM, 0, sv);
    acc_connection_t *c = acc_get_connection(&ctx);
    c->fd = sv[0];
    c->recv = acc_unix_recv;
    c->send = acc_conn_socket_send;

    (void)!write(sv[1], "ping", 4); /* (void)! 压制 glibc __wur（-O3 下 -Werror） */
    char b[8] = {0};
    ACC_ASSERT_EQ(c->recv(c, b, sizeof(b)), 4);
    ACC_ASSERT_STR_EQ(b, "ping");
    ACC_ASSERT_EQ(c->send(c, "pong", 4), 4);
    char rb[8] = {0};
    ACC_ASSERT_EQ(read(sv[1], rb, sizeof(rb)), 4);
    ACC_ASSERT_STR_EQ(rb, "pong");

    close(sv[1]);
    acc_close_connection(&ctx, c); /* fd=sv[0] 由 close 收走 */
    ACC_ASSERT_EQ(ctx.free_connections_n, 4);
}

ACC_TEST(conn_write_buffers_then_flush)
{
    setup_ctx();
    int sv[2];
    socketpair(AF_UNIX, SOCK_STREAM, 0, sv);
    acc_connection_t *c = acc_get_connection(&ctx);
    c->fd = sv[0];

    ACC_ASSERT_EQ(acc_conn_write(&ctx, c, "hello", 5), 0);
    ACC_ASSERT_EQ(acc_buf_len(c->write_buf), 5);
    ACC_ASSERT_EQ(acc_conn_flush(c), ACC_BUF_OK);
    char rb[8] = {0};
    ACC_ASSERT_EQ(read(sv[1], rb, sizeof(rb)), 5);
    ACC_ASSERT_STR_EQ(rb, "hello");
    close(sv[1]);
    acc_close_connection(&ctx, c);
}

ACC_TEST_MAIN();
