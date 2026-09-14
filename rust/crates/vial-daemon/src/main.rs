mod hid;
mod layer;

use clap::Parser;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::thread::sleep;
use std::time::Duration;

static RUNNING: AtomicBool = AtomicBool::new(true);

extern "C" fn handle_signal(_: libc::c_int) {
    RUNNING.store(false, Ordering::SeqCst);
}

fn parse_hex_or_dec(s: &str) -> Result<u16, String> {
    if let Some(stripped) = s.strip_prefix("0x").or_else(|| s.strip_prefix("0X")) {
        u16::from_str_radix(stripped, 16).map_err(|e| e.to_string())
    } else {
        s.parse::<u16>().map_err(|e| e.to_string())
    }
}

#[derive(Parser, Debug)]
#[command(
    name = "vial-daemon",
    about = "Lightweight background daemon for tracking active keyboard layer on W-Corne DH747",
    version
)]
pub struct Cli {
    /// Vendor ID of keyboard (hex with 0x or decimal)
    #[arg(long, default_value_t = hid::DEFAULT_VID, value_parser = parse_hex_or_dec)]
    pub vid: u16,

    /// Product ID of keyboard (hex with 0x or decimal)
    #[arg(long, default_value_t = hid::DEFAULT_PID, value_parser = parse_hex_or_dec)]
    pub pid: u16,

    /// Substring to match in HID_UNIQ serial
    #[arg(long, default_value = hid::DEFAULT_SERIAL)]
    pub serial: String,

    /// Explicit path to raw HID device (e.g. /dev/hidraw10)
    #[arg(long)]
    pub device: Option<PathBuf>,

    /// Path to file where current layer name is written
    #[arg(long, default_value = "/tmp/vial_layer")]
    pub output: PathBuf,

    /// Real-time signal offset for Waybar (SIGRTMIN + offset)
    #[arg(long, default_value_t = 8)]
    pub waybar_signal_offset: i32,

    /// Polling interval in milliseconds
    #[arg(long, default_value_t = 15)]
    pub poll_interval_ms: u64,

    /// Tapping term in milliseconds for hold vs tap detection
    #[arg(long, default_value_t = 200)]
    pub tapping_term_ms: u64,

    /// Verbose logging output
    #[arg(short, long)]
    pub verbose: bool,
}

fn update_layer(output_path: &Path, layer: layer::Layer, signal_offset: i32, verbose: bool) {
    if let Err(e) = layer::write_layer_file(output_path, layer) {
        eprintln!("[vial-daemon] Error writing to {}: {}", output_path.display(), e);
    }
    let notified = layer::notify_waybar(signal_offset);
    if verbose {
        eprintln!(
            "[vial-daemon] Layer updated to {} (notified {} waybar process(es))",
            layer.as_str(),
            notified
        );
    }
}

fn main() {
    let cli = Cli::parse();

    // Register signal handlers for graceful termination
    unsafe {
        libc::signal(libc::SIGINT, handle_signal as *const () as libc::sighandler_t);
        libc::signal(libc::SIGTERM, handle_signal as *const () as libc::sighandler_t);
    }

    eprintln!(
        "[vial-daemon] Starting (VID: 0x{:04x}, PID: 0x{:04x}, serial: '{}')",
        cli.vid, cli.pid, cli.serial
    );
    eprintln!(
        "[vial-daemon] Layer output: {}, Waybar signal: SIGRTMIN+{}",
        cli.output.display(),
        cli.waybar_signal_offset
    );

    let mut tracker = layer::LayerTracker::new(cli.tapping_term_ms);

    // Initial state setup: write BASE to output and notify waybar
    update_layer(
        &cli.output,
        tracker.current_layer(),
        cli.waybar_signal_offset,
        cli.verbose,
    );

    let poll_interval = Duration::from_millis(cli.poll_interval_ms);
    let reconnect_interval = Duration::from_millis(1000);

    let mut logged_waiting = false;

    while RUNNING.load(Ordering::SeqCst) {
        // Attempt to discover and open keyboard raw HID interface
        let mut dev = match hid::find_device(
            cli.vid,
            cli.pid,
            &cli.serial,
            cli.device.as_deref(),
        ) {
            Ok(d) => {
                logged_waiting = false;
                d
            }
            Err(e) => {
                if !logged_waiting {
                    eprintln!(
                        "[vial-daemon] Keyboard not connected ({e}), waiting for device..."
                    );
                    logged_waiting = true;
                }
                sleep(reconnect_interval);
                continue;
            }
        };

        eprintln!(
            "[vial-daemon] Connected to keyboard at {}",
            dev.path.display()
        );

        if let Ok(unlocked) = dev.check_unlocked(100) {
            if !unlocked {
                eprintln!(
                    "[vial-daemon] Warning: keyboard is locked in Vial security model. Matrix reads may be restricted."
                );
                let _ = dev.unlock_start(100);
            }
        }

        // Active polling loop
        while RUNNING.load(Ordering::SeqCst) {
            match dev.poll_matrix(100) {
                Ok(matrix) => {
                    if let Some(new_layer) =
                        tracker.update(matrix.fn_down, matrix.del_down, matrix.other_down)
                    {
                        update_layer(
                            &cli.output,
                            new_layer,
                            cli.waybar_signal_offset,
                            cli.verbose,
                        );
                    }
                }
                Err(e) => {
                    eprintln!(
                        "[vial-daemon] Device error on {}: {e}. Reconnecting...",
                        dev.path.display()
                    );
                    if let Some(new_layer) = tracker.reset() {
                        update_layer(
                            &cli.output,
                            new_layer,
                            cli.waybar_signal_offset,
                            cli.verbose,
                        );
                    }
                    break;
                }
            }

            sleep(poll_interval);
        }
    }

    eprintln!("[vial-daemon] Exiting gracefully...");
    // Reset output to BASE on exit
    update_layer(
        &cli.output,
        layer::Layer::Base,
        cli.waybar_signal_offset,
        cli.verbose,
    );
}
