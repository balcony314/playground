/* 表驱动配置解析：默认值回填、类型解析器、include 递归展开 */
#include "acc_conf.h"

#include <ctype.h>
#include <errno.h>
#include <limits.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "acc_string.h"

#define ACC_CONF_CMD_MAX 18 /* 命令表项数：与 init_cmds 中登记项保持一致 */
#define ACC_CONF_LINE_MAX 1024

/* 严格十进制整数解析：整串消费、无溢出才成功（语义同 acc_strtoll_def，但显式区分错误，
 * 避免“默认值哨兵”与合法值重叠导致的误判） */
static int parse_ll_strict(const char *s, long long *out)
{
    char *end = NULL;

    if (s == NULL || *s == '\0')
        return -1;
    errno = 0;
    long long v = strtoll(s, &end, 10);
    if (errno == ERANGE || end == s || *end != '\0')
        return -1;
    *out = v;
    return 0;
}

/* 从 s 提取可选符号+数字前缀存入 buf，返回单位后缀起始指针；格式非法返回 NULL */
static const char *split_num_prefix(const char *s, char *buf, size_t cap)
{
    size_t n = 0;

    if (s == NULL)
        return NULL;
    if (*s == '+' || *s == '-') {
        if (cap < 2)
            return NULL;
        buf[n++] = *s++;
    }
    while (isdigit((unsigned char)*s)) {
        if (n + 1 >= cap)
            return NULL; /* 数字位数过多，必然溢出 */
        buf[n++] = *s++;
    }
    buf[n] = '\0';
    if (n == 0 || (n == 1 && (buf[0] == '+' || buf[0] == '-')))
        return NULL; /* 只有符号没有数字 */
    return s;
}

int acc_conf_parse_int(const char *v, void *addr, int cap)
{
    long long n = 0;

    (void)cap;
    if (parse_ll_strict(v, &n) != 0 || n < INT_MIN || n > INT_MAX)
        return -1;
    if (addr != NULL)
        *(int *)addr = (int)n;
    return 0;
}

int acc_conf_parse_bool(const char *v, void *addr, int cap)
{
    int val;

    (void)cap;
    if (v == NULL)
        return -1;
    if (acc_strcaseeq(v, "yes") || acc_strcaseeq(v, "true") || acc_strcaseeq(v, "on"))
        val = 1;
    else if (acc_strcaseeq(v, "no") || acc_strcaseeq(v, "false") || acc_strcaseeq(v, "off"))
        val = 0;
    else
        return -1;
    if (addr != NULL)
        *(int *)addr = val;
    return 0;
}

int acc_conf_parse_string(const char *v, void *addr, int cap)
{
    if (v == NULL)
        return -1;
    if (addr == NULL)
        return 0; /* 字符串无更多格式约束，仅校验即通过 */
    if (cap <= 0)
        return -1;
    if (acc_strlcpy((char *)addr, v, (size_t)cap) >= (size_t)cap)
        return -1; /* 值超出缓冲区，拒绝静默截断 */
    return 0;
}

int acc_conf_parse_memsize(const char *v, void *addr, int cap)
{
    char numbuf[32];
    const char *unit = NULL;
    unsigned long long mult = 1;
    long long n = 0;

    (void)cap;
    unit = split_num_prefix(v, numbuf, sizeof(numbuf));
    if (unit == NULL || parse_ll_strict(numbuf, &n) != 0 || n < 0)
        return -1;
    if (acc_strcaseeq(unit, "k"))
        mult = 1024ULL;
    else if (acc_strcaseeq(unit, "m"))
        mult = 1024ULL * 1024;
    else if (acc_strcaseeq(unit, "g"))
        mult = 1024ULL * 1024 * 1024;
    else if (*unit != '\0')
        return -1; /* 未知单位 */
    if ((unsigned long long)n > UINT_MAX / mult)
        return -1; /* 乘以倍率后必超 unsigned 存储范围 */
    if (addr != NULL)
        *(unsigned *)addr = (unsigned)((unsigned long long)n * mult);
    return 0;
}

