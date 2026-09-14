use std::fs;
use std::io;
use std::path::Path;
use std::time::{Duration, Instant};

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Layer {
    Base,
    Lower,
    Raise,
}

impl Layer {
    pub fn as_str(&self) -> &'static str {
        match self {
            Layer::Base => "BASE",
            Layer::Lower => "LOWER",
            Layer::Raise => "RAISE",
        }
    }
}

pub struct LayerTracker {
    current_layer: Layer,
    locked_layer: Option<Layer>,
    fn_held: bool,
    del_held: bool,
    fn_press_time: Option<Instant>,
    del_press_time: Option<Instant>,
    fn_other_pressed: bool,
    del_other_pressed: bool,
    fn_unlocking: bool,
    del_unlocking: bool,
    tapping_term: Duration,
}

impl LayerTracker {
    pub fn new(tapping_term_ms: u64) -> Self {
        Self {
            current_layer: Layer::Base,
            locked_layer: None,
            fn_held: false,
            del_held: false,
            fn_press_time: None,
            del_press_time: None,
            fn_other_pressed: false,
            del_other_pressed: false,
            fn_unlocking: false,
            del_unlocking: false,
            tapping_term: Duration::from_millis(tapping_term_ms),
        }
    }

    pub fn current_layer(&self) -> Layer {
        self.current_layer
    }

    pub fn reset(&mut self) -> Option<Layer> {
        self.locked_layer = None;
        self.fn_held = false;
        self.del_held = false;
        self.fn_press_time = None;
        self.del_press_time = None;
        self.fn_other_pressed = false;
        self.del_other_pressed = false;
        self.fn_unlocking = false;
        self.del_unlocking = false;

        if self.current_layer != Layer::Base {
            self.current_layer = Layer::Base;
            Some(Layer::Base)
        } else {
            None
        }
    }

    pub fn update(&mut self, fn_down: bool, del_down: bool, other_down: bool) -> Option<Layer> {
        let now = Instant::now();

        // 1. Check Fn press / release edges
        if fn_down && !self.fn_held {
            self.fn_held = true;
            self.fn_press_time = Some(now);
            self.fn_other_pressed = false;

            if self.locked_layer == Some(Layer::Lower) {
                // Tapping or pressing Fn on Layer 1 triggers TO(0) returning to Base
                self.locked_layer = None;
                self.fn_unlocking = true;
            } else {
                self.fn_unlocking = false;
                if self.locked_layer == Some(Layer::Raise) {
                    self.locked_layer = None;
                }
            }
        } else if !fn_down && self.fn_held {
            self.fn_held = false;
            let duration = self.fn_press_time.map(|t| now.duration_since(t)).unwrap_or_default();

            if self.fn_unlocking {
                self.fn_unlocking = false;
            } else {
                let is_tap = !self.fn_other_pressed && duration < self.tapping_term;
                if is_tap {
                    self.locked_layer = Some(Layer::Lower);
                }
            }
        } else if fn_down && self.fn_held && other_down {
            self.fn_other_pressed = true;
        }

        // 2. Check Del press / release edges
        if del_down && !self.del_held {
            self.del_held = true;
            self.del_press_time = Some(now);
            self.del_other_pressed = false;

            if self.locked_layer == Some(Layer::Raise) {
                // Tapping or pressing Del on Layer 2 triggers TO(0) returning to Base
                self.locked_layer = None;
                self.del_unlocking = true;
            } else {
                self.del_unlocking = false;
                if self.locked_layer == Some(Layer::Lower) {
                    self.locked_layer = None;
                }
            }
        } else if !del_down && self.del_held {
            self.del_held = false;
            let duration = self.del_press_time.map(|t| now.duration_since(t)).unwrap_or_default();

            if self.del_unlocking {
                self.del_unlocking = false;
            } else {
                let is_tap = !self.del_other_pressed && duration < self.tapping_term;
                if is_tap {
                    self.locked_layer = Some(Layer::Raise);
                }
            }
        } else if del_down && self.del_held && other_down {
            self.del_other_pressed = true;
        }

        // 3. Compute active layer
        let fn_active = self.fn_held && !self.fn_unlocking;
        let del_active = self.del_held && !self.del_unlocking;

        let new_layer = if fn_active && del_active {
            match (self.fn_press_time, self.del_press_time) {
                (Some(t_fn), Some(t_del)) if t_del >= t_fn => Layer::Raise,
                _ => Layer::Lower,
            }
        } else if fn_active {
            Layer::Lower
        } else if del_active {
            Layer::Raise
        } else {
            self.locked_layer.unwrap_or(Layer::Base)
        };

        if new_layer != self.current_layer {
            self.current_layer = new_layer;
            Some(new_layer)
        } else {
            None
        }
    }
}

