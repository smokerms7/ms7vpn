//go:build windows

package app

import (
	"encoding/binary"
	"net"
	"syscall"
	"time"
	"unsafe"
)

// IP_UNICAST_IF: исходящие пакеты сокета идут через указанный адаптер, даже
// если маршрут по умолчанию забрал TUN. Так же привязывает сокеты Xray.
const ipUnicastIF = 31

func boundDialer(index int) *net.Dialer {
	return &net.Dialer{
		Timeout: 8 * time.Second,
		Control: func(network, _ string, raw syscall.RawConn) error {
			var setErr error
			err := raw.Control(func(fd uintptr) {
				var order [4]byte
				binary.BigEndian.PutUint32(order[:], uint32(index))
				value := *(*uint32)(unsafe.Pointer(&order[0]))
				setErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, ipUnicastIF, int(value))
			})
			if err != nil {
				return err
			}
			return setErr
		},
	}
}
