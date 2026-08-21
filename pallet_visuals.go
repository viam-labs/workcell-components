package workcellcomponents

import (
	"fmt"

	"github.com/golang/geo/r3"
	"go.viam.com/rdk/spatialmath"
)

// Pallet style — picked from PalletConfig.Style at construction time.
// Influences which sub-primitive set palletVisuals builds.
const (
	palletStyleStringer = "stringer" // default — 3 lengthwise stringers under a slatted top
	palletStyleBlock    = "block"    // 9 blocks (3×3) under a slatted top
	palletStylePlastic  = "plastic"  // single-piece slate-grey (Phase B simple fallback)
)

// Anatomy of a standard GMA stringer pallet, in mm. Top + bottom deck
// boards are ~18 mm thick (¾"); the stringer height fills the gap.
// Counts adapt to the pallet's dimensions so a 200 mm tray and a
// 1500 mm pallet both render with proportional slat density and no
// overlap. The "GMA" constants below are reference values that hold
// for a 1219.2 × 1016.0 × 152.4 mm pallet — see adaptiveSlatCount and
// adaptiveStringerWidth for the scaling rules.
const (
	gmaDeckBoardThicknessMM = 18.0 // ¾"
	gmaStringerWidthMM      = 90.0 // ~3.5" — typical 2×4 stringer width
	gmaStringerCount        = 3
	gmaBlockGridX           = 3 // 3×3 block layout for the block-pallet variant
	gmaBlockGridY           = 3
	gmaBlockEdgeMM          = 100.0 // 100 mm cube footprint for blocks (capped to width/length/4)

	// Slat / board sizing targets. Boards aim for ~175 mm wide on a
	// real pallet; trays adapt the count down to keep the look
	// believable. Floors / ceilings on count prevent pathological
	// extremes.
	palletDesiredSlatWidthMM    = 175.0
	palletMinSlatCount          = 2
	palletMaxSlatCount          = 11
	palletDesiredBottomBoardWMM = 280.0 // bottom deck has fewer, wider boards
	palletMinBottomBoardCount   = 2
	palletMaxBottomBoardCount   = 7
)

// Darker brown for stringers / blocks — contrasts with the wood-tan
// top deck so the pallet "reads" as layered rather than monolithic.
var palletStringerColor = Color{R: 130, G: 92, B: 55, A: 1}

// Slightly lighter than top deck for the bottom boards so a viewer
// looking up-from-below sees the layers distinctly.
var palletBottomBoardColor = Color{R: 178, G: 138, B: 87, A: 1}

// Plastic-style fallback color (slate grey).
var palletPlasticColor = Color{R: 80, G: 90, B: 100, A: 1}

// Default tray-exchange animation values; see PalletConfig.
const (
	defaultExchangeTravelMM     = 1200.0
	defaultExchangeLoadHeightMM = 200.0
)

// trayExchangeState is what the paired tray-dock sensor said, reduced
// to the visual's terms.
type trayExchangeState struct {
	present      bool    // a tray is docked
	fraction     float64 // 0..1 progress of the exchange
	travelMM     float64 // how far the tray travels off the dock
	loadHeightMM float64 // load silhouette on the outbound tray
}

