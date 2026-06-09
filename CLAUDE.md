# workcell-components

## What this is

Viam module that registers the components and the scene service of an automation workcell. As of 0.6.0 there are twelve registered models:

### Core components

- **`viam:workcell-components:pallet`** — pallet on the workcell floor. Renders as a slatted GMA pallet (7 top deck slats + 3 stringers + 3 bottom boards) by default; `style: "block"` swaps stringers for 9 blocks; `style: "plastic"` is a single-piece slate-grey body. Pose comes from the standard `frame:` block. Default dims: 1219.2 × 1016.0 × 152.4 mm, wood-tan.

- **`viam:workcell-components:pick-station`** — inbound conveyor / fixture where boxes arrive. Renders as a roller bed (12 capsules) + 2 side rails + 4 legs + direction arrow + translucent next-pick target. Pose + incline come from `frame:` (incline encoded in `frame.orientation`). Default dims: 400 × 400 × 40 mm. `roller_spin_period_s > 0` animates the rollers.

### Safety hardware affordances (drag-place via frame block)

- **`viam:workcell-components:safety-fence`** — wire-mesh perimeter panel: 2 horizontal rails + N vertical posts + translucent screen fill.
- **`viam:workcell-components:light-curtain`** — paired transmitter/receiver towers with N horizontal beams between them; beams pulse green when `state: "safe"`, render red when `state: "broken"`.
- **`viam:workcell-components:e-stop`** — red mushroom button on a yellow post with a small base plate.
- **`viam:workcell-components:stack-light`** — multi-segment status beacon tower. Configurable colors (red/yellow/orange/green/blue/white) and per-segment states (solid/flash/off). Flash segments flicker via library animation.

### Decoration affordances (drag-place via frame block)

- **`viam:workcell-components:tote-stack`** — stack of N identical boxes along x/y/z. Useful for demo dressing.
- **`viam:workcell-components:robot-pedestal`** — base under the arm: foot + column + flange.
- **`viam:workcell-components:hmi-cabinet`** — operator HMI panel on a stand: base + post + body + screen.
- **`viam:workcell-components:floor-decal`** — thin Box on the floor; optional `stripe_pattern: "hazard"` produces alternating yellow/black sub-boxes.
- **`viam:workcell-components:workcell-bounds`** — wireframe footprint of the cell (12 capsule edges of a bounding box).

### Scene service

- **`viam:workcell-components:workcell-scene`** (`rdk:service:world_state_store`) — polls every configured component every `tick_interval_secs` (default 1.0), calls each one's `get_visuals` DoCommand verb, translates the wire-format responses into typed `visuals.Visual` primitives, and pushes the diff to the 3D viewer. Built on `github.com/viam-labs/viam-viz-helpers-go`'s `SceneServiceBase` — gets the WSS gRPC plumbing, subscriber broadcast, animation tick loop, standard DoCommand verbs (`list` / `clear` / `snapshot` / `apply_events`) and the renderer's metadata-only-update workaround (REMOVE + re-ADD with rotated UUID for color/opacity changes) for free.

  Config:

  ```jsonc
  {
    "component_names":    ["pallet", "pick-station", "fence-north", "stack-light", ...],
    "pallet_names":       ["pallet"],          // deprecated alias, still honored
    "pick_station_names": ["pick-station"],    // deprecated alias, still honored
    "tick_interval_secs": 1.0
  }
  ```

  All named components must implement `get_visuals` via DoCommand (every model in this module does). Adding a new affordance component requires zero changes to `workcell_scene.go`.

## Companion modules

| Module | Role |
|---|---|
| `viam:pack-sequencer` | WorldStateStore service: owns pack-order math. Reads pallet pose+dims from this module via DoCommand. |
| `viam:cell-configure-webapp` | Apps-only module shipping the operator UI. |
| `shrews-testing:palletizing-module` | Palletizer state machine. Reads pickup-side poses from `pick-station` via DoCommand; reads pack-order from `pack-sequencer`. |

## The `get_visuals` contract

Every component in this module implements:

```
DoCommand({"get_visuals": true}) → {
  "visuals": [
    {
      "type": "frame",                    // always first — the group anchor
      "label": "pallet/group",
      "parent_frame": "world",
      "pose":  {x, y, z, o_x, o_y, o_z, theta},
      "show_axes_helper": false
    },
    {
      "type": "box" | "capsule" | "sphere" | "arrow" | "mesh",
      "label": "pallet/slat-0",
      "parent_frame": "pallet/group",     // children parent to the anchor
      "pose":  {x, y, z, o_x, o_y, o_z, theta},   // RELATIVE to the anchor
      "dims_mm":  {x, y, z}      // box
      "radius_mm": 12, "length_mm": 380,  // capsule / arrow
      "color":   {r, g, b, opacity},
      "animation": {"mode": "spin", "period_s": 1.0, ...}   // optional
    },
    ...
  ]
}
```

The contract lives in `visuals_wire.go` (`visualWire` struct + JSON tags). The translator from this wire format to typed `visuals.Visual` lives in the same file (`wireToVisual`). Workcell-scene calls each sibling's `get_visuals`, parses the response, and routes the result through the SceneServiceBase via `apply_events`.

### Visual grouping under a parent frame

Every component's `get_visuals` response leads with one `type: "frame"` entry — an invisible transform anchor labelled `{component}/group`. All sub-primitives are reparented to it with poses expressed *relative to the anchor*; the renderer recomposes anchor∘child to the original world placement. This keeps the 3D viewer's entity list tidy (a slatted pallet is one collapsible node, not 13 loose boxes), lets a component move/hide as a unit, and means a pose change only re-sends the anchor. See `visuals_group.go::groupUnderFrame`. A hidden component (`visible: false`) publishes *only* the invisible anchor. The component's `show_axes` option drives the anchor's axes-helper triad.

