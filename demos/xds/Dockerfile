# syntax=docker/dockerfile:1

# ---- 构建阶段：拉取依赖并编译 ----
FROM golang:1.23-alpine AS builder
# 依赖经国内代理拉取，失败时直连（goproxy.cn 不通的环境仍可构建）
ENV GOPROXY=https://goproxy.cn,direct
WORKDIR /build
# 先单独拷贝依赖描述并预下载，依赖未变时命中缓存层
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# 版本注入与 Taskfile 的 build task 语义对齐：默认 dev，可用
# --build-arg VERSION=v1.0.0 REVISION=<git-short-hash> 覆盖
ARG VERSION=dev
ARG REVISION=unknown
# CGO 禁用产出静态二进制（alpine 运行镜像可直接执行）；-s -w 去除符号与调试信息
RUN CGO_ENABLED=0 go build -ldflags "-s -w \
    -X github.com/balcony314/xds/pkg/buildinfo.buildVersion=${VERSION} \
    -X github.com/balcony314/xds/pkg/buildinfo.buildGitRevision=${REVISION}" \
    -o bin/xds-server ./cmd/xds-server

# ---- 运行阶段：最小镜像 ----
FROM alpine:3.20
# ca-certificates：Nacos 集群开启 TLS / openapi 调用时需要根证书
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
WORKDIR /home/work/xds-server
# 只拷贝二进制与默认配置（进程默认读取 ./conf/<CONF_ENV>.yaml）
COPY --from=builder /build/bin/xds-server ./
COPY --from=builder /build/conf/ ./conf/
# 非 root 运行
USER app
# 8080=web 管理 API；8090=xDS gRPC；8091=xDS REST。
# admin 端口(8081)仅绑定 127.0.0.1，不对外暴露
EXPOSE 8080 8090 8091
CMD ["./xds-server"]
