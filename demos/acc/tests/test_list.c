#include "acc_test.h"
#include "acc_list.h"

typedef struct {
    int v;
    struct acc_list node;
} item_t;

ACC_TEST(list_add_del_roundtrip)
{
    struct acc_list head;
    item_t a = {1, {0}}, b = {2, {0}};

    acc_list_init(&head);
    ACC_ASSERT(acc_list_empty(&head));
    acc_list_add(&a.node, &head);
    acc_list_add(&b.node, &head);
    ACC_ASSERT(!acc_list_empty(&head));

    item_t *first = ACC_LIST_ENTRY(head.next, item_t, node);
    ACC_ASSERT_EQ(first->v, 2); /* add 头插 */
    item_t *last = ACC_LIST_ENTRY(head.prev, item_t, node);
    ACC_ASSERT_EQ(last->v, 1);

    acc_list_del(&a.node);
    acc_list_del(&b.node);
    ACC_ASSERT(acc_list_empty(&head));
}

ACC_TEST(list_add_tail_order)
{
    struct acc_list head;
    item_t a = {1, {0}}, b = {2, {0}};

    acc_list_init(&head);
    acc_list_add_tail(&a.node, &head);
    acc_list_add_tail(&b.node, &head);
    int seen[2] = {0}, i = 0;
    ACC_LIST_FOR_EACH(pos, &head)
    {
        seen[i++] = ACC_LIST_ENTRY(pos, item_t, node)->v;
    }
    ACC_ASSERT_EQ(seen[0], 1);
    ACC_ASSERT_EQ(seen[1], 2);
}

ACC_TEST_MAIN();
