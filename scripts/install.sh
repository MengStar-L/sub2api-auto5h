#!/bin/sh
set -eu
umask 077

repo="MengStar-L/sub2api-auto5h"
service_name="sub2api-auto5h"
default_install_dir="/opt/sub2apiauto5h"
default_port="2555"
unit_file="/etc/systemd/system/sub2api-auto5h.service"
legacy_binary="/usr/local/bin/sub2api-auto5h"
legacy_config_dir="/etc/sub2api-auto5h"
legacy_state_dir="/var/lib/sub2api-auto5h"

die() {
  echo "Error: $*" >&2
  exit 1
}

validate_install_dir() {
  value=${1:-}
  case "$value" in
    /*) ;;
    *) return 1 ;;
  esac
  case "$value" in
    *[!A-Za-z0-9_./-]*|*//*|*/./*|*/../*|*/.|*/..|*/) return 1 ;;
  esac
  case "$value" in
    /|/bin|/boot|/dev|/etc|/home|/lib|/lib64|/media|/mnt|/opt|/proc|/root|/run|/sbin|/srv|/sys|/tmp|/usr|/usr/local|/usr/local/bin|/var) return 1 ;;
  esac
  normalized=$(realpath -m -- "$value") || return 1
  [ "$normalized" = "$value" ] || return 1
  printf '%s\n' "$normalized"
}

paths_overlap() {
  first=$(validate_install_dir "$1") || return 1
  second=$(validate_install_dir "$2") || return 1
  case "$first/" in
    "$second/"*) return 0 ;;
  esac
  case "$second/" in
    "$first/"*) return 0 ;;
  esac
  return 1
}

validate_port() {
  value=${1:-}
  case "$value" in
    ""|0|0*|*[!0-9]*) return 1 ;;
  esac
  if [ "$value" -gt 65535 ]; then
    return 1
  fi
  printf '%s\n' "$value"
}

read_unit_install_dir() {
  source_unit=${1:-$unit_file}
  [ -f "$source_unit" ] || return 1
  markers=$(sed -n 's/^# SUB2API_AUTO5H_INSTALL_DIR=//p' "$source_unit")
  [ -n "$markers" ] || return 1
  [ "$(printf '%s\n' "$markers" | wc -l | tr -d ' ')" = "1" ] || return 1
  validate_install_dir "$markers"
}

read_configured_port() {
  source_env=${1:-}
  [ -f "$source_env" ] || return 1
  value=$(sed -n 's/^SUB2API_AUTO5H_LISTEN=.*:\([0-9][0-9]*\)$/\1/p' "$source_env" | tail -n 1)
  validate_port "$value"
}

prompt_value() {
  label=$1
  default_value=$2
  if [ -r /dev/tty ] && [ -w /dev/tty ]; then
    printf '%s [%s]: ' "$label" "$default_value" >/dev/tty
    reply=""
    IFS= read -r reply </dev/tty || true
    if [ -n "$reply" ]; then
      printf '%s\n' "$reply"
      return
    fi
  fi
  printf '%s\n' "$default_value"
}

select_install_dir() {
  suggested=$1
  if [ "${SUB2API_AUTO5H_INSTALL_DIR+x}" = "x" ]; then
    selected=$SUB2API_AUTO5H_INSTALL_DIR
  else
    selected=$(prompt_value "Installation directory" "$suggested")
  fi
  validate_install_dir "$selected" || die "installation directory must be a safe absolute path below a dedicated directory"
}

select_port() {
  suggested=$1
  if [ "${SUB2API_AUTO5H_PORT+x}" = "x" ]; then
    selected=$SUB2API_AUTO5H_PORT
  else
    selected=$(prompt_value "Listen port" "$suggested")
  fi
  validate_port "$selected" || die "listen port must be an integer from 1 to 65535 without leading zeroes"
}

render_unit() {
  template=$1
  output=$2
  install_root=$(validate_install_dir "$3") || return 1
  sed "s|$default_install_dir|$install_root|g" "$template" >"$output"
  grep -qx "# SUB2API_AUTO5H_INSTALL_DIR=$install_root" "$output"
  if [ "$install_root" != "$default_install_dir" ] && grep -q "$default_install_dir" "$output"; then
    return 1
  fi
}

