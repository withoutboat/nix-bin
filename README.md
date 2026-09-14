# nix-bin

Custom CLI utilities, daemons, and tools for NixOS.

## Crates

### `vial-daemon`

A lightweight background daemon for tracking the active layer on Vial-compatible keyboards (specifically tuned for **W-Corne DH747**).

#### Features

- **Direct Raw HID Communication**: Connects directly to `/dev/hidraw*` without external C library dependencies.
- **Auto-Discovery**: Locates the W-Corne keyboard by Vendor ID (`0x55d4`), Product ID (`0x0461`), and serial (`vial:f64c2b3c`), automatically probing the VIA/Vial raw HID interface.
- **Layer Tracking**:
  - Monitors thumb keys **Fn** at `(3,7)` and **Del** at `(3,8)`.
  - Momentary hold or toggle lock for **Lower** (`LOWER`) and **Raise** (`RAISE`).
  - Automatic return to **Base** (`BASE`).
  - Writes the active layer name atomically to `/tmp/vial_layer`.
- **Instant Waybar Updates**: Sends real-time signal `SIGRTMIN+8` to Waybar processes upon every layer transition, instantly refreshing the `custom/vial` module.
- **Hotplug Resilient**: Gracefully handles disconnects/reconnects and automatically restores state.

#### Usage

```bash
# Run with Nix
nix run github:withoutboat/nix-bin#vial-daemon

# Run with Cargo
cd rust && cargo run --release -p vial-daemon
```

#### CLI Options

```
Options:
      --vid <VID>
          Vendor ID of keyboard (hex with 0x or decimal) [default: 21972 (0x55d4)]
      --pid <PID>
          Product ID of keyboard (hex with 0x or decimal) [default: 1121 (0x0461)]
      --serial <SERIAL>
          Substring to match in HID_UNIQ serial [default: vial:f64c2b3c]
      --device <DEVICE>
          Explicit path to raw HID device (e.g. /dev/hidraw10)
      --output <OUTPUT>
          Path to file where current layer name is written [default: /tmp/vial_layer]
      --waybar-signal-offset <WAYBAR_SIGNAL_OFFSET>
          Real-time signal offset for Waybar (SIGRTMIN + offset) [default: 8]
      --poll-interval-ms <POLL_INTERVAL_MS>
          Polling interval in milliseconds [default: 15]
      --tapping-term-ms <TAPPING_TERM_MS>
          Tapping term in milliseconds for hold vs tap detection [default: 200]
  -v, --verbose
          Verbose logging output
```

#### Waybar Configuration Example

```json
"custom/vial": {
  "format": "  {}",
  "exec": "test -s /tmp/vial_layer && cat /tmp/vial_layer || echo 'BASE'",
  "signal": 8,
  "tooltip": true,
  "tooltip-format": "Vial Layer: {}"
}
```

## CLI Tools

### `projector`

A Go CLI utility for orchestrating and synchronizing workspace git repositories across projects based on a projects YAML configuration.

#### Features

- **Project Workspaces**: Maps projects (e.g. `work`, `nix`, `personal`) to `$HOME/<project_name>/<repo_name>`.
- **Flexible Configuration**: Supports list-of-mappings and dictionary formats, custom repo names, and environment variable/path autodetection.
- **Helm Charts Workflow**:
  - Optionally specify a charts repository and aliases to discover service repositories from deployment images.
  - Automatically clones/pulls service repositories and checks out matching branches/tags.
- **Dynamic Project Roots (Zellij Sessionizer Integration)**:
  - Automatically records all project root paths into `$HOME/.config/projector/roots` upon synchronization.
  - Command `projector roots` (or `--roots`) outputs project roots to stdout, allowing sessionizers like `zellij-sessionizer` to query roots dynamically without hardcoding.

#### Usage

```bash
# Run with Nix
nix run github:withoutboat/nix-bin#projector -- [path_to_projects.yml]

# Or with flags
projector --file ~/.config/projector/projects.yml
projector --dry-run

# Output project roots for sessionizers (e.g. zellij)
projector roots
```

## Development

```bash
# Enter development shell
nix develop

# Rust checks and tests
cd rust
cargo check --workspace
cargo clippy --workspace -- -D warnings
cargo test --workspace
cargo build --release --workspace

# Go build
cd ../go
go build -v ./cmd/projector
```
