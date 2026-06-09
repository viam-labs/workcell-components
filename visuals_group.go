package workcellcomponents

import (
	"fmt"
	"math"

	"github.com/golang/geo/r3"
	"go.viam.com/rdk/spatialmath"
)

// Grouping — every component publishes its sub-primitives under a
// single parent anchor Frame instead of as loose world-frame entries.
//
// Why: a slatted pallet is ~13 boxes, a roller conveyor ~25 — without
// grouping the 3D viewer's entity list is a wall of "pallet/slat-3"
// rows. A parent Frame makes the renderer treat the whole component
// as one hierarchy node: it collapses in a tree-capable viewer, moves
// as a unit, and hides as a unit.
//
// The anchor Frame is labelled "{component}/group". It sits at the
// component's world pose; every child is reparented to it with a pose
// expressed RELATIVE to the anchor. The renderer recomposes
// anchor∘child to the original world placement — but now a pose
// change only has to move the anchor; children ride along untouched.

// groupUnderFrame wraps world-frame child visuals under a parent
// anchor Frame. Returns [frame, child0, child1, ...].
//
//   - componentName: the component's resource name; the anchor frame
//     is labelled "{componentName}/group".
//   - anchor: the component's world pose — where the frame sits.
//   - showAxes: when true, append an X·Y·Z arrow triad as child
//     visuals. NOT wired to the anchor frame's ShowAxesHelper — see the
//     note below.
//   - children: sub-visuals built in WORLD frame (the existing
//     builders compose world poses via compose()). groupUnderFrame
//     converts each to an anchor-relative pose.
//
// Passing nil children yields just the (invisible) anchor frame —
// that is the canonical "component hidden" representation.
//
// Why show_axes is a triad of child arrows, not the frame's
// ShowAxesHelper: ShowAxesHelper is metadata. Toggling it makes the
// anchor frame a metadata-only change, which the renderer can only
// apply via REMOVE+re-ADD (it drops metadata.* on UPDATE). Removing
// the anchor orphans every child parented to it — the whole component
// blinks out until the scene is republished. Emitting the axes as
// ordinary child geometry keeps the anchor frame immutable, so
// toggling show_axes only ever ADDs/REMOVEs the three arrows.
func groupUnderFrame(
	componentName string,
	anchor spatialmath.Pose,
	showAxes bool,
	children []visualWire,
) []visualWire {
	frameLabel := componentName + "/group"
	out := make([]visualWire, 0, len(children)+4)
	out = append(out, visualWire{
		Type:        "frame",
		Label:       frameLabel,
		ParentFrame: defaultParentFrame,
		Pose:        poseToWire(anchor),
		// ShowAxesHelper deliberately left false — keep the anchor
		// immutable so it never respawns and orphans its children.
	})
	extent := 0.0
	for _, c := range children {
		childWorld := wirePoseToSpatial(c.Pose)
		// PoseBetween(anchor, childWorld) is the pose L such that
		// Compose(anchor, L) == childWorld — i.e. childWorld expressed
		// in the anchor's frame. The renderer reverses this.
		local := spatialmath.PoseBetween(anchor, childWorld)
		c.Pose = poseToWire(local)
		c.ParentFrame = frameLabel
		out = append(out, c)
		pt := local.Point()
		for _, v := range []float64{math.Abs(pt.X), math.Abs(pt.Y), math.Abs(pt.Z)} {
			if v > extent {
				extent = v
			}
		}
	}
	if showAxes {
		out = append(out, axisTriad(componentName, frameLabel, extent)...)
	}
	return out
}

// axisTriad returns three arrow visuals — a red +X, green +Y, blue +Z
// triad rooted at the component's origin, parented to its anchor
// frame. `extent` is the largest child offset from the origin; the
// arrows are sized to read against the component without dwarfing it.
func axisTriad(componentName, frameLabel string, extent float64) []visualWire {
	length := extent*1.5 + 90
	if length < 140 {
		length = 140
	}
	if length > 1700 {
		length = 1700
	}
	radius := length * 0.022
	if radius < 4 {
		radius = 4
	}
	axes := []struct {
		suffix     string
		ox, oy, oz float64
		col        visualColorWire
	}{
		{"axis-x", 1, 0, 0, visualColorWire{R: 224, G: 64, B: 64, Opacity: 1}},
		{"axis-y", 0, 1, 0, visualColorWire{R: 70, G: 200, B: 96, Opacity: 1}},
		{"axis-z", 0, 0, 1, visualColorWire{R: 74, G: 132, B: 240, Opacity: 1}},
	}
	out := make([]visualWire, 0, 3)
	for _, a := range axes {
		col := a.col
		// Arrow centres on its mid-shaft; offset by length/2 so the
		// tail sits at the origin and the tip points outward.
		out = append(out, visualWire{
			Type:        "arrow",
			Label:       fmt.Sprintf("%s/%s", componentName, a.suffix),
			ParentFrame: frameLabel,
			Pose: &visualPoseWire{
				X:  a.ox * length / 2,
				Y:  a.oy * length / 2,
				Z:  a.oz * length / 2,
				OX: a.ox, OY: a.oy, OZ: a.oz,
			},
			LengthMM: length,
			RadiusMM: radius,
			Color:    &col,
		})
	}
	return out
}

// wirePoseToSpatial converts a wire-format pose back into a
// spatialmath.Pose. A missing or all-zero orientation vector is
// treated as the identity (+Z), matching the renderer's convention.
func wirePoseToSpatial(p *visualPoseWire) spatialmath.Pose {
	if p == nil {
		return spatialmath.NewZeroPose()
	}
	ox, oy, oz := p.OX, p.OY, p.OZ
	if ox == 0 && oy == 0 && oz == 0 {
		oz = 1
	}
	return spatialmath.NewPose(
		r3.Vector{X: p.X, Y: p.Y, Z: p.Z},
		&spatialmath.OrientationVectorDegrees{OX: ox, OY: oy, OZ: oz, Theta: p.Theta},
	)
}
