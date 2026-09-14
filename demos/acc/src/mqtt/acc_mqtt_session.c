/* MQTT 会话状态机：半包切包、CONNECT/PUBLISH/SUBSCRIBE/UNSUBSCRIBE/PINGREQ
 * 协议分支、PUBLISH 线程池转发卸载。对齐 raw 的
 * ads_access_application.c 状态机骨架（去业务）。
 *
 * 线程模型与锁纪律：
 * - 读路径（recv/切包/协议分支）仅事件循环线程执行，不加锁；
 * - 全部写路径（应答 acc_conn_write、批后 flush、线程池 send_publish、
 *   关闭时写事件摘除与缓冲复位）先持 c->buf_mu；
 * - 转发任务自包含（topic/payload 堆拷贝），不引用发布连接：发布者
 *   qos1 已回 PUBACK 即受理，其断开/复用不丢消息；订阅者侧由
 *   send_publish 锁内 fd 检查 + router 桶锁互斥保证存活校验；
 * - acc_mqtt_on_close 的路由表清理不持 buf_mu：转发 worker 持桶读锁后
 *   再取 buf_mu，若 close 先持 buf_mu 再等桶写锁会构成 AB-BA 死锁。 */
#include "mqtt/acc_mqtt_session.h"

#include <pthread.h>
#include <stdatomic.h>
#include <stdlib.h>
#include <string.h>

#include "MQTTPacket.h"
#include "core/acc_core.h"
#include "net/acc_connection.h"
#include "util/acc_log.h"
#include "util/acc_metrics.h"
#include "util/acc_string.h"
#include "util/acc_time.h"

/* 初始读缓冲与上限回退（conf.max_read_buffer_size 为 0 时） */
#define ACC_SESSION_READ_BUF_INIT 4096
#define ACC_SESSION_READ_BUF_LIMIT (1u << 20)
/* SUBSCRIBE/UNSUBSCRIBE 单包 filter 数独立上限：临时数组按剩余长度推
 * 上界分配，无 cap 时 8 字节包头声明超大 rem_len 即可远程放大分配 */
#define ACC_SESSION_MAX_FILTERS 1024

/* 依赖注入的上下文：worker init 时 acc_mqtt_set_context 一次注入 */
static acc_context_t *g_ctx;
/* 转发任务丢弃计数（队列满/分配失败/组包或入写缓冲失败），metrics 预留 */
static atomic_uint_fast64_t g_forward_dropped;
/* 出向 qos1 PUBLISH 报文标识：池线程并发递增，1..65535 轮转（0 非法） */
static atomic_uint g_packet_id;

void acc_mqtt_set_context(acc_context_t *ctx)
{
    g_ctx = ctx;
}

uint64_t acc_mqtt_forward_dropped(void)
{
    return atomic_load(&g_forward_dropped);
}

/* 转发丢弃计数：全局原子计数与 metrics counter 同点递增（单一入口防漏埋） */
static void forward_dropped_inc(void)
{
    atomic_fetch_add(&g_forward_dropped, 1);
    acc_metrics_count("acc_forward_drop_total", 1);
}

/* 读缓冲管理 ----------------------------------------------------------------*/

static uint32_t read_buf_limit(void)
{
    unsigned limit = (g_ctx != NULL) ? g_ctx->conf.max_read_buffer_size : 0;

    return limit != 0 ? limit : ACC_SESSION_READ_BUF_LIMIT;
}

/* 前移压缩：丢弃已消费前缀，未决数据挪回 readbuf 头部 */
static void compact_readbuf(acc_connection_t *c)
{
    uint32_t n = c->readlen - c->readoffset;

    if (c->readoffset == 0)
        return;
    if (n > 0)
        memmove(c->readbuf, c->readbuf + c->readoffset, n);
    c->readoffset = 0;
    c->readlen = n;
}

/* 半包：确保 readbuf 容得下整包 need_len 字节。容量不足时先压缩再
 * realloc（上限 conf.max_read_buffer_size，零配置回退 1MB），超限/分配
 * 失败返回 -1（关连接）；可等待更多数据返回 0 */
