# redis 源码 内部数据结构
##  redis - 动态字符串

### sdshdr 数据结构
```c
struct sdshdr {

    // buf 已占用长度
    int len;

    // buf 剩余可用长度
    int free;

    // 实际保存字符串数据的地方
    // 利用c99(C99 specification 6.7.2.1.16)中引入的 flexible array member,通过buf来引用sdshdr后面的地址，
    // 详情google "flexible array member"
    char buf[];
};
```

### buf 进行扩展策略
```c
sds sdsMakeRoomFor(
    sds s,
    size_t addlen   // 需要增加的空间长度
) 
{
    struct sdshdr *sh, *newsh;
    size_t free = sdsavail(s);
    size_t len, newlen;

    // 剩余空间可以满足需求，无须扩展
    if (free >= addlen) return s;

    sh = (void*) (s-(sizeof(struct sdshdr)));

    // 目前 buf 长度
    len = sdslen(s);
    // 新 buf 长度
    newlen = (len+addlen);
    // 如果新 buf 长度小于 SDS_MAX_PREALLOC 长度
    // 那么将 buf 的长度设为新 buf 长度的两倍
    if (newlen < SDS_MAX_PREALLOC)
        newlen *= 2;
    else
        newlen += SDS_MAX_PREALLOC;

    // 扩展长度
    newsh = zrealloc(sh, sizeof(struct sdshdr)+newlen+1);

    if (newsh == NULL) return NULL;

    newsh->free = newlen - len;

    return newsh->buf;
}
```
***
## redis - 双端列表 
*Node：Redis 列表使用两种数据结构作为底层实现：*

1. 双端队列
2. 压缩队列

### 双端链表的实现
```c
/*
 * 链表节点
 */
typedef struct listNode {

    // 前驱节点
    struct listNode *prev;

    // 后继节点
    struct listNode *next;

    // 值
    void *value;

} listNode;

/*
 * 链表迭代器
 */
typedef struct listIter {

    // 下一节点
    listNode *next;

    // 迭代方向
    int direction;

} listIter;

/*
 * 链表
 */
typedef struct list {

    // 表头指针
    listNode *head;

    // 表尾指针
    listNode *tail;

    // 节点数量
    unsigned long len;

    // 复制函数
    void *(*dup)(void *ptr);
    // 释放函数
    void (*free)(void *ptr);
    // 比对函数
    int (*match)(void *ptr, void *key);
} list;
```
***
## redis - 字典
Redis 的 Hash 类型键使用以下两种数据结构作为底层实现:
1. 字典
2. 压缩列表

### 字典 
```c
/*
 * 字典
 *
 * 每个字典使用两个哈希表，用于实现渐进式 rehash
 */
typedef struct dict {

    // 特定于类型的处理函数
    dictType *type;

    // 类型处理函数的私有数据
    void *privdata;

    // 哈希表（2个）
    dictht ht[2];       

    // 记录 rehash 进度的标志，值为-1 表示 rehash 未进行
    int rehashidx;

    // 当前正在运作的安全迭代器数量
    int iterators;      

} dict;
```
### 处理函数
```c
/*
 * 特定于类型的一簇处理函数
 */
typedef struct dictType {
    // 计算键的哈希值函数, 计算key在hash table中的存储位置，不同的dict可以有不同的hash function.
    unsigned int (*hashFunction)(const void *key);
    // 复制键的函数
    void *(*keyDup)(void *privdata, const void *key);
    // 复制值的函数
    void *(*valDup)(void *privdata, const void *obj);
    // 对比两个键的函数
    int (*keyCompare)(void *privdata, const void *key1, const void *key2);
    // 键的释构函数
    void (*keyDestructor)(void *privdata, void *key);
    // 值的释构函数
    void (*valDestructor)(void *privdata, void *obj);
} dictType
```
### 哈希表
```c
/*
 * 哈希表
 */
typedef struct dictht {

    // 哈希表节点指针数组（俗称桶，bucket）
    dictEntry **table;      

    // 指针数组的大小
    unsigned long size;     

    // 指针数组的长度掩码，用于计算索引值
    unsigned long sizemask; 

    // 哈希表现有的节点数量
    unsigned long used;     

} dictht;
```
### 哈希表节点
```c
/*
 * 哈希表节点
 */
typedef struct dictEntry {
    // 键
    void *key;

    // 值
    union {
        void *val;
        uint64_t u64;
        int64_t s64;
    } v;

    // 链往后继节点
    struct dictEntry *next; 

} dictEntry;
```
### 迭代器

```c
/*
 * 字典迭代器
 *
 * 如果 safe 属性的值为 1 ，那么表示这个迭代器是一个安全迭代器。
 * 当安全迭代器正在迭代一个字典时，该字典仍然可以调用 dictAdd 、 dictFind 和其他函数。
 *
 * 如果 safe 属性的值为 0 ，那么表示这不是一个安全迭代器。
 * 如果正在运作的迭代器是不安全迭代器，那么它只可以对字典调用 dictNext 函数。
 */
typedef struct dictIterator {

    // 正在迭代的字典
    dict *d;                

    int table,              // 正在迭代的哈希表的号码（0 或者 1）
        index,              // 正在迭代的哈希表数组的索引
        safe;               // 是否安全？

    dictEntry *entry,       // 当前哈希节点
              *nextEntry;   // 当前哈希节点的后继节点
} dictIterator;
```
### 哈希算法
1.  MurmurHash2 32 bit 算法
2.  djb 算 法

### ReHash
执行过程
1. 创建一个比 ht[0]->table **更大的** ht[1]->table
2. 将 ht[0]->table 中的所有键值对迁移到 ht[1]->table
3. 将原来 ht[0]->table 清空，用 ht[1] 替换 ht[0]

### 收缩
执行过程
1. 创建一个比 ht[0]->table **更小的** ht[1]->table
2. 将 ht[0]->table 中的所有键值对迁移到 ht[1]->table
3. 将原来 ht[0]->table 清空，用 ht[1] 替换 ht[0]
***
## redis - 跳跃表
### 跳跃表
```c
/*
 * 跳跃表
 */
typedef struct zskiplist {
    // 头节点，尾节点
    struct zskiplistNode *header, *tail;
    // 节点数量
    unsigned long length;
    // 目前表内节点的最大层数
    int level;
} zskiplist;
```
### 跳跃表节点
```c
/*
 * 跳跃表节点
 */
typedef struct zskiplistNode {
    // member 对象
    robj *obj;
    // 分值
    double score;
    // 后退指针
    struct zskiplistNode *backward;
    // 层
    struct zskiplistLevel {
        // 前进指针
        struct zskiplistNode *forward;
        // 这个层跨越的节点数量
        unsigned int span;
    } level[];
} zskiplistNode;
```
##  redis - 整数集合
### 升级连续内存空间

## redis - 压缩列表
### ziplist 结构
```
|<---- ziplist header ---->|<---------- entries ------------->|<-end->|
|4 bytes |4 bytes |2 bytes |<------------- ? bytes ---------->| 1 byte|
|zlbytes |zltail  |zllen   |entry1|entry2|entry3|entry4|......|zlend  |
```
### 节点的构成
```
|<---------------- entry --------------->|
+----------------+--------+------+-------+
|pre_entry_length|encodine|length|content|
+----------------+--------+------+-------+
```