write_environment() {
  existing_env=$1
  output_env=$2
  install_root=$(validate_install_dir "$3") || return 1
  listen_port=$(validate_port "$4") || return 1
  key_binary=${5:-}
  temporary="$output_env.new"

  if [ -f "$existing_env" ]; then
    sed '/^SUB2API_AUTO5H_LISTEN=/d; /^SUB2API_AUTO5H_DB_PATH=/d' "$existing_env" >"$temporary"
  else
    [ -n "$key_binary" ] && [ -x "$key_binary" ] || return 1
    master_key=$($key_binary keygen | sed -n 's/^SUB2API_AUTO5H_MASTER_KEY=//p')
    [ -n "$master_key" ] || return 1
    {
      printf 'SUB2API_AUTO5H_MASTER_KEY=%s\n' "$master_key"
      printf 'SUB2API_AUTO5H_COOKIE_SECURE=false\n'
    } >"$temporary"
  fi
  {
    printf 'SUB2API_AUTO5H_LISTEN=0.0.0.0:%s\n' "$listen_port"
    printf 'SUB2API_AUTO5H_DB_PATH=%s/data/app.db\n' "$install_root"
  } >>"$temporary"
  mv -f "$temporary" "$output_env"
}

directory_is_empty() {
  directory=$1
  [ ! -e "$directory" ] && return 0
  [ -d "$directory" ] || return 1
  [ ! -L "$directory" ] || return 1
  [ -z "$(find "$directory" -mindepth 1 -maxdepth 1 -print -quit)" ]
}

backup_database() {
  source_data=$1
  backup_dir=$2
  backup_version=$3
  [ -d "$source_data" ] || return 0
  set --
  for name in app.db app.db-wal app.db-shm; do
    if [ -f "$source_data/$name" ]; then
      set -- "$@" "$name"
    fi
  done
  [ "$#" -gt 0 ] || return 0
  backup="$backup_dir/backup-before-$backup_version-$(date -u +%Y%m%dT%H%M%SZ).tar.gz"
  tar -czf "$backup" -C "$source_data" "$@"
  chown "$service_name:$service_name" "$backup"
  chmod 0600 "$backup"
}

copy_database() {
  source_data=$1
  target_data=$2
  [ -d "$source_data" ] || return 0
  for name in app.db app.db-wal app.db-shm; do
    if [ -f "$source_data/$name" ]; then
      cp -p "$source_data/$name" "$target_data/$name"
    fi
  done
}

copy_backups() {
  source_backups=$1
  target_backups=$2
  [ -d "$source_backups" ] || return 0
  for backup in "$source_backups"/backup-*.tar.gz; do
    [ -f "$backup" ] || continue
    cp -p "$backup" "$target_backups/"
  done
}

if [ "${SUB2API_AUTO5H_SOURCE_ONLY:-0}" = "1" ]; then
  return 0 2>/dev/null || exit 0
fi

