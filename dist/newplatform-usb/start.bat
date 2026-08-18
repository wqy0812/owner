@echo off
chcp 65001 >nul 2>&1
cd /d "%~dp0"

echo.
echo ╔══════════════════════════════════════════════════╗
echo ║        NewPlatform Demo  —  离线模式             ║
echo ╚══════════════════════════════════════════════════╝
echo.
echo   打开浏览器访问: http://127.0.0.1:8080
echo   按 Ctrl+C 停止
echo.

if exist "bin\newplatform-windows-amd64.exe" (
    bin\newplatform-windows-amd64.exe
) else (
    echo 未找到 Windows 二进制。请使用 --cross 参数重新打包。
    pause
)