/// Atomically write layer name to target output path
pub fn write_layer_file(path: &Path, layer: Layer) -> io::Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    let content = layer.as_str();
    let tmp_path = path.with_extension("tmp");
    fs::write(&tmp_path, content)?;
    fs::rename(&tmp_path, path)?;
    Ok(())
}

/// Find all running Waybar processes and send real-time signal SIGRTMIN + offset
pub fn notify_waybar(signal_offset: i32) -> usize {
    let sig_num = libc::SIGRTMIN() + signal_offset;
    let mut notified = 0;

    if let Ok(entries) = fs::read_dir("/proc") {
        for entry in entries.flatten() {
            if let Ok(file_type) = entry.file_type() {
                if file_type.is_dir() {
                    let name = entry.file_name();
                    if let Ok(pid) = name.to_string_lossy().parse::<libc::pid_t>() {
                        let comm_path = entry.path().join("comm");
                        let comm = fs::read_to_string(&comm_path).unwrap_or_default();
                        let comm_trimmed = comm.trim();

                        let is_waybar = comm_trimmed == "waybar"
                            || comm_trimmed == ".waybar-wrapped"
                            || fs::read_to_string(entry.path().join("cmdline"))
                                .map(|cmd| cmd.contains("waybar"))
                                .unwrap_or(false);

                        if is_waybar {
                            unsafe {
                                if libc::kill(pid, sig_num) == 0 {
                                    notified += 1;
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    notified
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::thread::sleep;

    #[test]
    fn test_initial_state_is_base() {
        let tracker = LayerTracker::new(200);
        assert_eq!(tracker.current_layer(), Layer::Base);
    }

    #[test]
    fn test_momentary_lower() {
        let mut tracker = LayerTracker::new(50);
        // Press Fn
        let res = tracker.update(true, false, false);
        assert_eq!(res, Some(Layer::Lower));
        assert_eq!(tracker.current_layer(), Layer::Lower);

        // Type another key while holding
        let res = tracker.update(true, false, true);
        assert_eq!(res, None);

        // Sleep to exceed tapping term
        sleep(Duration::from_millis(60));

        // Release Fn
        let res = tracker.update(false, false, false);
        assert_eq!(res, Some(Layer::Base));
        assert_eq!(tracker.current_layer(), Layer::Base);
    }

    #[test]
    fn test_tap_to_lock_lower_and_unlock() {
        let mut tracker = LayerTracker::new(100);
        // Press Fn
        let res = tracker.update(true, false, false);
        assert_eq!(res, Some(Layer::Lower));

        // Quick release without other keys (< 100ms)
        let res = tracker.update(false, false, false);
        assert_eq!(res, None);
        assert_eq!(tracker.current_layer(), Layer::Lower);

        // We are locked in Lower. Now press Fn again to unlock (TO(0))
        let res = tracker.update(true, false, false);
        assert_eq!(res, Some(Layer::Base));
        assert_eq!(tracker.current_layer(), Layer::Base);

        // Release Fn
        let res = tracker.update(false, false, false);
        assert_eq!(res, None);
        assert_eq!(tracker.current_layer(), Layer::Base);
    }

    #[test]
    fn test_momentary_raise() {
        let mut tracker = LayerTracker::new(50);
        // Press Del
        let res = tracker.update(false, true, false);
        assert_eq!(res, Some(Layer::Raise));
        assert_eq!(tracker.current_layer(), Layer::Raise);

        // Hold Del past tapping term
        sleep(Duration::from_millis(60));

        // Release Del
        let res = tracker.update(false, false, false);
        assert_eq!(res, Some(Layer::Base));
        assert_eq!(tracker.current_layer(), Layer::Base);
    }

    #[test]
    fn test_tap_to_lock_raise_and_unlock() {
        let mut tracker = LayerTracker::new(100);
        // Press Del
        let res = tracker.update(false, true, false);
        assert_eq!(res, Some(Layer::Raise));

        // Quick release (< 100ms)
        let res = tracker.update(false, false, false);
        assert_eq!(res, None);
        assert_eq!(tracker.current_layer(), Layer::Raise);

        // Locked in Raise. Press Del again to unlock
        let res = tracker.update(false, true, false);
        assert_eq!(res, Some(Layer::Base));
        assert_eq!(tracker.current_layer(), Layer::Base);

        // Release Del
        let res = tracker.update(false, false, false);
        assert_eq!(res, None);
        assert_eq!(tracker.current_layer(), Layer::Base);
    }

    #[test]
    fn test_reset() {
        let mut tracker = LayerTracker::new(100);
        tracker.update(true, false, false);
        assert_eq!(tracker.current_layer(), Layer::Lower);

        let res = tracker.reset();
        assert_eq!(res, Some(Layer::Base));
        assert_eq!(tracker.current_layer(), Layer::Base);
    }
}
