#!/bin/bash
# harbrr installer for Bytesized Hosting.
#
# UNTESTED: carried over from the upstream qui installer, which is likewise
# marked untested — no Bytesized account was available to validate it.
#
# Docs: https://autobrr.com
# Repo: https://github.com/autobrr/harbrr

set -euo pipefail

appdir="$HOME/apps/harbrr"
datadir="$HOME/.config/harbrr"

function port() {
  LOW_BOUND=$1
  UPPER_BOUND=$2
  comm -23 <(seq "${LOW_BOUND}" "${UPPER_BOUND}" | sort) <(ss -Htan | awk '{print $4}' | cut -d':' -f2 | sort -u) | shuf -n 1
}

# Download binaries
function _download_latest_release() {
  echo
  echo "Downloading binaries.."

  # Get latest release url
  latest=$(curl -sL https://api.github.com/repos/autobrr/harbrr/releases/latest | grep "linux_x86_64" | grep browser_download_url | grep ".tar.gz" | cut -d \" -f4) || {
    echo "Failed to query GitHub for latest version"
    exit 1
  }

  if [ -z "$latest" ]; then
    echo "No linux_x86_64 .tar.gz asset in the latest release of autobrr/harbrr"
    exit 1
  fi

  # Download release to archive
  if ! curl "$latest" -L -o "$HOME/tmp/harbrr.tar.gz"; then
    echo "Download failed, exiting"
    exit 1
  fi

  echo "Successfully downloaded latest release"

  # Extract archive
  tar -xzf "$HOME/tmp/harbrr.tar.gz" -C "$appdir"

  echo "Extracted archive $HOME/tmp/harbrr.tar.gz to $appdir"

  # Remove archive
  rm "$HOME/tmp/harbrr.tar.gz"

  echo "Removed archive $HOME/tmp/harbrr.tar.gz"
}

# Create startup script
function _create_startup_script() {

  echo "Creating startup script"

  cat <<EOF | tee "$HOME/.startup/harbrr" >/dev/null
#!/bin/bash

export TMPDIR=\$HOME/tmp
/sbin/start-stop-daemon --start -u \$USER -d \$HOME -b -x \$HOME/apps/harbrr/harbrr -a \$HOME/apps/harbrr/harbrr -- serve --data-dir \$HOME/.config/harbrr
EOF

  chmod +x "$HOME/.startup/harbrr"

  echo "Service created"
}

# Create shutdown script
function _create_shutdown_script() {

  echo "Creating shutdown script"

  cat <<EOF | tee "${HOME}/.shutdown/harbrr" >/dev/null
#!/bin/bash

export TMPDIR=\$HOME/tmp
/sbin/start-stop-daemon --stop -u \$USER -d \$HOME -x \$HOME/apps/harbrr/harbrr -a \$HOME/apps/harbrr/harbrr --retry=TERM/15/KILL/10 -- serve --data-dir \$HOME/.config/harbrr
EOF

  chmod +x "$HOME/.shutdown/harbrr"

  echo "Service created"
}

# Start service
function _service_start() {
  if [ -f "${HOME}/.startup/harbrr" ]; then
    /bin/bash "$HOME"/.startup/harbrr
    echo
    echo "Started harbrr service"
  fi
}

# Stop service
function _service_shutdown() {
  if [ -f "${HOME}/.shutdown/harbrr" ]; then
    /bin/bash "$HOME"/.shutdown/harbrr
    echo
    echo "Shutdown harbrr service"
  fi
}

# Install harbrr
function _install() {
  if [ -d "$appdir" ]; then
    echo "Already installed"
    exit 0
  fi

  mkdir -p "${HOME}/tmp" "$appdir"

  # download binaries
  _download_latest_release

  # create startup and shutdown scripts
  _create_startup_script
  _create_shutdown_script

  # definitions/ is harbrr's drop-in tracker-definition directory
  mkdir -p "$datadir/definitions"

  # get port
  port=$(port 10000 15000)
  echo "port: $port"

  cat <<EOF | tee "$datadir/config.toml" >/dev/null
# config.toml — written by the harbrr Bytesized installer.
# Full reference: https://github.com/autobrr/harbrr

[server]
host = "0.0.0.0"
port = ${port}
# Served under a subpath so it can sit behind a reverse proxy unchanged.
base_url = "/harbrr"

[log]
# trace | debug | info | warn | error
level = "info"
EOF

  # Start service
  _service_start

  echo "harbrr has been successfully installed!"
  echo "You can access your harbrr instance via the following URL: http://$(hostname -f):${port}/harbrr/"
  echo "Create your admin account on the first-run setup screen."

  exit 0
}

# Backup install
function _backup_install() {
  if [ ! -d "${HOME}/apps/backup" ]; then
    mkdir -p "${HOME}/apps/backup"
  fi

  if [ -d "$datadir" ]; then
    backup="${HOME}/apps/backup/harbrr-$(date +"%FT%H%M").bak.tar.gz"
    echo
    echo "Creating a backup of the current instance's data directory.."
    tar -czf "${backup}" -C "${HOME}/.config/" "harbrr"
    echo
    echo "Created backup of harbrr: $backup"
  fi
}

# Update install
function _update() {
  if [ -d "$appdir" ]; then
    # stop
    _service_shutdown

    # backup
    _backup_install

    # harbrr has no self-updater — re-download the latest release
    mkdir -p "${HOME}/tmp"
    _download_latest_release

    sleep 4

    # restart service
    _service_start

    echo "harbrr has been updated!"
  fi
}

# Remove install
function _remove() {
  if [ -d "$appdir" ]; then

    # stop
    _service_shutdown

    # remove startup script
    if [ -f "${HOME}/.startup/harbrr" ]; then
      rm "${HOME}/.startup/harbrr"
    fi

    # remove shutdown script
    if [ -f "${HOME}/.shutdown/harbrr" ]; then
      rm "${HOME}/.shutdown/harbrr"
    fi

    _backup_install

    if [ -d "$datadir" ]; then
      rm -rf "$datadir"
    fi

    rm -rf "$appdir"

    echo "harbrr has been removed!"
  fi
}

echo
echo "NOTE: this Bytesized installer is untested — please report problems at"
echo "https://github.com/autobrr/harbrr/issues"

# if harbrr already exists, then ask what to do
if [ ! -d "$appdir" ]; then
  echo
  echo "Installing harbrr..."
  _install
else
  echo
  echo "Existing install of harbrr detected. Install will be backed up."

  select status in "Install" "Update" "Uninstall" "Backup" quit; do

    case ${status} in
    "Install")
      _install
      ;;
    "Update")
      _update
      exit 0
      ;;
    "Uninstall")
      _remove
      exit 0
      ;;
    "Backup")
      _backup_install
      exit 0
      ;;
    quit)
      exit 0
      ;;
    *)
      echo "Invalid option $REPLY"
      exit 0
      ;;
    esac
  done
fi

exit 0
