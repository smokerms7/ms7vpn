package app

import (
	"context"
	"net"
	"time"

	"ms7vpn/internal/model"
	"ms7vpn/internal/subscription"
)

// Физический адаптер ищется через PowerShell, это 1–2 с. Для пинга и загрузки
// подписки он нужен постоянно, поэтому ответ живёт несколько минут.
const physicalInterfaceTTL = 3 * time.Minute

func (a *App) cachedPhysicalInterface(ctx context.Context) string {
	a.ifaceMu.Lock()
	defer a.ifaceMu.Unlock()
	if a.ifaceName != "" && time.Since(a.ifaceAt) < physicalInterfaceTTL {
		return a.ifaceName
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	name, err := physicalInterface(lookupCtx)
	cancel()
	if err != nil {
		return a.ifaceName // прежний ответ лучше, чем никакого
	}
	a.ifaceName, a.ifaceAt = name, time.Now()
	return name
}

// directDialer — соединения мимо собственного туннеля. Пока VPN выключен,
// обходить нечего, и возвращается nil.
func (a *App) directDialer(ctx context.Context) subscription.DialFunc {
	conn := a.Snapshot().Connection
	if !conn.Connected || conn.Mode == model.ModeZapret {
		return nil
	}
	name := a.cachedPhysicalInterface(ctx)
	if name == "" {
		return nil
	}
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil
	}
	dialer := boundDialer(iface.Index)
	if dialer == nil {
		return nil
	}
	// IP_UNICAST_IF задан для IPv4, поэтому и адрес берётся только IPv4.
	return func(ctx context.Context, _ string, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp4", address)
	}
}