// palletVisuals returns the typed visual primitives that make up one
// pallet at the given world pose, dims, color, and visual options.
//
// All sub-primitives are grouped under a single anchor Frame
// ("{name}/group") — see visuals_group.go. Sub-primitive labels stay
// namespaced under the component name (e.g. "pallet/slat-0") for
// stable, addressable identity.
//
// When opts.Visible is explicitly false, returns just the (invisible)
// anchor frame — the component keeps a presence in the WSS state
// while all geometry is hidden.
func palletVisuals(
	name string,
	pose spatialmath.Pose,
	width, length, thickness float64,
	topColor Color,
	style string,
	opts VisualOptions,
	exchange *trayExchangeState,
	hasDock bool,
	bedTravelMM float64,
) []visualWire {
	if opts.Visible != nil && !*opts.Visible {
		return groupUnderFrame(name, pose, false, nil)
	}

	// The outfeed conveyor is fixed: the pallet rides it, so it is
	// placed from the dock pose before any exchange offset.
	var bed []visualWire
	if hasDock {
		bed = outfeedBedVisuals(name, pose, width, length, thickness,
			bedTravelMM, opts)
	}

	// Tray exchange: while the dock reports no tray, the visual slides.
	// The line flows through the cell: first half of the exchange the
	// full pallet leaves along +X (out the right side) with a load
	// silhouette riding it; second half the empty replacement rides in
	// from -X (the left side). The dock pose itself (motion targets)
	// never moves.
	var exchangeLoad float64
	if exchange != nil && !exchange.present {
		f := clamp01(exchange.fraction)
		var offsetX float64
		if f < 0.5 {
			offsetX = exchange.travelMM * (f * 2)
			exchangeLoad = exchange.loadHeightMM
		} else {
			offsetX = -exchange.travelMM * (2 - f*2)
		}
		pose = spatialmath.Compose(pose,
			spatialmath.NewPoseFromPoint(r3.Vector{X: offsetX}))
	}

	style = normalizePalletStyle(style)

	var children []visualWire
	if style == palletStylePlastic {
		children = []visualWire{
			palletPlasticBox(name, pose, width, length, thickness, palletPlasticColor, opts),
		}
	} else {
		children = append(children, palletTopDeckSlats(name, pose, width, length, thickness, topColor, opts)...)
		if style == palletStyleBlock {
			children = append(children, palletBlocks(name, pose, width, length, thickness, palletStringerColor, opts)...)
		} else {
			children = append(children, palletStringers(name, pose, width, length, thickness, palletStringerColor, opts)...)
		}
		children = append(children, palletBottomDeckBoards(name, pose, width, length, thickness, palletBottomBoardColor, opts)...)
	}
	// Load silhouette on the outbound tray: one tan block the size of
	// the deck footprint, so a dispatched tray reads as full.
	if exchangeLoad > 0 {
		children = append(children, boxAt(
			fmt.Sprintf("%s/outbound-load", name),
			compose(pose, 0, 0, thickness/2+exchangeLoad/2, 0, 0, 1, 0),
			width*0.92, length*0.92, exchangeLoad,
			cardboardColor, opts,
		))
	}
	children = append(children, bed...)

	return groupUnderFrame(name, pose, opts.ShowAxes, children)
}

func normalizePalletStyle(s string) string {
	switch s {
	case palletStyleBlock, palletStylePlastic:
		return s
	}
	return palletStyleStringer
}

// palletTopDeckSlats — N boards spanning the length direction (Y),
// evenly distributed across the width direction (X). N adapts to the
// width: a 1219 mm pallet gets ~7 slats, a 200 mm tray gets ~2.
// Each slat is thinner than the full thickness so the gap layer
// beneath is visible. Top face of each slat aligns with the pallet's
// top face.
func palletTopDeckSlats(
	name string,
	pose spatialmath.Pose,
	width, length, thickness float64,
	color Color,
	opts VisualOptions,
) []visualWire {
	board := palletBoardThickness(thickness)
	count := adaptiveSlatCount(width, palletDesiredSlatWidthMM, palletMinSlatCount, palletMaxSlatCount)
	slatWidth := slatWidthForCount(width, count)
	gap := slatGapForCount(width, slatWidth, count)
	startX := -width/2 + slatWidth/2

	out := make([]visualWire, 0, count)
	for i := 0; i < count; i++ {
		localX := startX + float64(i)*(slatWidth+gap)
		localZ := thickness/2 - board/2
		out = append(out, boxAt(
			fmt.Sprintf("%s/slat-%d", name, i),
			compose(pose, localX, 0, localZ, 0, 0, 1, 0),
			slatWidth, length, board,
			color, opts,
		))
	}
	return out
}

// palletStringers — 3 long beams running the WIDTH (X axis), spaced
// across the LENGTH (Y). Sit in the middle Z layer (between top and
// bottom decks). Stringer width adapts to short pallets so the three
// stringers fit without overlap on trays as short as ~150 mm.
func palletStringers(
	name string,
	pose spatialmath.Pose,
	width, length, thickness float64,
	color Color,
	opts VisualOptions,
) []visualWire {
	board := palletBoardThickness(thickness)
	stringerHeight := thickness - 2*board
	if stringerHeight <= 0 {
		stringerHeight = thickness * 0.6 // pathologically-thin pallets fall back to 60%
	}
	stringerWidth := adaptiveStringerWidth(length, gmaStringerCount, gmaStringerWidthMM)
	startY := -length/2 + stringerWidth/2
	endY := length/2 - stringerWidth/2
	step := 0.0
	if gmaStringerCount > 1 {
		step = (endY - startY) / float64(gmaStringerCount-1)
	}
	out := make([]visualWire, 0, gmaStringerCount)
	for i := 0; i < gmaStringerCount; i++ {
		localY := startY + float64(i)*step
		out = append(out, boxAt(
			fmt.Sprintf("%s/stringer-%d", name, i),
			compose(pose, 0, localY, 0, 0, 0, 1, 0),
			width, stringerWidth, stringerHeight,
			color, opts,
		))
	}
	return out
}