static int need_more_data(acc_connection_t *c, int need_len)
{
    if (need_len <= 0 || (uint32_t)need_len <= c->readbufsize)
        return 0;
    compact_readbuf(c);
    if ((uint32_t)need_len <= c->readbufsize)
        return 0;

    uint32_t limit = read_buf_limit();
    if ((uint32_t)need_len > limit) {
        ACC_LOGW("整包 %d 字节超读缓冲上限 %u, 关连接", need_len, limit);
        return -1;
    }
    unsigned char *nb = realloc(c->readbuf, (size_t)need_len);
    if (nb == NULL) {
        ACC_LOGE("readbuf 扩容至 %d 字节失败", need_len);
        return -1;
    }
    c->readbuf = nb;
    c->readbufsize = (uint32_t)need_len;
    return 0;
}

/* recv 前保证缓冲有空闲空间：先压缩，仍满则倍增扩容至上限 */
static int ensure_read_space(acc_connection_t *c)
{
    if (c->readlen < c->readbufsize)
        return 0;
    compact_readbuf(c);
    if (c->readlen < c->readbufsize)
        return 0;

    uint32_t limit = read_buf_limit();
    if (c->readbufsize >= limit) {
        ACC_LOGW("读缓冲满且已达上限 %u, 关连接", limit);
        return -1;
    }
    uint32_t nsize = c->readbufsize * 2;
    if (nsize > limit)
        nsize = limit;
    unsigned char *nb = realloc(c->readbuf, nsize);
    if (nb == NULL) {
        ACC_LOGE("readbuf 扩容至 %u 字节失败", nsize);
        return -1;
    }
    c->readbuf = nb;
    c->readbufsize = nsize;
    return 0;
}

/* 消费一包：前移读偏移；恰好消费完则双零归位 */
static void consume_packet(acc_connection_t *c, int walk_len)
{
    c->readoffset += (uint32_t)walk_len;
    if (c->readoffset >= c->readlen) {
        c->readoffset = 0;
        c->readlen = 0;
    }
}

/* 固定头完整性：走一遍剩余长度 varint。
 * 返回固定头总长（type 字节 + varint）；varint 未收全返回 0；
 * 超过 4 字节（非法编码）返回 -1。前置：vendored 的 decodeBuf 用静态
 * 游标逐字节读、不感知边界，先在本侧确认 varint 收全可杜绝越界读 */
static int mqtt_fixed_header_len(const unsigned char *p, uint32_t avail)
{
    for (uint32_t j = 1; j <= 4; j++) {
        if (j >= avail)
            return 0;
        if ((p[j] & 0x80u) == 0)
            return (int)(j + 1);
    }
    return -1;
}

/* 剩余长度值（仅可在 fixed_header_len 校验通过后调用） */
static uint32_t mqtt_rem_len(const unsigned char *p)
{
    uint32_t v = 0, mul = 1;

    for (int i = 1; i <= 4; i++) {
        v += (uint32_t)(p[i] & 0x7fu) * mul;
        mul *= 128;
        if ((p[i] & 0x80u) == 0)
            break;
    }
    return v;
}

/* 会话 subtopics 登记 --------------------------------------------------------*/

static int session_add_subtopic(acc_mqtt_session_t *s, const char *filter)
{
    ACC_LIST_FOR_EACH(pos, &s->subtopics) {
        acc_mqtt_subtopic_t *st = ACC_LIST_ENTRY(pos, acc_mqtt_subtopic_t, q);
        if (strcmp(st->filter, filter) == 0)
            return 0; /* 重复订阅幂等 */
    }

    acc_mqtt_subtopic_t *st = calloc(1, sizeof(*st));
    if (st == NULL)
        return -1;
    acc_strlcpy(st->filter, filter, sizeof(st->filter));
    acc_list_add_tail(&st->q, &s->subtopics);
    return 0;
}

static void session_del_subtopic(acc_mqtt_session_t *s, const char *filter)
{
    ACC_LIST_FOR_EACH_SAFE(pos, n, &s->subtopics) {
        acc_mqtt_subtopic_t *st = ACC_LIST_ENTRY(pos, acc_mqtt_subtopic_t, q);
        if (strcmp(st->filter, filter) == 0) {
            acc_list_del(&st->q);
            free(st);
            return;
        }
    }
}

