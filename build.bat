@echo off
@rem LogAuditorGo build script

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

echo [3/3] Compiling standalone Go binary with embedded frontend...
if not exist "build" mkdir "build"
go build -o build\LogAuditorGo.exe cmd\LogAuditorGo\main.go
if %ERRORLEVEL% neq 0 (
    echo [ERROR] go build failed!
    exit /b %ERRORLEVEL%
)

echo ========================================================
echo [SUCCESS] Build completed successfully!
echo Output binary: build\LogAuditorGo.exe
echo Frontend has been embedded into the binary.
echo ========================================================