// palletBlocks — 9 cube-ish blocks arranged in a 3×3 grid, occupying
// the middle Z layer. The block-pallet variant (vs stringer pallet).
func palletBlocks(
	name string,
	pose spatialmath.Pose,
	width, length, thickness float64,
	color Color,
	opts VisualOptions,
) []visualWire {
	board := palletBoardThickness(thickness)
	blockHeight := thickness - 2*board
	if blockHeight <= 0 {
		blockHeight = thickness * 0.6
	}
	blockX := minF(gmaBlockEdgeMM, width/4)
	blockY := minF(gmaBlockEdgeMM, length/4)

	startX := -width/2 + blockX/2
	endX := width/2 - blockX/2
	startY := -length/2 + blockY/2
	endY := length/2 - blockY/2
	stepX := 0.0
	stepY := 0.0
	if gmaBlockGridX > 1 {
		stepX = (endX - startX) / float64(gmaBlockGridX-1)
	}
	if gmaBlockGridY > 1 {
		stepY = (endY - startY) / float64(gmaBlockGridY-1)
	}

	out := make([]visualWire, 0, gmaBlockGridX*gmaBlockGridY)
	for ix := 0; ix < gmaBlockGridX; ix++ {
		for iy := 0; iy < gmaBlockGridY; iy++ {
			localX := startX + float64(ix)*stepX
			localY := startY + float64(iy)*stepY
			out = append(out, boxAt(
				fmt.Sprintf("%s/block-%d-%d", name, ix, iy),
				compose(pose, localX, localY, 0, 0, 0, 1, 0),
				blockX, blockY, blockHeight,
				color, opts,
			))
		}
	}
	return out
}

// palletBottomDeckBoards — N long boards spanning the length, spaced
// across the width. Bottom-face aligned with the pallet's bottom.
// Count adapts to width: fewer + wider than the top deck (real GMA
// pallets have 3 bottom boards under 7 top slats).
func palletBottomDeckBoards(
	name string,
	pose spatialmath.Pose,
	width, length, thickness float64,
	color Color,
	opts VisualOptions,
) []visualWire {
	board := palletBoardThickness(thickness)
	count := adaptiveSlatCount(width, palletDesiredBottomBoardWMM,
		palletMinBottomBoardCount, palletMaxBottomBoardCount)
	boardWidth := slatWidthForCount(width, count)
	gap := slatGapForCount(width, boardWidth, count)
	startX := -width/2 + boardWidth/2

	out := make([]visualWire, 0, count)
	for i := 0; i < count; i++ {
		localX := startX + float64(i)*(boardWidth+gap)
		localZ := -thickness/2 + board/2
		out = append(out, boxAt(
			fmt.Sprintf("%s/bottom-board-%d", name, i),
			compose(pose, localX, 0, localZ, 0, 0, 1, 0),
			boardWidth, length, board,
			color, opts,
		))
	}
	return out
}

// palletPlasticBox — the plastic-pallet style fallback: a single
// rounded-looking box at the full pallet footprint. Phase B places a
// straightforward Box; future enhancement could swap in a Mesh asset.
func palletPlasticBox(
	name string,
	pose spatialmath.Pose,
	width, length, thickness float64,
	color Color,
	opts VisualOptions,
) visualWire {
	return boxAt(
		fmt.Sprintf("%s/body", name),
		pose, width, length, thickness, color, opts,
	)
}

// palletBoardThickness scales the board-board thickness with the
// pallet's overall thickness so a small-scale pallet keeps the layered
// look. Cap at the GMA standard (18 mm) for typical pallets, scale
// down for thin demo pallets.
func palletBoardThickness(thickness float64) float64 {
	if thickness <= 0 {
		return 1
	}
	t := gmaDeckBoardThicknessMM
	if t > thickness/3 {
		t = thickness / 3
	}
	return t
}

