#include "acc_test.h"
#include "acc_mqtt_router.h"

static int dummy_conn_a, dummy_conn_b; /* 指针只做身份比较 */

ACC_TEST(topic_match_matrix)
{
    struct { const char *f, *t; int want; } cases[] = {
        {"a/b", "a/b", 1},
        {"a/b", "a/b/c", 0},
        {"a/+/c", "a/b/c", 1},
        {"a/+/c", "a/b/d", 0},
        {"a/+", "a/b", 1},
        {"a/+", "a", 0},
        {"a/+", "a/b/c", 0},
        {"#", "a/b/c", 1},
        {"#", "a", 1},
        {"#", "", 1},      /* '#' 匹配空 topic 也成立 */
        {"a/#", "a", 1},   /* 'a/#' 匹配父层 a */
        {"a/#", "a/b/c", 1},
        {"a/#", "b", 0},
        {"+/b", "a/b", 1},
        {"+/+", "a/b", 1},
        {"+/+", "a", 0},
        {"a/b", "a/+", 0}, /* 发布 topic 不允许通配符 */
        {"", "a", 0},
    };
    for (size_t i = 0; i < sizeof(cases) / sizeof(cases[0]); i++)
        ACC_ASSERT_EQ(acc_topic_match(cases[i].f, cases[i].t), cases[i].want);
}

ACC_TEST(filter_valid)
{
    ACC_ASSERT_EQ(acc_topic_filter_valid("a/b"), 1);
    ACC_ASSERT_EQ(acc_topic_filter_valid("a/+/b"), 1);
    ACC_ASSERT_EQ(acc_topic_filter_valid("a/#"), 1);
    ACC_ASSERT_EQ(acc_topic_filter_valid("#"), 1);
    ACC_ASSERT_EQ(acc_topic_filter_valid("a#"), 0);
    ACC_ASSERT_EQ(acc_topic_filter_valid("a/#/b"), 0);
    ACC_ASSERT_EQ(acc_topic_filter_valid("a+"), 0);
    ACC_ASSERT_EQ(acc_topic_filter_valid(""), 0);
    ACC_ASSERT_EQ(acc_topic_filter_valid(NULL), 0);
}

static int forward_count = 0;
static int count_cb(acc_connection_t *c, void *arg)
{
    (void)c;
    forward_count += (int)(intptr_t)arg;
    return 0;
}

ACC_TEST(sub_unsub_forward)
{
    acc_sub_map_t *m = acc_sub_map_create(16);
    ACC_ASSERT(m != NULL);
    ACC_ASSERT_EQ(acc_sub_map_subscribe(m, "a/+", (acc_connection_t *)&dummy_conn_a), 0);
    ACC_ASSERT_EQ(acc_sub_map_subscribe(m, "a/+", (acc_connection_t *)&dummy_conn_a), 0); /* 幂等 */
    ACC_ASSERT_EQ(acc_sub_map_subscribe(m, "#", (acc_connection_t *)&dummy_conn_b), 0);
    ACC_ASSERT_EQ(acc_sub_map_conn_subs(m, (acc_connection_t *)&dummy_conn_a), 1);
    ACC_ASSERT_EQ(acc_sub_map_conn_subs(m, (acc_connection_t *)&dummy_conn_b), 1);

    forward_count = 0;
    size_t hits = acc_sub_map_forward_each(m, "a/b", count_cb, (void *)(intptr_t)1);
    ACC_ASSERT_EQ(hits, 2); /* a/+ 与 # 都命中 */
    ACC_ASSERT_EQ(forward_count, 2);

    ACC_ASSERT_EQ(acc_sub_map_unsubscribe(m, "a/+", (acc_connection_t *)&dummy_conn_a), 0);
    hits = acc_sub_map_forward_each(m, "a/b", count_cb, (void *)(intptr_t)1);
    ACC_ASSERT_EQ(hits, 1);

    acc_sub_map_del_conn(m, (acc_connection_t *)&dummy_conn_b);
    hits = acc_sub_map_forward_each(m, "a/b", count_cb, (void *)(intptr_t)1);
    ACC_ASSERT_EQ(hits, 0);

    acc_sub_map_destroy(m);
}

ACC_TEST_MAIN();
