#include "acc_mqtt_router.h"

#include <pthread.h>
#include <stdlib.h>
#include <string.h>

#include "util/acc_list.h"
#include "util/acc_string.h"

/* 订阅节点：挂在该 filter 的订阅者链上，仅存连接指针身份 */
struct sub_conn {
    acc_connection_t *c;
    struct acc_list q;
};

/* filter 节点：一个 topic filter 及其全部订阅者 */
struct sub_filter {
    char filter[ACC_MQTT_TOPIC_MAX + 1];
    struct acc_list bucket_q; /* 挂入所属桶的 filter 链 */
    struct acc_list conns;    /* sub_conn 链头 */
    size_t nconns;
};

/* 桶：filter 链 + 读写锁（订阅/退订/清连接写锁，转发/计数读锁） */
struct acc_sub_bucket {
    struct acc_list filters;
    pthread_rwlock_t rwlock;
};

struct acc_sub_map {
    size_t nbuckets;
    struct acc_sub_bucket *buckets;
};

/* BKDR 字符串哈希 */
static size_t filter_hash(const char *s)
{
    size_t h = 0;

    while (*s != '\0')
        h = h * 131 + (unsigned char)*s++;
    return h;
}

static struct acc_sub_bucket *map_bucket(acc_sub_map_t *m, const char *filter)
{
    return &m->buckets[filter_hash(filter) % m->nbuckets];
}

/* 在桶内查找同名 filter 节点（须持锁），找不到返回 NULL */
static struct sub_filter *bucket_find(struct acc_sub_bucket *b,
                                      const char *filter)
{
    ACC_LIST_FOR_EACH(pos, &b->filters) {
        struct sub_filter *f = ACC_LIST_ENTRY(pos, struct sub_filter,
                                              bucket_q);
        if (strcmp(f->filter, filter) == 0)
            return f;
    }
    return NULL;
}

/* 在 filter 节点内按指针身份找连接订阅，找不到返回 NULL */
static struct sub_conn *filter_find_conn(struct sub_filter *f,
                                         const acc_connection_t *c)
{
    ACC_LIST_FOR_EACH(pos, &f->conns) {
        struct sub_conn *sc = ACC_LIST_ENTRY(pos, struct sub_conn, q);
        if (sc->c == c)
            return sc;
    }
    return NULL;
}

/* 摘除并释放 filter 节点的全部订阅节点（须持桶写锁） */
static void filter_free_conns(struct sub_filter *f)
{
    ACC_LIST_FOR_EACH_SAFE(pos, n, &f->conns) {
        struct sub_conn *sc = ACC_LIST_ENTRY(pos, struct sub_conn, q);
        acc_list_del(&sc->q);
        free(sc);
    }
    f->nconns = 0;
}

acc_sub_map_t *acc_sub_map_create(size_t nbuckets)
{
    acc_sub_map_t *m;

    if (nbuckets == 0)
        return NULL;
    m = calloc(1, sizeof(*m));
    if (m == NULL)
        return NULL;
    m->nbuckets = nbuckets;
    m->buckets = calloc(nbuckets, sizeof(*m->buckets));
    if (m->buckets == NULL) {
        free(m);
        return NULL;
    }
    for (size_t i = 0; i < nbuckets; i++) {
        acc_list_init(&m->buckets[i].filters);
        if (pthread_rwlock_init(&m->buckets[i].rwlock, NULL) != 0) {
            /* 回滚已初始化的桶 */
            for (size_t j = 0; j < i; j++)
                pthread_rwlock_destroy(&m->buckets[j].rwlock);
            free(m->buckets);
            free(m);
            return NULL;
        }
    }
    return m;
}

void acc_sub_map_destroy(acc_sub_map_t *m)
{
    if (m == NULL)
        return;
    for (size_t i = 0; i < m->nbuckets; i++) {
        ACC_LIST_FOR_EACH_SAFE(pos, n, &m->buckets[i].filters) {
            struct sub_filter *f = ACC_LIST_ENTRY(pos, struct sub_filter,
                                                  bucket_q);
            acc_list_del(&f->bucket_q);
            filter_free_conns(f);
            free(f);
        }
        pthread_rwlock_destroy(&m->buckets[i].rwlock);
    }
    free(m->buckets);
    free(m);
}