/* 写路径统一入口：应答包入写缓冲（持 buf_mu 与池线程 send_publish 串行） */
static int session_write(acc_connection_t *c, const void *buf, size_t n)
{
    int r = -1;

    pthread_mutex_lock(&c->buf_mu);
    if (c->fd >= 0)
        r = acc_conn_write(g_ctx, c, buf, n);
    pthread_mutex_unlock(&c->buf_mu);
    return r;
}

/* 下行 PUBLISH：serialize 后入写缓冲并主动 flush。可由池线程调用，
 * 全程持 buf_mu；失败计数丢弃（优雅降级，进程不退出） */
void acc_mqtt_send_publish(acc_connection_t *c, const char *topic,
                           const unsigned char *payload, size_t len, int qos)
{
    if (c == NULL || c->fd < 0 || g_ctx == NULL || topic == NULL ||
        topic[0] == '\0')
        return;
    if (qos != 1)
        qos = 0; /* 出向仅 0/1 */

    unsigned char *out = malloc(len + ACC_MQTT_TOPIC_MAX + 16);
    if (out == NULL) {
        forward_dropped_inc();
        ACC_LOGW("PUBLISH 组包分配失败, 丢弃 topic=%s", topic);
        return;
    }

    MQTTString tn = MQTTString_initializer;
    tn.cstring = (char *)topic;
    unsigned short pid = 0;
    if (qos == 1)
        pid = (unsigned short)(atomic_fetch_add(&g_packet_id, 1) % 65535u + 1);
    int n = MQTTSerialize_publish(out, (int)(len + ACC_MQTT_TOPIC_MAX + 16), 0,
                                  qos, 0, pid, tn,
                                  (unsigned char *)payload, (int)len);
    if (n <= 0) {
        forward_dropped_inc();
        ACC_LOGW("PUBLISH 序列化失败, 丢弃 topic=%s", topic);
        free(out);
        return;
    }

    pthread_mutex_lock(&c->buf_mu);
    if (c->fd >= 0) {
        if (acc_conn_write(g_ctx, c, out, (size_t)n) == 0) {
            acc_metrics_count("acc_packets_tx_total", 1); /* 真实入缓冲才计 */
            acc_conn_flush(c); /* 写空摘写事件；EAGAIN 残余留 EPOLLOUT */
        } else { /* 慢订阅者写缓冲满等：计数丢弃，消除盲区 */
            forward_dropped_inc();
            ACC_LOGW("PUBLISH 入写缓冲失败, 丢弃 topic=%s", topic);
        }
    }
    pthread_mutex_unlock(&c->buf_mu);
    free(out);
}

/* 转发任务 ------------------------------------------------------------------*/

static void forward_drop(acc_forward_task_t *t, const char *why)
{
    forward_dropped_inc();
    ACC_LOGW("PUBLISH 转发任务丢弃(%s), topic=%s", why,
             (t != NULL) ? t->topic : "?");
    if (t != NULL) {
        free(t->payload);
        free(t);
    }
}

/* 路由表命中回调：arg 即转发任务本体，逐订阅者组包下发 */
static int forward_cb(acc_connection_t *c, void *arg)
{
    acc_forward_task_t *t = arg;

    acc_mqtt_send_publish(c, t->topic, t->payload, t->payload_len, t->qos);
    return 0; /* 恒 0：逐订阅者下发，不提前终止 */
}

/* 池线程任务体：路由表遍历逐订阅者下发 → 释放堆拷贝。
 * 任务自包含（topic/payload 均拷贝），发布者此刻断开/复用不影响投递——
 * qos1 PUBLISH 回 PUBACK 即受理，断开不得丢已受理消息 */
static void forward_task_run(void *data)
{
    acc_forward_task_t *t = data;

    if (t == NULL)
        return;
    if (g_ctx != NULL && g_ctx->sub_map != NULL)
        acc_sub_map_forward_each(g_ctx->sub_map, t->topic, forward_cb, t);
    free(t->payload);
    free(t);
}

/* 队列水位 gauge：提交成功与满队失败两路都刷新——满队瞬间的
 * pending 恰为 queue_size（峰值水位），失败路径不刷新会低估 */