## Conventions

- **Pose authority lives in the `frame:` block, not Config attributes.** Operators drag the component in the 3D viewer and changes propagate via `resource.AlwaysRebuild`. No custom drag-to-attribute sync.
- **Dimensions + color are intrinsic to the model.** Operators don't need a `frame.geometry` block to get a sensibly-sized component. Precedence: `frame.geometry` > Config attrs > hard-coded defaults. `frame.geometry` preserved for legacy drag-and-save (pallet + pick-station only — affordance components don't read it).
- **Live updates: `set_*` DoCommand verbs mutate in-memory state.** Consumers fetch on-demand via DoCommand and pick up changes immediately. Motion-planner collision geometry still reads from `frame.geometry` at cell-config-load time.
- **Internal pose: pallet uses centroid (Viam frame convention); pick-station uses bottom-left-top corner internally** so pack-order math reads from the corner outward, and exposes `get_visual_pose` for the centroid that the scene service needs.
- **DoCommand is the only RPC surface.** No custom Viam API; everything is generic. Verb names match `viam:pack-sequencer` + palletizer conventions.
- **Color changes rotate UUIDs.** Renderer drops `metadata.*` paths on UPDATED events (see [[feedback_renderer_update_path_matcher]]) — the visuals library's `applyEvents` handles this by detecting metadata-only changes and emitting REMOVE + re-ADD with a fresh UUID. Code in `workcell-scene` doesn't need to think about this.

## Dependencies

- `github.com/viam-labs/viam-viz-helpers-go` — the typed scene library. Powers `workcell-scene` (embeds `visuals.SceneServiceBase`) and the wire-format translator in `visuals_wire.go`.
- `github.com/viam-labs/viamkit/geom` — `Vec3D` type alias for `BoxOriginOffsetMM` in pick-station + `box_dims_mm` in tote-stack.

## Layout

```
workcell-components/
├── go.mod
├── meta.json
├── Makefile
├── VERSION
├── README.md
├── CLAUDE.md                  (this file)
├── helpers.go                 (asFloat, Color, asColor, validateColor, defaultLen, clamp01, r3vec, coerceStringSlice)
├── decoration.go              (decorationBase — shared scaffolding for affordance components)
├── visuals_wire.go            (visualWire + wireToVisual + visualWireToMap)
├── pallet.go                  (PalletConfig + pallet struct + DoCommand)
├── pallet_visuals.go          (palletVisuals + helpers: boxAt/capsuleAt/arrowAt/sphereAt/compose/poseToWire/colorToWire)
├── pick_station.go            (PickStationConfig + pickStation struct + DoCommand)
├── pick_station_visuals.go    (pickStationVisuals — rollers/rails/legs/arrow/target)
├── workcell_scene.go          (workcell-scene service — embeds SceneServiceBase + poll loop)
├── safety_fence.go            (Tier 2)
├── light_curtain.go           (Tier 2)
├── e_stop.go                  (Tier 2)
├── stack_light.go             (Tier 2)
├── tote_stack.go              (Tier 3)
├── robot_pedestal.go          (Tier 3)
├── hmi_cabinet.go             (Tier 3)
├── floor_decal.go             (Tier 3)
├── workcell_bounds.go         (Tier 3)
├── visuals_wire_test.go       (Phase A/B/C/E unit tests)
├── pure_test.go               (existing pose/dim tests)
└── cmd/module/main.go         (registers all 12 models via module.ModularMain)
```

## Build + publish

```
make module.tar.gz
viam module upload --version 0.6.X-rcN --platform linux/amd64 module.tar.gz
```

Bump `VERSION` first; `make publish` reads it. Pallet modules ship as prereleases (`-rcN`) by default — see [[feedback_pallet_modules_prerelease]].

## What to watch when editing

- **Frame ↔ attribute drift.** If a setting can be expressed in `frame.geometry` or `frame.orientation`, prefer the frame — operator drag-and-save must keep working.
- **Backward compat with `latest-with-prerelease` consumers.** DoCommand response shape changes are wire breaks. Adding new fields is fine; renaming or removing is not. The new `get_visuals` verb on pallet/pick-station is additive.
- **viamkit + viam-viz-helpers-go version drift.** When bumping either, run `go test ./...` across all sibling modules.
- **`workcell-scene` is type-agnostic.** A new affordance just needs a new file with the standard `get_visuals` handler and a registration in `cmd/module/main.go` + `meta.json`. Do NOT add per-type code to `workcell_scene.go`.
- **Animations attach to per-Visual entries in `get_visuals` responses.** The library's tick loop dispatches them at 30 Hz. Available modes: `spin`, `pulse`, `flicker`, `breathe`, `oscillate`, `swing`, `orbit`. Wire format: `{"mode": "...", "period_s": ..., "axis": "...", ...}`. See `visuals_wire.go::animToLib`.

## Repo + registry

- GitHub: [`viam-labs/workcell-components`](https://github.com/viam-labs/workcell-components)
- Registry: `viam:workcell-components`
- Latest published: `0.5.0` — single-Box visuals via `viamkit/viz`.
- In progress: `0.6.0-rc2` — migrated to `viam-viz-helpers-go`. Multi-primitive pallet + pick-station composites (Phase B). Nine new affordance components (Phase C/D): safety-fence, light-curtain, e-stop, stack-light, tote-stack, robot-pedestal, hmi-cabinet, floor-decal, workcell-bounds. Animations integrated (Phase E): roller spin + stack-light flash + light-curtain beam pulse. `get_schema` verb on all 11 component models for webapp form generation. rc2: every component's visuals grouped under a `{component}/group` anchor frame.
