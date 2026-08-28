#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
test_root=$(mktemp -d)
trap 'rm -rf "$test_root"' EXIT INT TERM

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

expect_failure() {
  if "$@" >/dev/null 2>&1; then
    fail "command unexpectedly succeeded: $*"
  fi
}

(
  SUB2API_AUTO5H_SOURCE_ONLY=1
  export SUB2API_AUTO5H_SOURCE_ONLY
  . "$script_dir/install.sh"

  [ "$(validate_install_dir /opt/sub2apiauto5h)" = "/opt/sub2apiauto5h" ] || fail "default install directory was rejected"
  [ "$(validate_install_dir /srv/sub2api-auto5h_2)" = "/srv/sub2api-auto5h_2" ] || fail "safe custom directory was rejected"
  expect_failure validate_install_dir /
  expect_failure validate_install_dir /opt
  expect_failure validate_install_dir relative/path
  expect_failure validate_install_dir /opt/../etc
  expect_failure validate_install_dir /opt/sub2api\ auto5h
  expect_failure validate_install_dir /opt/sub2api/
  paths_overlap /opt/sub2api /opt/sub2api/data || fail "nested paths were not detected"
  paths_overlap /opt/sub2api/data /opt/sub2api || fail "parent paths were not detected"
  expect_failure paths_overlap /opt/sub2api /opt/sub2api-other

  [ "$(validate_port 1)" = "1" ] || fail "port 1 was rejected"
  [ "$(validate_port 2555)" = "2555" ] || fail "default port was rejected"
  [ "$(validate_port 65535)" = "65535" ] || fail "port 65535 was rejected"
  expect_failure validate_port 0
  expect_failure validate_port 02555
  expect_failure validate_port 65536
  expect_failure validate_port abc

  SUB2API_AUTO5H_INSTALL_DIR=/srv/from-environment
  SUB2API_AUTO5H_PORT=4321
  export SUB2API_AUTO5H_INSTALL_DIR SUB2API_AUTO5H_PORT
  [ "$(select_install_dir /opt/ignored)" = "/srv/from-environment" ] || fail "install directory environment override was ignored"
  [ "$(select_port 2555)" = "4321" ] || fail "port environment override was ignored"
  unset SUB2API_AUTO5H_INSTALL_DIR SUB2API_AUTO5H_PORT

  custom_root="/srv/sub2apiauto5h"
  rendered="$test_root/rendered.service"
  render_unit "$repo_root/packaging/sub2api-auto5h.service" "$rendered" "$custom_root" || fail "unit rendering failed"
  grep -qx "# SUB2API_AUTO5H_INSTALL_DIR=$custom_root" "$rendered" || fail "unit marker was not rendered"
  grep -qx "EnvironmentFile=$custom_root/config/sub2api-auto5h.env" "$rendered" || fail "environment path was not rendered"
  grep -qx "ExecStart=$custom_root/sub2api-auto5h serve" "$rendered" || fail "binary path was not rendered"
  grep -qx "ReadWritePaths=$custom_root/data $custom_root/backups" "$rendered" || fail "write paths were not rendered"
  ! grep -q '/opt/sub2apiauto5h' "$rendered" || fail "default root remained in custom unit"

  old_env="$test_root/legacy.env"
  new_env="$test_root/new.env"
  {
    echo 'SUB2API_AUTO5H_MASTER_KEY=known-key'
    echo 'SUB2API_AUTO5H_LISTEN=127.0.0.1:8090'
    echo 'SUB2API_AUTO5H_DB_PATH=/var/lib/sub2api-auto5h/app.db'
    echo 'SUB2API_AUTO5H_COOKIE_SECURE=true'
    echo 'SUB2API_AUTO5H_FUTURE=value'
  } >"$old_env"
  write_environment "$old_env" "$new_env" "$custom_root" 3000 "" || fail "environment rewrite failed"
  grep -qx 'SUB2API_AUTO5H_MASTER_KEY=known-key' "$new_env" || fail "master key was not preserved"
  grep -qx 'SUB2API_AUTO5H_LISTEN=127.0.0.1:3000' "$new_env" || fail "listen address was not updated"
  grep -qx "SUB2API_AUTO5H_DB_PATH=$custom_root/data/app.db" "$new_env" || fail "database path was not updated"
  grep -qx 'SUB2API_AUTO5H_COOKIE_SECURE=true' "$new_env" || fail "cookie setting was not preserved"
  grep -qx 'SUB2API_AUTO5H_FUTURE=value' "$new_env" || fail "unknown setting was not preserved"
  ! grep -q '8090' "$new_env" || fail "legacy listen port remained"
  ! grep -q '/var/lib/sub2api-auto5h/app.db' "$new_env" || fail "legacy database path remained"
  [ "$(read_configured_port "$new_env")" = "3000" ] || fail "configured port could not be read"

  fake_binary="$test_root/fake-sub2api-auto5h"
  fresh_env="$test_root/fresh.env"
  {
    echo '#!/bin/sh'
    echo "echo 'SUB2API_AUTO5H_MASTER_KEY=fresh-key'"
  } >"$fake_binary"
  chmod 0755 "$fake_binary"
  write_environment "$test_root/missing.env" "$fresh_env" "$custom_root" 2555 "$fake_binary" || fail "fresh environment generation failed"
  grep -qx 'SUB2API_AUTO5H_MASTER_KEY=fresh-key' "$fresh_env" || fail "fresh master key was not written"
  grep -qx 'SUB2API_AUTO5H_COOKIE_SECURE=false' "$fresh_env" || fail "fresh cookie default was not written"
  grep -qx 'SUB2API_AUTO5H_LISTEN=127.0.0.1:2555' "$fresh_env" || fail "fresh listen default was not written"
)

(
  SUB2API_AUTO5H_SOURCE_ONLY=1
  export SUB2API_AUTO5H_SOURCE_ONLY
  . "$script_dir/uninstall.sh"

  unit="$test_root/installed.service"
  echo '# SUB2API_AUTO5H_INSTALL_DIR=/srv/sub2apiauto5h' >"$unit"
  [ "$(read_unit_install_dir "$unit")" = "/srv/sub2apiauto5h" ] || fail "uninstaller did not discover install root"
  echo '# SUB2API_AUTO5H_INSTALL_DIR=/srv/duplicate' >>"$unit"
  expect_failure read_unit_install_dir "$unit"

  removable="$test_root/removable"
  sibling="$test_root/sibling.keep"
  mkdir -p "$removable/config" "$removable/data" "$removable/backups"
  : >"$removable/data/app.db"
  : >"$sibling"
  remove_install_root "$removable" || fail "validated temporary root was not removed"
  [ ! -e "$removable" ] || fail "temporary root still exists"
  [ -f "$sibling" ] || fail "sibling path was removed"
  expect_failure remove_install_root /
  expect_failure remove_install_root /opt

  recognizable="$test_root/recognizable"
  mkdir -p "$recognizable/config"
  : >"$recognizable/config/sub2api-auto5h.env"
  recognize_install_root "$recognizable" || fail "valid installation evidence was rejected"
  expect_failure recognize_install_root "$test_root"
)

echo "Lifecycle script tests passed."
