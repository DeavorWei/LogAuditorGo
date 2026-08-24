#!/bin/bash
set -e

echo "========================================================"
echo " LogAuditorGo 一键编译打包脚本 (Frontend + Backend Embed)"
echo "========================================================"

echo "[1/3] 构建前端静态资源..."
cd web
npm install
npm run build
cd ..

echo "[2/3] 整理 Go 依赖..."
go mod tidy

echo "[3/3] 编译嵌入前端的纯单二进制可执行文件..."
mkdir -p build
go build -ldflags="-s -w" -o build/LogAuditorGo cmd/LogAuditorGo/main.go

echo "========================================================"
echo "[SUCCESS] 编译完成!"
echo "生成文件: build/LogAuditorGo"
echo "说明: 该二进制文件已完整嵌入前端 SPA 页面，可单独拷贝至任意目录/机器运行。"
echo "========================================================"
