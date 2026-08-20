package workcellcomponents

import (
	"fmt"
	"math"

	"go.viam.com/rdk/spatialmath"
)

// Roller-bed conveyor visual constants. Counts are sensible defaults
// that scale linearly with the pick-station's length so a small demo
// station still gets recognizable roller density. Side rails + leg
// sizes also adapt so a 100 mm-wide demo conveyor doesn't end up with
// 30 mm rails consuming most of its width.
const (
	pickStationDesiredRollerSpacingMM = 60.0 // target center-to-center
	pickStationMinRollers             = 3
	pickStationMaxRollers             = 24
	pickStationSideRailWidthMM        = 30.0
	pickStationLegInsetMM             = 40.0 // legs inset from corners
	pickStationLegSquareMM            = 50.0
	pickStationDirectionArrowMul      = 0.65 // arrow length as fraction of length_mm
)

// Color palette for the pick-station composite. Defaults align with
// the typical industrial look: dark blue painted side rails, machined-
// aluminum rollers, gloss-black legs.
var (
	pickStationRailColor   = Color{R: 50, G: 80, B: 120, A: 1}   // dark blue
	pickStationRollerColor = Color{R: 180, G: 184, B: 190, A: 1} // brushed aluminum
	pickStationLegColor    = Color{R: 28, G: 28, B: 32, A: 1}    // gloss black
	pickStationArrowColor  = Color{R: 220, G: 220, B: 60, A: 1}  // safety yellow
	pickStationTargetColor = Color{R: 60, G: 220, B: 120, A: 0.35}
	// Matches the pack-sequencer's placed-box color exactly, so the
	// box the arm lifts looks like the box that arrived.
	pickStationInfeedBoxColor = cardboardColor
)

// Default infeed-box dims: a hair under the course's 200x150x100 box
// so the picked box hides this one while the two coincide.
const (
	defaultInfeedBoxWidthMM  = 196.0
	defaultInfeedBoxLengthMM = 146.0
	defaultInfeedBoxHeightMM = 98.0
)

// infeedBoxState is what the paired box-detect sensor said, reduced to
// the visual's terms.
type infeedBoxState struct {
	present  bool    // a box is waiting at the pickup point
	fraction float64 // 0..1 progress of the next box down the bed
	dims     Vec3D
	color    Color
}

