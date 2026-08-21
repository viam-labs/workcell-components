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

// BoxDetectModel — a presence sensor over the pick-station infeed.
//
// It answers one question: is there a box waiting to be picked?
//
// Nothing upstream of the pick-station is simulated, so this models the
// conveyor rather than reading it. A box is present; when the picker takes it
// (DoCommand {"take": true}), the next one arrives interval_seconds later.
// That is enough to drive a real control loop: the caller has to wait for the
// cell rather than assume a box is always there.
var BoxDetectModel = resource.NewModel("viam", "workcell-components", "box-detect")

const (
	defaultBoxDetectIntervalSec = 4.0
	// A day is far past any useful infeed rate, and keeps the float to
	// time.Duration conversion well inside int64.
	maxBoxDetectIntervalSec = 86400.0
)

type BoxDetectConfig struct {
	// IntervalSeconds is how long the infeed takes to present the next box
	// after one is taken. Omit it, or set 0, for the 4-second default.
	IntervalSeconds float64 `json:"interval_seconds,omitempty"`

	// StartEmpty presents no box until the first interval has elapsed.
	// Default is to start with a box waiting.
	StartEmpty bool `json:"start_empty,omitempty"`

	// Enabled turns the infeed on. Disabled (the default), the sensor
	// reports no box, take refuses, and the paired pick-station renders
	// no infeed box: the conveyor stands still until someone flips
	// this to true.
	Enabled bool `json:"enabled,omitempty"`
}

func (c *BoxDetectConfig) Validate(_ string) ([]string, []string, error) {
	if c.IntervalSeconds < 0 {
		return nil, nil, errors.New("interval_seconds must be zero or greater")
	}
	if c.IntervalSeconds > maxBoxDetectIntervalSec {
		return nil, nil, fmt.Errorf("interval_seconds must be %.0f or less",
			maxBoxDetectIntervalSec)
	}
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(sensor.API, BoxDetectModel,
		resource.Registration[sensor.Sensor, *BoxDetectConfig]{
			Constructor: newBoxDetect,
		},
	)
}

type boxDetect struct {
	resource.Named
	resource.AlwaysRebuild
	resource.TriviallyCloseable

	logger logging.Logger

	mu       sync.Mutex
	enabled  bool
	interval time.Duration
	// readyAt is when the next box is presented. Zero means one is already
	// waiting.
	readyAt time.Time
}

func newBoxDetect(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (sensor.Sensor, error) {
	cfg, err := resource.NativeConfig[*BoxDetectConfig](conf)
	if err != nil {
		return nil, err
	}

	interval := cfg.IntervalSeconds
	if interval == 0 {
		interval = defaultBoxDetectIntervalSec
	}

	b := &boxDetect{
		Named:    conf.ResourceName().AsNamed(),
		logger:   logger,
		enabled:  cfg.Enabled,
		interval: time.Duration(interval * float64(time.Second)),
	}
	if cfg.StartEmpty {
		b.readyAt = time.Now().Add(b.interval)
	}
	return b, nil
}

// present reports whether a box is waiting, and how long until one is.
// Read-only; caller holds the lock.
func (b *boxDetect) present() (bool, time.Duration) {
	if !b.enabled {
		return false, 0
	}
	if b.readyAt.IsZero() {
		return true, 0
	}
	if remaining := time.Until(b.readyAt); remaining > 0 {
		return false, remaining
	}
	return true, 0
}

func (b *boxDetect) Readings(
	_ context.Context, _ map[string]interface{},
) (map[string]interface{}, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	here, remaining := b.present()
	return map[string]interface{}{
		"enabled":            b.enabled,
		"box_present":        here,
		"seconds_until_next": remaining.Seconds(),
		"interval_seconds":   b.interval.Seconds(),
	}, nil
}

func (b *boxDetect) DoCommand(
	_ context.Context, cmd map[string]interface{},
) (map[string]interface{}, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Truthiness rather than an exact `== true`: a protobuf Struct round-trip
	// can deliver the flag as a number or a string, and the caller meant yes.
	switch {
	case isTruthy(cmd["take"]):
		here, _ := b.present()
		if !here {
			reason := "no box at the pick-station"
			if !b.enabled {
				reason = "infeed disabled: set enabled: true on box-detect"
			}
			return map[string]interface{}{
				"taken": false,
				"error": reason,
			}, nil
		}
		b.readyAt = time.Now().Add(b.interval)
		return map[string]interface{}{
			"taken":              true,
			"seconds_until_next": b.interval.Seconds(),
		}, nil

	case isTruthy(cmd["reset"]):
		b.readyAt = time.Time{}
		return map[string]interface{}{"box_present": true}, nil

	default:
		return nil, fmt.Errorf("box-detect: unknown command %v", cmd)
	}
}
