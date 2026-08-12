package workcellcomponents

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"go.viam.com/rdk/components/sensor"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/services/worldstatestore"
)

// PalletEmptyModel — reports how much of the pallet is occupied.
//
// The reading is DERIVED, not told: it asks the pack sequencer for the pack
// progress it already maintains. Whoever placed the boxes reports each
// placement to the sequencer as part of placing it, so this sensor reads cell
// state rather than a caller's variable, and a pallet reset on the sequencer
// reads as empty here on the next reading.
//
// Deliberately NOT counting transforms from ListUUIDs: those are drawn box
// visuals, which include the box sitting at the pick-station before it is
// grasped and the one riding the gripper in transit. Counting them reports a
// pallet as occupied before anything is on it.
var PalletEmptyModel = resource.NewModel("viam", "workcell-components", "pallet-empty")

const defaultWorldStateStoreName = "pack-sequencer"

type PalletEmptyConfig struct {
	// WorldStateStore names the world state store holding the pack state.
	// Defaults to "pack-sequencer".
	WorldStateStore string `json:"world_state_store,omitempty"`
}

func (c *PalletEmptyConfig) Validate(_ string) ([]string, []string, error) {
	if c.WorldStateStore != "" && strings.TrimSpace(c.WorldStateStore) == "" {
		return nil, nil, errors.New("world_state_store must name a resource")
	}
	// Fully qualified, so the graph edge is API-checked rather than resolved
	// by simple-name search across every component and service.
	return []string{worldstatestore.Named(c.storeName()).String()}, nil, nil
}

func (c *PalletEmptyConfig) storeName() string {
	if name := strings.TrimSpace(c.WorldStateStore); name != "" {
		return name
	}
	return defaultWorldStateStoreName
}

func init() {
	resource.RegisterComponent(sensor.API, PalletEmptyModel,
		resource.Registration[sensor.Sensor, *PalletEmptyConfig]{
			Constructor: newPalletEmpty,
		},
	)
}

// store and logger are set once in the constructor and never mutated:
// AlwaysRebuild means a configuration change destroys and rebuilds this
// resource, so there is no reconfigure path and no lock is needed.
type palletEmpty struct {
	resource.Named
	resource.AlwaysRebuild
	resource.TriviallyCloseable

	logger logging.Logger
	store  worldstatestore.Service
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

	return &palletEmpty{
		Named:  conf.ResourceName().AsNamed(),
		logger: logger,
		store:  store,
	}, nil
}

// The pack sequencer renamed its progress verb between versions: 0.3.0, which
// the viam101-workcell fragment pins, answers get_progress and reports
// placed_count; 0.4.0 and later answer get_status and report placed. Ask for
// both rather than pinning this sensor to one sequencer.
var progressVerbs = []string{"get_progress", "get_status"}

func (p *palletEmpty) Readings(
	ctx context.Context, _ map[string]interface{},
) (map[string]interface{}, error) {
	var status map[string]interface{}
	var err error
	for _, verb := range progressVerbs {
		status, err = p.store.DoCommand(ctx, map[string]interface{}{verb: true})
		if err == nil {
			break
		}
	}
	if err != nil {
		// A store that is up but erroring is otherwise invisible on the card.
		p.logger.Warnw("pallet-empty: no progress verb answered",
			"tried", progressVerbs, "error", err)
		return nil, err
	}

	placed := int(asFloat(status["placed_count"]))
	if _, ok := status["placed_count"]; !ok {
		placed = int(asFloat(status["placed"]))
	}
	total := int(asFloat(status["total"]))
	complete, _ := status["complete"].(bool)

	return map[string]interface{}{
		"pallet_empty":    placed == 0,
		"pallet_full":     complete || (total > 0 && placed >= total),
		"boxes_on_pallet": placed,
		"capacity":        total,
	}, nil
}

func (p *palletEmpty) DoCommand(
	_ context.Context, cmd map[string]interface{},
) (map[string]interface{}, error) {
	// The reading is derived from the sequencer, so there is nothing here to
	// set. Clearing the pallet is the sequencer's job (reset_progress).
	return nil, fmt.Errorf("pallet-empty: no commands, it reads %s: %v",
		defaultWorldStateStoreName, cmd)
}
