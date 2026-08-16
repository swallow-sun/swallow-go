.PHONY: run build chat clean tidy test vet check

APP_NAME := swallow-go

## 启动 HTTP 服务
run:
	go run ./cmd/server

## 编译 HTTP 服务
build:
	go build -o bin/$(APP_NAME).exe ./cmd/server

## 启动 CLI 对话
chat:
	go run ./cmd/chat

## 整理 Go 依赖
tidy:
	go mod tidy

## 运行单元测试
test:
	go test ./...

## 运行静态检查
vet:
	go vet ./...

## 执行完整质量检查
check: test vet
	go build ./...

## 清理构建产物
clean:
	if exist bin del /s /q bin