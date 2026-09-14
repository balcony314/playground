#ifndef ACC_LIST_H
#define ACC_LIST_H

#include <stddef.h>

/* 内核式侵入双向循环链表 */
struct acc_list {
    struct acc_list *prev;
    struct acc_list *next;
};

#define ACC_LIST_ENTRY(ptr, type, member) \
    ((type *)((char *)(ptr) - offsetof(type, member)))

#define ACC_LIST_FOR_EACH(pos, head) \
    for (struct acc_list *pos = (head)->next; pos != (head); pos = pos->next)

#define ACC_LIST_FOR_EACH_SAFE(pos, n, head)                        \
    for (struct acc_list *pos = (head)->next, *n = pos->next;       \
         pos != (head); pos = n, n = pos->next)

static inline void acc_list_init(struct acc_list *l)
{
    l->prev = l->next = l;
}

static inline void __acc_list_add(struct acc_list *n, struct acc_list *prev,
                                  struct acc_list *next)
{
    next->prev = n;
    n->next = next;
    n->prev = prev;
    prev->next = n;
}

static inline void acc_list_add(struct acc_list *n, struct acc_list *head)
{
    __acc_list_add(n, head, head->next); /* 头插 */
}

static inline void acc_list_add_tail(struct acc_list *n, struct acc_list *head)
{
    __acc_list_add(n, head->prev, head); /* 尾插 */
}

static inline void acc_list_del(struct acc_list *e)
{
    e->next->prev = e->prev;
    e->prev->next = e->next;
    acc_list_init(e);
}

static inline int acc_list_empty(const struct acc_list *head)
{
    return head->next == head;
}

#endif /* ACC_LIST_H */
