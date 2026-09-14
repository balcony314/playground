/* acc 端到端验收客户端：spawn build/acc（e2e.conf）→ 用 vendored
 * MQTTPacket 组包走完整 MQTT 3.1.1 流程 → SIGTERM 优雅退出并校验退出码。
 *
 * 用例：1 connect/connack rc=0；2 subscribe a/+ → SUBACK granted ≤1；
 * 3 第二连接 publish a/b qos0 → 订阅端收 PUBLISH（topic/payload 校验）；
 * 4 publish qos1 → PUBACK；5 subscribe # → publish x/y/z → 收到；
 * 6 TLS（conf/cert.pem 缺失时 SKIP）在 tls_port 重复 1–3。
 *
 * 约定：须在仓库根目录运行（task e2e 保证），acc 与证书均按 cwd 相对
 * 定位后 realpath 转绝对再 spawn。
 *
 * 切包说明：MQTTPacket_decode 为 getcharfn 传输层签名，不能用于缓冲区
 * 切包，read_packet 自行手写 varint 解码。 */
#include <arpa/inet.h>
#include <errno.h>
#include <limits.h>
#include <netinet/in.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <sys/types.h>
#include <sys/wait.h>
#include <time.h>
#include <unistd.h>

#include <openssl/ssl.h>

#include "MQTTPacket.h" /* 先于其余 MQTT 头：MQTTString/常量定义于此 */
#include "MQTTConnect.h"
#include "MQTTPublish.h"
#include "MQTTSubscribe.h"

/* e2e 端口（与 tests/e2e/e2e.conf 一致） */
#define E2E_TCP_PORT 11883
#define E2E_TLS_PORT 18883
/* 阻塞 socket 收发超时（秒）：防对端异常时用例挂死 */
#define E2E_SOCK_TIMEOUT_SEC 5
/* 端口就绪轮询：50 次 × 100ms（对齐 tests/tls_smoke.sh） */
#define E2E_PORT_RETRY 50
/* 优雅退出窗口：30 次 × 100ms = 3s，超窗 SIGKILL 判 FAIL */
#define E2E_EXIT_RETRY 30
/* 单包缓冲上限（e2e 用例包均 < 512 字节） */
#define E2E_BUF_MAX 2048

/* 连接封装：阻塞 fd + 可选 TLS 层 */
typedef struct {
    int fd;
    SSL_CTX *ctx; /* TLS 连接的客户端上下文（随连接创建/销毁） */
    SSL *ssl;
} e2e_conn_t;

static pid_t g_acc_pid = -1; /* spawn 出的 acc master 进程 */
static int g_passed;         /* 通过用例数（汇总打印） */
static int g_failed;         /* 失败用例数 */

/* 用例结果登记与统一打印 */
static void case_report(int bad, const char *name)
{
    if (bad == 0) {
        g_passed++;
        printf("== %s: PASS\n", name);
    } else {
        g_failed++;
        printf("== %s: FAIL\n", name);
    }
}

static void e2e_sleep_ms(int ms)
{
    struct timespec ts = { ms / 1000, (long)(ms % 1000) * 1000000L };

    nanosleep(&ts, NULL);
}

/* ---------------------------------------------------------------- 传输层 */

/* 精确读 n 字节：不足即失败（超时/对端关闭） */
static int e2e_read(e2e_conn_t *c, void *buf, size_t n)
{
    unsigned char *p = buf;
    size_t got = 0;

    while (got < n) {
        ssize_t r;
        if (c->ssl != NULL) {
            r = SSL_read(c->ssl, p + got, (int)(n - got));
            if (r <= 0)
                return -1;
        } else {
            r = read(c->fd, p + got, n - got);
            if (r <= 0)
                return -1;
        }
        got += (size_t)r;
    }
    return 0;
}

/* 精确写 n 字节 */
static int e2e_write(e2e_conn_t *c, const void *buf, size_t n)
{
    const unsigned char *p = buf;
    size_t sent = 0;

    while (sent < n) {
        ssize_t r;
        if (c->ssl != NULL) {
            r = SSL_write(c->ssl, p + sent, (int)(n - sent));
            if (r <= 0)
                return -1;
        } else {
            r = write(c->fd, p + sent, n - sent);
            if (r <= 0)
                return -1;
        }
        sent += (size_t)r;
    }
    return 0;
}

