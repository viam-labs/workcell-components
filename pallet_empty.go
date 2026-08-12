package workcellcomponents

import (
	"bytes"
	"context"
	"errors"
	"sync"

	"go.viam.com/rdk/components/sensor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/services/worldstatestore"
)

// PalletEmptyModel — reports how much of the pallet is occupied.
//
// The reading is DERIVED, not told: it counts the box transforms the pack
// sequencer is holding. Whoever placed those boxes does not have to report
// anything, and a pallet cleared by another operator reads as empty here
// immediately.
var PalletEmptyModel = resource.NewModel("viam", "workcell-components", "pallet-empty")

// Transform UUIDs for boxes are "box-<seq>" (or "box-<seq>-v<N>" while a box
// is in flight), so this prefix identifies them.
var boxUUIDPrefix = []byte("box-")

const defaultPalletCapacity = 8

type PalletEmptyConfig struct {
	// WorldStateStore names the world state store holding the pack state.
	// Defaults to "pack-sequencer".
	WorldStateStore string `json:"world_state_store,omitempty"`

	// Capacity is how many boxes a full pallet holds. Used only to report
	// pallet_full; zero disables that reading.
	Capacity int `json:"capacity,omitempty"`
}

func (c *PalletEmptyConfig) Validate(_ string) ([]string, []string, error) {
	if c.Capacity < 0 {
		return nil, nil, errors.New("capacity must be zero or greater")
	}
	// Declaring the store as a required dependency means viam-server builds
	// this sensor only once the store is running.
	return []string{c.storeName()}, nil, nil
}

func (c *PalletEmptyConfig) storeName() string {
	if c.WorldStateStore == "" {
		return "pack-sequencer"
	}
	return c.WorldStateStore
}

func init() {
	resource.RegisterComponent(sensor.API, PalletEmptyModel,
		resource.Registration[sensor.Sensor, *PalletEmptyConfig]{
			Constructor: newPalletEmpty,
		},
	)
}

type palletEmpty struct {
	resource.Named
	resource.AlwaysRebuild
	resource.TriviallyCloseable

	logger logging.Logger

	mu       sync.Mutex
	store    worldstatestore.Service
	capacity int
}

func newPalletEmpty(
	_ context.Context,
	deps resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (sensor.Sensor, error) {
	cfg, err := resource.NativeConfig[*PalletEmptyConfig](conf)
	if err != nil {
		return nil, err
	}

	store, err := resource.FromDependencies[worldstatestore.Service](
		deps, worldstatestore.Named(cfg.storeName()))
	if err != nil {
		return nil, err
	}

	capacity := cfg.Capacity
	if capacity == 0 {
		capacity = defaultPalletCapacity
	}

	return &palletEmpty{
		Named:    conf.ResourceName().AsNamed(),
		logger:   logger,
		store:    store,
		capacity: capacity,
	}, nil
}

func (p *palletEmpty) Readings(
	ctx context.Context, _ map[string]interface{},
) (map[string]interface{}, error) {
	p.mu.Lock()
	store, capacity := p.store, p.capacity
	p.mu.Unlock()

	uuids, err := store.ListUUIDs(ctx, nil)
	if err != nil {
		return nil, err
	}

	count := 0
	for _, u := range uuids {
		if bytes.HasPrefix(u, boxUUIDPrefix) {
			count++
		}
	}

	return map[string]interface{}{
		"pallet_empty":    count == 0,
		"pallet_full":     count >= capacity,
		"boxes_on_pallet": count,
		"capacity":        capacity,
	}, nil
}

func (p *palletEmpty) DoCommand(
	_ context.Context, _ map[string]interface{},
) (map[string]interface{}, error) {
	// The reading is derived from the world state store, so there is nothing
	// here to set. Clearing the pallet is the sequencer's job.
	return map[string]interface{}{
		"error": "pallet-empty has no commands; it reads the world state store",
	}, nil
}
