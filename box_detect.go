package workcellcomponents

import (
	"context"
	"errors"
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

const defaultBoxDetectIntervalSec = 4.0

type BoxDetectConfig struct {
	// IntervalSeconds is how long the infeed takes to present the next box
	// after one is taken.
	IntervalSeconds float64 `json:"interval_seconds,omitempty"`

	// StartEmpty presents no box until the first interval has elapsed.
	// Default is to start with a box waiting.
	StartEmpty bool `json:"start_empty,omitempty"`
}

func (c *BoxDetectConfig) Validate(_ string) ([]string, []string, error) {
	if c.IntervalSeconds < 0 {
		return nil, nil, errors.New("interval_seconds must be zero or greater")
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
		interval: time.Duration(interval * float64(time.Second)),
	}
	if cfg.StartEmpty {
		b.readyAt = time.Now().Add(b.interval)
	}
	return b, nil
}

// present reports whether a box is waiting, and how long until one is.
// Caller holds the lock.
func (b *boxDetect) present() (bool, time.Duration) {
	if b.readyAt.IsZero() {
		return true, 0
	}
	remaining := time.Until(b.readyAt)
	if remaining <= 0 {
		// The box has arrived; latch it so the reading stops depending on
		// the clock until it is taken again.
		b.readyAt = time.Time{}
		return true, 0
	}
	return false, remaining
}

func (b *boxDetect) Readings(
	_ context.Context, _ map[string]interface{},
) (map[string]interface{}, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	here, remaining := b.present()
	return map[string]interface{}{
		"box_present":        here,
		"seconds_until_next": remaining.Seconds(),
	}, nil
}

func (b *boxDetect) DoCommand(
	_ context.Context, cmd map[string]interface{},
) (map[string]interface{}, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch {
	case cmd["take"] == true:
		here, _ := b.present()
		if !here {
			return map[string]interface{}{
				"taken": false,
				"error": "no box at the pick-station",
			}, nil
		}
		b.readyAt = time.Now().Add(b.interval)
		return map[string]interface{}{
			"taken":              true,
			"seconds_until_next": b.interval.Seconds(),
		}, nil

	case cmd["reset"] == true:
		b.readyAt = time.Time{}
		return map[string]interface{}{"box_present": true}, nil

	default:
		return map[string]interface{}{
			"error": "unknown command; expected take or reset",
		}, nil
	}
}
