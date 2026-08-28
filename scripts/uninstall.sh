#!/bin/sh
set -eu
umask 077

service_name="sub2api-auto5h"
unit_file="/etc/systemd/system/sub2api-auto5h.service"
legacy_binary="/usr/local/bin/sub2api-auto5h"
legacy_config_dir="/etc/sub2api-auto5h"
legacy_state_dir="/var/lib/sub2api-auto5h"
confirmation_phrase="REMOVE sub2api-auto5h"

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

read_unit_install_dir() {
  source_unit=${1:-$unit_file}
  [ -f "$source_unit" ] || return 1
  markers=$(sed -n 's/^# SUB2API_AUTO5H_INSTALL_DIR=//p' "$source_unit")
  [ -n "$markers" ] || return 1
  [ "$(printf '%s\n' "$markers" | wc -l | tr -d ' ')" = "1" ] || return 1
  validate_install_dir "$markers"
}

remove_install_root() {
  install_root=$(validate_install_dir "$1") || return 1
  [ -e "$install_root" ] || [ -L "$install_root" ] || return 0
  rm -rf -- "$install_root"
}

recognize_install_root() {
  install_root=$(validate_install_dir "$1") || return 1
  [ -f "$install_root/sub2api-auto5h" ] ||
    [ -f "$install_root/uninstall.sh" ] ||
    [ -f "$install_root/config/sub2api-auto5h.env" ]
}

if [ "${SUB2API_AUTO5H_SOURCE_ONLY:-0}" = "1" ]; then
  return 0 2>/dev/null || exit 0
fi

main() {
  if [ "$(id -u)" -ne 0 ]; then
    die "run this uninstaller as root"
  fi
  for command_name in sed systemctl id userdel getent groupdel rm realpath; do
    command -v "$command_name" >/dev/null 2>&1 || die "required command not found: $command_name"
  done

  install_root=""
  legacy_only=false
  marked_root=""
  marked_root=$(read_unit_install_dir "$unit_file" 2>/dev/null) || marked_root=""
  if [ "${SUB2API_AUTO5H_INSTALL_DIR+x}" = "x" ]; then
    install_root=$(validate_install_dir "$SUB2API_AUTO5H_INSTALL_DIR") || die "unsafe installation directory"
    if [ -n "$marked_root" ] && [ "$marked_root" != "$install_root" ]; then
      die "specified installation directory does not match the systemd unit"
    fi
    if [ -z "$marked_root" ]; then
      recognize_install_root "$install_root" || die "specified directory is not a recognizable sub2api-auto5h installation"
    fi
  elif [ -n "$marked_root" ]; then
    install_root=$marked_root
  elif [ -f "$legacy_binary" ] || [ -d "$legacy_config_dir" ] || [ -d "$legacy_state_dir" ]; then
    install_root=""
    legacy_only=true
  else
    die "could not determine the installed sub2api-auto5h directory"
  fi

  echo "This permanently removes sub2api-auto5h, including:"
  echo "  systemd unit: $unit_file"
  if [ -n "$install_root" ]; then
    echo "  installation root: $install_root"
    echo "  configuration: $install_root/config"
    echo "  database: $install_root/data"
    echo "  local backups: $install_root/backups"
  fi
  if [ "$legacy_only" = true ] || [ -e "$legacy_binary" ] || [ -d "$legacy_config_dir" ] || [ -d "$legacy_state_dir" ]; then
    echo "  legacy binary/configuration/state paths"
  fi
  echo "  system account and group: $service_name"
  echo "This deletion cannot be undone."

  if [ "${SUB2API_AUTO5H_UNINSTALL_CONFIRM:-}" != "yes" ]; then
    [ -r /dev/tty ] && [ -w /dev/tty ] || die "no TTY available; set SUB2API_AUTO5H_UNINSTALL_CONFIRM=yes for noninteractive removal"
    printf 'Type "%s" to continue: ' "$confirmation_phrase" >/dev/tty
    reply=""
    IFS= read -r reply </dev/tty || true
    [ "$reply" = "$confirmation_phrase" ] || die "confirmation did not match; nothing was removed"
  fi

  systemctl disable --now "$service_name.service" >/dev/null 2>&1 || true
  rm -f -- "$unit_file"
  systemctl daemon-reload
  systemctl reset-failed "$service_name.service" >/dev/null 2>&1 || true

  if [ -n "$install_root" ]; then
    remove_install_root "$install_root" || die "refusing to remove unsafe installation directory"
  fi
  rm -f -- "$legacy_binary"
  rm -rf -- "$legacy_config_dir" "$legacy_state_dir"

  if id "$service_name" >/dev/null 2>&1; then
    userdel "$service_name"
  fi
  if getent group "$service_name" >/dev/null 2>&1; then
    groupdel "$service_name"
  fi
  echo "sub2api-auto5h was completely removed."
}

main "$@"
