#include "acc_string.h"

#include <ctype.h>
#include <errno.h>
#include <stdlib.h>

/* BSD strlcpy：最多拷贝 n-1 字节并保证 NUL 结尾，返回 src 全长 */
size_t acc_strlcpy(char *dst, const char *src, size_t n)
{
    size_t srclen = 0;

    while (src[srclen] != '\0')
        srclen++;

    if (n > 0) {
        size_t i = 0;
        for (; i < n - 1 && src[i] != '\0'; i++)
            dst[i] = src[i];
        dst[i] = '\0';
    }
    return srclen;
}

/* BSD strlcat：追加 src 到 dst（缓冲区总长 n），返回初始 dst 长度 + src 全长 */
size_t acc_strlcat(char *dst, const char *src, size_t n)
{
    size_t dlen = 0, slen = 0, i = 0;

    while (dlen < n && dst[dlen] != '\0')
        dlen++;

    while (src[slen] != '\0')
        slen++;

    if (dlen == n) /* dst 未以 NUL 结尾或已满，无安全追加点 */
        return n + slen;

    for (; dlen + i < n - 1 && src[i] != '\0'; i++)
        dst[dlen + i] = src[i];
    dst[dlen + i] = '\0';

    return dlen + slen;
}

/* 忽略大小写逐字符比较，相等返回 1，否则返回 0 */
int acc_strcaseeq(const char *a, const char *b)
{
    while (*a != '\0' && *b != '\0') {
        if (tolower((unsigned char)*a) != tolower((unsigned char)*b))
            return 0;
        a++;
        b++;
    }
    return *a == *b;
}

/* 十进制转 long long：要求全串合法消费，失败或溢出返回 dflt */
long long acc_strtoll_def(const char *s, long long dflt)
{
    char *end = NULL;

    if (s == NULL || *s == '\0')
        return dflt;

    errno = 0;
    long long v = strtoll(s, &end, 10);
    if (errno == ERANGE || end == s || *end != '\0')
        return dflt;
    return v;
}
