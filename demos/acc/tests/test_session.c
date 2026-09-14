/* Task 12：MQTT 会话状态机单测。
 * socketpair 喂字节流直驱 request_handler，验证
 * CONNECT→CONNACK、SUBSCRIBE→SUBACK→线程池转发、PUBLISH qos1→PUBACK */
#include "acc_test.h"

#include <fcntl.h>
#include <stdint.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>

#include "MQTTPacket.h"
#include "acc_connection.h"
#include "acc_core.h"
#include "acc_epoll.h"
#include "acc_mqtt_session.h"

static acc_context_t ctx;

static void setup_ctx(void)
{
    memset(&ctx, 0, sizeof(ctx));
    ACC_ASSERT_EQ(acc_epoll_init(), 0); /* 写事件注册依赖 epoll 实例 */
    ctx.connections_n = 4;
    acc_list_init(&ctx.inused_connection);
    ACC_ASSERT_EQ(acc_create_connections_pool(&ctx), 0);
    ctx.sub_map = acc_sub_map_create(8);
    ACC_ASSERT(ctx.sub_map != NULL);
    ACC_ASSERT_EQ(acc_thread_pool_init(&ctx.thread_pool, 2, 64), 0);
    ctx.on_conn_close = acc_mqtt_on_close; /* 产品关连接路径的 session 清理 */
    acc_mqtt_set_context(&ctx);
}

/* 返回连接，*peer_fd 写入测试驱动端 fd。
 * 注意：ACC_ASSERT 宏内含裸 return，不可用于本非 void 辅助函数 */
static acc_connection_t *new_conn(int *peer_fd)
{
    int sv[2];
    if (socketpair(AF_UNIX, SOCK_STREAM, 0, sv) != 0) {
        fprintf(stderr, "socketpair 失败\n");
        exit(1);
    }
    acc_connection_t *c = acc_get_connection(&ctx);
    if (c == NULL) {
        fprintf(stderr, "连接池取连接失败\n");
        exit(1);
    }
    c->fd = sv[0];
    /* 连接端置非阻塞：对齐生产 accept4(SOCK_NONBLOCK) 语义——
     * request_handler 空转 recv 须以 EAGAIN 返回而非阻塞 */
    fcntl(c->fd, F_SETFL, O_NONBLOCK);
    *peer_fd = sv[1];
    acc_mqtt_init_connection(c);
    if (acc_epoll_add_connection(c) != 0) { /* handler 经 ev->data 取 c */
        fprintf(stderr, "连接事件注册失败\n");
        exit(1);
    }
    return c;
}

/* 非阻塞读走 peer_fd 全部内容 */
static int drain(int fd, unsigned char *out, int outcap)
{
    fcntl(fd, F_SETFL, O_NONBLOCK);
    int total = 0;
    for (;;) {
        int n = (int)read(fd, out + total, (size_t)(outcap - total));
        if (n <= 0)
            break;
        total += n;
    }
    return total;
}

/* 驱动连接读事件直到对端出现输出或超时(ms) */
static int pump_until_output(acc_connection_t *c, int peer_fd,
                             unsigned char *out, int outcap, int timeout_ms)
{
    for (int i = 0; i < timeout_ms / 10; i++) {
        acc_mqtt_request_handler(c->read);
        int n = drain(peer_fd, out, outcap);
        if (n > 0)
            return n;
        usleep(10000);
    }
    return 0;
}

static void teardown_conn(acc_connection_t *c, int peer_fd)
{
    acc_mqtt_on_close(c);
    acc_close_connection(&ctx, c);
    close(peer_fd);
}

ACC_TEST(connect_gets_connack)
{
    setup_ctx();
    int peer;
    acc_connection_t *c = new_conn(&peer);

    MQTTPacket_connectData opts = MQTTPacket_connectData_initializer;
    opts.clientID.cstring = (char *)"c1";
    unsigned char buf[256];
    int len = MQTTSerialize_connect(buf, sizeof(buf), &opts);
    ACC_ASSERT_EQ((int)write(peer, buf, (size_t)len), len);

    unsigned char out[512];
    int olen = pump_until_output(c, peer, out, sizeof(out), 2000);
    ACC_ASSERT(olen > 0);
    ACC_ASSERT_EQ(MQTTPacket_type(out, olen), CONNACK);
    teardown_conn(c, peer);
    acc_thread_pool_destroy(&ctx.thread_pool);
    acc_sub_map_destroy(ctx.sub_map);
}

