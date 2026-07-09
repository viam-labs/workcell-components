package workcellcomponents

import (
	"context"
	"fmt"

	"go.viam.com/rdk/components/generic"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
)

// ScanTunnelModel — fixed-mount barcode/SKU scan tunnel over a
// conveyor, modeled on commercial units (Cognex Modular Vision
// Tunnel, Zebra scan tunnels): an aluminum gantry frame spanning the
// belt, downward-aimed reader heads under the crossbar, an
// illumination bar, optional side readers on the posts (3-sided
// configuration), and a pulsing scan line on the belt surface.
//
// Visual composition:
//   - Two Box posts + one Box crossbar (anodized-grey gantry frame)
//   - A slim white illumination bar under the crossbar
//   - ReaderCount near-black Box reader heads hanging from the
//     crossbar, each with a Sphere lens on its underside
//   - When Sides == 3: one reader head on each post's inner face,
//     aimed across the belt
//   - When ScanLineHeightMM > 0: a red scan line Box at that height,
//     pulsing softly
//
// Frame origin: FLOOR midway between the two posts. Local +X is the
// span direction (perpendicular to conveyor flow); +Z up. Set
// frame.translation.z = 0 to plant the tunnel on the floor.
var ScanTunnelModel = resource.NewModel("viam", "workcell-components", "scan-tunnel")

const (
	defaultTunnelSpanMM      = 700.0 // clear inner span between posts
	defaultTunnelHeightMM    = 700.0 // clear height under the crossbar
	defaultTunnelFrameMM     = 60.0  // extrusion cross-section
	defaultTunnelReaderCount = 3
	defaultTunnelReaderWMM   = 90.0 // reader head width (across span)
	defaultTunnelReaderDMM   = 70.0 // reader head depth (along flow)
	defaultTunnelReaderHMM   = 60.0 // reader head height
	defaultTunnelLensRMM     = 16.0
	defaultTunnelLightBarMM  = 18.0 // illumination bar square section
	defaultTunnelScanLineMM  = 24.0 // scan line depth (along flow)
)

var (
	defaultTunnelFrameColor  = Color{R: 70, G: 72, B: 78, A: 1}    // anodized grey
	defaultTunnelReaderColor = Color{R: 25, G: 25, B: 28, A: 1}    // near-black head
	defaultTunnelLensColor   = Color{R: 45, G: 70, B: 110, A: 1}   // coated optics
	defaultTunnelLightColor  = Color{R: 235, G: 238, B: 245, A: 1} // bar light
	defaultTunnelLineColor   = Color{R: 235, G: 45, B: 40, A: 0.9} // scan line
)

// ScanTunnelConfig captures the tunnel's geometry and reader layout.
// All fields optional; the zero config renders a 3-sided tunnel sized
// for a ~450 mm belt.
type ScanTunnelConfig struct {
	Label string `json:"label,omitempty"`

	// SpanMM is the clear inner span between the posts.
	SpanMM float64 `json:"span_mm,omitempty"`
	// HeightMM is the clear height under the crossbar.
	HeightMM float64 `json:"height_mm,omitempty"`

	// Sides: 1 (top readers only) or 3 (top + one reader per post,
	// as in a 3-sided commercial tunnel). Default 3.
	Sides int `json:"sides,omitempty"`

	// ReaderCount is the number of heads under the crossbar. Default 3.
	ReaderCount int `json:"reader_count,omitempty"`

	// ScanLineHeightMM draws a pulsing red scan line across the span
	// at this height (set it to the belt's top surface). 0 disables.
	ScanLineHeightMM float64 `json:"scan_line_height_mm,omitempty"`

	// Color override for the gantry frame. Reader/lens/line colors
	// are intrinsic.
	Color *Color `json:"color,omitempty"`

	VisualOptions
}

func (c *ScanTunnelConfig) Validate(_ string) ([]string, []string, error) {
	switch c.Sides {
	case 0, 1, 3:
	default:
		return nil, nil, fmt.Errorf("scan-tunnel: sides must be 1 or 3, got %d", c.Sides)
	}
	if c.ReaderCount < 0 || c.ReaderCount > 12 {
		return nil, nil, fmt.Errorf("scan-tunnel: reader_count must be 0..12, got %d", c.ReaderCount)
	}
	if c.Color != nil {
		if err := validateColor(*c.Color); err != nil {
			return nil, nil, err
		}
	}
	return nil, nil, nil
}

