#!/bin/bash
# harbrr installer for Feral Hosting.
# docs: https://autobrr.com
# repo: https://github.com/autobrr/harbrr

user=$(whoami)
bin="$HOME/bin/harbrr"
datadir="$HOME/.config/harbrr"
nginxconf="$HOME/.nginx/conf.d/000-default-server.d/harbrr.conf"

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
  mkdir -p "$HOME/bin/"

  tar xfv "$HOME/harbrr.tar.gz" --directory "$HOME/bin/" || {
    echo "Failed to extract"
    exit 1
  }

  echo "Archive extracted"

  rm -f "$HOME/harbrr.tar.gz"
}

# Create nginx conf. Takes port as argument $1 and username as $2
function _nginx_conf() {
  cat <<EOF | tee "${nginxconf}" >/dev/null
location /harbrr/ {
    proxy_set_header X-Real-IP \$remote_addr;
    proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
    proxy_set_header Host \$http_x_host;
    proxy_set_header X-NginX-Proxy true;

    proxy_pass http://10.0.0.1:$1/$2/harbrr/;
    proxy_redirect off;
}
EOF

  # reload nginx
  _nginx_reload

  echo "Nginx conf created"
}

# Remove nginx conf
function _nginx_remove() {
  if [ -f "${nginxconf}" ]; then
    rm "${nginxconf}"

    # reload nginx
    _nginx_reload

    echo "Nginx conf removed"
  fi
}

function _nginx_reload() {
  /usr/sbin/nginx -s reload -c ~/.nginx/nginx.conf
}

function _start() {
  screen -dmS harbrr /bin/bash -c "$bin serve --data-dir=$datadir"
}

function _install() {
  # set PATH so it includes user's private bin if it exists
  if ! grep -qs "$HOME/bin" "$HOME/.profile"; then
    cat <<EOF >>"$HOME/.profile"
if [ -d "$HOME/bin" ] ; then
    PATH="$HOME/bin:\$PATH"
fi
EOF
  fi

  _download
  port=$(port 10000 12000)

  mkdir -p "$datadir"

  cat >"$datadir/config.toml" <<CFG
# config.toml — written by the harbrr Feral installer.
# Full reference: https://github.com/autobrr/harbrr

[server]
# Feral's nginx reaches the app over the internal interface.
host = "10.0.0.1"
port = ${port}
# Served behind Feral's nginx under a subpath.
base_url = "/${user}/harbrr"

[log]
# trace | debug | info | warn | error
level = "info"
CFG

  # Create nginx conf
  _nginx_conf "$port" "${user}"

  cat >"$HOME/.harbrr.cron" <<CRONC
 #!/bin/bash
 if pgrep -f "$bin serve --data-dir=$datadir" > /dev/null
 then
     echo "harbrr is running."
 else
     echo "harbrr is not running, starting harbrr"
     screen -dmS harbrr /bin/bash -c "$bin serve --data-dir=$datadir"
 fi
 exit
CRONC
  chmod +x "$HOME/.harbrr.cron"

  _start

  echo ""
  echo "Adding to crontab"
  (
    crontab -l 2>/dev/null
    echo "*/5 * * * * $HOME/.harbrr.cron >/dev/null 2>&1"
  ) | crontab -

  echo ""
  echo "Installation complete!"
  echo ""
  echo "Now browse to your harbrr instance at https://$HOSTNAME.feralhosting.com/$user/harbrr/"
  echo "and create your admin account on the first-run setup screen."
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

function _kill_harbrr() {
  # First try to kill the screen session
  if screen -list | grep -q "harbrr"; then
    screen -S harbrr -X quit
    echo "Killed harbrr screen session"
  fi

  # Then kill any remaining processes matching the exact command
  if pgrep -f "$bin serve --data-dir=$datadir" > /dev/null; then
    pkill -f "$bin serve --data-dir=$datadir"
    echo "Killed harbrr process"
  fi
}

function _update() {
  if [[ ! -f $bin ]]; then
    echo "harbrr not installed!"
    exit 1
  fi

  _kill_harbrr

  # backup
  _backup_install

  # harbrr has no self-updater — re-download the latest release
  _download

  _start
  echo "harbrr has been updated!"
}

function _remove() {
  if [[ ! -f $bin ]]; then
    echo "harbrr not installed!"
    exit 1
  fi
  echo "Killing harbrr..."
  _kill_harbrr

  # backup
  _backup_install

  _nginx_remove

  crontab -l 2>/dev/null | sed -e "/harbrr/d" | crontab -
  echo "Removed crontab entry"

  rm -rf "$datadir"
  rm -f "$bin"
  rm -f "$HOME/.harbrr.cron"

  echo "harbrr has been removed"
}

# if harbrr already exists, then ask what to do
if [ ! -d "$datadir" ]; then
  echo
  echo "Installing harbrr..."
  _install
  exit 0
else
  echo
  echo "Existing install of harbrr detected. Install will be backed up."

  select status in "Install" "Update" "Uninstall" "Backup" quit; do

    case ${status} in
    "Install")
      _install
      exit 0
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
