use std::fs::{self, File, OpenOptions};
use std::io::{self, Read, Write};
use std::os::unix::fs::OpenOptionsExt;
use std::os::unix::io::AsRawFd;
use std::path::{Path, PathBuf};

pub const DEFAULT_VID: u16 = 0x55d4;
pub const DEFAULT_PID: u16 = 0x0461;
pub const DEFAULT_SERIAL: &str = "vial:f64c2b3c";

pub const REPORT_SIZE: usize = 32;

const VIA_GET_PROTOCOL_VERSION: u8 = 0x01;
const VIA_GET_KEYBOARD_VALUE: u8 = 0x02;
const VIA_ID_SWITCH_MATRIX_STATE: u8 = 0x03;

const VIAL_PREFIX: u8 = 0xfe;
const VIAL_GET_UNLOCK_STATUS: u8 = 0x05;
const VIAL_UNLOCK_START: u8 = 0x06;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub struct MatrixState {
    pub fn_down: bool,
    pub del_down: bool,
    pub other_down: bool,
}

pub struct RawHidDevice {
    file: File,
    pub path: PathBuf,
}

impl RawHidDevice {
    pub fn open(path: &Path) -> io::Result<Self> {
        let file = OpenOptions::new()
            .read(true)
            .write(true)
            .custom_flags(libc::O_NONBLOCK)
            .open(path)?;

        let mut dev = Self {
            file,
            path: path.to_path_buf(),
        };

        // Validate that device responds to VIA protocol
        dev.probe(50)?;
        Ok(dev)
    }

    /// Perform a 32-byte report exchange (33 bytes written including 0x00 report ID)
    pub fn xfer(&mut self, cmd: &[u8], timeout_ms: i32) -> io::Result<[u8; REPORT_SIZE]> {
        if cmd.len() > REPORT_SIZE {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "command payload too large for raw HID report",
            ));
        }

        let mut out = [0u8; REPORT_SIZE + 1];
        out[0] = 0x00; // report ID for unnumbered HID reports
        out[1..1 + cmd.len()].copy_from_slice(cmd);

        self.file.write_all(&out)?;

        let fd = self.file.as_raw_fd();
        let mut pfd = libc::pollfd {
            fd,
            events: libc::POLLIN,
            revents: 0,
        };

        let ret = unsafe { libc::poll(&mut pfd, 1, timeout_ms) };
        if ret < 0 {
            return Err(io::Error::last_os_error());
        }
        if ret == 0 {
            return Err(io::Error::new(
                io::ErrorKind::TimedOut,
                "hidraw read timed out",
            ));
        }

        if (pfd.revents & (libc::POLLERR | libc::POLLHUP | libc::POLLNVAL)) != 0 {
            return Err(io::Error::new(
                io::ErrorKind::ConnectionReset,
                "hidraw device reported error or hangup",
            ));
        }

        let mut buf = [0u8; REPORT_SIZE];
        let n = self.file.read(&mut buf)?;
        if n < REPORT_SIZE {
            return Err(io::Error::new(
                io::ErrorKind::UnexpectedEof,
                format!("short read from hidraw: expected {REPORT_SIZE}, got {n}"),
            ));
        }

        Ok(buf)
    }

    /// Query VIA protocol version to verify interface
    pub fn probe(&mut self, timeout_ms: i32) -> io::Result<u16> {
        let resp = self.xfer(&[VIA_GET_PROTOCOL_VERSION], timeout_ms)?;
        if resp[0] != VIA_GET_PROTOCOL_VERSION {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                format!("device does not speak VIA protocol (got 0x{:02x})", resp[0]),
            ));
        }
        let ver = u16::from_be_bytes([resp[1], resp[2]]);
        Ok(ver)
    }

    /// Check if keyboard is unlocked in Vial security model
    pub fn check_unlocked(&mut self, timeout_ms: i32) -> io::Result<bool> {
        let resp = self.xfer(&[VIAL_PREFIX, VIAL_GET_UNLOCK_STATUS], timeout_ms)?;
        // resp[0] is 1 if unlocked, 0 if locked
        Ok(resp[0] == 1)
    }

    /// Request unlock start
    pub fn unlock_start(&mut self, timeout_ms: i32) -> io::Result<()> {
        let _ = self.xfer(&[VIAL_PREFIX, VIAL_UNLOCK_START], timeout_ms)?;
        Ok(())
    }

    /// Read switch matrix and extract states of (3,7) and (3,8)
    pub fn poll_matrix(&mut self, timeout_ms: i32) -> io::Result<MatrixState> {
        let resp = self.xfer(
            &[VIA_GET_KEYBOARD_VALUE, VIA_ID_SWITCH_MATRIX_STATE],
            timeout_ms,
        )?;

        if resp[0] != VIA_GET_KEYBOARD_VALUE || resp[1] != VIA_ID_SWITCH_MATRIX_STATE {
            return Err(io::Error::new(
                io::ErrorKind::InvalidData,
                "invalid switch matrix response header",
            ));
        }

        // resp[2..10] contains rows 0..3 (2 bytes each for 12 columns)
        // Row 3 is bytes [8] (cols 8..11) and [9] (cols 0..7)
        // Col 7 (Fn): bit 7 of resp[9]
        // Col 8 (Del): bit 0 of resp[8]
        let del_down = (resp[8] & (1 << 0)) != 0;
        let fn_down = (resp[9] & (1 << 7)) != 0;

        // Check if any other key in the matrix is pressed
        let other_down = resp[2..8].iter().any(|&b| b != 0)
            || (resp[8] & !0x01) != 0
            || (resp[9] & !0x80) != 0;

        Ok(MatrixState {
            fn_down,
            del_down,
            other_down,
        })
    }
}