ACC_TEST(subscribe_then_publish_forwarded)
{
    setup_ctx();
    int peer_a, peer_b;
    acc_connection_t *a = new_conn(&peer_a), *b = new_conn(&peer_b);
    unsigned char buf[512], out[1024];

    /* A: CONNECT */
    MQTTPacket_connectData opts = MQTTPacket_connectData_initializer;
    opts.clientID.cstring = (char *)"sub-client";
    int len = MQTTSerialize_connect(buf, sizeof(buf), &opts);
    ACC_ASSERT_EQ((int)write(peer_a, buf, (size_t)len), len);
    ACC_ASSERT(pump_until_output(a, peer_a, out, sizeof(out), 2000) > 0);

    /* A: SUBSCRIBE a/+ (qos1) */
    MQTTString filters[1] = {MQTTString_initializer};
    const char *fs[] = {"a/+"};
    int qos_req[1] = {1}, granted[1] = {0};
    filters[0].cstring = (char *)fs[0];
    len = MQTTSerialize_subscribe(buf, sizeof(buf), 0, 7, 1,
                                  filters, qos_req);
    ACC_ASSERT_EQ((int)write(peer_a, buf, (size_t)len), len);
    int olen = pump_until_output(a, peer_a, out, sizeof(out), 2000);
    ACC_ASSERT(olen > 0);
    ACC_ASSERT_EQ(MQTTPacket_type(out, olen), SUBACK);
    unsigned short pid; int count; int wl;
    ACC_ASSERT_EQ(MQTTDeserialize_suback(&pid, 1, &count, granted, out, olen,
                                         &wl), 1);
    ACC_ASSERT_EQ(granted[0], 1);

    /* B: CONNECT + PUBLISH a/b qos0 */
    opts.clientID.cstring = (char *)"pub-client";
    len = MQTTSerialize_connect(buf, sizeof(buf), &opts);
    (void)!write(peer_b, buf, (size_t)len); /* (void)! 压制 __wur */
    pump_until_output(b, peer_b, out, sizeof(out), 2000);
    const char *payload = "ping-xyz";
    MQTTString topic = MQTTString_initializer;
    topic.cstring = (char *)"a/b";
    len = MQTTSerialize_publish(buf, sizeof(buf), 0, 0, 0, 0, topic,
                                (unsigned char *)payload,
                                (unsigned int)strlen(payload));
    ACC_ASSERT_EQ((int)write(peer_b, buf, (size_t)len), len);
    pump_until_output(b, peer_b, out, sizeof(out), 2000); /* B 无输出 */

    /* A 收到转发 PUBLISH（经线程池，轮询至 2s） */
    olen = 0;
    for (int i = 0; i < 200 && olen == 0; i++) {
        usleep(10000);
        olen = drain(peer_a, out, sizeof(out));
    }
    ACC_ASSERT(olen > 0);
    ACC_ASSERT_EQ(MQTTPacket_type(out, olen), PUBLISH);
    unsigned char dup, retained, *pl;
    int qos, plen; unsigned short apid; MQTTString rt;
    int nl, wl2;
    ACC_ASSERT_EQ(MQTTDeserialize_publish(&dup, &qos, &retained, &apid, &rt,
                                          &pl, &plen, out, olen, &nl, &wl2), 1);
    ACC_ASSERT_EQ(rt.lenstring.len, 3);
    ACC_ASSERT_EQ(memcmp(rt.lenstring.data, "a/b", 3), 0);
    ACC_ASSERT_EQ(plen, (int)strlen(payload));
    ACC_ASSERT_EQ(memcmp(pl, payload, strlen(payload)), 0);

    teardown_conn(a, peer_a);
    teardown_conn(b, peer_b);
    acc_thread_pool_destroy(&ctx.thread_pool);
    acc_sub_map_destroy(ctx.sub_map);
}

