package workcellcomponents

import (
	"strings"
	"testing"

	"github.com/golang/geo/r3"
	visuals "github.com/viam-labs/viam-viz-helpers-go"
	"go.viam.com/rdk/spatialmath"
)

// rollerCount counts the pick-station roller capsules in a visuals set.
func rollerCount(entries []visualWire) int {
	n := 0
	for _, e := range entries {
		if strings.Contains(e.Label, "/roller-") {
			n++
		}
	}
	return n
}

func stationWithRollers(render bool) []visualWire {
	pose := spatialmath.NewPoseFromPoint(r3.Vector{X: 400, Y: -650, Z: 200})
	return pickStationVisuals(
		"pick-station", pose, 400, 1100, 40,
		defaultPickStationColor, VisualOptions{}, 200,
		Vec3D{X: 0, Y: 1, Z: 0}, nil, 0, 2.0, render, nil,
	)
}

// render_rollers=false must remove the roller objects entirely -- not merely
// stop animating them. Object count is what drives ListUUIDs/GetTransform.
func TestRenderRollersRemovesObjects(t *testing.T) {
	with := stationWithRollers(true)
	without := stationWithRollers(false)

	if rollerCount(with) == 0 {
		t.Fatal("render_rollers=true produced no rollers")
	}
	if got := rollerCount(without); got != 0 {
		t.Fatalf("render_rollers=false still emitted %d rollers", got)
	}
	// The station must still be recognisable: deck and rails survive.
	for _, want := range []string{"/deck", "/rail-0"} {
		found := false
		for _, e := range without {
			if strings.HasSuffix(e.Label, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("render_rollers=false dropped %q too", want)
		}
	}
	if len(without) >= len(with) {
		t.Errorf("expected fewer visuals without rollers: %d vs %d",
			len(without), len(with))
	}
}

// The rollers are the animated ones -- with render_rollers=true and a spin
// period set, at least one entry must carry an animation spec.
func TestRollersCarryAnimationWhenRendered(t *testing.T) {
	animated := 0
	for _, e := range stationWithRollers(true) {
		if e.Animation != nil {
			animated++
		}
	}
	if animated == 0 {
		t.Fatal("expected animated rollers when roller_spin_period_s > 0")
	}
	for _, e := range stationWithRollers(false) {
		if e.Animation != nil {
			t.Fatalf("render_rollers=false left an animated entry: %s", e.Label)
		}
	}
}

// animations_enabled=false must strip the spec at decode, leaving the object
// present but inert -- that is what isolates animation cost from scene size.
func TestWireToVisualOptsStripsAnimation(t *testing.T) {
	m := map[string]interface{}{
		"type":      "capsule",
		"label":     "pick-station/roller-00",
		"radius_mm": 10.0,
		"length_mm": 300.0,
		"animation": map[string]interface{}{
			"mode":     "spin",
			"period_s": 2.0,
		},
	}

	kept, err := wireToVisualOpts(m, false)
	if err != nil {
		t.Fatalf("decode (keep): %v", err)
	}
	if !visuals.IsAnimated(kept.ToItem().Animation) {
		t.Fatal("expected animation to survive when not stripping")
	}

	stripped, err := wireToVisualOpts(m, true)
	if err != nil {
		t.Fatalf("decode (strip): %v", err)
	}
	if visuals.IsAnimated(stripped.ToItem().Animation) {
		t.Fatal("animation survived stripAnimation=true")
	}
	if stripped.ToItem().Label != "pick-station/roller-00" {
		t.Fatal("stripping must not change the object itself")
	}
}

// Defaults must preserve existing behaviour for configs that set neither.
func TestAnimationDefaults(t *testing.T) {
	var c WorkcellSceneConfig
	if !c.animationsEnabled() {
		t.Error("animations must default to enabled")
	}
	if got := c.animationTickHz(); got != defaultAnimationTickHz {
		t.Errorf("tick hz default = %v, want %v", got, defaultAnimationTickHz)
	}
	off := false
	c.AnimationsEnabled = &off
	if c.animationsEnabled() {
		t.Error("animations_enabled=false must disable")
	}
	c.AnimationTickHz = 0.001
	if got := c.animationTickHz(); got != minAnimationTickHz {
		t.Errorf("tick hz clamp = %v, want %v", got, minAnimationTickHz)
	}

	var p pickStation
	if !p.renderRollers() {
		t.Error("render_rollers must default to true")
	}
}
