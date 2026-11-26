@echo off
rem Rebuilds what changed and starts KaraokeDock for development.
rem Extra arguments go to the server, such as: dev.bat -port 8081
setlocal
cd /d "%~dp0"
title KaraokeDock (dev)

where go >nul 2>&1 || (echo Go was not found. Install it, then open this again. & goto :failed)
where npm >nul 2>&1 || (echo Node.js was not found. Install it, then open this again. & goto :failed)
if not exist "web\node_modules" (echo Run scripts\setup.ps1 once first. & goto :failed)

rem A running copy would stop the new one being saved.
taskkill /im karaokedock.exe /t /f >nul 2>&1

rem Exits 0 when the pages have changed since they were last built.
powershell -NoProfile -Command "$built = Get-ChildItem 'server\internal\webui\dist' -File -ErrorAction SilentlyContinue | Sort-Object LastWriteTime | Select-Object -First 1; if (-not $built) { exit 0 }; $new = @(Get-ChildItem 'web\src' -Recurse -File) + @(Get-ChildItem 'web' -File) | Where-Object { $_.LastWriteTime -gt $built.LastWriteTime }; if ($new) { exit 0 }; exit 1"
if errorlevel 1 goto :server

echo Building the pages...
pushd web
call npm run build
if errorlevel 1 (popd & goto :failed)
popd

:server
echo Building the server...
pushd server
go build -o ..\bin\karaokedock.exe .\cmd\karaokedock
if errorlevel 1 (popd & goto :failed)
popd

echo Starting KaraokeDock. Press Ctrl+C to stop.
bin\karaokedock.exe --metadata data\metadata --sidecar sidecar %*
if errorlevel 1 goto :failed
exit /b 0

:failed
echo.
echo Something went wrong. See the message above.
pause
exit /b 1
