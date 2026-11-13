# 说明：本环境 Docker Hub 不可达，且未预置 golang/alpine 基础镜像。
# 因此构建方式改为「主机交叉编译 + 本地 redis:7-alpine 作运行底座」：
#   - 主机已安装 Go 1.24，执行 CGO_ENABLED=0 GOOS=linux go build 产出 Linux 二进制 petstore-server
#   - 运行阶段使用本地已有的 redis:7-alpine（alpine 系，自带 busybox wget 供健康检查）
# 若日后Docker Hub可达，可改回标准多阶段（golang:1.24 构建 + alpine:3.20 运行）。
FROM redis:7-alpine
WORKDIR /app

# 预编译二进制 + 运行时所需的 migrations 与 web 目录（服务按磁盘路径读取）
COPY petstore-server /app/petstore-server
COPY migrations /app/migrations
COPY web /app/web

EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --start-period=25s --retries=5 \
  CMD wget -q -O /dev/null http://localhost:8080/ || exit 1

ENTRYPOINT ["/app/petstore-server"]
