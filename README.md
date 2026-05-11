# workcell-components

Generic components for palletizing and picking workcells: a `pallet` component
(pose + dimensions + geometry) and a `pick-station` component (inbound box
presentation surface).

Both components are pure config containers — their world pose comes from the
standard `frame` block on the resource config, and dimensions come from
`frame.geometry`. They expose pose and dimension queries via `DoCommand` so
sibling services (such as `viam:pack-sequencer:sequencer`) and motion-task
modules (such as a palletizer) can read them through a single source of truth.

## Models

| Model | API | Purpose |
|---|---|---|
| `viam:workcell-components:pallet` | `rdk:component:generic` | The pallet itself — pose, length × width × thickness, optional drag-in-3D-viewer config. |
| `viam:workcell-components:pick-station` | `rdk:component:generic` | The inbound surface where boxes arrive. Carries a vacuum-grasp offset, a pre-grasp safe height, and a per-box rotation. |

## Configuration

Both components consume the standard frame block. The pallet's `frame.geometry`
should be a box with dimensions matching the physical pallet (e.g. 1219.2 × 1016
× 152.4 mm for a standard GMA pallet). The pick-station's `frame.geometry` is
similarly a box matching the inbound surface.

See `viam:pack-sequencer:sequencer` for the service that computes pack order
against these components.

## Build

```
make module.tar.gz
viam module upload --version <X.Y.Z-rcN> --platform linux/amd64 module.tar.gz
```