static void tp_queue_gauge(void)
{
    if (g_ctx != NULL)
        acc_metrics_gauge("acc_tp_queue_used",
                          acc_thread_pool_pending(&g_ctx->thread_pool));
}

/* 组转发任务（topic/payload 堆拷贝，自包含）投线程池；
 * 队列满/分配失败计数丢弃并 WARN（不阻断协议层） */
static int post_forward(const char *topic, const unsigned char *payload,
                        uint32_t len, int qos)
{
    acc_forward_task_t *t = calloc(1, sizeof(*t));

    if (t != NULL)
        t->payload = malloc(len > 0 ? len : 1);
    if (t == NULL || t->payload == NULL) {
        forward_drop(t, "任务分配失败");
        return -1;
    }

    t->qos = qos;
    t->dup = 0;
    acc_strlcpy(t->topic, topic, sizeof(t->topic));
    if (len > 0)
        memcpy(t->payload, payload, len);
    t->payload_len = len;

    if (g_ctx == NULL) {
        forward_drop(t, "线程池队列满");
        return -1;
    }
    if (acc_thread_pool_post(&g_ctx->thread_pool, forward_task_run, t) != 0) {
        tp_queue_gauge(); /* 满队瞬间即峰值：失败路径同样刷新 */
        forward_drop(t, "线程池队列满");
        return -1;
    }
    tp_queue_gauge();
    return 0;
}

/* 协议分支 ------------------------------------------------------------------*/

static int handle_connect(acc_connection_t *c, acc_mqtt_session_t *s,
                          unsigned char *data, uint32_t avail)
{
    MQTTPacket_connectData d = MQTTPacket_connectData_initializer;
    int need_len = 0, walk_len = 0;
    int ret = MQTTDeserialize_connect(&d, data, (int)avail, &need_len,
                                      &walk_len);

    if (ret == -1)
        return need_more_data(c, need_len);
    if (ret == 0) {
        /* 协议名/版本不符等：vendored 已知瑕疵——该路径仍写 walk_len，勿依赖 */
        ACC_LOGW("CONNECT 解析失败(协议名/版本?), 关连接");
        return -1;
    }
    if (s->connected) {
        ACC_LOGW("重复 CONNECT, 关连接");
        return -1;
    }

    const char *cid = d.clientID.lenstring.data;
    size_t cidlen = (cid != NULL) ? (size_t)d.clientID.lenstring.len : 0;
    unsigned char ab[4];
    int n;
    if (cidlen == 0 || cidlen > ACC_MQTT_CLIENT_ID_MAX) {
        ACC_LOGW("client_id 非法(len=%zu), 回 CONNACK rc=2 后关连接", cidlen);
        n = MQTTSerialize_connack(ab, sizeof(ab), 2, 0);
        if (n > 0)
            session_write(c, ab, (size_t)n);
        return -1;
    }

    memcpy(s->client_id, cid, cidlen);
    s->client_id[cidlen] = '\0';
    s->connected = 1;

    n = MQTTSerialize_connack(ab, sizeof(ab), 0, 0);
    if (n <= 0 || session_write(c, ab, (size_t)n) != 0) {
        ACC_LOGE("CONNACK 写入失败");
        return -1;
    }
    ACC_LOGD("CONNECT 成功, client_id=%s", s->client_id);
    consume_packet(c, walk_len);
    return 1;
}