int acc_topic_filter_valid(const char *filter)
{
    const char *p;

    if (filter == NULL || filter[0] == '\0')
        return 0;
    p = filter;
    for (;;) {
        const char *slash = strchr(p, '/');
        size_t len = slash ? (size_t)(slash - p) : strlen(p);
        int last = (slash == NULL);

        /* '#' 只能独占末层：层级内含 '#' 须整层恰为 "#" 且后无更多层 */
        if (memchr(p, '#', len) != NULL) {
            if (len != 1 || p[0] != '#' || !last)
                return 0;
        }
        /* '+' 须独占单层：层级内含 '+' 须整层恰为 "+" */
        if (memchr(p, '+', len) != NULL) {
            if (len != 1 || p[0] != '+')
                return 0;
        }
        if (last)
            return 1;
        p = slash + 1;
    }
}

int acc_topic_match(const char *filter, const char *topic)
{
    const char *f, *t;

    if (filter == NULL || topic == NULL)
        return 0;
    f = filter;
    t = topic;
    for (;;) {
        const char *fs = strchr(f, '/');
        const char *ts = strchr(t, '/');
        size_t fl = fs ? (size_t)(fs - f) : strlen(f);
        size_t tl = ts ? (size_t)(ts - t) : strlen(t);

        if (fl == 1 && f[0] == '#')
            return 1; /* '#' 匹配剩余全部层级（含空、含父层） */
        if (fl == 1 && f[0] == '+') {
            /* '+' 恒匹配任意单层（含空层），放行 */
        } else if (fl != tl || memcmp(f, t, fl) != 0) {
            return 0; /* 字面量层级须长度相等且逐字节相同 */
        }
        if (fs == NULL && ts == NULL)
            return 1; /* 双方层级同时耗尽 */
        if (fs == NULL)
            return 0; /* filter 已尽 topic 未尽（'#' 已提前返回） */
        if (ts == NULL) {
            /* topic 已尽：filter 剩余恰为 "/#" 时 '#' 匹配父层（含空） */
            if (fs[1] == '#' && fs[2] == '\0')
                return 1;
            return 0;
        }
        f = fs + 1;
        t = ts + 1;
    }
}

int acc_sub_map_subscribe(acc_sub_map_t *m, const char *filter,
                          acc_connection_t *c)
{
    struct acc_sub_bucket *b;
    struct sub_filter *f;
    int ret = -1;

    if (m == NULL || filter == NULL || c == NULL)
        return -1;
    if (strlen(filter) > ACC_MQTT_TOPIC_MAX)
        return -1; /* 节点缓冲上限 256 字节 */
    if (!acc_topic_filter_valid(filter))
        return -1;

    b = map_bucket(m, filter);
    pthread_rwlock_wrlock(&b->rwlock);
    f = bucket_find(b, filter);
    if (f == NULL) {
        f = calloc(1, sizeof(*f));
        if (f != NULL) {
            acc_strlcpy(f->filter, filter, sizeof(f->filter));
            acc_list_init(&f->conns);
            acc_list_add_tail(&f->bucket_q, &b->filters);
        }
    }
    if (f != NULL) {
        if (filter_find_conn(f, c) != NULL) {
            ret = 0; /* 重复订阅幂等 */
        } else {
            struct sub_conn *sc = calloc(1, sizeof(*sc));

            if (sc != NULL) {
                sc->c = c;
                acc_list_add_tail(&sc->q, &f->conns);
                f->nconns++;
                ret = 0;
            }
        }
    }
    pthread_rwlock_unlock(&b->rwlock);
    return ret;
}

