@echo off
chcp 65001 >nul
setlocal
cd /d "%~dp0"

set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=0
set LDFLAGS=-H windowsgui -s -w

if not exist dist mkdir dist

echo [1/8] Тесты
go test ./...
if errorlevel 1 exit /b 1

echo [2/8] Приложение
go build -trimpath -ldflags="%LDFLAGS%" -o dist\MS7VPN.exe .\cmd\ms7vpn
if errorlevel 1 exit /b 1

echo [3/8] Деинсталлятор
rem Кладём в dist, а не в папку встраивания: иначе он попадал в полный
rem установщик дважды - отдельным файлом и внутри архива.
go build -trimpath -ldflags="%LDFLAGS%" -o dist\uninstaller.exe .\cmd\uninstaller
if errorlevel 1 exit /b 1

echo [4/8] Обычный payload (его качает онлайн-установщик)
rem Только то, что нужно при первом запуске. geoip.dat и geosite.dat сюда
rem не кладём: маршрутизация работает на явных подсетях, а geosite.dat нужен
rem лишь для блокировки рекламы. Кто её включит - тому хватит полной версии
rem или отдельной загрузки.
set LEAN=dist\MS7VPN.exe bundle\xray.exe bundle\wintun.dll dist\uninstaller.exe
go run .\cmd\mkpayload -o dist\MS7VPN-payload.zip %LEAN%
if errorlevel 1 exit /b 1

echo [5/8] Полный payload (встраивается внутрь MS7VPN-Setup-Full.exe)
rem Здесь всё, что нужно для установки вообще без интернета: geo-файлы и
rem автономный установщик WebView2. Если их нет в bundle - полная сборка
rem получится такой же, как обычная, и WebView2 при отсутствии будет
rem скачиваться.
set FULL=%LEAN%
if exist bundle\geosite.dat set FULL=%FULL% bundle\geosite.dat
if exist bundle\geoip.dat set FULL=%FULL% bundle\geoip.dat
if exist bundle\MicrosoftEdgeWebView2RuntimeInstallerX64.exe set FULL=%FULL% bundle\MicrosoftEdgeWebView2RuntimeInstallerX64.exe
if exist bundle\MicrosoftEdgeWebview2Setup.exe set FULL=%FULL% bundle\MicrosoftEdgeWebview2Setup.exe
go run .\cmd\mkpayload -o cmd\installer\payload\payload.zip %FULL%
if errorlevel 1 exit /b 1

echo [6/8] Установщик (онлайн, файлы скачиваются с GitHub)
rem Без тега offline директива go:embed не подключается, и payload внутрь
rem не попадает - отсюда и шесть мегабайт вместо двухсот.
go build -trimpath -ldflags="%LDFLAGS%" -o dist\MS7VPN-Setup.exe .\cmd\installer
if errorlevel 1 exit /b 1

echo [7/8] Установщик (полный, всё внутри)
go build -trimpath -tags offline -ldflags="%LDFLAGS%" -o dist\MS7VPN-Setup-Full.exe .\cmd\installer
if errorlevel 1 exit /b 1

echo [8/8] Контрольные суммы
rem Имя MS7VPN-payload.zip задано в коде (update.PayloadAssetName): именно
rem под ним онлайн-установщик ищет архив во вложениях выпуска. Переименуете -
rem установщик перестанет находить файлы программы.
go run .\cmd\mkpayload -sum dist\MS7VPN-Setup.exe dist\MS7VPN-Setup-Full.exe dist\MS7VPN-payload.zip dist\MS7VPN.exe
if errorlevel 1 exit /b 1

echo.
echo Готово. В выпуск GitHub приложить все три файла и суммы к ним:
echo   dist\MS7VPN-Setup.exe          + .sha256   (главный, с сайта)
echo   dist\MS7VPN-Setup-Full.exe     + .sha256   (без интернета)
echo   dist\MS7VPN-payload.zip        + .sha256   (его качает онлайн-установщик)
echo.
echo ВАЖНО: пока выпуск не опубликован, онлайн-установщик работать не будет -
echo ему неоткуда брать файлы программы.
endlocal
