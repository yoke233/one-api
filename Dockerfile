FROM --platform=$BUILDPLATFORM node:16 AS builder
RUN npm config set registry https://registry.npmmirror.com
WORKDIR /web
COPY ./VERSION .
COPY ./web .

# WORKDIR /web/default
# RUN npm install
# RUN DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION=$(cat VERSION) npm run build

# WORKDIR /web/berry
# RUN npm install
# RUN DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION=$(cat VERSION) npm run build

WORKDIR /web/air
RUN npm install
RUN DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION=$(cat VERSION) npm run build

FROM golang:alpine AS builder2

# 设置环境变量，使用国内的 Alpine 镜像源
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories
RUN apk add --no-cache \
    gcc \
    musl-dev \
    sqlite-dev \
    build-base
RUN go env -w  GOPROXY=https://goproxy.cn,direct

ENV GO111MODULE=on \
    CGO_ENABLED=1 \
    GOOS=linux

WORKDIR /build
ADD go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=builder /web/build ./web/build
RUN go build -trimpath -ldflags "-s -w -extldflags '-static'" -o one-api

FROM alpine

# 设置环境变量，使用国内的 Alpine 镜像源
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories

RUN apk update \
    && apk upgrade \
    && apk add --no-cache ca-certificates tzdata \
    && update-ca-certificates 2>/dev/null || true

COPY --from=builder2 /build/one-api /
EXPOSE 3000
WORKDIR /data
ENTRYPOINT ["/one-api"]