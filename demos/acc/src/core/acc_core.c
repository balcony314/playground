/* acc_context 辅助：SSL_CTX 初始化、master 侧服务初始化/清理、路径绝对化。
 * 对齐 raw 的 ads_work_init_sslctx（改用未弃用的 TLS_server_method 并
 * 强制 TLS1.2 下限），证书缺失降级而非退出 */
#include "acc_core.h"

#include <stdio.h>
#include <string.h>
#include <unistd.h>

#include <openssl/ssl.h>

#include "net/acc_listening.h"
#include "util/acc_log.h"

int acc_init_ssl_ctx(acc_context_t *ctx)
{
    SSL_CTX *ssl_ctx = SSL_CTX_new(TLS_server_method());
    if (ssl_ctx == NULL) {
        ACC_LOGE("SSL_CTX_new 失败（TLS 降级关闭）");
        return -1;
    }

    SSL_CTX_set_min_proto_version(ssl_ctx, TLS1_2_VERSION);
    SSL_CTX_set_options(ssl_ctx, SSL_OP_NO_SSLv3);
    SSL_CTX_set_verify(ssl_ctx, SSL_VERIFY_NONE, NULL);
    /* ECDHE 前向保密：不显式设置曲线/ECB 门限——OpenSSL 1.1+ 默认
     * 协商 X25519 等现代组，硬编码列表反而钉死旧默认（与 raw 一致） */

    if (SSL_CTX_use_certificate_file(ssl_ctx, ctx->conf.cert_file,
                                     SSL_FILETYPE_PEM) != 1) {
        ACC_LOGE("加载证书失败: %s（TLS 降级关闭）", ctx->conf.cert_file);
        SSL_CTX_free(ssl_ctx);
        return -1;
    }
    if (SSL_CTX_use_PrivateKey_file(ssl_ctx, ctx->conf.key_file,
                                    SSL_FILETYPE_PEM) != 1) {
        ACC_LOGE("加载私钥失败: %s（TLS 降级关闭）", ctx->conf.key_file);
        SSL_CTX_free(ssl_ctx);
        return -1;
    }
    if (SSL_CTX_check_private_key(ssl_ctx) != 1) {
        ACC_LOGE("证书与私钥不匹配（TLS 降级关闭）");
        SSL_CTX_free(ssl_ctx);
        return -1;
    }

    ctx->ssl_ctx = ssl_ctx;
    return 0;
}

int acc_server_init(acc_context_t *ctx)
{
    /* TLS 上下文：证书缺失/非法时降级为 NULL，仅放弃 TLS 监听（不退出）；
     * worker 继承该结果，不再重试（证书文件不会自愈） */
    if (ctx->conf.tls_port > 0)
        acc_init_ssl_ctx(ctx);

    return acc_create_listening_sockets(ctx);
}

void acc_server_shutdown(acc_context_t *ctx)
{
    acc_close_listening_sockets(ctx);
    if (ctx->ssl_ctx != NULL) {
        SSL_CTX_free(ctx->ssl_ctx);
        ctx->ssl_ctx = NULL;
    }
}

int acc_path_to_absolute(char *path, size_t cap)
{
    if (path == NULL || path[0] == '\0' || path[0] == '/')
        return 0;

    char cwd[ACC_CONF_PATH_MAX * 2];
    if (getcwd(cwd, sizeof(cwd)) == NULL)
        return -1;

    char abs[ACC_CONF_PATH_MAX * 2];
    int w = snprintf(abs, sizeof(abs), "%s/%s", cwd, path);
    if (w <= 0 || (size_t)w >= sizeof(abs) || (size_t)w >= cap)
        return -1; /* 缓冲装不下：保持原路径由调用方处置 */

    memcpy(path, abs, (size_t)w + 1);
    return 0;
}