static int handle_publish(acc_connection_t *c, unsigned char *data,
                          uint32_t avail)
{
    unsigned char dup, retained, *payload;
    int qos, payloadlen, need_len = 0, walk_len = 0;
    unsigned short packetid = 0;
    MQTTString topic;
    int ret = MQTTDeserialize_publish(&dup, &qos, &retained, &packetid,
                                      &topic, &payload, &payloadlen, data,
                                      (int)avail, &need_len, &walk_len);

    if (ret == -1)
        return need_more_data(c, need_len);
    if (ret == 0) {
        ACC_LOGW("PUBLISH 解析失败, 关连接");
        return -1;
    }
    if (qos < 0 || qos > 2 || payloadlen < 0) {
        ACC_LOGW("PUBLISH qos=%d 非法, 关连接", qos);
        return -1;
    }

    const char *tp = topic.lenstring.data;
    size_t tlen = (tp != NULL) ? (size_t)topic.lenstring.len : 0;
    if (tlen == 0 || tlen > ACC_MQTT_TOPIC_MAX ||
        memchr(tp, '+', tlen) != NULL || memchr(tp, '#', tlen) != NULL) {
        ACC_LOGW("PUBLISH topic 非法(len=%zu), 关连接", tlen);
        return -1;
    }

    if (qos >= 1) { /* qos1/qos2 均先回 PUBACK（spec 第 5 节表格） */
        unsigned char ab[4];
        int n = MQTTSerialize_ack(ab, sizeof(ab), PUBACK, 0, packetid);
        if (n <= 0 || session_write(c, ab, (size_t)n) != 0)
            return -1;
    }

    char top[ACC_MQTT_TOPIC_MAX + 1];
    memcpy(top, tp, tlen);
    top[tlen] = '\0';
    /* qos2：回 PUBACK 后按 qos0 转发（不实现完整 qos2 握手，spec 声明） */
    int fqos = (qos == 2) ? 0 : qos;
    if (post_forward(top, payload, (uint32_t)payloadlen, fqos) != 0)
        ACC_LOGW("转发任务丢弃, topic=%s", top); /* 已计数，不阻断协议层 */
    consume_packet(c, walk_len);
    return 1;
}

static int handle_puback(acc_connection_t *c, unsigned char *data,
                         uint32_t avail)
{
    unsigned char ptype, dup;
    unsigned short packetid;
    int need_len = 0, walk_len = 0;
    int ret = MQTTDeserialize_ack(&ptype, &dup, &packetid, data, (int)avail,
                                  &need_len, &walk_len);

    if (ret == -1)
        return need_more_data(c, need_len);
    if (ret == 0) {
        ACC_LOGW("PUBACK 解析失败, 关连接");
        return -1;
    }
    (void)ptype;
    (void)packetid; /* 本服务不主动发 qos1：入站应答直接丢弃 */
    consume_packet(c, walk_len);
    return 1;
}

static int handle_subscribe(acc_connection_t *c, acc_mqtt_session_t *s,
                            unsigned char *data, uint32_t avail)
{
    if (g_ctx == NULL || g_ctx->sub_map == NULL)
        return -1;

    /* 声明长度预检（先于临时数组分配）：整包超读缓冲上限直接关连接，
     * 堵 8 字节包头声明超大 rem_len 的远程分配放大（varint 已由调用方
     * 预检收全，此处读 rem 安全） */
    uint32_t rem = mqtt_rem_len(data);
    if (5 + (uint64_t)rem > read_buf_limit()) {
        ACC_LOGW("SUBSCRIBE 声明长度 %u 超读缓冲上限, 关连接", rem);
        return -1;
    }
    /* vendored 的 filter 解析循环不检查 maxcount，按剩余长度推上界分配，
     * 杜绝恶意多 filter 包越界写（每个 filter 项至少 3 字节）；独立
     * cap 拦超量 filter 包 */
    size_t maxf = rem / 3 + 1;
    if (maxf > ACC_SESSION_MAX_FILTERS) {
        ACC_LOGW("SUBSCRIBE filter 数超上限 %d, 关连接",
                 ACC_SESSION_MAX_FILTERS);
        return -1;
    }
    MQTTString *filters = malloc(maxf * sizeof(*filters));
    int *reqqos = malloc(maxf * sizeof(*reqqos));
    int *granted = malloc(maxf * sizeof(*granted));
    unsigned char dup;
    unsigned short packetid;
    int count = 0, need_len = 0, walk_len = 0;
    int ret, ok = -1, n;

    if (filters == NULL || reqqos == NULL || granted == NULL) {
        ACC_LOGE("SUBSCRIBE 临时数组分配失败");
        free(filters);
        free(reqqos);
        free(granted);
        return -1;
    }

    ret = MQTTDeserialize_subscribe(&dup, &packetid, (int)maxf, &count,
                                    filters, reqqos, data, (int)avail,
                                    &need_len, &walk_len);
    if (ret == -1) { /* 半包：直接返回 0（等数据）/ -1（超限） */
        ok = need_more_data(c, need_len);
        free(filters);
        free(reqqos);
        free(granted);
        return ok;
    }
    if (ret == 0 || count <= 0) {
        ACC_LOGW("SUBSCRIBE 解析失败或 filter 数为 0, 关连接");
        goto out;
    }

    for (int i = 0; i < count; i++) {
        granted[i] = 0x80; /* 失败码先置，成功再覆写 */
        int rq = reqqos[i];
        if (rq < 0 || rq > 2)
            continue; /* qos 字节非法：该 filter granted=0x80 */
        const char *fp = filters[i].lenstring.data;
        size_t flen = (fp != NULL) ? (size_t)filters[i].lenstring.len : 0;
        if (flen == 0 || flen > ACC_MQTT_TOPIC_MAX)
            continue;
        char flt[ACC_MQTT_TOPIC_MAX + 1];
        memcpy(flt, fp, flen);
        flt[flen] = '\0';
        if (!acc_topic_filter_valid(flt))
            continue;
        /* 会话节点先登记再入路由表：中途失败即可整体回退，两侧一致 */
        if (session_add_subtopic(s, flt) != 0)
            continue;
        if (acc_sub_map_subscribe(g_ctx->sub_map, flt, c) != 0) {
            session_del_subtopic(s, flt);
            continue;
        }
        granted[i] = (rq >= 1) ? 1 : 0; /* granted = min(req, 1) */
    }

    unsigned char *ab = malloc((size_t)count + 8);
    if (ab != NULL) {
        n = MQTTSerialize_suback(ab, (int)count + 8, packetid, count, granted);
        if (n > 0)
            ok = session_write(c, ab, (size_t)n);
        free(ab);
    }

out:
    free(filters);
    free(reqqos);
    free(granted);
    if (ok != 0)
        return -1;
    consume_packet(c, walk_len);
    return 1;
}