main() {
  if [ "$(id -u)" -ne 0 ]; then
    die "run this installer as root"
  fi

  for command_name in curl sed grep sha256sum tar install systemctl getent groupadd useradd usermod find cp chown chmod mv rm mktemp head tail wc tr date realpath; do
    command -v "$command_name" >/dev/null 2>&1 || die "required command not found: $command_name"
  done

  current_root=""
  if current_root=$(read_unit_install_dir "$unit_file" 2>/dev/null); then
    current_config="$current_root/config/sub2api-auto5h.env"
    current_data="$current_root/data"
    current_backups="$current_root/backups"
    suggested_root=$current_root
  else
    current_root=""
    current_config=""
    current_data=""
    current_backups=""
    suggested_root=$default_install_dir
    if [ -f "$legacy_config_dir/sub2api-auto5h.env" ] || [ -f "$legacy_state_dir/app.db" ] || [ -f "$legacy_binary" ]; then
      current_config="$legacy_config_dir/sub2api-auto5h.env"
      current_data=$legacy_state_dir
      current_backups=$legacy_state_dir
    fi
  fi

  suggested_port=$default_port
  if [ -n "$current_config" ] && detected_port=$(read_configured_port "$current_config" 2>/dev/null); then
    suggested_port=$detected_port
  fi
  install_root=$(select_install_dir "$suggested_root")
  listen_port=$(select_port "$suggested_port")

  if [ -L "$install_root" ]; then
    die "installation directory must not be a symbolic link"
  fi
  if [ -n "$current_root" ] && [ "$current_root" != "$install_root" ] && paths_overlap "$current_root" "$install_root"; then
    die "new and previous installation directories must not contain one another"
  fi
  if [ -z "$current_root" ] && [ -n "$current_config" ]; then
    if paths_overlap "$legacy_config_dir" "$install_root" || paths_overlap "$legacy_state_dir" "$install_root"; then
      die "new installation directory must not overlap the legacy configuration or state directory"
    fi
  fi
  if [ -z "$current_root" ] || [ "$current_root" != "$install_root" ]; then
    directory_is_empty "$install_root" || die "target installation directory is not empty"
  fi

  case "$(uname -m)" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) die "unsupported architecture: $(uname -m)" ;;
  esac

  version="${SUB2API_AUTO5H_VERSION:-latest}"
  if [ "$version" = "latest" ]; then
    version=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
  fi
  [ -n "$version" ] || die "could not resolve a release version"
  case "$version" in
    *[!A-Za-z0-9._-]*) die "release version contains unsafe characters" ;;
  esac

  asset="sub2api-auto5h-linux-$arch.tar.gz"
  base="https://github.com/$repo/releases/download/$version"
  tmp_dir=$(mktemp -d)
  trap 'rm -rf "$tmp_dir"' EXIT INT TERM
  curl -fsSL "$base/$asset" -o "$tmp_dir/$asset"
  curl -fsSL "$base/SHA256SUMS" -o "$tmp_dir/SHA256SUMS"
  (cd "$tmp_dir" && grep "  $asset$" SHA256SUMS | sha256sum -c -)
  tar -xzf "$tmp_dir/$asset" -C "$tmp_dir"
  [ -x "$tmp_dir/sub2api-auto5h" ] || die "release archive has no executable binary"
  [ -f "$tmp_dir/sub2api-auto5h.service" ] || die "release archive has no systemd unit"
  [ -f "$tmp_dir/uninstall.sh" ] || die "release archive has no uninstall script"

  if systemctl is-active --quiet "$service_name.service"; then
    systemctl stop "$service_name.service"
  fi

  if ! getent group "$service_name" >/dev/null 2>&1; then
    groupadd --system "$service_name"
  fi
  if ! id "$service_name" >/dev/null 2>&1; then
    useradd --system --gid "$service_name" --home-dir "$install_root/data" --shell /usr/sbin/nologin "$service_name"
  fi

  config_dir="$install_root/config"
  data_dir="$install_root/data"
  backups_dir="$install_root/backups"
  install -d -m 0750 -o root -g "$service_name" "$install_root" "$config_dir"
  install -d -m 0700 -o "$service_name" -g "$service_name" "$data_dir" "$backups_dir"

  if [ -n "$current_data" ]; then
    backup_database "$current_data" "$backups_dir" "$version"
    if [ "$current_data" != "$data_dir" ]; then
      copy_database "$current_data" "$data_dir"
    fi
  fi
  if [ -n "$current_backups" ] && [ "$current_backups" != "$backups_dir" ]; then
    copy_backups "$current_backups" "$backups_dir"
  fi
  chown -R "$service_name:$service_name" "$data_dir" "$backups_dir"
  chmod 0700 "$data_dir" "$backups_dir"

  target_env="$config_dir/sub2api-auto5h.env"
  source_env=$target_env
  if [ -n "$current_config" ] && [ -f "$current_config" ]; then
    source_env=$current_config
  fi
  write_environment "$source_env" "$target_env" "$install_root" "$listen_port" "$tmp_dir/sub2api-auto5h" || die "could not write environment file"
  chown root:"$service_name" "$target_env"
  chmod 0600 "$target_env"

  install -m 0755 "$tmp_dir/sub2api-auto5h" "$install_root/sub2api-auto5h.new"
  mv -f "$install_root/sub2api-auto5h.new" "$install_root/sub2api-auto5h"
  install -m 0755 "$tmp_dir/uninstall.sh" "$install_root/uninstall.sh.new"
  mv -f "$install_root/uninstall.sh.new" "$install_root/uninstall.sh"
  render_unit "$tmp_dir/sub2api-auto5h.service" "$tmp_dir/sub2api-auto5h.service.rendered" "$install_root" || die "could not render systemd unit"
  install -m 0644 "$tmp_dir/sub2api-auto5h.service.rendered" "$unit_file"
  usermod --home "$data_dir" "$service_name"

  systemctl daemon-reload
  if ! systemctl enable --now "$service_name.service" || ! systemctl is-active --quiet "$service_name.service"; then
    die "service failed to start; database and any migration source were retained for recovery"
  fi

  if [ -n "$current_root" ] && [ "$current_root" != "$install_root" ]; then
    validated_old_root=$(validate_install_dir "$current_root") || die "refusing to clean unsafe previous installation path"
    rm -rf -- "$validated_old_root"
  elif [ -z "$current_root" ] && [ -n "$current_config" ]; then
    rm -f -- "$legacy_binary"
    rm -rf -- "$legacy_config_dir" "$legacy_state_dir"
  fi

  echo "Installed sub2api-auto5h $version."
  echo "Installation directory: $install_root"
  echo "Panel address: http://127.0.0.1:$listen_port"
  echo "Read the setup token with: journalctl -u $service_name -n 30 --no-pager"
}

main "$@"
