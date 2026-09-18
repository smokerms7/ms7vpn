package xray

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

// Счётчики трафика Xray отдаёт через gRPC-метод StatsService/QueryStats.
//
// Тянуть ради двух чисел библиотеку grpc-go (это плюс десяток мегабайт к
// размеру программы) нерационально, поэтому запрос собирается вручную:
// протокол здесь предельно простой — HTTP/2 без шифрования, один кадр
// с длиной и два поля protobuf.

// Traffic — счётчики и скорость исходящего соединения.
type Traffic struct {
	UplinkBytes     int64
	DownlinkBytes   int64
	UplinkBitsSec   int64
	DownlinkBitsSec int64
}

// StatsClient опрашивает локальный API Xray.
type StatsClient struct {
	port   int
	client *http.Client

	mu       sync.Mutex
	lastAt   time.Time
	lastUp   int64
	lastDown int64
}

func NewStatsClient(port int) *StatsClient {
	transport := &http2.Transport{
		// Локальный API работает без TLS, поэтому используем h2c.
		AllowHTTP: true,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, addr)
		},
	}
	return &StatsClient{
		port:   port,
		client: &http.Client{Transport: transport, Timeout: 3 * time.Second},
	}
}

// Query возвращает накопленный трафик и скорость с прошлого опроса.
func (c *StatsClient) Query(ctx context.Context) (Traffic, error) {
	stats, err := c.queryStats(ctx)
	if err != nil {
		return Traffic{}, err
	}

	var up, down int64
	for name, value := range stats {
		switch {
		case strings.HasSuffix(name, ">>>uplink"):
			up += value
		case strings.HasSuffix(name, ">>>downlink"):
			down += value
		}
	}

	now := time.Now()
	result := Traffic{UplinkBytes: up, DownlinkBytes: down}

	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.lastAt.IsZero() {
		seconds := now.Sub(c.lastAt).Seconds()
		// Счётчики Xray только растут; при перезапуске ядра они сбрасываются,
		// поэтому отрицательную разницу просто игнорируем.
		if seconds >= 0.2 && up >= c.lastUp && down >= c.lastDown {
			result.UplinkBitsSec = int64(float64(up-c.lastUp) * 8 / seconds)
			result.DownlinkBitsSec = int64(float64(down-c.lastDown) * 8 / seconds)
		}
	}
	c.lastAt, c.lastUp, c.lastDown = now, up, down
	return result, nil
}

// Reset забывает предыдущее измерение — нужен при переподключении.
func (c *StatsClient) Reset() {
	c.mu.Lock()
	c.lastAt = time.Time{}
	c.lastUp, c.lastDown = 0, 0
	c.mu.Unlock()
}

func (c *StatsClient) queryStats(ctx context.Context) (map[string]int64, error) {
	// QueryStatsRequest: поле 1 — строка-шаблон, поле 2 — сбрасывать ли счётчики.
	payload := appendProtoString(nil, 1, "outbound>>>proxy>>>traffic")
	frame := make([]byte, 5, 5+len(payload))
	binary.BigEndian.PutUint32(frame[1:5], uint32(len(payload)))
	frame = append(frame, payload...)

	url := fmt.Sprintf("http://127.0.0.1:%d/xray.app.stats.command.StatsService/QueryStats", c.port)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/grpc")
	request.Header.Set("TE", "trailers")

	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if len(body) < 5 {
		return nil, errors.New("пустой ответ статистики")
	}
	length := binary.BigEndian.Uint32(body[1:5])
	if int(length)+5 > len(body) {
		return nil, errors.New("обрезанный ответ статистики")
	}
	return parseQueryStatsResponse(body[5 : 5+int(length)])
}

// parseQueryStatsResponse разбирает QueryStatsResponse: повторяющееся поле 1
// со вложенным Stat{name=1, value=2}.
func parseQueryStatsResponse(data []byte) (map[string]int64, error) {
	result := map[string]int64{}
	for len(data) > 0 {
		field, wire, rest, err := readProtoTag(data)
		if err != nil {
			return nil, err
		}
		data = rest
		if field != 1 || wire != 2 {
			data, err = skipProtoField(wire, data)
			if err != nil {
				return nil, err
			}
			continue
		}
		size, rest, err := readVarint(data)
		if err != nil || int(size) > len(rest) {
			return nil, errors.New("повреждённый ответ статистики")
		}
		name, value, err := parseStat(rest[:size])
		if err != nil {
			return nil, err
		}
		if name != "" {
			result[name] = value
		}
		data = rest[size:]
	}
	return result, nil
}

func parseStat(data []byte) (string, int64, error) {
	var name string
	var value int64
	for len(data) > 0 {
		field, wire, rest, err := readProtoTag(data)
		if err != nil {
			return "", 0, err
		}
		data = rest
		switch {
		case field == 1 && wire == 2:
			size, rest, err := readVarint(data)
			if err != nil || int(size) > len(rest) {
				return "", 0, errors.New("повреждённое имя счётчика")
			}
			name = string(rest[:size])
			data = rest[size:]
		case field == 2 && wire == 0:
			raw, rest, err := readVarint(data)
			if err != nil {
				return "", 0, err
			}
			value = int64(raw)
			data = rest
		default:
			data, err = skipProtoField(wire, data)
			if err != nil {
				return "", 0, err
			}
		}
	}
	return name, value, nil
}

func appendProtoString(buffer []byte, field int, value string) []byte {
	buffer = appendVarint(buffer, uint64(field)<<3|2)
	buffer = appendVarint(buffer, uint64(len(value)))
	return append(buffer, value...)
}

func appendVarint(buffer []byte, value uint64) []byte {
	for value >= 0x80 {
		buffer = append(buffer, byte(value)|0x80)
		value >>= 7
	}
	return append(buffer, byte(value))
}

func readVarint(data []byte) (uint64, []byte, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		if len(data) == 0 {
			return 0, nil, errors.New("обрыв varint")
		}
		b := data[0]
		data = data[1:]
		value |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return value, data, nil
		}
	}
	return 0, nil, errors.New("слишком длинный varint")
}

func readProtoTag(data []byte) (field int, wire byte, rest []byte, err error) {
	tag, rest, err := readVarint(data)
	if err != nil {
		return 0, 0, nil, err
	}
	return int(tag >> 3), byte(tag & 7), rest, nil
}

func skipProtoField(wire byte, data []byte) ([]byte, error) {
	switch wire {
	case 0:
		_, rest, err := readVarint(data)
		return rest, err
	case 1:
		if len(data) < 8 {
			return nil, errors.New("обрыв поля")
		}
		return data[8:], nil
	case 2:
		size, rest, err := readVarint(data)
		if err != nil || int(size) > len(rest) {
			return nil, errors.New("обрыв поля")
		}
		return rest[size:], nil
	case 5:
		if len(data) < 4 {
			return nil, errors.New("обрыв поля")
		}
		return data[4:], nil
	}
	return nil, fmt.Errorf("неизвестный тип поля %d", wire)
}