static int handle_unsubscribe(acc_connection_t *c, acc_mqtt_session_t *s,
                              unsigned char *data, uint32_t avail)
{
    if (g_ctx == NULL || g_ctx->sub_map == NULL)
        return -1;

    /* 声明长度预检先于数组分配 + 独立 cap（同 handle_subscribe） */
    uint32_t rem = mqtt_rem_len(data);
    if (5 + (uint64_t)rem > read_buf_limit()) {
        ACC_LOGW("UNSUBSCRIBE 声明长度 %u 超读缓冲上限, 关连接", rem);
        return -1;
    }
    size_t maxf = rem / 2 + 1; /* 每项至少 2 字节长度前缀 */
    if (maxf > ACC_SESSION_MAX_FILTERS) {
        ACC_LOGW("UNSUBSCRIBE filter 数超上限 %d, 关连接",
                 ACC_SESSION_MAX_FILTERS);
        return -1;
    }
    MQTTString *filters = malloc(maxf * sizeof(*filters));
    unsigned char dup;
    unsigned short packetid;
    int count = 0, need_len = 0, walk_len = 0;
    int ret, ok = -1, n;

    if (filters == NULL) {
        ACC_LOGE("UNSUBSCRIBE 临时数组分配失败");
        return -1;
    }

    ret = MQTTDeserialize_unsubscribe(&dup, &packetid, (int)maxf, &count,
                                      filters, data, (int)avail, &need_len,
                                      &walk_len);
    if (ret == -1) { /* 半包：直接返回 0（等数据）/ -1（超限） */
        ok = need_more_data(c, need_len);
        free(filters);
        return ok;
    }
    if (ret == 0 || count <= 0) {
        ACC_LOGW("UNSUBSCRIBE 解析失败或 filter 数为 0, 关连接");
        goto out;
    }

    for (int i = 0; i < count; i++) {
        const char *fp = filters[i].lenstring.data;
        size_t flen = (fp != NULL) ? (size_t)filters[i].lenstring.len : 0;
        if (flen == 0 || flen > ACC_MQTT_TOPIC_MAX)
            continue; /* 未订阅过的 filter：静默忽略 */
        char flt[ACC_MQTT_TOPIC_MAX + 1];
        memcpy(flt, fp, flen);
        flt[flen] = '\0';
        acc_sub_map_unsubscribe(g_ctx->sub_map, flt, c);
        session_del_subtopic(s, flt);
    }

    unsigned char ab[8];
    n = MQTTSerialize_unsuback(ab, sizeof(ab), packetid);
    if (n > 0)
        ok = session_write(c, ab, (size_t)n);

out:
    free(filters);
    if (ok != 0)
        return -1;
    consume_packet(c, walk_len);
    return 1;
}

