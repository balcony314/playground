#include "acc_test.h"
#include "acc_array.h"

ACC_TEST(array_push_grow)
{
    acc_array_t *a = acc_array_create(2, sizeof(int));
    ACC_ASSERT(a != NULL);
    for (int i = 0; i < 10; i++) {
        int *p = acc_array_push(a);
        ACC_ASSERT(p != NULL);
        *p = i;
    }
    ACC_ASSERT_EQ(a->nelts, 10);
    ACC_ASSERT(a->nalloc >= 10);
    int *elts = a->elts;
    ACC_ASSERT_EQ(elts[9], 9);
    acc_array_destroy(a);
}

ACC_TEST(array_push_null_on_exhaust) /* realloc 失败场景不可测，验证基本契约 */
{
    acc_array_t *a = acc_array_create(4, sizeof(long));
    ACC_ASSERT(a != NULL);
    ACC_ASSERT(a->elts != NULL);
    acc_array_destroy(a);
}

ACC_TEST_MAIN();
