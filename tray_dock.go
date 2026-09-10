package workcellcomponents

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.viam.com/rdk/components/sensor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

// TrayDockModel — a presence sensor over the outbound tray dock.
//
// It answers one question: is an empty tray docked and ready to pack onto?
//
// Nothing downstream of the dock is simulated, so this models the outbound
// conveyor rather than reading it. A tray is docked; when the cell dispatches
// the full one (DoCommand {"dispatch": true}), the tray leaves and an empty
// replacement docks exchange_seconds later. That is enough to drive a real
// control loop: the caller has to wait for the exchange rather than assume a
// tray is always there.
var TrayDockModel = resource.NewModel("viam", "workcell-components", "tray-dock")

const (
	defaultTrayExchangeSec = 8.0
	maxTrayExchangeSec     = 86400.0
)

type TrayDockConfig struct {
	// ExchangeSeconds is how long a dispatch takes: the full tray leaving
	// and the empty replacement docking. Omit it, or set 0, for the
	// 8-second default.
	ExchangeSeconds float64 `json:"exchange_seconds,omitempty"`

	// StartEmpty docks no tray until the first exchange has elapsed.
	// Default is to start with an empty tray docked.
	StartEmpty bool `json:"start_empty,omitempty"`
}

func (c *TrayDockConfig) Validate(_ string) ([]string, []string, error) {
	if c.ExchangeSeconds < 0 {
		return nil, nil, errors.New("exchange_seconds must be zero or greater")
	}
	if c.ExchangeSeconds > maxTrayExchangeSec {
		return nil, nil, fmt.Errorf("exchange_seconds must be %.0f or less",
			maxTrayExchangeSec)
	}
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(sensor.API, TrayDockModel,
		resource.Registration[sensor.Sensor, *TrayDockConfig]{
			Constructor: newTrayDock,
		},
	)
}

type trayDock struct {
	resource.Named
	resource.AlwaysRebuild
	resource.TriviallyCloseable

	logger logging.Logger

	mu       sync.Mutex
	exchange time.Duration
	// dockedAt is when the replacement tray docks. Zero means one is
	// already docked.
	dockedAt time.Time
}

func newTrayDock(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (sensor.Sensor, error) {
	cfg, err := resource.NativeConfig[*TrayDockConfig](conf)
	if err != nil {
		return nil, err
	}

	exchange := cfg.ExchangeSeconds
	if exchange == 0 {
		exchange = defaultTrayExchangeSec
	}

	t := &trayDock{
		Named:    conf.ResourceName().AsNamed(),
		logger:   logger,
		exchange: time.Duration(exchange * float64(time.Second)),
	}
	if cfg.StartEmpty {
		t.dockedAt = time.Now().Add(t.exchange)
	}
	return t, nil
}

// docked reports whether a tray is docked, and how long until one is.
// Read-only; caller holds the lock.
func (t *trayDock) docked() (bool, time.Duration) {
	if t.dockedAt.IsZero() {
		return true, 0
	}
	if remaining := time.Until(t.dockedAt); remaining > 0 {
		return false, remaining
	}
	return true, 0
}

func (t *trayDock) Readings(
	_ context.Context, _ map[string]interface{},
) (map[string]interface{}, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	here, remaining := t.docked()
	return map[string]interface{}{
		"tray_present":         here,
		"seconds_until_docked": remaining.Seconds(),
		"exchange_seconds":     t.exchange.Seconds(),
	}, nil
}

func (t *trayDock) DoCommand(
	_ context.Context, cmd map[string]interface{},
) (map[string]interface{}, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch {
	case isTruthy(cmd["dispatch"]):
		here, _ := t.docked()
		if !here {
			return map[string]interface{}{
				"dispatched": false,
				"error":      "no tray at the dock",
			}, nil
		}
		t.dockedAt = time.Now().Add(t.exchange)
		return map[string]interface{}{
			"dispatched":           true,
			"seconds_until_docked": t.exchange.Seconds(),
		}, nil

	case isTruthy(cmd["reset"]):
		t.dockedAt = time.Time{}
		return map[string]interface{}{"tray_present": true}, nil

	default:
		return nil, fmt.Errorf("tray-dock: unknown command %v", cmd)
	}
}