int acc_conf_parse_time(const char *v, void *addr, int cap)
{
    char numbuf[32];
    const char *unit = NULL;
    long long mult = 1;
    long long n = 0;

    (void)cap;
    unit = split_num_prefix(v, numbuf, sizeof(numbuf));
    if (unit == NULL || parse_ll_strict(numbuf, &n) != 0 || n < 0)
        return -1;
    if (acc_strcaseeq(unit, "s") || *unit == '\0') /* 裸数字按秒 */
        mult = 1;
    else if (acc_strcaseeq(unit, "m"))
        mult = 60;
    else if (acc_strcaseeq(unit, "h"))
        mult = 3600;
    else
        return -1; /* 未知单位或复合形式（如 1h30m 只识别出 h30m） */
    if (n > INT_MAX / mult)
        return -1; /* 换算后必超 int 范围 */
    if (addr != NULL)
        *(int *)addr = (int)(n * mult);
    return 0;
}

void acc_conf_init_cmds(const acc_conf_t *conf, acc_conf_cmd_t *cmds, int *ncmds)
{
    int n = 0;

/* 登记一项：addr 显式去 const 仅用于写入指向字段（load 前提下安全），cap 自动取字段大小 */
#define ACC_CMD_ENTRY(key, fn, field, dfltstr)                                  \
    do {                                                                        \
        cmds[n].name = (key);                                                   \
        cmds[n].parse = (fn);                                                   \
        cmds[n].addr = (void *)&((acc_conf_t *)conf)->field;                    \
        cmds[n].cap = (int)sizeof(((acc_conf_t *)0)->field);                    \
        cmds[n].dflt = (dfltstr);                                               \
        n++;                                                                    \
    } while (0)

    ACC_CMD_ENTRY("log_level", acc_conf_parse_int, log_level, "0");
    ACC_CMD_ENTRY("log_path", acc_conf_parse_string, log_path, "./logs/");
    ACC_CMD_ENTRY("enable_daemon", acc_conf_parse_bool, enable_daemon, "no");
    ACC_CMD_ENTRY("worker_process", acc_conf_parse_int, worker_process, "2");
    ACC_CMD_ENTRY("connections_per_worker", acc_conf_parse_int, connections_per_worker, "1024");
    ACC_CMD_ENTRY("server", acc_conf_parse_string, server, "127.0.0.1");
    ACC_CMD_ENTRY("tcp_port", acc_conf_parse_int, tcp_port, "1883");
    ACC_CMD_ENTRY("tls_port", acc_conf_parse_int, tls_port, "8883");
    ACC_CMD_ENTRY("cert_file", acc_conf_parse_string, cert_file, "conf/cert.pem");
    ACC_CMD_ENTRY("key_file", acc_conf_parse_string, key_file, "conf/key.pem");
    ACC_CMD_ENTRY("accept_mutex", acc_conf_parse_bool, accept_mutex, "no");
    ACC_CMD_ENTRY("accept_mutex_delay_ms", acc_conf_parse_int, accept_mutex_delay_ms, "500");
    ACC_CMD_ENTRY("client_timeout", acc_conf_parse_time, client_timeout, "300");
    ACC_CMD_ENTRY("max_read_buffer_size", acc_conf_parse_memsize, max_read_buffer_size, "1048576");
    ACC_CMD_ENTRY("max_open_fd", acc_conf_parse_int, max_open_fd, "65536");
    ACC_CMD_ENTRY("thread_pool_thread_num", acc_conf_parse_int, thread_pool_thread_num, "4");
    ACC_CMD_ENTRY("thread_pool_queue_size", acc_conf_parse_int, thread_pool_queue_size, "1000");
    ACC_CMD_ENTRY("metrics_dir", acc_conf_parse_string, metrics_dir, "./metrics/");

    *ncmds = n;
#undef ACC_CMD_ENTRY
}

/* 按命令表回填默认值：默认值文本统一走 parse 回调，保证与文件解析同一套校验 */
static void conf_defaults(acc_conf_t *conf)
{
    acc_conf_cmd_t cmds[ACC_CONF_CMD_MAX];
    int ncmds = 0;

    acc_conf_init_cmds(conf, cmds, &ncmds);
    for (int i = 0; i < ncmds; i++)
        cmds[i].parse(cmds[i].dflt, cmds[i].addr, cmds[i].cap);
}

