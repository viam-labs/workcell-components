# workcell-components

## What this is

Viam module that registers two generic components:

- **`viam:workcell-components:pallet`** — represents a pallet on the workcell floor. Owns its pose + dimensions via the standard `frame:` block (drag-to-place in the 3D viewer just works). Single attribute: optional `label`. Exposes pose + dims via `get_pose` / `get_dimensions` / `get_attributes` DoCommands.

- **`viam:workcell-components:pick-station`** — represents the inbound conveyor or static fixture where boxes arrive for the palletizer to grab. Owns its pose + dims via `frame:` (including the conveyor incline angles, encoded in `frame.orientation`). Attributes: `lowest_point_height_mm`, `box_origin_offset_mm`, `box_theta_deg`, `pick_home_z_offset_mm`, `label`. Exposes pose, dims, vacuum-grasp pose, pick-home waypoint via DoCommand.

These are two of the four sibling modules in the workcell ecosystem. The others:

| Module | Role |
|---|---|
| `viam:pack-sequencer` | WorldStateStore service: owns pack-order math, cursor, placed-set. Reads pallet pose+dims from this module via DoCommand at construction. |
| `viam:cell-configure-webapp` | Apps-only module shipping the operator UI. |
| `shrews-testing:palletizing-module` | The palletizer state machine. Reads pickup-side poses from `pick-station` via DoCommand; reads pack-order from `pack-sequencer`. |

## Conventions

- **Pose authority lives in the `frame:` block, not Config attributes.** Operators can drag the component in the 3D viewer and the changes propagate via `resource.AlwaysRebuild`. No custom drag-to-attribute sync needed.
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
├── helpers.go        (asFloat utility)
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
- Latest published: `0.1.1-rc1`
