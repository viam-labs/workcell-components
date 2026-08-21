package workcellcomponents

import (
	"context"
	"testing"
	"time"

	"github.com/golang/geo/r3"
	"go.viam.com/rdk/logging"
	"go.viam.com/rdk/resource"
	"go.viam.com/rdk/spatialmath"
)

func TestIsTruthy(t *testing.T) {
	for _, tc := range []struct {
		in   interface{}
		want bool
	}{
		{true, true}, {false, false},
		{1.0, true}, {0.0, false},
		{1, true}, {0, false},
		{"true", true}, {"True", true}, {"1", true},
		{"false", false}, {"", false}, {nil, false},
	} {
		if got := isTruthy(tc.in); got != tc.want {
			t.Errorf("isTruthy(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func newTestBoxDetect(t *testing.T, cfg *BoxDetectConfig) *boxDetect {
	t.Helper()
	res, err := newBoxDetect(context.Background(), nil, resource.Config{
		Name:                "box-detect",
		ConvertedAttributes: cfg,
	}, logging.NewTestLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	return res.(*boxDetect)
}

func TestBoxDetectDisabledByDefault(t *testing.T) {
	b := newTestBoxDetect(t, &BoxDetectConfig{IntervalSeconds: 60})
	rd, _ := b.Readings(context.Background(), nil)
	if rd["enabled"] != false || rd["box_present"] != false {
		t.Fatalf("disabled infeed must report enabled:false, "+
			"box_present:false; got %v", rd)
	}
	out, _ := b.DoCommand(context.Background(),
		map[string]interface{}{"take": true})
	if out["taken"] != false {
		t.Fatalf("take on a disabled infeed must refuse: %v", out)
	}
}

func TestBoxDetectLifecycle(t *testing.T) {
	b := newTestBoxDetect(t, &BoxDetectConfig{IntervalSeconds: 60, Enabled: true})
	rd, _ := b.Readings(context.Background(), nil)
	if rd["box_present"] != true {
		t.Fatal("expected a box waiting at start")
	}
	if rd["interval_seconds"].(float64) != 60 {
		t.Fatalf("interval_seconds = %v", rd["interval_seconds"])
	}
	out, _ := b.DoCommand(context.Background(),
		map[string]interface{}{"take": true})
	if out["taken"] != true {
		t.Fatalf("take failed: %v", out)
	}
	rd, _ = b.Readings(context.Background(), nil)
	if rd["box_present"] != false {
		t.Fatal("box still present after take")
	}
	if s := rd["seconds_until_next"].(float64); s <= 0 || s > 60 {
		t.Fatalf("seconds_until_next = %v", s)
	}
	out, _ = b.DoCommand(context.Background(),
		map[string]interface{}{"take": true})
	if out["taken"] != false {
		t.Fatal("take from empty infeed should refuse")
	}
	b.DoCommand(context.Background(), map[string]interface{}{"reset": true})
	rd, _ = b.Readings(context.Background(), nil)
	if rd["box_present"] != true {
		t.Fatal("reset should dock a box")
	}
}

func TestBoxDetectStartEmpty(t *testing.T) {
	b := newTestBoxDetect(t, &BoxDetectConfig{
		IntervalSeconds: 60, StartEmpty: true, Enabled: true})
	rd, _ := b.Readings(context.Background(), nil)
	if rd["box_present"] != false {
		t.Fatal("StartEmpty should begin with no box")
	}
}

func TestTrayDockLifecycle(t *testing.T) {
	res, err := newTrayDock(context.Background(), nil, resource.Config{
		Name:                "tray-dock",
		ConvertedAttributes: &TrayDockConfig{ExchangeSeconds: 60},
	}, logging.NewTestLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	d := res.(*trayDock)
	rd, _ := d.Readings(context.Background(), nil)
	if rd["tray_present"] != true {
		t.Fatal("expected a docked tray at start")
	}
	out, _ := d.DoCommand(context.Background(),
		map[string]interface{}{"dispatch": true})
	if out["dispatched"] != true {
		t.Fatalf("dispatch failed: %v", out)
	}
	rd, _ = d.Readings(context.Background(), nil)
	if rd["tray_present"] != false {
		t.Fatal("tray still present after dispatch")
	}
	out, _ = d.DoCommand(context.Background(),
		map[string]interface{}{"dispatch": true})
	if out["dispatched"] != false {
		t.Fatal("dispatch with no tray should refuse")
	}
}

// findChild digs a labeled child out of a station/pallet visual group.
func findChild(t *testing.T, entries []visualWire, label string) *visualWire {
	t.Helper()
	for i := range entries {
		if entries[i].Label == label {
			return &entries[i]
		}
	}
	return nil
}

func stationEntries(infeed *infeedBoxState, dir Vec3D, offset *Vec3D) []visualWire {
	pose := spatialmath.NewPoseFromPoint(r3.Vector{X: 400, Y: -650, Z: 200})
	return pickStationVisuals(
		"pick-station", pose, 400, 1100, 40,
		defaultPickStationColor, VisualOptions{}, 200,
		dir, offset, 0, 0, infeed,
	)
}

func TestInfeedBoxWaitsAtPickup(t *testing.T) {
	entries := stationEntries(
		&infeedBoxState{present: true,
			dims: Vec3D{X: 196, Y: 146, Z: 98}, color: cardboardColor},
		Vec3D{Y: 1}, &Vec3D{X: 200, Y: 900})
	box := findChild(t, entries, "pick-station/infeed-box")
	if box == nil {
		t.Fatal("no infeed-box rendered")
	}
	// pickup local center: (200-200, 900-550) = (0, 350) from centroid
	if box.Pose == nil || box.Pose.Y != 350 {
		t.Fatalf("waiting box at y=%v, want 350", box.Pose)
	}
}

func TestInfeedBoxTravels(t *testing.T) {
	// halfway: entry edge local y=-550, pickup y=350, expect y=-100
	entries := stationEntries(
		&infeedBoxState{present: false, fraction: 0.5,
			dims: Vec3D{X: 196, Y: 146, Z: 98}, color: cardboardColor},
		Vec3D{Y: 1}, &Vec3D{X: 200, Y: 900})
	box := findChild(t, entries, "pick-station/infeed-box")
	if box == nil {
		t.Fatal("no infeed-box rendered")
	}
	if box.Pose.Y != -100 {
		t.Fatalf("halfway box at y=%v, want -100", box.Pose.Y)
	}
}

func TestInfeedBoxNilOffsetStillRenders(t *testing.T) {
	entries := stationEntries(
		&infeedBoxState{present: true,
			dims: Vec3D{X: 196, Y: 146, Z: 98}, color: cardboardColor},
		Vec3D{Y: 1}, nil)
	if findChild(t, entries, "pick-station/infeed-box") == nil {
		t.Fatal("nil box_origin_offset_mm must not disable the infeed box")
	}
}

func palletEntries(ex *trayExchangeState) []visualWire {
	pose := spatialmath.NewPoseFromPoint(r3.Vector{X: 200, Y: 500, Z: 50})
	return palletVisuals("pallet", pose, 500, 350, 100,
		Color{R: 180, G: 140, B: 90, A: 1}, "stringer",
		VisualOptions{}, ex)
}

func TestTrayExchangePhases(t *testing.T) {
	anchorY := func(entries []visualWire) float64 {
		g := findChild(t, entries, "pallet/group")
		if g == nil || g.Pose == nil {
			t.Fatal("no group anchor")
		}
		return g.Pose.Y
	}
	docked := palletEntries(&trayExchangeState{
		present: true, travelMM: 1200, loadHeightMM: 200})
	if y := anchorY(docked); y != 500 {
		t.Fatalf("docked tray at y=%v, want 500", y)
	}
	if findChild(t, docked, "pallet/outbound-load") != nil {
		t.Fatal("docked tray must not wear the load silhouette")
	}
	quarter := palletEntries(&trayExchangeState{
		present: false, fraction: 0.25, travelMM: 1200, loadHeightMM: 200})
	if y := anchorY(quarter); y != 1100 {
		t.Fatalf("outbound tray at y=%v, want 1100", y)
	}
	if findChild(t, quarter, "pallet/outbound-load") == nil {
		t.Fatal("outbound tray should carry the load silhouette")
	}
	threeQ := palletEntries(&trayExchangeState{
		present: false, fraction: 0.75, travelMM: 1200, loadHeightMM: 200})
	if y := anchorY(threeQ); y != 1100 {
		t.Fatalf("inbound tray at y=%v, want 1100", y)
	}
	if findChild(t, threeQ, "pallet/outbound-load") != nil {
		t.Fatal("inbound empty tray must not carry a load")
	}
}

func TestSensorReadTimeoutIsBounded(t *testing.T) {
	if sensorReadTimeout > 2*time.Second {
		t.Fatal("sensor read timeout must stay small; it can hold up a tick")
	}
}

// The station's take verb forwards to the paired box-detect, so a
// module can empty the infeed without holding the sensor itself.
func TestPickStationTakeForwarding(t *testing.T) {
	// Unpaired station refuses politely instead of erroring.
	p := &pickStation{}
	out, err := p.DoCommand(context.Background(),
		map[string]interface{}{"take": true})
	if err != nil {
		t.Fatalf("unpaired take errored: %v", err)
	}
	if out["taken"] != false {
		t.Fatalf("unpaired take = %v, want taken:false", out)
	}

	// Paired station consumes the sensor's waiting box.
	b := newTestBoxDetect(t, &BoxDetectConfig{IntervalSeconds: 60, Enabled: true})
	p = &pickStation{infeed: b}
	out, err = p.DoCommand(context.Background(),
		map[string]interface{}{"take": true})
	if err != nil {
		t.Fatalf("paired take errored: %v", err)
	}
	if out["taken"] != true {
		t.Fatalf("paired take = %v, want taken:true", out)
	}
	rd, _ := b.Readings(context.Background(), nil)
	if rd["box_present"] != false {
		t.Fatal("sensor still reports a box after station take")
	}
}
