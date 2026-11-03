//go:build windows

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"ms7vpn/internal/xray"
)

// The script only reads OS state. Routing/DNS are owned and removed by sing-box.
func tunEnvironment(ctx context.Context) (string, error) {
	const script = `$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[System.Text.UTF8Encoding]::new(); $routes=@(Get-NetRoute -AddressFamily IPv4 | Where-Object {$_.DestinationPrefix -in @('0.0.0.0/0','0.0.0.0/1','128.0.0.0/1')}); $adapters=@(Get-NetAdapter); $foreign=@(); foreach($r in $routes){$a=$adapters | Where-Object {$_.ifIndex -eq $r.InterfaceIndex}; if($a -and $a.Status -eq 'Up' -and -not $a.HardwareInterface -and $a.Name -ne 'MS7VPN-TUN'){$foreign+=$a.Name}}; $physical=@(foreach($r in $routes){$a=$adapters | Where-Object {$_.ifIndex -eq $r.InterfaceIndex -and $_.HardwareInterface -and $_.Status -eq 'Up'};if($a -and $r.DestinationPrefix -eq '0.0.0.0/0'){$i=Get-NetIPInterface -InterfaceIndex $r.InterfaceIndex -AddressFamily IPv4;[PSCustomObject]@{name=$a.Name;metric=($r.RouteMetric+$i.InterfaceMetric)}}}); $best=$physical | Sort-Object metric | Select-Object -First 1; @{interface=$best.name;foreign=@($foreign | Select-Object -Unique)} | ConvertTo-Json -Compress`
	output, err := networkRead(ctx, script)
	if err != nil {
		return "", err
	}
	var state struct {
		Interface string   `json:"interface"`
		Foreign   []string `json:"foreign"`
	}
	if err := json.Unmarshal(output, &state); err != nil {
		return "", fmt.Errorf("чтение сетевых интерфейсов: %w", err)
	}
	if len(state.Foreign) > 0 {
		return "", fmt.Errorf("сначала отключите другой VPN: %s. Одновременные TUN могут перехватывать трафик друг друга", strings.Join(state.Foreign, ", "))
	}
	if state.Interface == "" {
		return "", fmt.Errorf("не найден сетевой интерфейс с выходом в интернет")
	}
	return state.Interface, nil
}

func verifyTunRoutes(ctx context.Context) error {
	const script = `$ErrorActionPreference='Stop'; $a=Get-NetAdapter -Name 'MS7VPN-TUN'; if($a.Status -ne 'Up'){throw 'MS7VPN-TUN не поднят'}; $ip=@(Get-NetIPAddress -InterfaceIndex $a.ifIndex -AddressFamily IPv4 | Where-Object {$_.IPAddress -eq '172.28.217.1'}); if(-not $ip){throw 'MS7VPN-TUN не получил адрес'}; foreach($prefix in @('0.0.0.0/1','128.0.0.0/1')){$r=@(Get-NetRoute -InterfaceIndex $a.ifIndex -DestinationPrefix $prefix -ErrorAction SilentlyContinue);if(-not $r){throw ('Отсутствует маршрут '+$prefix)}}; 'ok'`
	_, err := networkRead(ctx, script)
	return err
}

func networkRead(ctx context.Context, script string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", script)
	xray.PrepareChild(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("проверка сети Windows: %s", strings.TrimSpace(string(out)))
	}
	return out, nil
}
