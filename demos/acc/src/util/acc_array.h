#ifndef ACC_ARRAY_H
#define ACC_ARRAY_H

#include <stddef.h>

/* 动态数组：elts 指向连续元素存储，size 为单元素字节数 */
typedef struct {
    void *elts;   /* 元素存储区 */
    size_t nelts; /* 已用元素数 */
    size_t nalloc; /* 已分配元素数 */
    size_t size;  /* 单元素字节数 */
} acc_array_t;

/* 预分配 n 个 size 字节的元素；n 或 size 为 0、乘法溢出时返回 NULL */
acc_array_t *acc_array_create(size_t n, size_t size);

/* 释放数组及其存储区，允许 NULL 入参 */
void acc_array_destroy(acc_array_t *a);

/* 追加一个元素并返回新槽位；容量满时按 2 倍扩容，分配失败返回 NULL */
void *acc_array_push(acc_array_t *a);

#endif /* ACC_ARRAY_H */