ACC_TEST(publish_qos1_gets_puback)
{
    setup_ctx();
    int peer;
    acc_connection_t *c = new_conn(&peer);
    unsigned char buf[512], out[512];

    MQTTPacket_connectData opts = MQTTPacket_connectData_initializer;
    opts.clientID.cstring = (char *)"qos1-client";
    int len = MQTTSerialize_connect(buf, sizeof(buf), &opts);
    (void)!write(peer, buf, (size_t)len); /* (void)! 压制 __wur */
    pump_until_output(c, peer, out, sizeof(out), 2000);

    MQTTString topic = MQTTString_initializer;
    topic.cstring = (char *)"x/y";
    len = MQTTSerialize_publish(buf, sizeof(buf), 0, 1, 0, 99, topic,
                                (unsigned char *)"z", 1);
    ACC_ASSERT_EQ((int)write(peer, buf, (size_t)len), len);
    int olen = pump_until_output(c, peer, out, sizeof(out), 2000);
    ACC_ASSERT(olen > 0);
    ACC_ASSERT_EQ(MQTTPacket_type(out, olen), PUBACK);
    unsigned char dtype, duph; unsigned short pid;
    int nl, wl;
    ACC_ASSERT_EQ(MQTTDeserialize_ack(&dtype, &duph, &pid, out, olen,
                                      &nl, &wl), 1);
    ACC_ASSERT_EQ(pid, 99);

    teardown_conn(c, peer);
    acc_thread_pool_destroy(&ctx.thread_pool);
    acc_sub_map_destroy(ctx.sub_map);
}

/* A 订阅后，B 同一批写 PUBLISH(qos1)+DISCONNECT：mosquitto_pub 默认模式。
 * 断言 B 仍收到 PUBACK（close 前 flush），A 收到转发（发布者断开不丢
 * 已受理消息——回归审查修复） */
ACC_TEST(publish_then_disconnect_still_forwarded)
{
    setup_ctx();
    int peer_a, peer_b;
    acc_connection_t *a = new_conn(&peer_a), *b = new_conn(&peer_b);
    unsigned char buf[512], out[1024];

    /* A: CONNECT + SUBSCRIBE a/+ */
    MQTTPacket_connectData opts = MQTTPacket_connectData_initializer;
    opts.clientID.cstring = (char *)"hold-sub";
    int len = MQTTSerialize_connect(buf, sizeof(buf), &opts);
    ACC_ASSERT_EQ((int)write(peer_a, buf, (size_t)len), len);
    ACC_ASSERT(pump_until_output(a, peer_a, out, sizeof(out), 2000) > 0);
    MQTTString filters[1] = {MQTTString_initializer};
    filters[0].cstring = (char *)"a/+";
    int qos_req[1] = {1};
    len = MQTTSerialize_subscribe(buf, sizeof(buf), 0, 5, 1, filters, qos_req);
    ACC_ASSERT_EQ((int)write(peer_a, buf, (size_t)len), len);
    ACC_ASSERT(pump_until_output(a, peer_a, out, sizeof(out), 2000) > 0);

    /* B: CONNECT */
    opts.clientID.cstring = (char *)"fire-and-forget";
    len = MQTTSerialize_connect(buf, sizeof(buf), &opts);
    ACC_ASSERT_EQ((int)write(peer_b, buf, (size_t)len), len);
    ACC_ASSERT(pump_until_output(b, peer_b, out, sizeof(out), 2000) > 0);

    /* B 同一批字节流：PUBLISH(qos1, a/c) + DISCONNECT */
    MQTTString topic = MQTTString_initializer;
    topic.cstring = (char *)"a/c";
    const char *payload = "last-word";
    len = MQTTSerialize_publish(buf, sizeof(buf), 0, 1, 0, 42, topic,
                                (unsigned char *)payload,
                                (unsigned int)strlen(payload));
    unsigned char batch[512];
    memcpy(batch, buf, (size_t)len);
    int dlen = MQTTSerialize_zero(batch + len, (int)sizeof(batch) - len,
                                  DISCONNECT);
    ACC_ASSERT_EQ(dlen, 2);
    ACC_ASSERT_EQ((int)write(peer_b, batch, (size_t)(len + dlen)), len + dlen);

    /* B 一次事件处理完整批：DISCONNECT 关连接，PUBACK 在 close 前已
     * flush；handler 关闭后残调用有 fd<0 防护，pump 守卫跳过 */
    int olen = 0;
    for (int i = 0; i < 100 && olen == 0; i++) {
        if (b->fd >= 0)
            acc_mqtt_request_handler(b->read);
        olen = drain(peer_b, out, sizeof(out));
        if (olen == 0)
            usleep(10000);
    }
    ACC_ASSERT(olen > 0);
    ACC_ASSERT_EQ(MQTTPacket_type(out, olen), PUBACK);

    /* A 最终收到转发 PUBLISH（发布者已断开，轮询至 2s） */
    olen = 0;
    for (int i = 0; i < 200 && olen == 0; i++) {
        usleep(10000);
        olen = drain(peer_a, out, sizeof(out));
    }
    ACC_ASSERT(olen > 0);
    ACC_ASSERT_EQ(MQTTPacket_type(out, olen), PUBLISH);

    teardown_conn(a, peer_a);
    close(peer_b); /* b 已由 DISCONNECT 路径关闭回池，勿重复 teardown */
    acc_thread_pool_destroy(&ctx.thread_pool);
    acc_sub_map_destroy(ctx.sub_map);
}

