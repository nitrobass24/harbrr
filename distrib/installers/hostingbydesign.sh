#!/bin/bash
# harbrr installer for Hosting by Design (Seedbox.io).
# docs: https://autobrr.com
# repo: https://github.com/autobrr/harbrr

bin="$HOME/bin/harbrr"
datadir="$HOME/.config/harbrr"
mkdir -p "$HOME/.logs/"
export log="$HOME/.logs/harbrr.log"
touch "$log"

function port() {
  LOW_BOUND=$1
  UPPER_BOUND=$2
  comm -23 <(seq "${LOW_BOUND}" "${UPPER_BOUND}" | sort) <(ss -Htan | awk '{print $4}' | cut -d':' -f2 | sort -u) | shuf -n 1
}

function harbrr_download_latest() {
  echo "Downloading harbrr release archive"

  latest=$(curl -sL https://api.github.com/repos/autobrr/harbrr/releases/latest | grep "linux_x86_64" | grep browser_download_url | grep ".tar.gz" | cut -d \" -f4) || {
    echo "Failed to query GitHub for latest version"
    exit 1
  }

  if [ -z "$latest" ]; then
    echo "No linux_x86_64 .tar.gz asset in the latest release of autobrr/harbrr"
    exit 1
  fi

  if ! curl "$latest" -L -o "$HOME/harbrr.tar.gz" >>"$log" 2>&1; then
    echo "Download failed, exiting"
    exit 1
  fi
  echo "Archive downloaded"

  echo "Extracting archive"
  mkdir -p "$HOME/bin/"
  tar xfv "$HOME/harbrr.tar.gz" --directory "$HOME/bin/" >>"$log" 2>&1 || {
    echo "Failed to extract"
    exit 1
  }
  rm -rf "$HOME/harbrr.tar.gz"
  echo "Archive extracted"
}

_systemd() {
  type="simple"

  # "systemd 250 (250.3-2)" / "systemd 247.3" — keep the bare integer major.
  sysver=$(systemctl --version | awk 'NR==1 {print $2}')
  sysver=${sysver%%[!0-9]*}
  if [ -n "$sysver" ] && [ "$sysver" -ge 240 ]; then
    type="exec"
  fi
  echo "Installing Systemd service"
  mkdir -p "$HOME/.config/systemd/user/"
  cat >"$HOME/.config/systemd/user/harbrr.service" <<EOF
[Unit]
Description=harbrr service
After=syslog.target network-online.target
[Service]
Type=${type}
ExecStart=$bin serve --data-dir=$datadir
[Install]
WantedBy=default.target
EOF
  echo "Service installed"
}

function _configure {
  port=$(port 10000 12000)

  mkdir -p "$datadir"
  cat >"$datadir/config.toml" <<CFG
# config.toml — written by the harbrr Hosting by Design installer.
# Full reference: https://github.com/autobrr/harbrr

[server]
host = "0.0.0.0"
port = ${port}
# Served under a subpath so it can sit behind a reverse proxy unchanged.
base_url = "/harbrr"

[log]
# trace | debug | info | warn | error
level = "info"
CFG

  systemctl --user daemon-reload 2>&1 | tee -a "${log}"
  systemctl --user enable --now harbrr 2>&1 | tee -a "${log}"
  echo "harbrr is now installed and running at http://$(hostname -f):${port}/harbrr/" | tee -a "${log}"
  echo "Create your admin account on the first-run setup screen." | tee -a "${log}"
}

function _remove() {
  if [[ ! -f $bin ]]; then
    echo "harbrr not installed!"
    exit 1
  fi
  systemctl stop --user harbrr
  systemctl disable --user harbrr

  # backup
  _backup_install

  rm "$HOME/.config/systemd/user/harbrr.service"
  rm -rf "$datadir"
  rm "$bin"
}

function _upgrade {
  if [[ ! -f $bin ]]; then
    echo "harbrr not installed!"
    exit 1
  fi

  systemctl stop --user harbrr

  # backup
  _backup_install

  # harbrr has no self-updater — re-download the latest release
  harbrr_download_latest
  systemctl restart --user harbrr
}

# Backup install
function _backup_install() {
  if [ ! -d "${HOME}/backup" ]; then
    mkdir -p "${HOME}/backup"
  fi

  if [ -d "$datadir" ]; then
    backup="${HOME}/backup/harbrr-$(date +"%FT%H%M").bak.tar.gz"
    echo
    echo "Creating a backup of the current instance's data directory.."
    tar -czf "${backup}" -C "${HOME}/.config" "harbrr"
    echo
    echo "Created backup of harbrr: $backup"
  fi
}

echo "Welcome to the harbrr installer..."
echo ""
echo "What do you like to do?"
echo "Logs are stored at ${log}"
echo "install = Install harbrr"
echo "upgrade = upgrades harbrr to latest version"
echo "uninstall = Completely removes harbrr"
echo "exit = Exits Installer"
while true; do
  read -r -p "Enter it here: " choice
  case $choice in
  "install")
    harbrr_download_latest
    _systemd
    _configure
    break
    ;;
  "uninstall")
    _remove
    break
    ;;
  "upgrade")
    _upgrade
    break
    ;;
  "exit")
    break
    ;;
  *)
    echo "Unknown Option."
    ;;
  esac
done
exit
