//go:build windows

package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"ms7vpn/internal/xray"
)

type vpnProcessHandle struct {
	pid    uint32
	name   string
	handle windows.Handle
}

// All termination handles are opened before stopping anything. If an elevated
// Happ owns the connection, ask Windows for elevation before partial shutdown.
func stopConflictingVPNs(ctx context.Context, ownPaths []string) (stopped []string, retErr error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	targets := []vpnProcessHandle{}
	defer func() {
		for _, p := range targets {
			windows.CloseHandle(p.handle)
		}
	}()
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		name := windows.UTF16ToString(entry.ExeFile[:])
		lower := strings.ToLower(name)
		if entry.ProcessID == uint32(os.Getpid()) || (!vpnClients[lower] && !vpnCores[lower]) {
			continue
		}
		query, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if openErr != nil {
			if openErr == windows.ERROR_INVALID_PARAMETER {
				continue
			}
			return nil, fmt.Errorf("%w: %s", errVPNConflictNeedsAdmin, name)
		}
		buffer := make([]uint16, windows.MAX_LONG_PATH)
		size := uint32(len(buffer))
		pathErr := windows.QueryFullProcessImageName(query, 0, &buffer[0], &size)
		windows.CloseHandle(query)
		if pathErr != nil {
			return nil, fmt.Errorf("не удалось проверить путь %s: %w", name, pathErr)
		}
		if !conflictingVPNProcess(name, windows.UTF16ToString(buffer[:size]), ownPaths) {
			continue
		}
		handle, openErr := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if openErr != nil {
			if openErr == windows.ERROR_INVALID_PARAMETER {
				continue
			}
			return nil, fmt.Errorf("%w: %s", errVPNConflictNeedsAdmin, name)
		}
		// Recheck the image on the retained handle: process IDs can be reused.
		size = uint32(len(buffer))
		if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
			windows.CloseHandle(handle)
			return nil, err
		}
		if !conflictingVPNProcess(name, windows.UTF16ToString(buffer[:size]), ownPaths) {
			windows.CloseHandle(handle)
			continue
		}
		targets = append(targets, vpnProcessHandle{entry.ProcessID, name, handle})
	}
	if err != windows.ERROR_NO_MORE_FILES {
		return nil, fmt.Errorf("список VPN-процессов: %w", err)
	}
	if len(targets) == 0 {
		return nil, nil
	}
	ports, err := vpnListeningPorts(ctx, targets)
	if err != nil {
		return nil, err
	}
	stopped = []string{}
	defer func() {
		if len(stopped) > 0 {
			retErr = errors.Join(retErr, clearStoppedVPNProxy(ports))
		}
	}()
	// Close GUI clients first, so they cannot immediately restart a stopped core.
	for _, clientPhase := range []bool{true, false} {
		for _, p := range targets {
			if vpnClients[strings.ToLower(p.name)] != clientPhase {
				continue
			}
			if ctx.Err() != nil {
				return stopped, fmt.Errorf("отключение других VPN прервано: %w", ctx.Err())
			}
			state, _ := windows.WaitForSingleObject(p.handle, 0)
			if state == windows.WAIT_OBJECT_0 {
				stopped = append(stopped, p.name)
				continue
			}
			if clientPhase {
				closeVPNWindows(p.pid)
				windows.WaitForSingleObject(p.handle, 600)
			}
			state, _ = windows.WaitForSingleObject(p.handle, 0)
			if state != windows.WAIT_OBJECT_0 {
				if err := windows.TerminateProcess(p.handle, 0); err != nil {
					return stopped, fmt.Errorf("не удалось отключить %s: %w", p.name, err)
				}
			}
			if state, _ := windows.WaitForSingleObject(p.handle, 2000); state != windows.WAIT_OBJECT_0 {
				return stopped, fmt.Errorf("%s не завершился", p.name)
			}
			stopped = append(stopped, p.name)
		}
	}
	// Wintun releases routes when its last owning handle closes.
	select {
	case <-ctx.Done():
		return stopped, ctx.Err()
	case <-time.After(400 * time.Millisecond):
	}
	return stopped, nil
}

func closeVPNWindows(pid uint32) {
	user32 := syscall.NewLazyDLL("user32.dll")
	enum := user32.NewProc("EnumWindows")
	owner := user32.NewProc("GetWindowThreadProcessId")
	post := user32.NewProc("PostMessageW")
	callback := syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
		var windowPID uint32
		owner.Call(hwnd, uintptr(unsafe.Pointer(&windowPID)))
		if windowPID == pid {
			post.Call(hwnd, 0x0010, 0, 0)
		} // WM_CLOSE
		return 1
	})
	enum.Call(callback, 0)
}

func vpnListeningPorts(ctx context.Context, targets []vpnProcessHandle) (map[int]bool, error) {
	pids := map[uint32]bool{}
	for _, p := range targets {
		pids[p.pid] = true
	}
	cmd := exec.CommandContext(ctx, "netstat.exe", "-ano", "-p", "tcp")
	xray.PrepareChild(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("проверка портов других VPN: %w", err)
	}
	ports := map[int]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 5 || fields[0] != "TCP" {
			continue
		}
		pid, err := strconv.ParseUint(fields[4], 10, 32)
		if err != nil || !pids[uint32(pid)] {
			continue
		}
		// Only listeners have a wildcard peer with port zero.
		_, peerPort, err := net.SplitHostPort(fields[2])
		if err != nil || peerPort != "0" {
			continue
		}
		host, port, err := net.SplitHostPort(fields[1])
		if err != nil {
			continue
		}
		ip := net.ParseIP(host)
		if ip == nil || (!ip.IsLoopback() && !ip.IsUnspecified()) {
			continue
		}
		n, err := strconv.Atoi(port)
		if err == nil {
			ports[n] = true
		}
	}
	return ports, nil
}

func clearStoppedVPNProxy(ports map[int]bool) error {
	server := queryRegistryValue("ProxyServer")
	changed := false
	if server.Exists && proxyUsesStoppedPort(server.Value, ports) {
		if err := setRegistryValue("ProxyEnable", "REG_DWORD", "0"); err != nil {
			return err
		}
		if err := deleteRegistryValue("ProxyServer"); err != nil {
			return err
		}
		changed = true
	}
	pac := queryRegistryValue("AutoConfigURL")
	if parsed, err := url.Parse(pac.Value); pac.Exists && err == nil && proxyUsesStoppedPort(parsed.Host, ports) {
		if err := deleteRegistryValue("AutoConfigURL"); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		notifyInternetSettings()
	}
	return nil
}
