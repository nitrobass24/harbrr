#!/bin/bash
# harbrr installer for Ultra.cc.
# Docs: https://autobrr.com
# Repo: https://github.com/autobrr/harbrr

set -euo pipefail

datadir="$HOME/.apps/harbrr"
unit="$HOME/.config/systemd/user/harbrr.service"
nginxconf="$HOME/.apps/nginx/proxy.d/harbrr.conf"

port=""
function _port_picker() {
  #Port-Picker by XAN & Raikiri

  while [ -z "${port}" ]; do
    app-ports show
    echo "Pick any application from the list above, that you're not currently using."
    echo "We'll be using this port for your harbrr installation"
    read -rp "$(tput setaf 4)$(tput bold)Application name in full[Example: pyload]: $(tput sgr0)" appname
    proper_app_name=$(app-ports show | grep -i "${appname}" | head -n 1 | cut -c 7-) || proper_app_name=''
    port=$(app-ports show | grep -i "${appname}" | head -n 1 | awk '{print $1}') || port=''
    if [ -z "${port}" ]; then
      echo "$(tput setaf 1)Invalid choice! Please choose an application from the list and avoid typos.$(tput sgr0)"
      echo "$(tput bold)Listing all applications again..$(tput sgr0)"
      sleep 5
      clear
    fi
  done
  echo "$(tput setaf 2)Are you sure you want to use ${proper_app_name}'s port? type 'yes' to proceed.$(tput sgr0)"
  read -r input
  if [ ! "${input}" = "yes" ]; then
    exit
  fi
  echo
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

  # Download release to archive
  if ! curl "$latest" -L -o "$HOME/tmp/harbrr.tar.gz"; then
    echo "Download failed, exiting"
    exit 1
  fi

  echo "Successfully downloaded latest release"

  # Extract archive
  tar -xzf "$HOME/tmp/harbrr.tar.gz" -C "$HOME/bin"

  echo "Extracted archive $HOME/tmp/harbrr.tar.gz to $HOME/bin"

  # Remove archive
  rm "$HOME/tmp/harbrr.tar.gz"

  echo "Removed archive $HOME/tmp/harbrr.tar.gz"
}

# Create nginx conf. Takes port as argument $1
function _nginx_conf() {
  cat <<EOF | tee "${nginxconf}" >/dev/null
location /harbrr/ {
    proxy_pass              http://127.0.0.1:$1;
    proxy_http_version      1.1;
    proxy_set_header        X-Forwarded-Host       \$http_host;
}
EOF

  echo "Nginx conf created"
}

# Remove nginx conf
function _nginx_remove() {
  if [ -f "${nginxconf}" ]; then
    rm "${nginxconf}"

    # Restart nginx
    app-nginx restart

    echo "Nginx conf removed"
  fi
}

# Create systemd service
function _systemd_create_service() {

  echo "Creating systemd service"

  mkdir -p "$(dirname "${unit}")"
  cat <<EOF | tee "${unit}" >/dev/null
[Unit]
Description=harbrr service
After=syslog.target network-online.target
[Service]
Type=simple
ExecStart=%h/bin/harbrr serve --data-dir=%h/.apps/harbrr
[Install]
WantedBy=multi-user.target
EOF

  echo "Service created"
}

# Enable and start systemd service
function _systemd_enable() {
  echo "Starting harbrr.."
  systemctl --user daemon-reload
  systemctl --user --quiet enable --now "harbrr.service"
  sleep 10

  if ! systemctl --user is-active --quiet "harbrr.service"; then
    echo "Your instance of harbrr failed to start properly, install aborted. Please check port selection, HDD IO and other resource utilization."
    exit 1
  fi
}

# Start systemd service
function _systemd_start() {
  if [ -f "${unit}" ]; then
    systemctl --user start "harbrr.service"
    echo
    echo "Started harbrr service"
  fi
}

# Stop systemd service
function _systemd_stop() {
  if systemctl --user is-active --quiet "harbrr.service" || [ -f "${unit}" ]; then
    systemctl --user stop "harbrr.service"
    echo
    echo "Stop harbrr service"
  fi
}

# Stop and disable systemd service
function _systemd_disable() {
  if systemctl --user is-active --quiet "harbrr.service" || [ -f "${unit}" ]; then
    systemctl --user stop "harbrr.service"
    systemctl --user --quiet disable "harbrr.service"
    echo
    echo "Disabled harbrr service"
  fi
}

# Install harbrr
function _install() {
  if [ -d "$datadir" ]; then
    echo "Already installed"
    exit 0
  fi

  mkdir -p "${HOME}/tmp" "${HOME}/bin" "$datadir"

  # download binaries
  _download_latest_release

  # get port
  _port_picker

  echo "Selected port $port"

  # create systemd service
  _systemd_create_service

  cat <<EOF | tee "$datadir/config.toml" >/dev/null
# config.toml — written by the harbrr Ultra.cc installer.
# Full reference: https://github.com/autobrr/harbrr

[server]
host = "127.0.0.1"
port = ${port}
# Served behind Ultra's nginx under a subpath.
base_url = "/harbrr"

[log]
# trace | debug | info | warn | error
level = "info"
EOF

  # Create nginx conf
  _nginx_conf "$port"

  # Restart nginx
  app-nginx restart

  # Enable and start systemd service
  _systemd_enable

  echo "harbrr has been successfully installed!"
  echo "You can access your harbrr instance via the following URL: https://${USER}.${HOSTNAME}.usbx.me/harbrr/"
  echo "Create your admin account on the first-run setup screen."

  exit 0
}

# Backup install
function _backup_install() {
  if [ ! -d "${HOME}/.apps/backup" ]; then
    mkdir -p "${HOME}/.apps/backup"
  fi

  if [ -d "$datadir" ]; then
    backup="${HOME}/.apps/backup/harbrr-$(date +"%FT%H%M").bak.tar.gz"
    echo
    echo "Creating a backup of the current instance's data directory.."
    tar -czf "${backup}" -C "${HOME}/.apps/" "harbrr"
    echo
    echo "Created backup of harbrr: $backup"
  fi
}

# Update install
function _update() {
  if [ -d "$datadir" ]; then
    # stop
    _systemd_stop

    # backup
    _backup_install

    # harbrr has no self-updater — re-download the latest release
    mkdir -p "${HOME}/tmp"
    _download_latest_release

    sleep 4

    # restart systemd service
    _systemd_start

    echo "harbrr has been updated!"
  fi
}

# Remove install
function _remove() {
  if [ -d "$datadir" ]; then
    _systemd_disable

    if [ -f "${unit}" ]; then
      rm "${unit}"
    fi

    _backup_install

    _nginx_remove

    rm -rf "$datadir"

    if [ -f "${HOME}/bin/harbrr" ]; then
      rm "${HOME}/bin/harbrr"
    fi

    echo "harbrr has been removed!"
  fi
}

# if harbrr already exists, then ask what to do
if [ ! -d "$datadir" ]; then
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
