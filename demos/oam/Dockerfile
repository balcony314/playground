# 构建阶段：编译静态二进制
FROM golang:1.26-alpine AS builder
WORKDIR /src

# 先拷贝依赖清单，充分利用层缓存
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -o /manager ./cmd

# 运行阶段：distroless 最小镜像
FROM gcr.io/distroless/static:nonroot
COPY --from=builder /manager /manager
USER nonroot:nonroot

ENTRYPOINT ["/manager"]