/* 取 file 所在目录（realpath 规范化），失败或异常时退回 "." */
static void file_dir_of(const char *file, char *dir, size_t cap)
{
    char resolved[PATH_MAX];
    char *slash = NULL;

    acc_strlcpy(dir, ".", cap);
    if (realpath(file, resolved) == NULL)
        return;
    slash = strrchr(resolved, '/');
    if (slash == NULL)
        return;
    if (slash == resolved)
        resolved[1] = '\0'; /* 文件位于根目录：保留 "/" */
    else
        *slash = '\0';
    acc_strlcpy(dir, resolved, cap);
}

static int parse_file(acc_conf_t *conf, const char *file, int depth);

/* 处理单行：跳过注释/节标记，切分 key/value 后查表解析；include 相对当前文件目录递归 */
static int handle_line(acc_conf_t *conf, const acc_conf_cmd_t *cmds, int ncmds,
                       const char *file, int lineno, const char *dir,
                       int depth, char *line)
{
    char *key = line;
    char *val = NULL;
    size_t vlen = 0;

    while (isspace((unsigned char)*key))
        key++;
    if (*key == '\0' || *key == '#' || *key == '[')
        return 0; /* 空行、注释、节标记一律跳过 */

    val = key;
    while (*val != '\0' && !isspace((unsigned char)*val))
        val++;
    if (*val == '\0') {
        fprintf(stderr, "acc_conf: %s:%d: 配置项 \"%s\" 缺少取值\n", file, lineno, key);
        return -1;
    }
    *val++ = '\0'; /* 在首个空白处切分 key 与 value */
    while (isspace((unsigned char)*val))
        val++;
    vlen = strlen(val);
    while (vlen > 0 && isspace((unsigned char)val[vlen - 1]))
        val[--vlen] = '\0'; /* 去掉值尾部空白（含换行） */

    if (strcmp(key, "include") == 0) {
        char incpath[ACC_CONF_PATH_MAX * 2];
        if (val[0] == '/')
            acc_strlcpy(incpath, val, sizeof(incpath));
        else
            snprintf(incpath, sizeof(incpath), "%s/%s", dir, val);
        return parse_file(conf, incpath, depth + 1);
    }

    for (int i = 0; i < ncmds; i++) {
        if (strcmp(cmds[i].name, key) != 0)
            continue;
        if (cmds[i].parse(val, cmds[i].addr, cmds[i].cap) != 0) {
            fprintf(stderr, "acc_conf: %s:%d: 配置项 \"%s\" 的值 \"%s\" 非法\n",
                    file, lineno, key, val);
            return -1;
        }
        return 0;
    }
    fprintf(stderr, "acc_conf: %s:%d: 未知配置项 \"%s\"\n", file, lineno, key);
    return -1;
}

/* 解析单个文件：depth 为 include 层级，主文件为 0 */
static int parse_file(acc_conf_t *conf, const char *file, int depth)
{
    FILE *fp = NULL;
    acc_conf_cmd_t cmds[ACC_CONF_CMD_MAX];
    char dir[ACC_CONF_PATH_MAX];
    char line[ACC_CONF_LINE_MAX];
    int ncmds = 0, lineno = 0, ret = 0;

    if (depth > ACC_CONF_INCLUDE_MAX_DEPTH) {
        fprintf(stderr, "acc_conf: include 嵌套深度超过 %d: %s\n",
                ACC_CONF_INCLUDE_MAX_DEPTH, file);
        return -1;
    }
    fp = fopen(file, "r");
    if (fp == NULL) {
        fprintf(stderr, "acc_conf: 无法打开配置文件 %s\n", file);
        return -1;
    }
    file_dir_of(file, dir, sizeof(dir));
    acc_conf_init_cmds(conf, cmds, &ncmds);

    while (fgets(line, sizeof(line), fp) != NULL) {
        lineno++;
        if (handle_line(conf, cmds, ncmds, file, lineno, dir, depth, line) != 0) {
            ret = -1;
            break;
        }
    }
    fclose(fp);
    return ret;
}

int acc_conf_load(acc_conf_t *conf, const char *file)
{
    if (conf == NULL || file == NULL)
        return -1;
    conf_defaults(conf);
    return parse_file(conf, file, 0);
}
