//go:build ms7android

package webui

import "embed"

// Assets — интерфейс ПК (assets) и мобильные добавки (mobile) для сборки
// под Android. Windows-сборка встраивает assets сама, в webui_windows.go.
//
//go:embed assets/* mobile/*
var Assets embed.FS