/* PINGREQ/DISCONNECT 无 payload：2 字节定长（第二字节须为 0），手工 walk 2 */
static int handle_pingreq(acc_connection_t *c, const unsigned char *data)
{
    if (data[1] != 0) {
        ACC_LOGW("PINGREQ 剩余长度非 0, 关连接");
        return -1;
    }
    unsigned char ab[4];
    int n = MQTTSerialize_zero(ab, sizeof(ab), PINGRESP);
    if (n <= 0 || session_write(c, ab, (size_t)n) != 0)
        return -1;
    consume_packet(c, 2);
    return 1;
}

static int handle_disconnect(const acc_mqtt_session_t *s,
                             const unsigned char *data)
{
    if (data[1] != 0) {
        ACC_LOGW("DISCONNECT 剩余长度非 0, 关连接");
        return -1;
    }
    ACC_LOGD("client_id=%s 主动 DISCONNECT, 关连接", s->client_id);
    return -1; /* 正常断开：调用方 flush 后关连接 */
}

int acc_mqtt_process_one_package(acc_context_t *ctx, acc_connection_t *c)
{
    (void)ctx; /* 上下文统一经 acc_mqtt_set_context 注入（worker 单实例） */

    if (c == NULL || c->data == NULL || c->readbuf == NULL || g_ctx == NULL)
        return -1;
    acc_mqtt_session_t *s = c->data;

    uint32_t avail = c->readlen - c->readoffset;
    if (avail < 2)
        return 0; /* 连固定头都不完整 */
    unsigned char *data = c->readbuf + c->readoffset;

    int hlen = mqtt_fixed_header_len(data, avail);
    if (hlen == 0)
        return 0; /* 剩余长度 varint 未收全 */
    if (hlen < 0) {
        ACC_LOGW("剩余长度 varint 超 4 字节, 关连接");
        return -1;
    }

    switch (data[0] >> 4) {
    case CONNECT:
        return handle_connect(c, s, data, avail);
    case PUBLISH:
        if (!s->connected) {
            ACC_LOGW("CONNECT 之前收到 PUBLISH, 关连接");
            return -1;
        }
        return handle_publish(c, data, avail);
    case PUBACK:
        if (!s->connected) {
            ACC_LOGW("CONNECT 之前收到 PUBACK, 关连接");
            return -1;
        }
        return handle_puback(c, data, avail);
    case SUBSCRIBE:
        if (!s->connected) {
            ACC_LOGW("CONNECT 之前收到 SUBSCRIBE, 关连接");
            return -1;
        }
        return handle_subscribe(c, s, data, avail);
    case UNSUBSCRIBE:
        if (!s->connected) {
            ACC_LOGW("CONNECT 之前收到 UNSUBSCRIBE, 关连接");
            return -1;
        }
        return handle_unsubscribe(c, s, data, avail);
    case PINGREQ:
        if (!s->connected) {
            ACC_LOGW("CONNECT 之前收到 PINGREQ, 关连接");
            return -1;
        }
        return handle_pingreq(c, data);
    case DISCONNECT:
        return handle_disconnect(s, data);
    default:
        ACC_LOGW("不接受的包类型 %d, 关连接", data[0] >> 4);
        return -1;
    }
}

/* 事件 handler ---------------------------------------------------------------*/