// adaptiveSlatCount picks how many slats / boards span a dimension
// so each slat ends up near `desiredWidthMM`. Clamped to [minCount,
// maxCount] so trays still get ≥2 slats and giant pallets don't end
// up with 20+ thin boards.
func adaptiveSlatCount(totalWidth, desiredWidthMM float64, minCount, maxCount int) int {
	if totalWidth <= 0 || desiredWidthMM <= 0 {
		return minCount
	}
	n := int(totalWidth/desiredWidthMM + 0.5) // round
	if n < minCount {
		return minCount
	}
	if n > maxCount {
		return maxCount
	}
	return n
}

// adaptiveStringerWidth shrinks the stringer's Y dimension when the
// pallet is too short for the GMA-default 90 mm width to fit `count`
// non-overlapping stringers. `count` non-overlapping stringers occupy
// at most `length / count` each; we cap to `length / (count*1.15)` so
// there's a small visible gap between adjacent stringers.
func adaptiveStringerWidth(length float64, count int, gmaDefault float64) float64 {
	if count <= 0 || length <= 0 {
		return gmaDefault
	}
	maxFit := length / (float64(count) * 1.15)
	if maxFit < gmaDefault {
		return maxFit
	}
	return gmaDefault
}

// slatWidthForCount returns the per-slat width that leaves a small
// gap between adjacent slats — roughly 10% of the slat width.
func slatWidthForCount(totalWidth float64, count int) float64 {
	if count <= 1 || totalWidth <= 0 {
		return totalWidth
	}
	// 7 slats with 6 gaps where each gap is ~10% of one slat:
	// totalWidth = count*slat + (count-1)*0.1*slat
	denom := float64(count) + 0.1*float64(count-1)
	return totalWidth / denom
}

// slatGapForCount mirrors slatWidthForCount — returns the gap that
// satisfies the same total-width constraint.
func slatGapForCount(totalWidth, slatWidth float64, count int) float64 {
	if count <= 1 {
		return 0
	}
	return (totalWidth - float64(count)*slatWidth) / float64(count-1)
}

// boxAt builds a wire-format Box entry at the given absolute world
// pose. opts.Opacity multiplies the color's intrinsic opacity to give
// operators a single "fade" knob.
// outfeedBedVisuals draws the pallet line: a fixed roller bed running
// straight through the cell along X, so an empty pallet visibly rides
// in from the left and the packed one rides out the right. The
// rollers' top sits at the pallet's bottom face; rails and legs carry
// it to the floor.
func outfeedBedVisuals(
	name string,
	dockPose spatialmath.Pose,
	width, length, thickness, travelMM float64,
	opts VisualOptions,
) []visualWire {
	if travelMM <= 0 {
		travelMM = defaultExchangeTravelMM
	}
	// The run spans the inbound side, the dock, and the outbound side.
	bedRun := 2*travelMM + width
	bedWidth := length + 60
	palletBottom := -thickness / 2
	railH := 60.0
	rollerRadius := 16.0
	deckZ := palletBottom - rollerRadius - 10
	center := dockPose

	var out []visualWire
	// Two side rails along the run.
	for i, sy := range []float64{-1, 1} {
		out = append(out, boxAt(
			fmt.Sprintf("%s/outfeed-rail-%d", name, i),
			compose(center, 0, sy*(bedWidth/2-15),
				palletBottom-railH/2+8, 0, 0, 1, 0),
			bedRun, 30, railH, pickStationRailColor, opts,
		))
	}
	// Rollers across the run, long axis along Y.
	count := adaptiveRollerCount(bedRun - 60)
	step := 0.0
	if count > 1 {
		step = (bedRun - 60) / float64(count-1)
	}
	for i := 0; i < count; i++ {
		localX := -bedRun/2 + 30 + float64(i)*step
		out = append(out, capsuleAt(
			fmt.Sprintf("%s/outfeed-roller-%02d", name, i),
			compose(center, localX, 0, palletBottom-rollerRadius, 0, 1, 0, 0),
			rollerRadius, bedWidth-70,
			pickStationRollerColor, opts,
		))
	}
	// Leg pairs at the ends and under the dock, floor to deck.
	legTop := deckZ
	legH := dockPose.Point().Z + legTop
	if legH > 20 {
		i := 0
		for _, lx := range []float64{-bedRun/2 + 40, 0, bedRun/2 - 40} {
			for _, sy := range []float64{-1, 1} {
				out = append(out, boxAt(
					fmt.Sprintf("%s/outfeed-leg-%d", name, i),
					compose(center, lx, sy*(bedWidth/2-30),
						legTop-legH/2, 0, 0, 1, 0),
					36, 36, legH, pickStationLegColor, opts,
				))
				i++
			}
		}
	}
	return out
}