/// Locate keyboard hidraw device node via sysfs or explicit path
pub fn find_device(
    vid: u16,
    pid: u16,
    serial: &str,
    explicit_path: Option<&Path>,
) -> io::Result<RawHidDevice> {
    if let Some(path) = explicit_path {
        return RawHidDevice::open(path);
    }

    let sysfs_hidraw = Path::new("/sys/class/hidraw");

    let mut candidate_paths: Vec<PathBuf> = Vec::new();

    if let Ok(entries) = fs::read_dir(sysfs_hidraw) {
        let mut dev_names: Vec<String> = entries
            .flatten()
            .map(|e| e.file_name().to_string_lossy().into_owned())
            .collect();

        // Sort naturally so hidraw0, hidraw1, ...
        dev_names.sort_by_key(|s| {
            s.strip_prefix("hidraw")
                .and_then(|n| n.parse::<u32>().ok())
                .unwrap_or(u32::MAX)
        });

        for name in dev_names {
            let uevent_path = sysfs_hidraw.join(&name).join("device").join("uevent");
            if let Ok(content) = fs::read_to_string(&uevent_path) {
                let mut matches_vid_pid = false;
                let mut matches_serial = serial.is_empty();

                for line in content.lines() {
                    if let Some((k, v)) = line.split_once('=') {
                        match k.trim() {
                            "HID_ID" => {
                                let parts: Vec<&str> = v.split(':').collect();
                                if parts.len() == 3 {
                                    let v_vid = u16::from_str_radix(parts[1].trim(), 16).unwrap_or(0);
                                    let v_pid = u16::from_str_radix(parts[2].trim(), 16).unwrap_or(0);
                                    if v_vid == vid && v_pid == pid {
                                        matches_vid_pid = true;
                                    }
                                }
                            }
                            "HID_UNIQ"
                                if !serial.is_empty()
                                    && v.to_lowercase().contains(&serial.to_lowercase()) =>
                            {
                                matches_serial = true;
                            }
                            _ => {}
                        }
                    }
                }

                if matches_vid_pid && matches_serial {
                    candidate_paths.push(PathBuf::from("/dev").join(&name));
                }
            }
        }
    }

    if candidate_paths.is_empty() {
        return Err(io::Error::new(
            io::ErrorKind::NotFound,
            format!(
                "no device matching VID:PID {:04x}:{:04x} with serial '{}' found in /sys/class/hidraw",
                vid, pid, serial
            ),
        ));
    }

    // Try each candidate until one responds to VIA raw HID probe
    for path in &candidate_paths {
        if let Ok(dev) = RawHidDevice::open(path) {
            return Ok(dev);
        }
    }

    Err(io::Error::new(
        io::ErrorKind::NotFound,
        format!(
            "found candidate devices ({:?}) but none answered VIA raw HID protocol",
            candidate_paths
        ),
    ))
}
