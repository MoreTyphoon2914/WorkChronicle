package location

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"worktracker/internal/model"
	wtwindows "worktracker/internal/windows"
)

type Fingerprint struct {
	IP         string `json:"IP"`
	Gateway    string `json:"Gateway"`
	GatewayMAC string `json:"GatewayMAC"`
}
type Runner interface {
	Run(context.Context) ([]byte, error)
}
type PowerShellRunner struct{}

const networkScript = `$cfg = Get-NetIPConfiguration -InterfaceAlias "Wi-Fi" -ErrorAction SilentlyContinue
if (-not $cfg) { return }
$gw = $cfg.IPv4DefaultGateway.NextHop
if (-not $gw) { return }
Test-Connection -ComputerName $gw -Count 1 -Quiet -ErrorAction SilentlyContinue | Out-Null
$neighbor = Get-NetNeighbor -InterfaceAlias "Wi-Fi" -IPAddress $gw -ErrorAction SilentlyContinue
[PSCustomObject]@{ IP=$cfg.IPv4Address.IPAddress; Gateway=$gw; GatewayMAC=$neighbor.LinkLayerAddress } | ConvertTo-Json -Compress`

func (PowerShellRunner) Run(ctx context.Context) ([]byte, error) {
	return wtwindows.HiddenCommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command", networkScript).Output()
}

type Result struct {
	Location   model.Location
	Evidence   model.LocationEvidence
	Health     model.HealthLevel
	ObservedAt time.Time
}
type Detector struct {
	Runner       Runner
	Office, Home map[string]struct{}
	StaleFor     time.Duration
	last         *Result
}

func New(r Runner, office, home []string, stale time.Duration) *Detector {
	d := &Detector{Runner: r, Office: macSet(office), Home: macSet(home), StaleFor: stale}
	return d
}
func (d *Detector) Observe(ctx context.Context, now time.Time) (Result, error) {
	b, err := d.Runner.Run(ctx)
	if err == nil && len(strings.TrimSpace(string(b))) > 0 {
		var f Fingerprint
		if json.Unmarshal(b, &f) == nil {
			mac := normalize(f.GatewayMAC)
			loc := model.Remote
			if _, ok := d.Office[mac]; ok {
				loc = model.Office
			}
			r := Result{Location: loc, Evidence: model.Confirmed, Health: model.Healthy, ObservedAt: now}
			d.last = &r
			return r, nil
		}
	}
	if d.last != nil && now.Sub(d.last.ObservedAt) <= d.StaleFor {
		r := *d.last
		r.Evidence = model.Stale
		r.Health = model.Degraded
		return r, fmt.Errorf("physical network temporarily unavailable")
	}
	return Result{Location: model.Remote, Evidence: model.Fallback, Health: model.Degraded, ObservedAt: now}, fmt.Errorf("physical network unavailable")
}
func normalize(s string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(s), ":", "-"))
}
func macSet(xs []string) map[string]struct{} {
	m := map[string]struct{}{}
	for _, x := range xs {
		if n := normalize(x); n != "" {
			m[n] = struct{}{}
		}
	}
	return m
}