int acc_sub_map_unsubscribe(acc_sub_map_t *m, const char *filter,
                            acc_connection_t *c)
{
    struct acc_sub_bucket *b;
    struct sub_filter *f;
    struct sub_conn *sc;
    int ret = -1;

    if (m == NULL || filter == NULL || c == NULL)
        return -1;

    b = map_bucket(m, filter);
    pthread_rwlock_wrlock(&b->rwlock);
    f = bucket_find(b, filter);
    if (f != NULL) {
        sc = filter_find_conn(f, c);
        if (sc != NULL) {
            acc_list_del(&sc->q);
            free(sc);
            f->nconns--;
            if (f->nconns == 0) { /* 空 filter 节点摘除释放 */
                acc_list_del(&f->bucket_q);
                filter_free_conns(f);
                free(f);
            }
            ret = 0;
        }
    }
    pthread_rwlock_unlock(&b->rwlock);
    return ret;
}

void acc_sub_map_del_conn(acc_sub_map_t *m, acc_connection_t *c)
{
    if (m == NULL || c == NULL)
        return;

    for (size_t i = 0; i < m->nbuckets; i++) {
        struct acc_sub_bucket *b = &m->buckets[i];

        pthread_rwlock_wrlock(&b->rwlock);
        ACC_LIST_FOR_EACH_SAFE(pos, n, &b->filters) {
            struct sub_filter *f = ACC_LIST_ENTRY(pos, struct sub_filter,
                                                  bucket_q);

            ACC_LIST_FOR_EACH_SAFE(cpos, cn, &f->conns) {
                struct sub_conn *sc = ACC_LIST_ENTRY(cpos, struct sub_conn,
                                                     q);
                if (sc->c == c) {
                    acc_list_del(&sc->q);
                    free(sc);
                    f->nconns--;
                }
            }
            if (f->nconns == 0) { /* 空 filter 节点摘除释放 */
                acc_list_del(&f->bucket_q);
                filter_free_conns(f);
                free(f);
            }
        }
        pthread_rwlock_unlock(&b->rwlock);
    }
}

size_t acc_sub_map_forward_each(acc_sub_map_t *m, const char *topic,
                                int (*cb)(acc_connection_t *c, void *arg),
                                void *arg)
{
    size_t hits = 0;
    int stopped = 0;

    if (m == NULL || topic == NULL || cb == NULL)
        return 0;

    for (size_t i = 0; i < m->nbuckets && !stopped; i++) {
        struct acc_sub_bucket *b = &m->buckets[i];

        pthread_rwlock_rdlock(&b->rwlock);
        ACC_LIST_FOR_EACH(pos, &b->filters) {
            struct sub_filter *f = ACC_LIST_ENTRY(pos, struct sub_filter,
                                                  bucket_q);

            if (!acc_topic_match(f->filter, topic))
                continue;
            ACC_LIST_FOR_EACH(cpos, &f->conns) {
                struct sub_conn *sc = ACC_LIST_ENTRY(cpos, struct sub_conn,
                                                     q);
                hits++;
                if (cb(sc->c, arg) != 0) { /* 非 0 终止本桶并停止后续桶 */
                    stopped = 1;
                    break;
                }
            }
            if (stopped)
                break;
        }
        pthread_rwlock_unlock(&b->rwlock);
    }
    return hits;
}

size_t acc_sub_map_conn_subs(const acc_sub_map_t *m,
                             const acc_connection_t *c)
{
    size_t count = 0;

    if (m == NULL || c == NULL)
        return 0;

    for (size_t i = 0; i < m->nbuckets; i++) {
        /* 只读遍历；rwlock 无 const 入参，去 const 加读锁 */
        struct acc_sub_bucket *b = &((acc_sub_map_t *)m)->buckets[i];

        pthread_rwlock_rdlock(&b->rwlock);
        ACC_LIST_FOR_EACH(pos, &b->filters) {
            struct sub_filter *f = ACC_LIST_ENTRY(pos, struct sub_filter,
                                                  bucket_q);
            count += (filter_find_conn(f, c) != NULL) ? 1 : 0;
        }
        pthread_rwlock_unlock(&b->rwlock);
    }
    return count;
}
