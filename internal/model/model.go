package model

import "time"

const AppVersion = "ms7.vs2.0"

type UserInfo struct {
	UploadBytes   int64 `json:"uploadBytes,omitempty"`
	DownloadBytes int64 `json:"downloadBytes,omitempty"`
	TotalBytes    int64 `json:"totalBytes,omitempty"`
	ExpireUnix    int64 `json:"expireUnix,omitempty"`

	// Статус приходит с панели как есть. Пустая строка означает, что панель
	// его не прислала — в этом случае интерфейс ничего не выдумывает.
	Status   string `json:"status,omitempty"`
	Username string `json:"username,omitempty"`
}

type Node struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Protocol          string            `json:"protocol"`
	Address           string            `json:"address"`
	Port              int               `json:"port"`
	Transport         string            `json:"transport,omitempty"`
	Security          string            `json:"security,omitempty"`
	SNI               string            `json:"sni,omitempty"`
	RawURI            string            `json:"rawUri"`
	PingMS            int               `json:"pingMs,omitempty"`
	PingStatus        string            `json:"pingStatus,omitempty"`
	LastPingAt        time.Time         `json:"lastPingAt,omitempty"`
	Supported         bool              `json:"supported"`
	UnsupportedReason string            `json:"unsupportedReason,omitempty"`
	Meta              map[string]string `json:"meta,omitempty"`
}

type Subscription struct {
	ID                    string    `json:"id"`
	Name                  string    `json:"name"`
	URL                   string    `json:"url"`
	Nodes                 []Node    `json:"nodes"`
	UpdatedAt             time.Time `json:"updatedAt"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdateIntervalMinutes int       `json:"updateIntervalMinutes,omitempty"`
	UserInfo              UserInfo  `json:"userInfo,omitempty"`
	LastError             string    `json:"lastError,omitempty"`
	Warnings              []string  `json:"warnings,omitempty"`
}

type Settings struct {
	ConnectionMode string `json:"connectionMode"` // tun | system-proxy
	AutoRefresh    bool   `json:"autoRefresh"`
	AutoConnect    bool   `json:"autoConnect"`
	BlockAds       bool   `json:"blockAds"`
	MinimizeToTray bool   `json:"minimizeToTray"`
	DNS1           string `json:"dns1"`
	DNS2           string `json:"dns2"`
	TestURL        string `json:"testUrl"`
	SupportURL     string `json:"supportUrl"`

	// Адрес файла с описанием последней сборки. Меняется в настройках —
	// сервер обновлений может переехать без перевыпуска программы.
	UpdateURL string `json:"updateUrl"`
}

type CoreState struct {
	Installed     bool   `json:"installed"`
	Version       string `json:"version"`
	Downloading   bool   `json:"downloading"`
	Progress      int    `json:"progress"`
	StatusMessage string `json:"statusMessage,omitempty"`
	LastError     string `json:"lastError,omitempty"`
}

type ConnectionState struct {
	Connected      bool      `json:"connected"`
	Connecting     bool      `json:"connecting"`
	NodeID         string    `json:"nodeId,omitempty"`
	NodeName       string    `json:"nodeName,omitempty"`
	Mode           string    `json:"mode,omitempty"`
	StartedAt      time.Time `json:"startedAt,omitempty"`
	LastError      string    `json:"lastError,omitempty"`
	StatusMessage  string    `json:"statusMessage,omitempty"`
	LocalHTTPProxy string    `json:"localHttpProxy,omitempty"`
	LocalSOCKS     string    `json:"localSocks,omitempty"`
	Warnings       []string  `json:"warnings,omitempty"`

	// Счётчики трафика текущего сеанса и мгновенная скорость.
	UplinkBytes     int64 `json:"uplinkBytes,omitempty"`
	DownlinkBytes   int64 `json:"downlinkBytes,omitempty"`
	UplinkBitsSec   int64 `json:"uplinkBitsSec,omitempty"`
	DownlinkBitsSec int64 `json:"downlinkBitsSec,omitempty"`
}

type AppState struct {
	Version        string          `json:"version"`
	Subscriptions  []Subscription  `json:"subscriptions"`
	SelectedNodeID string          `json:"selectedNodeId,omitempty"`
	SelectedSubID  string          `json:"selectedSubscriptionId,omitempty"`
	Favorites      map[string]bool `json:"favorites,omitempty"`
	Settings       Settings        `json:"settings"`
	Core           CoreState       `json:"core"`
	Connection     ConnectionState `json:"connection"`
	RecentNodeIDs  []string        `json:"recentNodeIds,omitempty"`
}

func DefaultSettings() Settings {
	return Settings{
		ConnectionMode: "system-proxy",
		AutoRefresh:    true,
		AutoConnect:    false,
		BlockAds:       false,
		MinimizeToTray: true,
		DNS1:           "1.1.1.1",
		DNS2:           "8.8.8.8",
		TestURL:        "https://www.gstatic.com/generate_204",
		SupportURL:     "https://t.me/ms7support",
		UpdateURL:      "https://github.com/smokerms7/ms7vpn",
	}
}

func DefaultState() AppState {
	return AppState{
		Version:       AppVersion,
		Subscriptions: []Subscription{},
		Favorites:     map[string]bool{},
		Settings:      DefaultSettings(),
		Core: CoreState{
			Version: "v26.3.27",
		},
		Connection:    ConnectionState{},
		RecentNodeIDs: []string{},
	}
}
