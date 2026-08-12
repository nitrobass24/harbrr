#!/bin/bash
# harbrr installer for Seedhost.
# docs: https://autobrr.com
# repo: https://github.com/autobrr/harbrr

set -euo pipefail

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

function _start() {
  screen -dmS harbrr /bin/bash -c "$bin serve --data-dir=$datadir >>$log 2>&1"
}

function _install {
  harbrr_download_latest
  port=$(port 10000 12000)

  mkdir -p "$datadir"
  cat >"$datadir/config.toml" <<CFG
# config.toml — written by the harbrr Seedhost installer.
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

  mkdir -p "$HOME/software/cron"
  cat >"$HOME/software/cron/harbrr.cron" <<CRONC
 #!/bin/bash
 if pgrep -f "$bin serve --data-dir=$datadir" > /dev/null
 then
     echo "harbrr is running."
 else
     echo "harbrr is not running, starting harbrr.."
     screen -dmS harbrr /bin/bash -c "$bin serve --data-dir=$datadir >>$log 2>&1"
 fi
 exit
CRONC
  chmod +x "$HOME/software/cron/harbrr.cron"

  echo ""
  echo "Installed. Please configure your crontab accordingly to restart on reboots etc"
  _start

  echo ""
  echo "Adding to crontab"
  (
    crontab -l 2>/dev/null
    echo "*/5 * * * * $HOME/software/cron/harbrr.cron >/dev/null 2>&1"
  ) | crontab -

  echo ""
  echo "harbrr is now installed and running at http://$(hostname -f):${port}/harbrr/" | tee -a "${log}"
  echo "Create your admin account on the first-run setup screen." | tee -a "${log}"
}

function _remove() {
  if [[ ! -f $bin ]]; then
    echo "harbrr not installed!"
    exit 1
  fi
  echo "Killing harbrr..."
  pkill "harbrr" || true

  # backup
  _backup_install

  # Tolerate "no crontab for <user>" so the cleanup below always runs.
  crontab -l 2>/dev/null | sed -e "/harbrr/d" | crontab - || true
  echo "Removed crontab entry"

  rm -rf "$datadir"
  rm -f "$bin"

  echo "harbrr has been removed"
}

function _upgrade {
  if [[ ! -f $bin ]]; then
    echo "harbrr not installed!"
    exit 1
  fi
  pkill "harbrr" || true

  # backup
  _backup_install

  # harbrr has no self-updater — re-download the latest release
  harbrr_download_latest

  pgrep -f "$bin serve" >/dev/null || _start
  echo "harbrr has been updated!"
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
    _install
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
