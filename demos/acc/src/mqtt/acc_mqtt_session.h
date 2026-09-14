#ifndef ACC_MQTT_SESSION_H
#define ACC_MQTT_SESSION_H

#include <stddef.h>
#include <stdint.h>

#include "event/acc_event.h"
#include "mqtt/acc_mqtt_router.h"

/* 上下文类型前向声明：与 core/acc_core.h 的重复 typedef 合法（C11），
 * 本头文件不反向依赖 core */
typedef struct acc_context acc_context_t;

/* 会话订阅 topic 节点：挂在 session->subtopics 链上，供退订/关闭反查 */
typedef struct acc_mqtt_subtopic {
    char filter[ACC_MQTT_TOPIC_MAX + 1];
    struct acc_list q;
} acc_mqtt_subtopic_t;

/* MQTT 会话：挂 c->data，随连接生命周期存在 */
typedef struct acc_mqtt_session {
    char client_id[ACC_MQTT_CLIENT_ID_MAX + 1]; /* CONNECT 时落档 */
    struct acc_list subtopics; /* acc_mqtt_subtopic_t 链头 */
    int connected; /* CONNECT 已完成：后续包才合法（重复 CONNECT 拒绝） */
} acc_mqtt_session_t;

/* PUBLISH 转发任务（线程池载荷，spec §4）：payload/topic 均为堆拷贝，
 * 执行后释放，自包含不引用发布连接——发布者 qos1 已回 PUBACK 即受理，
 * 其随后断开（mosquitto_pub 发布后立即 DISCONNECT 是默认模式）不得丢消息。
 * 订阅者侧存活校验由 send_publish 锁内 c->fd>=0 检查与 router 桶锁互斥保证 */
typedef struct acc_forward_task {
    int qos, dup; /* 转发 qos（发布侧已折算）；dup 恒 0 */
    char topic[ACC_MQTT_TOPIC_MAX + 1];
    unsigned char *payload; /* 堆分配拷贝，转发后释放 */
    uint32_t payload_len;   /* 受 max_read_buffer_size 约束（包先入 readbuf） */
} acc_forward_task_t;

/* 依赖注入：worker init 时注入全局上下文（sub_map/thread_pool/conf） */
void acc_mqtt_set_context(acc_context_t *ctx);

/* 接入初始化：挂读写 handler、建 session 到 c->data、初始化读缓冲 4K */
void acc_mqtt_init_connection(acc_connection_t *c);

/* 读事件：TLS 先推进握手；c->recv 填 readbuf → 循环 process_one_package，
 * 批后主动 flush（写空摘写事件、EAGAIN 残余留 EPOLLOUT） */
void acc_mqtt_request_handler(acc_event_t *ev);

/* 写事件：flush 写缓冲；写空删写事件；flush 硬错误关连接 */
void acc_mqtt_content_handler(acc_event_t *ev);

/* 处理 readbuf 中一个完整包：1=已处理一包，0=半包需更多数据，
 * -1=协议错误（调用方应 flush 后关连接） */
int acc_mqtt_process_one_package(acc_context_t *ctx, acc_connection_t *c);

/* 连接关闭回调：清订阅（路由表 del_conn + 会话 subtopics）、
 * free session、置 c->data = NULL；幂等 */
void acc_mqtt_on_close(acc_connection_t *c);

/* 组 PUBLISH（qos 0/1）入写缓冲并尝试 flush；线程池 worker 可调，
 * 内部以 c->buf_mu 串行化（锁纪律见 acc_connection.h） */
void acc_mqtt_send_publish(acc_connection_t *c, const char *topic,
                           const unsigned char *payload, size_t len, int qos);

/* 转发任务丢弃计数（队列满/分配失败/组包或入写缓冲失败），metrics 预留 */
uint64_t acc_mqtt_forward_dropped(void);

#endif /* ACC_MQTT_SESSION_H */