func init() {
	resource.RegisterComponent(generic.API, ScanTunnelModel,
		resource.Registration[resource.Resource, *ScanTunnelConfig]{
			Constructor: newScanTunnel,
		},
	)
}

type scanTunnel struct {
	*decorationBase
	span        float64
	height      float64
	sides       int
	readerCount int
	scanLineH   float64
}

func newScanTunnel(
	_ context.Context,
	_ resource.Dependencies,
	conf resource.Config,
	logger logging.Logger,
) (resource.Resource, error) {
	cfg, err := resource.NativeConfig[*ScanTunnelConfig](conf)
	if err != nil {
		return nil, err
	}
	base := newDecorationBase(conf.ResourceName(), logger, ScanTunnelModel,
		conf.Frame, cfg.Label, defaultTunnelFrameColor, cfg.Color, cfg.VisualOptions)
	sides := cfg.Sides
	if sides == 0 {
		sides = 3
	}
	readers := cfg.ReaderCount
	if readers == 0 {
		readers = defaultTunnelReaderCount
	}
	return &scanTunnel{
		decorationBase: base,
		span:           defaultLen(cfg.SpanMM, defaultTunnelSpanMM),
		height:         defaultLen(cfg.HeightMM, defaultTunnelHeightMM),
		sides:          sides,
		readerCount:    readers,
		scanLineH:      cfg.ScanLineHeightMM,
	}, nil
}

func (t *scanTunnel) DoCommand(_ context.Context, cmd map[string]interface{}) (map[string]interface{}, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, ok := cmd["get_pose"]; ok {
		return poseToWorldMap(t.pose), nil
	}
	if _, ok := cmd["get_visual_pose"]; ok {
		return poseToWorldMap(t.pose), nil
	}
	if _, ok := cmd["get_attributes"]; ok {
		return t.attributesMap(), nil
	}
	if _, ok := cmd["get_status"]; ok {
		return t.commonStatusMap(), nil
	}
	if _, ok := cmd["get_visuals"]; ok {
		return asWireResponse(t.buildVisuals())
	}
	if _, ok := cmd["get_schema"]; ok {
		return schemaToResponse(scanTunnelSchema())
	}
	if v, ok := cmd["set_attributes"]; ok {
		m, ok := v.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("set_attributes: expected object")
		}
		if s := asFloat(m["span_mm"]); s > 0 {
			t.span = s
		}
		if h := asFloat(m["height_mm"]); h > 0 {
			t.height = h
		}
		if s := int(asFloat(m["sides"])); s == 1 || s == 3 {
			t.sides = s
		}
		if rc := int(asFloat(m["reader_count"])); rc > 0 && rc <= 12 {
			t.readerCount = rc
		}
		if _, present := m["scan_line_height_mm"]; present {
			t.scanLineH = asFloat(m["scan_line_height_mm"])
		}
		if err := t.applyStandardSet(m); err != nil {
			return nil, fmt.Errorf("set_attributes: %w", err)
		}
		return withPersistHint(t.attributesMap(), nil)
	}
	return nil, fmt.Errorf("scan-tunnel: unknown command %v", cmd)
}

// Caller must hold t.mu.
func (t *scanTunnel) attributesMap() map[string]interface{} {
	out := t.commonAttributesMap()
	out["span_mm"] = t.span
	out["height_mm"] = t.height
	out["sides"] = t.sides
	out["reader_count"] = t.readerCount
	out["scan_line_height_mm"] = t.scanLineH
	return out
}

func scanTunnelSchema() []schemaEntry {
	return []schemaEntry{
		numEntry("span_mm", "Clear span", schemaGroupGeometry, "mm", 200, 3000, 10),
		numEntry("height_mm", "Clear height", schemaGroupGeometry, "mm", 200, 3000, 10),
		numEntry("sides", "Sides (1 = top only, 3 = + side readers)", schemaGroupGeometry, "", 1, 3, 2),
		numEntry("reader_count", "Top reader heads", schemaGroupGeometry, "", 1, 12, 1),
		numEntry("scan_line_height_mm", "Scan line height (0 = off)", schemaGroupBehavior, "mm", 0, 3000, 5),
		colorEntry("color", "Frame color", schemaGroupVisual),
		boolEntry("visible", "Visible", schemaGroupVisual),
		showAxesEntry(),
		numEntry("opacity", "Opacity multiplier", schemaGroupVisual, "", 0, 1, 0.05),
	}
}

