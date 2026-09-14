#ifndef ACC_MQTT_ROUTER_H
#define ACC_MQTT_ROUTER_H

#include <stddef.h>

/* 连接类型前向声明：与 net/acc_connection.h 的重复 typedef 合法（C11）。
 * router 全程只做连接指针身份比较，不解引用、不依赖 net/编解码，
 * 回调（组包写入）由 session 层注入 */
typedef struct acc_connection_s acc_connection_t;

/* topic / client-id 长度上限（不含结尾 NUL） */
#define ACC_MQTT_TOPIC_MAX 256
#define ACC_MQTT_CLIENT_ID_MAX 128

/* 订阅路由表：filter 哈希桶数组，桶级读写锁 */
typedef struct acc_sub_map acc_sub_map_t;

/* 创建 nbuckets 个桶的路由表；失败（含 nbuckets 为 0）返回 NULL */
acc_sub_map_t *acc_sub_map_create(size_t nbuckets);

/* 释放路由表及全部 filter/订阅节点 */
void acc_sub_map_destroy(acc_sub_map_t *m);

/* filter 合法性：'#' 仅能独占末层、'+' 独占单层；空/NULL filter 非法。
 * 合法返回 1，否则 0 */
int acc_topic_filter_valid(const char *filter);

/* 通配符匹配：topic 不得含通配符（含则不匹配）；匹配返回 1，否则 0。
 * '#' 匹配剩余全部层级（含空、含父层）；'+' 恒匹配单层 */
int acc_topic_match(const char *filter, const char *topic);

/* 订阅：重复订阅同一 filter 幂等返回 0；参数非法/filter 非法/超长/
 * 内存不足返回 -1 */
int acc_sub_map_subscribe(acc_sub_map_t *m, const char *filter,
                          acc_connection_t *c);

/* 退订：filter 或订阅不存在返回 -1；成功返回 0。
 * filter 节点清空后随之摘除释放 */
int acc_sub_map_unsubscribe(acc_sub_map_t *m, const char *filter,
                            acc_connection_t *c);

/* 连接关闭时摘除其全部订阅；空 filter 节点随之释放 */
void acc_sub_map_del_conn(acc_sub_map_t *m, acc_connection_t *c);

/* 遍历匹配 topic 的全部订阅连接逐个调 cb；返回已回调的命中数。
 * cb 返回非 0 时提前终止并停止后续桶遍历（返回已计数数） */
size_t acc_sub_map_forward_each(acc_sub_map_t *m, const char *topic,
                                int (*cb)(acc_connection_t *c, void *arg),
                                void *arg);

/* 连接 c 的订阅总数（测试用） */
size_t acc_sub_map_conn_subs(const acc_sub_map_t *m,
                             const acc_connection_t *c);

#endif /* ACC_MQTT_ROUTER_H */