/* 建立 TCP 连接（带收发超时），use_tls 时完成 SSL 握手（自签证书不校验） */
static int e2e_connect(uint16_t port, int use_tls, e2e_conn_t *c)
{
    struct sockaddr_in sa;
    struct timeval tv = { E2E_SOCK_TIMEOUT_SEC, 0 };

    memset(c, 0, sizeof(*c));
    c->fd = -1;

    memset(&sa, 0, sizeof(sa));
    sa.sin_family = AF_INET;
    sa.sin_port = htons(port);
    sa.sin_addr.s_addr = htonl(INADDR_LOOPBACK);

    int fd = socket(AF_INET, SOCK_STREAM, 0);
    if (fd < 0)
        return -1;
    setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
    setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof(tv));
    if (connect(fd, (struct sockaddr *)&sa, sizeof(sa)) != 0) {
        close(fd);
        return -1;
    }
    c->fd = fd;

    if (use_tls) {
        c->ctx = SSL_CTX_new(TLS_client_method());
        if (c->ctx == NULL)
            goto fail;
        SSL_CTX_set_verify(c->ctx, SSL_VERIFY_NONE, NULL); /* 自签测试证书 */
        c->ssl = SSL_new(c->ctx);
        if (c->ssl == NULL || SSL_set_fd(c->ssl, fd) != 1 ||
            SSL_connect(c->ssl) != 1)
            goto fail;
    }
    return 0;

fail:
    if (c->ssl != NULL) {
        SSL_free(c->ssl);
        c->ssl = NULL;
    }
    if (c->ctx != NULL) {
        SSL_CTX_free(c->ctx);
        c->ctx = NULL;
    }
    close(fd);
    c->fd = -1;
    return -1;
}

static void e2e_close(e2e_conn_t *c)
{
    if (c->ssl != NULL) {
        SSL_shutdown(c->ssl);
        SSL_free(c->ssl);
        c->ssl = NULL;
    }
    if (c->ctx != NULL) {
        SSL_CTX_free(c->ctx);
        c->ctx = NULL;
    }
    if (c->fd >= 0) {
        close(c->fd);
        c->fd = -1;
    }
}

/* 读一个完整 MQTT 包到 buf：先读 2 字节，手写 varint 解码 remaining
 * length（继续移位读字节直到高位为 0），total = 头长 + remaining，
 * 再读余下字节。返回整包长度，失败返回 -1 */
static int e2e_read_packet(e2e_conn_t *c, unsigned char *buf, size_t cap)
{
    uint32_t rem = 0, mul = 1, hlen = 2, total = 0;

    if (cap < 5)
        return -1;
    if (e2e_read(c, buf, 2) != 0)
        return -1;

    while ((buf[hlen - 1] & 0x80u) != 0) { /* varint 未终结 */
        if (hlen >= 5)
            return -1; /* varint 最多 4 字节（固定头 ≤5 字节） */
        if (e2e_read(c, buf + hlen, 1) != 0)
            return -1;
        hlen++;
    }
    for (uint32_t i = 1; i < hlen; i++) {
        rem += (uint32_t)(buf[i] & 0x7fu) * mul;
        mul *= 128;
    }

    total = hlen + rem;
    if ((size_t)total > cap)
        return -1;
    if (rem > 0 && e2e_read(c, buf + hlen, rem) != 0)
        return -1;
    return (int)total;
}

/* ------------------------------------------------------------ 协议用例 */

/* CONNECT → 期待 CONNACK rc=0 */
static int case_connect(e2e_conn_t *c, const char *client_id)
{
    MQTTPacket_connectData d = MQTTPacket_connectData_initializer;
    unsigned char out[256], resp[64];
    unsigned char sp = 1, rc = 0xff;
    int walk = 0;

    d.clientID.cstring = (char *)client_id;
    d.keepAliveInterval = 60;
    int n = MQTTSerialize_connect(out, sizeof(out), &d);
    if (n <= 0 || e2e_write(c, out, (size_t)n) != 0)
        return -1;

    int r = e2e_read_packet(c, resp, sizeof(resp));
    if (r < 2 || MQTTDeserialize_connack(&sp, &rc, resp, r, &walk) != 1 ||
        rc != 0)
        return -1;
    return 0;
}

/* SUBSCRIBE 单 filter → 期待 SUBACK 且 granted qos ≤ 1 */
static int case_subscribe(e2e_conn_t *c, const char *filter,
                          unsigned short packetid)
{
    unsigned char out[256], resp[64];
    MQTTString mf = MQTTString_initializer;
    int rqos = 1, walk = 0, count = 0, granted[1] = { 0xff };
    unsigned short pid = 0;

    mf.cstring = (char *)filter;
    int n = MQTTSerialize_subscribe(out, sizeof(out), 0, packetid, 1, &mf,
                                    &rqos);
    if (n <= 0 || e2e_write(c, out, (size_t)n) != 0)
        return -1;

    int r = e2e_read_packet(c, resp, sizeof(resp));
    /* granted=0x80（订阅失败码）经 vendored readChar（signed char）符号
     * 扩展为 -128：必须显式挡 <0，否则 >1 检查被逃逸 */
    if (r < 2 || MQTTDeserialize_suback(&pid, 1, &count, granted, resp, r,
                                        &walk) != 1 ||
        count != 1 || granted[0] < 0 || granted[0] > 1 || pid != packetid)
        return -1;
    return 0;
}