// pickStationVisuals returns the typed visual primitives that compose
// the pick-station: a roller bed, two side rails, four legs reaching
// down to the floor, a conveyor-direction arrow on the top surface,
// and a translucent "next box target" indicator at the grasp pose.
//
// Inputs are taken from live component state (pose is the centroid,
// not the corner — caller computes from p.pose via the existing
// centroid-offset). All sub-poses are world-frame, derived via
// spatialmath.Compose so any incline (pitch/roll in frame.orientation)
// propagates correctly.
func pickStationVisuals(
	name string,
	centerPose spatialmath.Pose,
	width, length, thickness float64,
	color Color,
	opts VisualOptions,
	lowestPointHeightMM float64,
	conveyorDir Vec3D,
	boxOriginOffset *Vec3D,
	boxThetaDeg float64,
	rollerSpinPeriodS float64,
	infeed *infeedBoxState,
) []visualWire {
	if opts.Visible != nil && !*opts.Visible {
		return groupUnderFrame(name, centerPose, false, nil)
	}

	out := []visualWire{}
	// Side-rail width adapts to small stations so a 100 mm-wide demo
	// conveyor doesn't lose most of its top surface to rails.
	sideRailWidth := minF(pickStationSideRailWidthMM, width/8)
	if sideRailWidth < 4 {
		sideRailWidth = 4
	}

	// Deck base — a thinner slab under the rollers. This is the surface
	// the `color` attribute paints (the rollers/rails/legs keep their
	// own industrial colors).
	deckThickness := minF(thickness*0.4, 25.0)
	deckZ := -thickness/2 + deckThickness/2
	out = append(out, boxAt(
		fmt.Sprintf("%s/deck", name),
		compose(centerPose, 0, 0, deckZ, 0, 0, 1, 0),
		width-2*sideRailWidth, length, deckThickness,
		color, opts,
	))

	// Roller bed: parallel capsules running along the width (X), each
	// laid horizontally with its long axis on station-local X. Rollers
	// sit just above the deck so the top of the rollers ≈ pose's Z.
	// Roller count adapts to length so a 200 mm-long station gets
	// ~3 well-spaced rollers instead of 12 overlapping ones.
	rollerRadius := minF(thickness*0.25, 18.0)
	if rollerRadius < 2 {
		rollerRadius = 2
	}
	rollerLength := width - 2*sideRailWidth - 8 // small clearance from rails
	if rollerLength < 10 {
		rollerLength = 10
	}
	startY := -length/2 + sideRailWidth
	endY := length/2 - sideRailWidth
	usableLen := endY - startY
	if usableLen <= 0 {
		usableLen = length // tiny stations: skip the rail margin
		startY = -length / 2
	}
	count := adaptiveRollerCount(usableLen)
	rollerStep := 0.0
	if count > 1 {
		rollerStep = usableLen / float64(count-1)
	}
	rollerZ := thickness/2 - rollerRadius
	var rollerAnim map[string]interface{}
	if rollerSpinPeriodS > 0 {
		rollerAnim = map[string]interface{}{
			"mode":     "spin",
			"period_s": rollerSpinPeriodS,
		}
	}
	for i := 0; i < count; i++ {
		localY := startY + float64(i)*rollerStep
		entry := capsuleAt(
			fmt.Sprintf("%s/roller-%02d", name, i),
			// OX=1 lays the capsule's long axis along station-local X.
			// Spin animation rotates around the entity's local Z, which
			// (because of OX=1) aligns with world +X — i.e. the roller's
			// long axis. That's the correct roller-spin direction.
			compose(centerPose, 0, localY, rollerZ, 1, 0, 0, 0),
			rollerRadius, rollerLength,
			pickStationRollerColor, opts,
		)
		if rollerAnim != nil {
			entry.Animation = rollerAnim
		}
		out = append(out, entry)
	}

	// Side rails — two boxes flanking the rollers on the long edges.
	railHeight := minF(thickness*1.2, 80.0)
	railZ := thickness/2 - railHeight/2 + 5 // slightly above the top to look like a containment lip
	railLocalX := width/2 - sideRailWidth/2
	for i, sign := range []float64{-1, 1} {
		out = append(out, boxAt(
			fmt.Sprintf("%s/rail-%d", name, i),
			compose(centerPose, sign*railLocalX, 0, railZ, 0, 0, 1, 0),
			sideRailWidth, length, railHeight,
			pickStationRailColor, opts,
		))
	}

	// Legs — 4 box posts at the corners (inset), reaching from the
	// deck-bottom down to the world floor (z=0). Length depends on the
	// LowestPointHeightMM config value; we fall back to the centroid's
	// Z if the operator didn't measure it.
	floorZ := computeFloorZFromCenter(centerPose, lowestPointHeightMM, thickness)
	if floorZ > 0 {
		legHeight := floorZ
		legZ := -thickness/2 - legHeight/2 // legs hang below the bottom of the deck
		legX := width/2 - pickStationLegInsetMM
		legY := length/2 - pickStationLegInsetMM
		legIdx := 0
		for _, sx := range []float64{-1, 1} {
			for _, sy := range []float64{-1, 1} {
				out = append(out, boxAt(
					fmt.Sprintf("%s/leg-%d", name, legIdx),
					compose(centerPose, sx*legX, sy*legY, legZ, 0, 0, 1, 0),
					pickStationLegSquareMM, pickStationLegSquareMM, legHeight,
					pickStationLegColor, opts,
				))
				legIdx++
			}
		}
	}

	// Conveyor direction arrow — sits just above the rollers, points
	// along the conveyor direction. The library's Arrow primitive's
	// local +Z is the arrow direction, so set OX/OY/OZ from the
	// (unit-normalized) ConveyorDirection vector.
	arrowLen := length * pickStationDirectionArrowMul
	arrowRadius := minF(thickness*0.15, 10.0)
	if arrowLen > 0 && arrowRadius > 0 {
		nx, ny, nz := normalizeDir(conveyorDir)
		// Arrow sits above the rollers' top surface by ~arrowRadius+2,
		// anchored at the conveyor's centroid surface. The library's
		// Arrow primitive centers its pose on the mid-shaft and renders
		// the shaft + tip along its local +Z, which the OX/OY/OZ
		// orientation vector aligns to the conveyor direction.
		arrowZ := thickness/2 + arrowRadius + 2
		out = append(out, arrowAt(
			fmt.Sprintf("%s/direction-arrow", name),
			compose(centerPose, 0, 0, arrowZ, nx, ny, nz, 0),
			arrowLen, arrowRadius,
			pickStationArrowColor, opts,
		))
	}

	// Next-box target — a translucent box at the grasp pose, ~120 mm
	// cube placed at the BoxOriginOffsetMM in station-local frame.
	// Helps operators see where the palletizer will reach next.
	if boxOriginOffset != nil && (boxOriginOffset.X != 0 || boxOriginOffset.Y != 0 || boxOriginOffset.Z != 0) {
		// Convert offset from station-local (corner-anchored) to
		// centroid-anchored: subtract (w/2, l/2, -t/2).
		localCenterX := boxOriginOffset.X - width/2
		localCenterY := boxOriginOffset.Y - length/2
		localCenterZ := boxOriginOffset.Z + thickness/2 + 60 // 60 mm above the surface
		out = append(out, boxAt(
			fmt.Sprintf("%s/next-box-target", name),
			compose(centerPose, localCenterX, localCenterY, localCenterZ, 0, 0, 1, boxThetaDeg),
			120, 120, 120,
			pickStationTargetColor, opts,
		))
	}

	// Infeed box — driven by the paired box-detect sensor. While the
	// sensor counts down, the box travels the bed toward the pickup
	// point; while box_present, it waits there.
	if infeed != nil {
		// A nil offset means the pickup sits at the corner origin,
		// same convention as pickupPose; the infeed box still renders.
		off := boxOriginOffset
		if off == nil {
			off = &Vec3D{}
		}
		px := off.X - width/2
		py := off.Y - length/2
		bz := off.Z + thickness/2 + infeed.dims.Z/2
		nx, ny, _ := normalizeDir(conveyorDir)
		// Entry point: walk backward from the pickup along the
		// conveyor direction to the bed's upstream edge (dominant
		// axis decides which edge).
		ex, ey := px, py
		if math.Abs(ny) >= math.Abs(nx) && ny != 0 {
			edge := -length / 2
			if ny < 0 {
				edge = length / 2
			}
			t := (py - edge) / ny
			ex, ey = px-nx*t, py-ny*t
		} else if nx != 0 {
			edge := -width / 2
			if nx < 0 {
				edge = width / 2
			}
			t := (px - edge) / nx
			ex, ey = px-nx*t, py-ny*t
		}
		bx, by := px, py
		if !infeed.present {
			f := clamp01(infeed.fraction)
			bx = ex + (px-ex)*f
			by = ey + (py-ey)*f
		}
		out = append(out, boxAt(
			fmt.Sprintf("%s/infeed-box", name),
			compose(centerPose, bx, by, bz, 0, 0, 1, boxThetaDeg),
			infeed.dims.X, infeed.dims.Y, infeed.dims.Z,
			infeed.color, opts,
		))
	}

	return groupUnderFrame(name, centerPose, opts.ShowAxes, out)
}

