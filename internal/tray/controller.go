package tray

import (
	"context"
	"fmt"
	"time"
)

// UI is implemented by the platform tray. Update may be called from a worker
// goroutine; Run owns the platform UI thread until shutdown.
type UI interface {
	Update(ViewModel)
	Run(context.Context, func()) error
}

type Controller struct {
	Provider Provider
	UI       UI
	Interval time.Duration
}

func (c Controller) Run(ctx context.Context, requestExit func()) error {
	if c.Provider == nil {
		return fmt.Errorf("tray status provider is required")
	}
	if c.UI == nil {
		return fmt.Errorf("tray UI is required")
	}
	if requestExit == nil {
		requestExit = func() {}
	}
	interval := c.Interval
	if interval <= 0 {
		interval = RefreshInterval
	}

	refreshCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	refresh := func() {
		status, err := c.Provider.Status(refreshCtx)
		if err != nil {
			c.UI.Update(FailureView(err))
			return
		}
		c.UI.Update(MapStatus(status))
	}
	go func() {
		defer close(done)
		refresh()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-refreshCtx.Done():
				return
			case <-ticker.C:
				refresh()
			}
		}
	}()
	err := c.UI.Run(refreshCtx, requestExit)
	cancel()
	<-done
	return err
}
