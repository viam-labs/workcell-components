# workcell-components

Generic components, decorations, and a scene service for palletizing / picking
workcells in Viam. Configure them via the standard `frame` block + per-model
attributes; the bundled `workcell-scene` service publishes their visuals to the
3D viewer, and the `workcell-tuner` web app gives operators a schema-driven
form editor with a live 3D preview per instance.

## Models

| Model | API | What it is |
|---|---|---|
| `viam:workcell-components:pallet` | `rdk:component:generic` | The pallet itself. Slatted GMA stringer/block or single-piece plastic. |
| `viam:workcell-components:pick-station` | `rdk:component:generic` | Inbound conveyor with rollers + side rails + legs + direction arrow + grasp-target indicator. |
| `viam:workcell-components:robot-pedestal` | `rdk:component:generic` | Base under the robot arm. |
| `viam:workcell-components:safety-fence` | `rdk:component:generic` | Wire-mesh perimeter panel. |
| `viam:workcell-components:light-curtain` | `rdk:component:generic` | Paired-tower light curtain. |
| `viam:workcell-components:e-stop` | `rdk:component:generic` | Mushroom-button on a post. |
| `viam:workcell-components:stack-light` | `rdk:component:generic` | Multi-segment status beacon. |
| `viam:workcell-components:tote-stack` | `rdk:component:generic` | Stack of N identical totes/boxes. |
| `viam:workcell-components:hmi-cabinet` | `rdk:component:generic` | Operator HMI panel on a stand. |
| `viam:workcell-components:floor-decal` | `rdk:component:generic` | Floor stripe or zone marking (solid or hazard). |
| `viam:workcell-components:workcell-bounds` | `rdk:component:generic` | Wire-frame footprint of the cell. |
| `viam:workcell-components:scan-tunnel` | `rdk:component:generic` | Fixed-mount barcode/SKU scan tunnel: gantry frame + reader heads + pulsing scan line (1- or 3-sided). |
| `viam:workcell-components:workcell-scene` | `rdk:service:world_state_store` | Polls every component's `get_visuals` and republishes to the 3D viewer. |
| `viam:workcell-components:box-detect` | `rdk:component:sensor` | Presence sensor over the pick-station infeed. Simulates the conveyor: a box waits until taken, the next arrives after `interval_seconds`. |
| `viam:workcell-components:pallet-empty` | `rdk:component:sensor` | Pallet occupancy, read from the pack sequencer's own progress. Reports `pallet_empty`, `pallet_full`, `boxes_on_pallet`, `capacity`. |

## Frame origins — where the frame block places each component

Each component derives its world placement from its `frame` block. **The frame
origin is not the same on every model** — some are body-centroid, some sit on
the floor, some anchor at the bottom of a stack. Set `frame.translation.z`
accordingly when you want the component to rest on the floor.

| Model | Frame origin | To rest on the floor, set `frame.translation.z =` |
|---|---|---|
| `pallet` | **Centroid** of the bounding box (Viam convention — drag-to-place in the 3D viewer drags the centroid). | `thickness_mm / 2` (≈ 76 mm for the GMA default) |
| `pick-station` | **Centroid** of the bounding box (its internal corner offset is hidden behind `get_*_pose`). | `lowest_point_height_mm + thickness_mm / 2` |
| `workcell-bounds` | **Centroid** of the bounding volume. | `height_mm / 2` |
| `safety-fence` | Centerline of the panel **base on the floor**. Local +X runs along the panel; +Z up. | `0` |
| `light-curtain` | **Floor** between the two towers. Local +X = span (tower-to-tower). | `0` |
| `e-stop` | Center of the base plate **on the floor**. | `0` |
| `stack-light` | Center of the base **on the floor**. | `0` |
| `robot-pedestal` | Center of the base **on the floor**. | `0` |
| `hmi-cabinet` | Center of the support post's base **on the floor**. | `0` |
| `floor-decal` | Bottom face of the decal **flush with the floor** (decal extends upward by `thickness_mm`). | `0` |
| `tote-stack` | **Bottom of the bottom-most box**. The stack grows from here along `stack_axis`. | `0` |
| `scan-tunnel` | **Floor** midway between the posts. Local +X = span (perpendicular to flow). | `0` |
| `workcell-scene` | n/a (service — no frame block). | — |

Local axes (in every model's own frame) follow Viam's right-handed convention:
**+X** is the local "width", **+Y** the local "length", **+Z** up. The pallet
has +X along its 1219 mm GMA-48″ direction and +Y along its 1016 mm 40″ side.

Rotations in `frame.orientation` always rotate around the **frame origin**
listed above — so e.g. spinning the pallet 90° about Z rotates around its
centroid, not a corner.

## Verbs

Every component answers a small uniform set of DoCommand verbs that the scene
service and the webapp consume:

| Verb | Returns |
|---|---|
| `get_pose` / `get_visual_pose` | World pose at the component's frame origin (and the visual centroid where they differ). |
| `get_attributes` | All editable attributes + current values. |
| `get_visuals` | Wire-format visual primitives (the workcell-scene relay). |
| `get_schema` | Typed attribute schema — `{key, type, label, group, unit, min, max, step, values, when, help}`. Drives the webapp form. |
| `get_status` / `get_summary` | Health snapshot + one-line human description. |
| `set_attributes` | Live update of any subset of editable attributes. Persists for the lifetime of the resource; reverts on reconfigure. |

The pallet adds `get_pallet_home_pose`, `get_top_face_center`, and
`get_corner_poses`; the pick-station adds `get_pickup_pose`,
`get_pick_home_pose`, `get_vacuum_pose`, and `get_conveyor_direction`.

## Workcell Tuner web app

`apps/workcell/index.html` is a `single_machine` Viam application that
discovers every workcell component on the connected machine, generates an edit
form from each one's `get_schema`, and renders a live Three.js 3D preview per
card built from `get_visuals`. Edit a field → **Apply** → `set_attributes`
fires → the machine's 3D tab shows the change within one workcell-scene tick.

## Build & publish

Cloud-build for all platforms in `meta.json` (linux/amd64, linux/arm64,
darwin/arm64) from a git branch or commit:

```
viam module build start --version <X.Y.Z-rcN> --ref <branch-or-sha>
viam module build list --id <returned-id>
```

Or build a single platform locally and upload:

```
make module.tar.gz
viam module upload --version <X.Y.Z-rcN> --platform linux/amd64 module.tar.gz
```

Bump `VERSION` first; both flows read it. After adding a new model or
application to `meta.json`, run `viam module update` once so the registry's
metadata catches up — `upload` alone only ships the binary.
