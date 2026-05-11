package workcellcomponents

import (
	"context"
	"fmt"
	"sync"

	"github.com/golang/geo/r3"
	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/referenceframe"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
)

// PalletModel is the resource model for the pallet component. The pallet's
// world pose comes from its `frame.translation` / `frame.orientation`, and
// its physical dimensions come from `frame.geometry` (a box). All
// pallet-aware consumers (the pack-sequencer service, the palletizer) read
// pose and dimensions from the pallet component via DoCommand so there's
// a single source of truth and the user can drag-and-save the frame
// directly from the Viam 3D viewer.
var PalletModel = resource.NewModel("viam", "workcell-components", "pallet")

// Standard GMA wooden-pallet dimensions (48" × 40" × ~6"). Used as
// fallbacks when frame.geometry is missing — the typical case for a
// freshly-added pallet component before the user has dragged it in the
// 3D viewer or filled in the config.
const (
	DefaultPalletWidthMM     = 1219.2 // 48 inches — pallet X
	DefaultPalletLengthMM    = 1016.0 // 40 inches — pallet Y
	DefaultPalletThicknessMM = 152.4  // 6 inches  — wooden base height (Z)
)

// PalletConfig is intentionally minimal. The pallet's pose and geometry
// live in the standard `frame` block on the resource config — that's how
// the Viam 3D viewer knows to render and drag the component.
type PalletConfig struct {
	Label string `json:"label,omitempty"`
}

func (c *PalletConfig) Validate(_ string) ([]string, []string, error) {
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(generic.API, PalletModel,
		resource.Registration[resource.Resource, *PalletConfig]{
			Constructor: newPallet,
		},
	)
}

type pallet struct {
	resource.AlwaysRebuild
	resource.TriviallyCloseable

	name   resource.Name
	logger logging.Logger

	mu sync.Mutex
	// Resolved at construction from conf.Frame. AlwaysRebuild means a
	// frame edit (drag-and-save in 3D viewer, or JSON edit) replays
	// newPallet, so these stay current without runtime polling.
	pose                       spatialmath.Pose
	width, length, thickness   float64
	cfg                        PalletConfig
}

func newPallet(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (resource.Resource, error) {
	cfg, err := resource.NativeConfig[*PalletConfig](conf)
	if err != nil {
		return nil, err
	}

	pose, w, l, t := poseAndDimsFromFrame(conf.Frame)

	logger.Infow("pallet configured",
		"x", pose.Point().X, "y", pose.Point().Y, "z", pose.Point().Z,
		"width_mm", w, "length_mm", l, "thickness_mm", t,
	)

	return &pallet{
		name:      conf.ResourceName(),
		logger:    logger,
		pose:      pose,
		width:     w,
		length:    l,
		thickness: t,
		cfg:       *cfg,
	}, nil
}

// poseAndDimsFromFrame extracts the pallet's world pose and its box-geometry
// dimensions from a LinkConfig. Falls back to identity pose / standard GMA
// dimensions for any missing piece — a freshly-added pallet component with
// no frame still has sensible defaults.
func poseAndDimsFromFrame(f *referenceframe.LinkConfig) (spatialmath.Pose, float64, float64, float64) {
	w, l, t := DefaultPalletWidthMM, DefaultPalletLengthMM, DefaultPalletThicknessMM
	point := r3.Vector{}
	var orient spatialmath.Orientation = &spatialmath.OrientationVectorDegrees{OZ: 1}

	if f != nil {
		point = f.Translation
		if f.Orientation != nil {
			if o, err := f.Orientation.ParseConfig(); err == nil {
				orient = o
			}
		}
		if f.Geometry != nil && f.Geometry.X > 0 && f.Geometry.Y > 0 && f.Geometry.Z > 0 {
			w = f.Geometry.X
			l = f.Geometry.Y
			t = f.Geometry.Z
		}
	}

	return spatialmath.NewPose(point, orient), w, l, t
}

func (p *pallet) Name() resource.Name { return p.name }

// DoCommand surface:
//
//	{"get_pose": true}        → {x, y, z, o_x, o_y, o_z, theta}
//	{"get_dimensions": true}  → {width_mm, length_mm, thickness_mm}
//	{"get_attributes": true}  → {label, width_mm, length_mm, thickness_mm,
//	                             pose: {...}}
func (p *pallet) DoCommand(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if _, ok := cmd["get_pose"]; ok {
		return poseToWorldMap(p.pose), nil
	}
	if _, ok := cmd["get_dimensions"]; ok {
		return map[string]interface{}{
			"width_mm":     p.width,
			"length_mm":    p.length,
			"thickness_mm": p.thickness,
		}, nil
	}
	if _, ok := cmd["get_attributes"]; ok {
		return map[string]interface{}{
			"label":        p.cfg.Label,
			"width_mm":     p.width,
			"length_mm":    p.length,
			"thickness_mm": p.thickness,
			"pose":         poseToWorldMap(p.pose),
		}, nil
	}
	return nil, fmt.Errorf("pallet: unknown command %v", cmd)
}

func poseToWorldMap(pose spatialmath.Pose) map[string]interface{} {
	pt := pose.Point()
	ov := pose.Orientation().OrientationVectorDegrees()
	return map[string]interface{}{
		"x": pt.X, "y": pt.Y, "z": pt.Z,
		"o_x": ov.OX, "o_y": ov.OY, "o_z": ov.OZ, "theta": ov.Theta,
	}
}