func boxAt(
	label string,
	pose spatialmath.Pose,
	dimX, dimY, dimZ float64,
	color Color,
	opts VisualOptions,
) visualWire {
	return visualWire{
		Type:        "box",
		Label:       label,
		ParentFrame: defaultParentFrame,
		Pose:        poseToWire(pose),
		DimsMM:      &visualDimsWire{X: dimX, Y: dimY, Z: dimZ},
		Color:       colorToWire(color, opts),
	}
}

// capsuleAt — wire-format Capsule entry. Used by pick-station rollers
// and any tube-like affordances.
func capsuleAt(
	label string,
	pose spatialmath.Pose,
	radiusMM, lengthMM float64,
	color Color,
	opts VisualOptions,
) visualWire {
	return visualWire{
		Type:        "capsule",
		Label:       label,
		ParentFrame: defaultParentFrame,
		Pose:        poseToWire(pose),
		RadiusMM:    radiusMM,
		LengthMM:    lengthMM,
		Color:       colorToWire(color, opts),
	}
}

// arrowAt — wire-format Arrow entry.
func arrowAt(
	label string,
	pose spatialmath.Pose,
	lengthMM, radiusMM float64,
	color Color,
	opts VisualOptions,
) visualWire {
	return visualWire{
		Type:        "arrow",
		Label:       label,
		ParentFrame: defaultParentFrame,
		Pose:        poseToWire(pose),
		LengthMM:    lengthMM,
		RadiusMM:    radiusMM,
		Color:       colorToWire(color, opts),
	}
}

// sphereAt — wire-format Sphere entry.
func sphereAt(
	label string,
	pose spatialmath.Pose,
	radiusMM float64,
	color Color,
	opts VisualOptions,
) visualWire {
	return visualWire{
		Type:        "sphere",
		Label:       label,
		ParentFrame: defaultParentFrame,
		Pose:        poseToWire(pose),
		RadiusMM:    radiusMM,
		Color:       colorToWire(color, opts),
	}
}

// compose translates a local-frame offset (in pallet-/component-local
// mm coordinates) into a world-frame pose, given the parent's world
// pose. Uses spatialmath.Compose so any frame-block orientation (e.g.
// pick-station incline) propagates correctly to child visuals.
func compose(parent spatialmath.Pose, x, y, z, ox, oy, oz, theta float64) spatialmath.Pose {
	local := spatialmath.NewPose(
		r3.Vector{X: x, Y: y, Z: z},
		&spatialmath.OrientationVectorDegrees{OX: ox, OY: oy, OZ: oz, Theta: theta},
	)
	return spatialmath.Compose(parent, local)
}

// poseToWire converts a spatialmath.Pose to the wire-format pose
// shape (snake_case keys matching get_pose / get_visual_pose).
func poseToWire(pose spatialmath.Pose) *visualPoseWire {
	pt := pose.Point()
	ov := pose.Orientation().OrientationVectorDegrees()
	return &visualPoseWire{
		X: pt.X, Y: pt.Y, Z: pt.Z,
		OX: ov.OX, OY: ov.OY, OZ: ov.OZ, Theta: ov.Theta,
	}
}

// colorToWire combines a Color (intrinsic) with VisualOptions.Opacity
// (per-component fade multiplier) into the wire-format color shape.
// The library reads opacity from the top-level entry rather than the
// color block, so we emit both for safety.
func colorToWire(c Color, opts VisualOptions) *visualColorWire {
	op := c.effectiveOpacity()
	if opts.Opacity != nil {
		op *= *opts.Opacity
	}
	if op < 0 {
		op = 0
	}
	if op > 1 {
		op = 1
	}
	return &visualColorWire{R: c.R, G: c.G, B: c.B, Opacity: op}
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// visualsToMaps converts a slice of visualWire entries to the
// []map[string]interface{} shape the get_visuals DoCommand response
// uses. Round-trips through encoding/json to honor the snake_case
// JSON tags.
func visualsToMaps(vs []visualWire) ([]map[string]interface{}, error) {
	out := make([]map[string]interface{}, 0, len(vs))
	for _, v := range vs {
		m, err := visualWireToMap(v)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}