/* PUBLISH（qos 0/1）；qos1 期待 PUBACK 且 packetid 一致 */
static int case_publish(e2e_conn_t *c, const char *topic, const char *payload,
                        int qos, unsigned short packetid)
{
    unsigned char out[E2E_BUF_MAX], resp[64];
    MQTTString tn = MQTTString_initializer;
    unsigned char ptype = 0, dup = 0;
    unsigned short pid = 0;
    int need = 0, walk = 0;

    tn.cstring = (char *)topic;
    size_t plen = strlen(payload);
    int n = MQTTSerialize_publish(out, sizeof(out), 0, qos, 0, packetid, tn,
                                  (unsigned char *)payload, (int)plen);
    if (n <= 0 || e2e_write(c, out, (size_t)n) != 0)
        return -1;

    if (qos >= 1) {
        int r = e2e_read_packet(c, resp, sizeof(resp));
        if (r < 2 || MQTTDeserialize_ack(&ptype, &dup, &pid, resp, r, &need,
                                         &walk) != 1 ||
            ptype != PUBACK || pid != packetid)
            return -1;
    }
    return 0;
}

/* 期待收到 PUBLISH 且 topic/payload 精确匹配 */
static int case_expect_publish(e2e_conn_t *c, const char *topic,
                               const char *payload)
{
    unsigned char buf[E2E_BUF_MAX];
    unsigned char dup, retained, *pl = NULL;
    int qos = -1, payloadlen = -1, need = 0, walk = 0;
    unsigned short pid = 0;
    MQTTString tn;

    int r = e2e_read_packet(c, buf, sizeof(buf));
    if (r < 2 ||
        MQTTDeserialize_publish(&dup, &qos, &retained, &pid, &tn, &pl,
                                &payloadlen, buf, r, &need, &walk) != 1)
        return -1;
    if (tn.lenstring.len != (int)strlen(topic) ||
        memcmp(tn.lenstring.data, topic, (size_t)tn.lenstring.len) != 0)
        return -1;
    if (payloadlen != (int)strlen(payload) ||
        memcmp(pl, payload, (size_t)payloadlen) != 0)
        return -1;
    return 0;
}

/* --------------------------------------------------------- 进程编排 */

static int spawn_acc(const char *acc_path, const char *conf_path)
{
    pid_t pid = fork();

    if (pid < 0)
        return -1;
    if (pid == 0) {
        execl(acc_path, "acc", "-c", conf_path, (char *)NULL);
        _exit(127); /* exec 失败：退出码 127 与 e2e 自身失败区分 */
    }
    g_acc_pid = pid;
    return 0;
}

/* 轮询端口就绪（50×100ms）；期间 acc 进程死亡则提前失败 */
static int wait_port_ready(uint16_t port)
{
    struct sockaddr_in sa;

    memset(&sa, 0, sizeof(sa));
    sa.sin_family = AF_INET;
    sa.sin_port = htons(port);
    sa.sin_addr.s_addr = htonl(INADDR_LOOPBACK);

    for (int i = 0; i < E2E_PORT_RETRY; i++) {
        int fd = socket(AF_INET, SOCK_STREAM, 0);
        if (fd >= 0) {
            if (connect(fd, (struct sockaddr *)&sa, sizeof(sa)) == 0) {
                close(fd);
                return 0;
            }
            close(fd);
        }
        if (g_acc_pid > 0 &&
            waitpid(g_acc_pid, NULL, WNOHANG) != 0) {
            g_acc_pid = -1;
            return -1; /* acc 启动即退：不再等端口 */
        }
        e2e_sleep_ms(100);
    }
    return -1;
}

/* SIGTERM 优雅退出（3s 轮询 waitpid），超时 SIGKILL 返回 -1；
 * 正常退出返回 0，异常退出码/被信号终止返回 -1 */
static int shutdown_acc(void)
{
    int st;

    if (g_acc_pid <= 0)
        return 0;
    kill(g_acc_pid, SIGTERM);
    for (int i = 0; i < E2E_EXIT_RETRY; i++) {
        if (waitpid(g_acc_pid, &st, WNOHANG) != 0)
            goto reaped;
        e2e_sleep_ms(100);
    }
    kill(g_acc_pid, SIGKILL);
    waitpid(g_acc_pid, &st, 0);

reaped:
    g_acc_pid = -1;
    return (WIFEXITED(st) && WEXITSTATUS(st) == 0) ? 0 : -1;
}

/* ---------------------------------------------------------------- main */

