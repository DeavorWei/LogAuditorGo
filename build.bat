@echo off
@rem LogAuditorGo build script
chcp 65001 >nul

echo [1/3] Building frontend assets...
cd web
call npm install
if %ERRORLEVEL% neq 0 (
    echo [ERROR] npm install failed!
    cd ..
    exit /b %ERRORLEVEL%
)

call npm run build
if %ERRORLEVEL% neq 0 (
    echo [ERROR] npm run build failed!
    cd ..
    exit /b %ERRORLEVEL%
)
cd ..

echo [2/3] Tidying Go modules...
go mod tidy
if %ERRORLEVEL% neq 0 (
    echo [ERROR] go mod tidy failed!
    exit /b %ERRORLEVEL%
)

echo [3/3] Compiling standalone Go binary with embedded frontend (Release mode)...
if not exist "build" mkdir "build"
@rem Release build:
@rem -trimpath: 移除编译路径信息，隐藏源码绝对路径
@rem -buildvcs=false: 移除 VCS (git) 版本元数据，减少信息泄露与冗余
@rem -ldflags="-s -w -H windowsgui":
@rem   -s: 去除符号表
@rem   -w: 去除 DWARF 调试信息（缩小体积）
@rem   -H windowsgui: 隐藏 Windows 控制台黑框 (GUI 子系统)
go build -trimpath -buildvcs=false -ldflags="-s -w -H windowsgui" -o build\LogAuditorGo.exe cmd\LogAuditorGo\main.go
if %ERRORLEVEL% neq 0 (
    echo [ERROR] go build failed!
    exit /b %ERRORLEVEL%
)

echo ========================================================
echo [SUCCESS] Build completed successfully!
echo Output binary: build\LogAuditorGo.exe
echo Frontend has been embedded into the binary.
echo ========================================================
