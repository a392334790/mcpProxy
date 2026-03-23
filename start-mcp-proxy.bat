@echo off
setlocal

cd /d "%~dp0"

if "%MCP_PROXY_CONFIG_FILE%"=="" (
    set "MCP_PROXY_CONFIG_FILE=configs\proxy.env.example"
)

if not exist "mcp-proxy.exe" (
    echo [ERROR] mcp-proxy.exe not found in %CD%
    exit /b 1
)

echo Starting mcp-proxy.exe
echo MCP_PROXY_CONFIG_FILE=%MCP_PROXY_CONFIG_FILE%
echo.

"%CD%\mcp-proxy.exe"