/* PUBLISH qos2：回 PUBACK 且按 qos0 转发（订阅者收到 qos==0） */
ACC_TEST(publish_qos2_gets_puback_forwards_qos0)
{
    setup_ctx();
    int peer_a, peer_b;
    acc_connection_t *a = new_conn(&peer_a), *b = new_conn(&peer_b);
    unsigned char buf[512], out[1024];

    /* A: CONNECT + SUBSCRIBE a/+ */
    MQTTPacket_connectData opts = MQTTPacket_connectData_initializer;
    opts.clientID.cstring = (char *)"q2-sub";
    int len = MQTTSerialize_connect(buf, sizeof(buf), &opts);
    ACC_ASSERT_EQ((int)write(peer_a, buf, (size_t)len), len);
    ACC_ASSERT(pump_until_output(a, peer_a, out, sizeof(out), 2000) > 0);
    MQTTString filters[1] = {MQTTString_initializer};
    filters[0].cstring = (char *)"a/+";
    int qos_req[1] = {1};
    len = MQTTSerialize_subscribe(buf, sizeof(buf), 0, 5, 1, filters, qos_req);
    ACC_ASSERT_EQ((int)write(peer_a, buf, (size_t)len), len);
    ACC_ASSERT(pump_until_output(a, peer_a, out, sizeof(out), 2000) > 0);

    /* B: CONNECT + PUBLISH qos2 → PUBACK */
    opts.clientID.cstring = (char *)"q2-pub";
    len = MQTTSerialize_connect(buf, sizeof(buf), &opts);
    ACC_ASSERT_EQ((int)write(peer_b, buf, (size_t)len), len);
    ACC_ASSERT(pump_until_output(b, peer_b, out, sizeof(out), 2000) > 0);
    MQTTString topic = MQTTString_initializer;
    topic.cstring = (char *)"a/q2";
    len = MQTTSerialize_publish(buf, sizeof(buf), 0, 2, 0, 7, topic,
                                (unsigned char *)"two", 3);
    ACC_ASSERT_EQ((int)write(peer_b, buf, (size_t)len), len);
    int olen = pump_until_output(b, peer_b, out, sizeof(out), 2000);
    ACC_ASSERT(olen > 0);
    ACC_ASSERT_EQ(MQTTPacket_type(out, olen), PUBACK);

    /* A 收到转发，qos 折算为 0 */
    olen = 0;
    for (int i = 0; i < 200 && olen == 0; i++) {
        usleep(10000);
        olen = drain(peer_a, out, sizeof(out));
    }
    ACC_ASSERT(olen > 0);
    ACC_ASSERT_EQ(MQTTPacket_type(out, olen), PUBLISH);
    unsigned char dup, retained, *pl;
    int qos, plen; unsigned short apid; MQTTString rt;
    int nl, wl;
    ACC_ASSERT_EQ(MQTTDeserialize_publish(&dup, &qos, &retained, &apid, &rt,
                                          &pl, &plen, out, olen, &nl,
                                          &wl), 1);
    ACC_ASSERT_EQ(qos, 0);
    ACC_ASSERT_EQ(plen, 3);
    ACC_ASSERT_EQ(memcmp(pl, "two", 3), 0);

    teardown_conn(a, peer_a);
    teardown_conn(b, peer_b);
    acc_thread_pool_destroy(&ctx.thread_pool);
    acc_sub_map_destroy(ctx.sub_map);
}

