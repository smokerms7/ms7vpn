@echo off
setlocal
cd /d "%~dp0"

set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=0
set LDFLAGS=-H windowsgui -s -w

if not exist dist mkdir dist

echo [1/5] Тесты
go test ./...
if errorlevel 1 exit /b 1

echo [2/5] Приложение
go build -trimpath -ldflags="%LDFLAGS%" -o dist\MS7VPN.exe .\cmd\ms7vpn
if errorlevel 1 exit /b 1

echo [3/5] Деинсталлятор
go build -trimpath -ldflags="%LDFLAGS%" -o cmd\installer\payload\uninstaller.exe .\cmd\uninstaller
if errorlevel 1 exit /b 1

echo [4/5] payload.zip
rem В архив кладём только то, что действительно нужно при первом запуске.
rem geoip.dat и geosite.dat (29 МБ) не нужны: маршрутизация работает на явных
rem подсетях, а geosite.dat требуется только для блокировки рекламы и
rem подтягивается отдельно, когда её включают.
set PAYLOAD=dist\MS7VPN.exe bundle\xray.exe bundle\wintun.dll
if exist bundle\MicrosoftEdgeWebview2Setup.exe set PAYLOAD=%PAYLOAD% bundle\MicrosoftEdgeWebview2Setup.exe
go run .\cmd\mkpayload -o cmd\installer\payload\payload.zip %PAYLOAD%
if errorlevel 1 exit /b 1

echo [5/5] Установщик
go build -trimpath -ldflags="%LDFLAGS%" -o dist\MS7VPN-Setup.exe .\cmd\installer
if errorlevel 1 exit /b 1

echo.
echo Готово:
echo   dist\MS7VPN.exe
echo   dist\MS7VPN-Setup.exe
endlocal
