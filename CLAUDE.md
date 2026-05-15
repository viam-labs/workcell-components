# workcell-components

## What this is

Viam module that registers two generic components:

- **`viam:workcell-components:pallet`** — represents a pallet on the workcell floor. Pose comes from the standard `frame:` block (drag-to-place in the 3D viewer just works). Dimensions and color are intrinsic to the model: a freshly-added pallet renders as a standard GMA pallet (1219.2 × 1016.0 × 152.4 mm, wood-tan) with no `frame.geometry` block required. Attributes: `width_mm`, `length_mm`, `thickness_mm`, `color`, `label` — all optional. Exposes pose + dims + color via `get_pose` / `get_dimensions` / `get_color` / `get_attributes`; live-updates via `set_dimensions` / `set_color` / `set_attributes`.

- **`viam:workcell-components:pick-station`** — represents the inbound conveyor or static fixture where boxes arrive for the palletizer to grab. Pose + incline come from `frame:` (incline encoded in `frame.orientation`). Dimensions and color are intrinsic, like pallet (defaults: 400 × 400 × 40 mm, metallic grey). Attributes: `width_mm`, `length_mm`, `thickness_mm`, `color`, `lowest_point_height_mm`, `box_origin_offset_mm`, `box_theta_deg`, `pick_home_z_offset_mm`, `label`. Exposes pose, dims, color, vacuum-grasp pose, pick-home waypoint via DoCommand; live-updates via `set_dimensions` / `set_color` / `set_attributes` (the last also accepts `box_theta_deg`, `box_origin_offset_mm`, `pick_home_z_offset_mm`).

These are two of the four sibling modules in the workcell ecosystem. The others:

| Module | Role |
|---|---|
| `viam:pack-sequencer` | WorldStateStore service: owns pack-order math, cursor, placed-set. Reads pallet pose+dims from this module via DoCommand at construction. |
| `viam:cell-configure-webapp` | Apps-only module shipping the operator UI. |
| `shrews-testing:palletizing-module` | The palletizer state machine. Reads pickup-side poses from `pick-station` via DoCommand; reads pack-order from `pack-sequencer`. |

## Conventions

- **Pose authority lives in the `frame:` block, not Config attributes.** Operators can drag the component in the 3D viewer and the changes propagate via `resource.AlwaysRebuild`. No custom drag-to-attribute sync needed.
- **Dimensions + color are intrinsic to the model.** Operators don't have to type a `frame.geometry` block to get a sensibly-sized component — the defaults render correctly out of the box. Precedence: `frame.geometry` > Config attrs > hard-coded defaults. `frame.geometry` is preserved for legacy drag-and-save in the 3D viewer.
- **Live updates: `set_*` DoCommand verbs mutate in-memory state.** Consumers (pack-sequencer, palletizer) fetch dims/pose on-demand via DoCommand and pick up changes immediately — no reconfigure of dependent modules required. Motion-planner collision geometry still reads from `frame.geometry` at cell-config-load time.
- **Internal pose is the bottom-left corner of the top face.** Viam's frame convention is centroid-based; both resources offset to corner internally so pack-order math reads naturally from the corner outward. See `pickStationPoseAndDimsFromFrame` / pallet equivalent.
- **DoCommand is the only RPC surface.** No custom Viam API; everything is generic. Verb names match `viam:pack-sequencer` and palletizer conventions.

## Dependencies

- `github.com/viam-labs/viamkit/geom` — `Vec3D` for `BoxOriginOffsetMM` in pick-station config. Type alias in `pick_station.go` keeps call sites terse. Pinned at the version in `go.mod`.

## Layout

```
workcell-components/
├── go.mod
├── meta.json
├── Makefile
├── VERSION
├── README.md
├── CLAUDE.md         (this file)
├── pallet.go         (PalletConfig + pallet struct + DoCommand handlers)
├── pick_station.go   (PickStationConfig + pickStation struct + DoCommand handlers)
├── helpers.go        (asFloat, Color, asColor, validateColor)
└── cmd/module/main.go
```

## Build + publish

```
make module.tar.gz
viam module upload --version 0.1.X-rcN --platform linux/amd64 --upload module.tar.gz
```

Bump `VERSION` first; `make publish` reads it.

## What to watch when editing

- **Frame ↔ attribute drift.** If a setting can be expressed in `frame.geometry` or `frame.orientation`, prefer the frame — operator drag-and-save needs to work.
- **Backward compat with `latest-with-prerelease` consumers.** Any DoCommand response shape change is a wire-format break. Adding new fields is fine; renaming or removing them isn't.
- **viamkit version drift.** When bumping viamkit, run `go test ./...` across all three sibling modules to catch incompatibilities before they ship.

## Repo + registry

- GitHub: [`viam-labs/workcell-components`](https://github.com/viam-labs/workcell-components)
- Registry: `viam:workcell-components`
- Latest published: `0.3.0` (`resource.Shaped` for planner-visible collision geometry; `get_vacuum_pose` / `get_pick_home_pose` accept nested-args calling convention)
