package model

import "time"

const AppVersion = "ms7.vs2.4"

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
	// Mode — чем защищаемся: обычным туннелем (vpn) или обходом DPI без
	// сервера (zapret). Поле отдельное от ConnectionMode: тот выбирает
	// способ завернуть трафик и к «Запрету» отношения не имеет.
	Mode string `json:"mode"` // vpn | zapret

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

	// Оформление окна: brand (фирменное), light (светлое), dark (чёрное).
	Theme string `json:"theme"`
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

// ZapretState — состояние режима «Запрет» для окна.
type ZapretState struct {
	// Available — лежат ли рядом с программой файлы режима. На не-Windows и
	// при вырезанном антивирусом бандле режим недоступен, и окно должно это
	// показать, а не предлагать включение, которое всегда падает.
	Available bool `json:"available"`

	Running      bool      `json:"running"`
	StrategyID   string    `json:"strategyId,omitempty"`
	StrategyName string    `json:"strategyName,omitempty"`
	StartedAt    time.Time `json:"startedAt,omitempty"`

	// Picked — подбор хоть раз доходил до конца. По этому признаку главный
	// экран решает, предлагать подбор при первом включении или просто
	// включаться с сохранённой стратегией.
	Picked bool `json:"picked"`

	LastError string `json:"lastError,omitempty"`

	// Pick — ход подбора. Пусто, когда подбор не запускался.
	Pick PickState `json:"pick"`
}

// PickState — ход автоподбора стратегии.
type PickState struct {
	Running   bool   `json:"running"`
	Index     int    `json:"index"`
	Total     int    `json:"total"`
	Current   string `json:"current,omitempty"`
	CurrentID string `json:"currentId,omitempty"`
	Done      bool   `json:"done"`
	Found     string `json:"found,omitempty"`
	FoundID   string `json:"foundId,omitempty"`

	// Full — найденная стратегия открыла и YouTube, и Discord. Только в этом
	// случае окно подбора закрывается само.
	Full  bool   `json:"full"`
	Error string `json:"error,omitempty"`
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
	Zapret         ZapretState     `json:"zapret"`
	RecentNodeIDs  []string        `json:"recentNodeIds,omitempty"`
}

func DefaultSettings() Settings {
	return Settings{
		Mode:           ModeVPN,
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
		Theme:          ThemeBrand,
	}
}

// Чем защищаемся. «Запрет» не требует сервера и не тратит подписку, но
// помогает только там, где провайдер режет по имени сайта.
const (
	ModeVPN    = "vpn"
	ModeZapret = "zapret"
)

// Оформления окна. Пустое значение в старых файлах состояния означает
// фирменное — так установки, сделанные до появления выбора, не меняют вид.
const (
	ThemeBrand = "brand"
	ThemeLight = "light"
	ThemeDark  = "dark"
)

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
