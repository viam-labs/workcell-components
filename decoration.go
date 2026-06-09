package workcellcomponents

import (
	"fmt"
	"sync"

	"github.com/golang/geo/r3"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/referenceframe"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
)

// decorationBase factors out the pose-extraction, label, and standard
// DoCommand scaffolding shared by every workcell-affordance component
// (safety-fence, light-curtain, e-stop, stack-light, tote-stack,
// robot-pedestal, hmi-cabinet, floor-decal, workcell-bounds).
//
// Each affordance embeds a *decorationBase and provides its own
// Config struct + visual builder. The base handles:
//
//   - frame-block pose resolution (drag-to-place in the 3D viewer)
//   - get_visual_pose / get_attributes / set_attributes / get_status
//   - color + visual-options (visible / opacity / show_axes) mutation
//   - mutex discipline (caller-locked methods documented)
//
// Each affordance still owns its `get_visuals` verb because the visual
// layout is what makes the affordance distinctive.
type decorationBase struct {
	resource.AlwaysRebuild
	resource.TriviallyCloseable

	name   resource.Name
	logger logging.Logger
	model  resource.Model

	mu    sync.Mutex
	pose  spatialmath.Pose
	label string
	color Color
	opts  VisualOptions
}

// newDecorationBase pulls the pose from the frame block and seeds the
// label/color from config defaults. Returns a base ready for embed by
// the concrete affordance constructor.
func newDecorationBase(
	name resource.Name,
	logger logging.Logger,
	model resource.Model,
	frame *referenceframe.LinkConfig,
	label string,
	defaultColor Color,
	color *Color,
	opts VisualOptions,
) *decorationBase {
	pose := poseFromLinkConfig(frame)
	c := defaultColor
	if color != nil {
		c = *color
	}
	if label == "" {
		label = name.Name
	}
	return &decorationBase{
		name:   name,
		logger: logger,
		model:  model,
		pose:   pose,
		label:  label,
		color:  c,
		opts:   opts,
	}
}

// poseFromLinkConfig extracts a world-frame pose from a frame block
// (translation + orientation). Returns identity at origin when the
// frame is missing — operators drag-place after adding.
func poseFromLinkConfig(f *referenceframe.LinkConfig) spatialmath.Pose {
	point := r3.Vector{}
	var orient spatialmath.Orientation = &spatialmath.OrientationVectorDegrees{OZ: 1}
	if f != nil {
		point = f.Translation
		if f.Orientation != nil {
			if o, err := f.Orientation.ParseConfig(); err == nil {
				orient = o
			}
		}
	}
	return spatialmath.NewPose(point, orient)
}

// Name implements resource.Resource.
func (d *decorationBase) Name() resource.Name { return d.name }

// applyStandardSet applies the universal mutations from a
// set_attributes map: color, label, visible, show_axes, opacity.
// Returns the changed flag (unused today; reserved for change-tracking).
// Caller must hold d.mu.
func (d *decorationBase) applyStandardSet(m map[string]interface{}) error {
	if cv, ok := m["color"]; ok {
		c, parsed := asColor(cv)
		if !parsed {
			return fmt.Errorf("color must be a {r,g,b,opacity?} object")
		}
		if err := validateColor(c); err != nil {
			return err
		}
		d.color = c
	}
	if lbl, ok := m["label"].(string); ok {
		d.label = lbl
	}
	applyVisualOptions(&d.opts, m)
	return nil
}

// commonAttributesMap is the shared subset of attributes every
// affordance exposes (pose, label, color, visual options).
// Concrete affordances add their own per-type fields.
// Caller must hold d.mu.
func (d *decorationBase) commonAttributesMap() map[string]interface{} {
	out := map[string]interface{}{
		"label": d.label,
		"color": d.color.toMap(),
		"pose":  poseToWorldMap(d.pose),
	}
	mergeVisualOptions(out, d.opts)
	return out
}

// commonStatusMap is the shared health snapshot.
// Caller must hold d.mu.
func (d *decorationBase) commonStatusMap() map[string]interface{} {
	visible := true
	if d.opts.Visible != nil {
		visible = *d.opts.Visible
	}
	return map[string]interface{}{
		"ok":        true,
		"model":     d.model.String(),
		"name":      d.name.String(),
		"visible":   visible,
		"show_axes": d.opts.ShowAxes,
	}
}

// asWireResponse wraps the standard get_visuals response shape.
func asWireResponse(entries []visualWire) (map[string]interface{}, error) {
	out, err := visualsToMaps(entries)
	if err != nil {
		return nil, fmt.Errorf("get_visuals: %w", err)
	}
	return map[string]interface{}{"visuals": out}, nil
}