/* UNSUBSCRIBE：回 UNSUBACK，此后同 topic 发布不再收到转发 */
ACC_TEST(unsubscribe_stops_forwarding)
{
    setup_ctx();
    int peer_a, peer_b;
    acc_connection_t *a = new_conn(&peer_a), *b = new_conn(&peer_b);
    unsigned char buf[512], out[1024];

    /* A: CONNECT + SUBSCRIBE a/+ → SUBACK */
    MQTTPacket_connectData opts = MQTTPacket_connectData_initializer;
    opts.clientID.cstring = (char *)"unsub-sub";
    int len = MQTTSerialize_connect(buf, sizeof(buf), &opts);
    ACC_ASSERT_EQ((int)write(peer_a, buf, (size_t)len), len);
    ACC_ASSERT(pump_until_output(a, peer_a, out, sizeof(out), 2000) > 0);
    MQTTString filters[1] = {MQTTString_initializer};
    filters[0].cstring = (char *)"a/+";
    int qos_req[1] = {1};
    len = MQTTSerialize_subscribe(buf, sizeof(buf), 0, 5, 1, filters, qos_req);
    ACC_ASSERT_EQ((int)write(peer_a, buf, (size_t)len), len);
    int olen = pump_until_output(a, peer_a, out, sizeof(out), 2000);
    ACC_ASSERT(olen > 0);
    ACC_ASSERT_EQ(MQTTPacket_type(out, olen), SUBACK);

    /* A: UNSUBSCRIBE a/+ → UNSUBACK */
    len = MQTTSerialize_unsubscribe(buf, sizeof(buf), 0, 5, 1, filters);
    ACC_ASSERT_EQ((int)write(peer_a, buf, (size_t)len), len);
    olen = pump_until_output(a, peer_a, out, sizeof(out), 2000);
    ACC_ASSERT(olen > 0);
    ACC_ASSERT_EQ(MQTTPacket_type(out, olen), UNSUBACK);

    /* B: CONNECT + PUBLISH a/b（qos1 → PUBACK） */
    opts.clientID.cstring = (char *)"unsub-pub";
    len = MQTTSerialize_connect(buf, sizeof(buf), &opts);
    ACC_ASSERT_EQ((int)write(peer_b, buf, (size_t)len), len);
    ACC_ASSERT(pump_until_output(b, peer_b, out, sizeof(out), 2000) > 0);
    MQTTString topic = MQTTString_initializer;
    topic.cstring = (char *)"a/b";
    len = MQTTSerialize_publish(buf, sizeof(buf), 0, 1, 0, 8, topic,
                                (unsigned char *)"gone", 4);
    ACC_ASSERT_EQ((int)write(peer_b, buf, (size_t)len), len);
    ACC_ASSERT(pump_until_output(b, peer_b, out, sizeof(out), 2000) > 0);

    /* A 不再收到转发：轮询 500ms 断言无输出 */
    for (int i = 0; i < 50; i++) {
        usleep(10000);
        ACC_ASSERT_EQ(drain(peer_a, out, sizeof(out)), 0);
    }

    teardown_conn(a, peer_a);
    teardown_conn(b, peer_b);
    acc_thread_pool_destroy(&ctx.thread_pool);
    acc_sub_map_destroy(ctx.sub_map);
}

ACC_TEST_MAIN();
