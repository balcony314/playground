#ifndef ACC_TEST_H
#define ACC_TEST_H

#include <inttypes.h>
#include <stdio.h>
#include <string.h>

typedef void (*acc_test_fn)(void);
typedef struct acc_test_case {
    const char *name;
    acc_test_fn fn;
    struct acc_test_case *next;
} acc_test_case_t;

static acc_test_case_t *acc_test_head = NULL;
static int acc_test_failed = 0;

#define ACC_TEST(fn)                                                        \
    static void fn(void);                                                   \
    static void acc_test_reg_##fn(void);                                    \
    __attribute__((constructor)) static void acc_test_reg_##fn(void)        \
    {                                                                       \
        static acc_test_case_t c = {#fn, fn, NULL};                         \
        c.next = acc_test_head;                                             \
        acc_test_head = &c;                                                 \
    }                                                                       \
    static void fn(void)

#define ACC_ASSERT(cond)                                                    \
    do {                                                                    \
        if (!(cond)) {                                                      \
            fprintf(stderr, "  FAIL %s:%d: %s\n", __FILE__, __LINE__, #cond); \
            acc_test_failed = 1;                                            \
            return;                                                         \
        }                                                                   \
    } while (0)

#define ACC_ASSERT_EQ(a, b)                                                 \
    do {                                                                    \
        int64_t _va = (int64_t)(a), _vb = (int64_t)(b);                     \
        if (_va != _vb) {                                                   \
            fprintf(stderr, "  FAIL %s:%d: %" PRId64 " != %" PRId64 "\n",   \
                    __FILE__, __LINE__, _va, _vb);                          \
            acc_test_failed = 1;                                            \
            return;                                                         \
        }                                                                   \
    } while (0)

#define ACC_ASSERT_STR_EQ(a, b)                                             \
    do {                                                                    \
        if (strcmp((a), (b)) != 0) {                                        \
            fprintf(stderr, "  FAIL %s:%d: \"%s\" != \"%s\"\n",             \
                    __FILE__, __LINE__, (a), (b));                          \
            acc_test_failed = 1;                                            \
            return;                                                         \
        }                                                                   \
    } while (0)

#define ACC_TEST_MAIN()                                                     \
    int main(void)                                                          \
    {                                                                       \
        int total = 0, failed = 0;                                          \
        for (acc_test_case_t *c = acc_test_head; c; c = c->next) {          \
            total++;                                                        \
            acc_test_failed = 0;                                            \
            fprintf(stderr, "== %s\n", c->name);                            \
            c->fn();                                                        \
            if (acc_test_failed)                                            \
                failed++;                                                   \
        }                                                                   \
        fprintf(stderr, "%d tests, %d failed\n", total, failed);            \
        return failed ? 1 : 0;                                              \
    }

#endif /* ACC_TEST_H */
