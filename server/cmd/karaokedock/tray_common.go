package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type trayLinks struct {
	AdminURL string
	TVURL    string
	DataDir  string
}

// openExistingAdmin checks if KaraokeDock is already running on port, and if so,
// opens the Admin page in the default browser and returns true.
func openExistingAdmin(port int) bool {
	client := &http.Client{Timeout: 800 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/ping", port))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	var body struct {
		App string `json:"app"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil || body.App != "karaokedock" {
		return false
	}
	openBrowser(fmt.Sprintf("http://localhost:%d/admin", port))
	return true
}
