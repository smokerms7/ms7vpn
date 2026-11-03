// vpncheck validates both engines using an existing subscription without
// changing that subscription. Run with -mode tun from an elevated terminal.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ms7vpn/internal/app"
	"ms7vpn/internal/model"
	"ms7vpn/internal/store"
)

type result struct {
	Engine  string `json:"engine"`
	Mode    string `json:"mode"`
	Node    string `json:"node"`
	OK      bool   `json:"ok"`
	Error   string `json:"error,omitempty"`
	Details string `json:"details,omitempty"`
}

func main() {
	mode := flag.String("mode", "system-proxy", "tun or system-proxy")
	match := flag.String("match", "Финляндия", "server name contains")
	report := flag.String("report", "vpncheck-report.json", "JSON report path")
	flag.Parse()
	if *mode != "tun" && *mode != "system-proxy" {
		fmt.Fprintln(os.Stderr, "invalid mode")
		os.Exit(2)
	}
	if *mode == "tun" && !app.IsAdministrator() {
		fmt.Fprintln(os.Stderr, "TUN test requires Run as administrator")
		os.Exit(2)
	}
	base, err := os.UserConfigDir()
	if err != nil {
		panic(err)
	}
	original, err := store.New(filepath.Join(base, "MS7VPN")).Load()
	if err != nil {
		panic(err)
	}
	var node model.Node
	for _, sub := range original.Subscriptions {
		for _, n := range sub.Nodes {
			if n.Protocol == "vless" && n.Transport == "raw" && strings.Contains(n.Name, *match) {
				node = n
				break
			}
		}
		if node.ID != "" {
			break
		}
	}
	if node.ID == "" {
		fmt.Fprintln(os.Stderr, "no matching VLESS/TCP node")
		os.Exit(2)
	}
	reportPath, err := filepath.Abs(*report)
	if err != nil {
		panic(err)
	}
	dir, err := os.MkdirTemp(filepath.Dir(reportPath), "profile-")
	if err != nil {
		panic(err)
	}
	// Contains private credentials: remove the temporary profile after all cores stop.
	defer os.RemoveAll(dir)
	state := model.DefaultState()
	state.Subscriptions = []model.Subscription{{ID: "diagnostic", Name: "Diagnostic", Nodes: []model.Node{node}}}
	state.SelectedNodeID = node.ID
	state.SelectedSubID = "diagnostic"
	if err := store.New(dir).Save(state); err != nil {
		panic(err)
	}
	application, err := app.New(dir)
	if err != nil {
		panic(err)
	}
	defer application.Shutdown()
	results := []result{}
	for _, engine := range []string{"xray", "sing-box"} {
		settings := application.Snapshot().Settings
		settings.Engine = engine
		settings.ConnectionMode = *mode
		_, err := application.SaveSettings(settings)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			_, err = application.Connect(ctx, node.ID, *mode)
			cancel()
		}
		r := result{Engine: engine, Mode: *mode, Node: node.Name, OK: err == nil}
		if err != nil {
			r.Error = err.Error()
			r.Details = application.CoreLog(15)
		}
		if _, stopErr := application.Disconnect(); stopErr != nil {
			r.OK = false
			r.Error += " disconnect: " + stopErr.Error()
		}
		results = append(results, r)
		fmt.Printf("%s %s: %v\n", engine, *mode, r.OK)
	}
	data, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(*report, data, 0600); err != nil {
		panic(err)
	}
	// Avoid os.Exit before deferred network cleanup.
	for _, r := range results {
		if !r.OK {
			fmt.Println("One or more checks failed; see report.")
			return
		}
	}
	fmt.Println("Both engines passed; network restored.")
}
