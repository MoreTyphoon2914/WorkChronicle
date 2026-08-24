package appstate

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type VLCDetector struct {
	URL, Password string
	Client        *http.Client
}

func (v *VLCDetector) ID() string { return "vlc" }
func (v *VLCDetector) Observe(ctx context.Context) (Observation, error) {
	now := time.Now().UTC()
	obs := Observation{Detector: v.ID(), State: "unavailable", ObservedAt: now}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.URL, nil)
	if err != nil {
		return obs, err
	}
	req.SetBasicAuth("", v.Password)
	resp, err := v.Client.Do(req)
	if err != nil {
		return obs, fmt.Errorf("vlc unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return obs, fmt.Errorf("vlc authentication failed")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return obs, fmt.Errorf("vlc returned %s", resp.Status)
	}
	var data struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return obs, fmt.Errorf("invalid vlc response: %w", err)
	}
	state := strings.ToLower(data.State)
	switch state {
	case "playing", "paused", "stopped":
		obs.State = state
		obs.Available = true
		return obs, nil
	default:
		return obs, fmt.Errorf("vlc returned unknown state")
	}
}