void acc_mqtt_request_handler(acc_event_t *ev)
{
    acc_connection_t *c = (ev != NULL) ? ev->data : NULL;

    if (c == NULL || c->fd < 0)
        return; /* posted 语境关闭后的残派发防护 */
    c->timestamp = acc_time_now();

    if (c->ssl != NULL && !c->ssl_connected) {
        ssize_t r = acc_ssl_handshake(c);
        if (r != 0) {
            if (r == ACC_CONN_ERR)
                acc_close_connection(g_ctx, c);
            return; /* AGAIN：等下次事件续推 */
        }
        /* 握手完成：落入本次 recv */
    }

    if (ensure_read_space(c) != 0) {
        acc_close_connection(g_ctx, c);
        return;
    }

    ssize_t n = c->recv(c, c->readbuf + c->readlen,
                        c->readbufsize - c->readlen);
    if (n == 0) { /* 对端关闭 */
        acc_close_connection(g_ctx, c);
        return;
    }
    if (n < 0) {
        if (n != ACC_CONN_AGAIN)
            acc_close_connection(g_ctx, c); /* 硬错误 */
        return; /* AGAIN：缓冲区无新增，等待下次读事件 */
    }
    c->readlen += (uint32_t)n;

    int err = 0;
    for (;;) {
        int r = acc_mqtt_process_one_package(g_ctx, c);
        if (r == 1) { /* 成功消费一包才计 rx（半包等待不计） */
            acc_metrics_count("acc_packets_rx_total", 1);
            continue;
        }
        if (r == -1)
            err = 1;
        break;
    }

    /* 批后主动冲刷：写空自动摘写事件，EAGAIN 残余留 EPOLLOUT */
    pthread_mutex_lock(&c->buf_mu);
    acc_conn_flush(c);
    pthread_mutex_unlock(&c->buf_mu);

    if (err) /* 协议错误：flush 完应答（如 CONNACK rc=2）后关连接 */
        acc_close_connection(g_ctx, c);
}

void acc_mqtt_content_handler(acc_event_t *ev)
{
    acc_connection_t *c = (ev != NULL) ? ev->data : NULL;

    if (c == NULL || c->fd < 0)
        return;

    if (c->ssl != NULL && !c->ssl_connected) { /* 握手期写事件：续推握手 */
        ssize_t r = acc_ssl_handshake(c);
        if (r == ACC_CONN_ERR) {
            acc_close_connection(g_ctx, c);
            return;
        }
        if (r == ACC_CONN_AGAIN)
            return;
        /* 握手完成：落入冲刷 */
    }

    pthread_mutex_lock(&c->buf_mu);
    int r = acc_conn_flush(c); /* 写空摘写事件；EAGAIN 保留进度 */
    pthread_mutex_unlock(&c->buf_mu);

    if (r == ACC_BUF_ERR)
        acc_close_connection(g_ctx, c);
}

/* 生命周期 ------------------------------------------------------------------*/

void acc_mqtt_init_connection(acc_connection_t *c)
{
    if (c == NULL)
        return;

    acc_mqtt_session_t *s = calloc(1, sizeof(*s));
    if (s == NULL) {
        ACC_LOGE("session 分配失败");
        if (g_ctx != NULL)
            acc_close_connection(g_ctx, c);
        return;
    }
    acc_list_init(&s->subtopics);

    free(c->readbuf); /* 防御：复用连接残留（free_connection 路径已释放） */
    c->readbuf = malloc(ACC_SESSION_READ_BUF_INIT);
    if (c->readbuf == NULL) {
        ACC_LOGE("读缓冲分配失败");
        free(s);
        if (g_ctx != NULL)
            acc_close_connection(g_ctx, c);
        return;
    }
    c->readbufsize = ACC_SESSION_READ_BUF_INIT;
    c->readlen = 0;
    c->readoffset = 0;

    c->data = s;
    c->read->handler = acc_mqtt_request_handler;
    c->write->handler = acc_mqtt_content_handler;
}

void acc_mqtt_on_close(acc_connection_t *c)
{
    if (c == NULL)
        return;
    acc_mqtt_session_t *s = c->data;
    if (s == NULL)
        return; /* 幂等：已清理过 */

    /* 路由表按连接批量清理（桶级写锁在 router 内）。此处不持 buf_mu：
     * 转发 worker 持桶读锁 → buf_mu，先持 buf_mu 再等桶写锁会死锁 */
    if (g_ctx != NULL && g_ctx->sub_map != NULL)
        acc_sub_map_del_conn(g_ctx->sub_map, c);

    ACC_LIST_FOR_EACH_SAFE(pos, n, &s->subtopics) {
        acc_mqtt_subtopic_t *st = ACC_LIST_ENTRY(pos, acc_mqtt_subtopic_t, q);
        acc_list_del(&st->q);
        free(st);
    }
    free(s);
    c->data = NULL;
}
