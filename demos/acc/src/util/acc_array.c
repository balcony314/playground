#include "acc_array.h"

#include <stdint.h>
#include <stdlib.h>

/* 创建数组：一次性分配控制块与 n 个元素，失败返回 NULL */
acc_array_t *acc_array_create(size_t n, size_t size)
{
    if (n == 0 || size == 0 || n > SIZE_MAX / size)
        return NULL;

    acc_array_t *a = malloc(sizeof(*a));
    if (a == NULL)
        return NULL;

    a->elts = malloc(n * size);
    if (a->elts == NULL) {
        free(a);
        return NULL;
    }

    a->nelts = 0;
    a->nalloc = n;
    a->size = size;
    return a;
}

/* 释放存储区与控制块 */
void acc_array_destroy(acc_array_t *a)
{
    if (a == NULL)
        return;
    free(a->elts);
    free(a);
}

/* 追加元素：有空位直接占用；否则 2 倍扩容，realloc 失败保持原状并返回 NULL */
void *acc_array_push(acc_array_t *a)
{
    if (a == NULL)
        return NULL;

    if (a->nelts == a->nalloc) {
        /* 容量翻倍溢出时直接拒绝，避免无效的 realloc 调用（先除 size 防中间值回绕） */
        if (a->nalloc > SIZE_MAX / a->size / 2)
            return NULL;
        /* 先存临时变量，realloc 失败时不覆盖 elts，旧数据不泄漏不失效 */
        void *elts = realloc(a->elts, a->nalloc * 2 * a->size);
        if (elts == NULL)
            return NULL;
        a->elts = elts;
        a->nalloc *= 2;
    }

    char *base = a->elts;
    void *slot = base + a->nelts * a->size;
    a->nelts++;
    return slot;
}
