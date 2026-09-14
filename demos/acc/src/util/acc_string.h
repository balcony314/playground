#ifndef ACC_STRING_H
#define ACC_STRING_H

#include <stddef.h>

size_t acc_strlcpy(char *dst, const char *src, size_t n);
size_t acc_strlcat(char *dst, const char *src, size_t n);
int acc_strcaseeq(const char *a, const char *b);
long long acc_strtoll_def(const char *s, long long dflt);

#endif /* ACC_STRING_H */