// computeFloorZFromCenter returns the distance from the bottom of
// the deck to the world floor (z=0). Uses the user-provided
// LowestPointHeightMM when available (most accurate), otherwise
// estimates from the centroid's world-Z minus half-thickness.
func computeFloorZFromCenter(centerPose spatialmath.Pose, lowestPointHeightMM, thickness float64) float64 {
	if lowestPointHeightMM > 0 {
		return lowestPointHeightMM
	}
	z := centerPose.Point().Z
	bottomZ := z - thickness/2
	if bottomZ <= 0 {
		return 0
	}
	return bottomZ
}

// adaptiveRollerCount picks how many rollers span a usable length so
// adjacent roller centers end up near pickStationDesiredRollerSpacingMM.
// Clamped to [pickStationMinRollers, pickStationMaxRollers] so tiny
// stations still get a recognizable bed and oversized ones don't
// produce a forest.
func adaptiveRollerCount(usableLength float64) int {
	if usableLength <= 0 {
		return pickStationMinRollers
	}
	n := int(usableLength/pickStationDesiredRollerSpacingMM + 0.5)
	if n < pickStationMinRollers {
		return pickStationMinRollers
	}
	if n > pickStationMaxRollers {
		return pickStationMaxRollers
	}
	return n
}

// normalizeDir returns a unit vector (or {0,1,0} if input is zero).
func normalizeDir(v Vec3D) (float64, float64, float64) {
	mag := v.X*v.X + v.Y*v.Y + v.Z*v.Z
	if mag <= 1e-9 {
		return 0, 1, 0
	}
	m := math.Sqrt(mag)
	return v.X / m, v.Y / m, v.Z / m
}