// Caller must hold t.mu.
func (t *scanTunnel) buildVisuals() []visualWire {
	if t.opts.Visible != nil && !*t.opts.Visible {
		return groupUnderFrame(t.name.Name, t.pose, false, nil)
	}
	f := defaultTunnelFrameMM
	out := []visualWire{}

	// Gantry: two posts + crossbar.
	postX := t.span/2 + f/2
	for i, sx := range []float64{-1, 1} {
		out = append(out, boxAt(
			fmt.Sprintf("%s/post-%d", t.name.Name, i),
			compose(t.pose, sx*postX, 0, t.height/2, 0, 0, 1, 0),
			f, f, t.height,
			t.color, t.opts,
		))
	}
	out = append(out, boxAt(
		fmt.Sprintf("%s/crossbar", t.name.Name),
		compose(t.pose, 0, 0, t.height+f/2, 0, 0, 1, 0),
		t.span+2*f, f, f,
		t.color, t.opts,
	))

	// Illumination bar under the crossbar.
	out = append(out, boxAt(
		fmt.Sprintf("%s/light-bar", t.name.Name),
		compose(t.pose, 0, f/2+defaultTunnelLightBarMM/2, t.height-defaultTunnelLightBarMM/2, 0, 0, 1, 0),
		t.span*0.9, defaultTunnelLightBarMM, defaultTunnelLightBarMM,
		defaultTunnelLightColor, t.opts,
	))

	// Top reader heads, evenly spaced across ~70% of the span, each
	// with a lens on its underside.
	usable := t.span * 0.7
	step := 0.0
	if t.readerCount > 1 {
		step = usable / float64(t.readerCount-1)
	}
	startX := -usable / 2
	if t.readerCount == 1 {
		startX = 0
	}
	headZ := t.height - defaultTunnelReaderHMM/2
	for i := 0; i < t.readerCount; i++ {
		x := startX + float64(i)*step
		out = append(out, boxAt(
			fmt.Sprintf("%s/reader-top-%d", t.name.Name, i),
			compose(t.pose, x, 0, headZ, 0, 0, 1, 0),
			defaultTunnelReaderWMM, defaultTunnelReaderDMM, defaultTunnelReaderHMM,
			defaultTunnelReaderColor, t.opts,
		))
		out = append(out, sphereAt(
			fmt.Sprintf("%s/lens-top-%d", t.name.Name, i),
			compose(t.pose, x, 0, headZ-defaultTunnelReaderHMM/2, 0, 0, 1, 0),
			defaultTunnelLensRMM,
			defaultTunnelLensColor, t.opts,
		))
	}

	// Side readers (3-sided tunnel): one per post inner face, at the
	// scan-line height when set, else at 45% of the clear height.
	if t.sides == 3 {
		sideZ := t.height * 0.45
		if t.scanLineH > 0 {
			sideZ = t.scanLineH + 150
		}
		for i, sx := range []float64{-1, 1} {
			inner := sx * (t.span/2 - defaultTunnelReaderHMM/2)
			out = append(out, boxAt(
				fmt.Sprintf("%s/reader-side-%d", t.name.Name, i),
				compose(t.pose, inner, 0, sideZ, 0, 0, 1, 0),
				defaultTunnelReaderHMM, defaultTunnelReaderDMM, defaultTunnelReaderWMM,
				defaultTunnelReaderColor, t.opts,
			))
			out = append(out, sphereAt(
				fmt.Sprintf("%s/lens-side-%d", t.name.Name, i),
				compose(t.pose, inner-sx*defaultTunnelReaderHMM/2, 0, sideZ, 0, 0, 1, 0),
				defaultTunnelLensRMM,
				defaultTunnelLensColor, t.opts,
			))
		}
	}

	// Pulsing scan line at the belt surface.
	if t.scanLineH > 0 {
		entry := boxAt(
			fmt.Sprintf("%s/scan-line", t.name.Name),
			compose(t.pose, 0, 0, t.scanLineH+2, 0, 0, 1, 0),
			t.span-20, defaultTunnelScanLineMM, 4,
			defaultTunnelLineColor, t.opts,
		)
		entry.Animation = map[string]interface{}{
			"mode":       "flicker",
			"period_s":   1.2,
			"duty_cycle": 0.7,
		}
		out = append(out, entry)
	}

	return groupUnderFrame(t.name.Name, t.pose, t.opts.ShowAxes, out)
}