/* 明文端口用例组 */
static void run_plain_cases(void)
{
    e2e_conn_t sub, pub;

    int conn_bad = e2e_connect(E2E_TCP_PORT, 0, &sub) +
                   e2e_connect(E2E_TCP_PORT, 0, &pub);

    /* 1. connect/connack rc=0（订阅连接）；建连失败并入本用例报告 */
    case_report(conn_bad + case_connect(&sub, "e2e_sub"),
                "case 1 tcp connect/connack");

    /* 2. subscribe a/+ → suback granted ≤1 */
    case_report(case_subscribe(&sub, "a/+", 11), "case 2 tcp subscribe a/+");

    /* 3. 第二连接 publish a/b qos0 → 订阅端收 PUBLISH（topic/payload 校验） */
    case_report(case_connect(&pub, "e2e_pub") +
                case_publish(&pub, "a/b", "hello", 0, 0) +
                case_expect_publish(&sub, "a/b", "hello"),
                "case 3 tcp pubsub a/b qos0");

    /* 4. publish qos1 → PUBACK；订阅端顺带消费 a/c（防残留串入用例 5） */
    case_report(case_publish(&pub, "a/c", "q1body", 1, 22) +
                case_expect_publish(&sub, "a/c", "q1body"),
                "case 4 tcp publish qos1 puback");

    /* 5. subscribe # → publish x/y/z → 收到（多级通配） */
    case_report(case_subscribe(&sub, "#", 33) +
                case_publish(&pub, "x/y/z", "deep", 0, 0) +
                case_expect_publish(&sub, "x/y/z", "deep"),
                "case 5 tcp wildcard # x/y/z");

    e2e_close(&sub);
    e2e_close(&pub);
}

/* TLS 用例组：在 tls_port 重复 1–3；返回 0 已执行 / 1 SKIP */
static int run_tls_cases(void)
{
    e2e_conn_t sub, pub;

    if (access("conf/cert.pem", R_OK) != 0 || access("conf/key.pem", R_OK) != 0)
        return 1; /* 证书缺失：SKIP（acc 缺证书降级跳过 TLS 监听是既有行为） */

    if (e2e_connect(E2E_TLS_PORT, 1, &sub) != 0 ||
        e2e_connect(E2E_TLS_PORT, 1, &pub) != 0) {
        e2e_close(&sub);
        e2e_close(&pub);
        case_report(-1, "case 6-8 tls connect/pubsub");
        return 0;
    }

    case_report(case_connect(&sub, "e2e_tls_sub"),
                "case 6 tls connect/connack");
    case_report(case_subscribe(&sub, "a/+", 44), "case 7 tls subscribe a/+");
    case_report(case_connect(&pub, "e2e_tls_pub") +
                case_publish(&pub, "a/b", "tlsbody", 0, 0) +
                case_expect_publish(&sub, "a/b", "tlsbody"),
                "case 8 tls pubsub a/b qos0");

    e2e_close(&sub);
    e2e_close(&pub);
    return 0;
}

int main(void)
{
    char acc_path[PATH_MAX], conf_path[PATH_MAX];
    int skipped = 0;

    /* 相对路径一律 realpath 转绝对再 spawn：与运行目录解耦（防 chdir 类问题） */
    if (realpath("build/acc", acc_path) == NULL) {
        fprintf(stderr,
                "FAIL: 未找到 build/acc（请在仓库根目录运行，先 task build）\n");
        return 1;
    }
    if (realpath("tests/e2e/e2e.conf", conf_path) == NULL) {
        fprintf(stderr, "FAIL: 未找到 tests/e2e/e2e.conf\n");
        return 1;
    }

    SSL_library_init(); /* OpenSSL 1.1+ 下为兼容空操作，显式调用无害 */

    if (spawn_acc(acc_path, conf_path) != 0) {
        fprintf(stderr, "FAIL: spawn acc 失败\n");
        return 1;
    }
    if (wait_port_ready(E2E_TCP_PORT) != 0) {
        fprintf(stderr, "FAIL: tcp 端口 %d 未就绪（acc 启动失败?）\n",
                E2E_TCP_PORT);
        shutdown_acc();
        return 1;
    }

    run_plain_cases();

    /* 明文端口就绪不代表 TLS 监听已就绪：进 TLS 用例前同样等待 18883 */
    if (wait_port_ready(E2E_TLS_PORT) != 0)
        case_report(-1, "wait tls port 18883 ready");

    if (run_tls_cases() != 0) {
        skipped++;
        printf("== case 6-8 tls: SKIP（conf/cert.pem 不存在，task gen-certs 可生成）\n");
    }

    /* 结束：SIGTERM 优雅退出 + 退出码校验 */
    case_report(shutdown_acc(), "case 9 shutdown sigterm exit0");

    if (g_failed == 0) {
        printf("E2E PASS（%d 通过 / 0 失败 / %d 跳过）\n", g_passed, skipped);
        return 0;
    }
    printf("E2E FAIL（%d 项失败 / %d 跳过）\n", g_failed, skipped);
    return 1;
}
