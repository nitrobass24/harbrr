#!/bin/bash
# harbrr installer for Whatbox.
# docs: https://autobrr.com
# repo: https://github.com/autobrr/harbrr

bin="$HOME/.local/bin/harbrr"
datadir="$HOME/.config/harbrr"

function port() {
  LOW_BOUND=$1
  UPPER_BOUND=$2
  comm -23 <(seq "${LOW_BOUND}" "${UPPER_BOUND}" | sort) <(ss -Htan | awk '{print $4}' | cut -d':' -f2 | sort -u) | shuf -n 1
}

function _download() {
  latest=$(curl -sL https://api.github.com/repos/autobrr/harbrr/releases/latest | grep "linux_x86_64" | grep browser_download_url | grep ".tar.gz" | cut -d \" -f4) || {
    echo "Failed to query GitHub for latest version"
    exit 1
  }

  if [ -z "$latest" ]; then
    echo "No linux_x86_64 .tar.gz asset in the latest release of autobrr/harbrr"
    exit 1
  fi

  if ! curl "$latest" -L -o "$HOME/harbrr.tar.gz"; then
    echo "Download failed, exiting"
    exit 1
  fi
  echo "Archive downloaded"

  echo "Extracting archive"
  mkdir -p "$HOME/.local/bin/"

  tar xfv "$HOME/harbrr.tar.gz" --directory "$HOME/.local/bin/" || {
    echo "Failed to extract"
    exit 1
  }
  rm -f "$HOME/harbrr.tar.gz"
  echo "Archive extracted"
}

function _start() {
  screen -dmS harbrr /bin/bash -c "$bin serve --data-dir=$datadir"
}

function _install() {
  # set PATH so it includes user's private bin if it exists
  if ! grep -qs ".local/bin" "$HOME/.profile"; then
    cat <<EOF >>"$HOME/.profile"
if [ -d "$HOME/.local/bin" ] ; then
    PATH="$HOME/.local/bin:\$PATH"
fi
EOF
  fi

  _download
  port=$(port 10000 12000)
  mkdir -p "$datadir"

  cat >"$datadir/config.toml" <<CFG
# config.toml — written by the harbrr Whatbox installer.
# Full reference: https://github.com/autobrr/harbrr

[server]
host = "0.0.0.0"
port = ${port}
# Whatbox serves this app on its own subdomain, so harbrr stays at the root.
# Set base_url (e.g. "/harbrr", no trailing slash) only for subpath serving.
#base_url = ""

[log]
# trace | debug | info | warn | error
level = "info"
CFG

  cat >"$HOME/.harbrr_restart.cron" <<CRONC
 #!/bin/bash
 if pgrep -f "$bin serve --data-dir=$datadir" > /dev/null
 then
     echo "harbrr is running."
 else
     echo "harbrr is not running, starting harbrr.."
     screen -dmS harbrr /bin/bash -c "$bin serve --data-dir=$datadir"
 fi
 exit
CRONC
  chmod +x "$HOME/.harbrr_restart.cron"

  echo "Installed. Please configure your crontab accordingly to restart on reboots etc"
  _start

  echo "@reboot $HOME/.harbrr_restart.cron >/dev/null 2>&1"
  echo "*/5 * * * * $HOME/.harbrr_restart.cron >/dev/null 2>&1"
  echo "crontab -e"

  srvName="${HOSTNAME%.whatbox.ca}"

  echo "Now browse to https://whatbox.ca/manage/domain/$srvName"
  echo "Simply add your port: ${port} and set appname to: harbrr"

  echo "After that browse to your harbrr instance at: https://harbrr.$HOSTNAME/"
  echo "and create your admin account on the first-run setup screen."
}

function _update() {
  pkill -f "$bin serve" || true
  _backup_install

  # harbrr has no self-updater — re-download the latest release
  _download
  pgrep -f "$bin serve" >/dev/null || _start

  echo "harbrr has been updated!"
}

function _remove() {
  pkill -f "$bin serve" || true

  _backup_install

  # Tolerate "no crontab for <user>" so the cleanup below always runs.
  crontab -l 2>/dev/null | sed -e "/harbrr/d" | crontab - || true
  echo "Removed crontab entry"

  rm -rf "$datadir"
  rm -f "$bin"
  rm -f "$HOME/.harbrr_restart.cron"

  echo "harbrr has been removed"
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
    _update
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
