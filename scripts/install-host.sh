#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
REPO_DIR="$(cd -- "${SCRIPT_DIR}/.." && pwd)"
readonly REPO_DIR
readonly CONFIG_DIR=/etc/llama-stack
readonly SERVICE_USER=llama-stack
readonly SERVICE_GROUP=llama-stack
readonly ADMIN_GROUP=llama-stack-admins

skip_packages=false
skip_llama_build=false
no_start=false

usage() {
    cat <<'EOF'
Usage: sudo ./scripts/install-host.sh [options]

Options:
  --skip-packages      Do not install Fedora or NVIDIA packages
  --skip-llama-build   Do not build and install llama.cpp
  --no-start           Install and render services without starting them
EOF
}

while (($# > 0)); do
    case "$1" in
        --skip-packages)
            skip_packages=true
            ;;
        --skip-llama-build)
            skip_llama_build=true
            ;;
        --no-start)
            no_start=true
            ;;
        -h | --help)
            usage
            exit 0
            ;;
        *)
            printf 'Unknown option: %s\n' "$1" >&2
            usage >&2
            exit 2
            ;;
    esac
    shift
done

if ((EUID != 0)); then
    printf 'Run this installer with sudo.\n' >&2
    exit 1
fi

if [[ ! -r /etc/os-release ]]; then
    printf 'Cannot identify the operating system.\n' >&2
    exit 1
fi

# shellcheck source=/dev/null
source /etc/os-release
if [[ ${ID:-} != fedora || ${VERSION_ID:-} != 44 ]]; then
    printf 'Fedora 44 is required; detected %s %s.\n' "${ID:-unknown}" "${VERSION_ID:-unknown}" >&2
    exit 1
fi

if [[ $skip_packages == false ]]; then
    dnf upgrade -y
    dnf install -y \
        ca-certificates cmake curl gcc gcc-c++ git golang jq make mokutil \
        libcurl-devel ninja-build openssl podman policycoreutils-python-utils \
        python3
fi

getent group "$SERVICE_GROUP" >/dev/null || groupadd --system "$SERVICE_GROUP"
getent group "$ADMIN_GROUP" >/dev/null || groupadd --system "$ADMIN_GROUP"

if ! getent passwd "$SERVICE_USER" >/dev/null; then
    useradd \
        --system \
        --gid "$SERVICE_GROUP" \
        --create-home \
        --home-dir /var/lib/llama-stack \
        --shell /usr/sbin/nologin \
        "$SERVICE_USER"
fi

for supplemental_group in video render; do
    if getent group "$supplemental_group" >/dev/null; then
        usermod --append --groups "$supplemental_group" "$SERVICE_USER"
    fi
done

if ! grep -q "^${SERVICE_USER}:" /etc/subuid; then
    usermod --add-subuids 200000-265535 "$SERVICE_USER"
fi
if ! grep -q "^${SERVICE_USER}:" /etc/subgid; then
    usermod --add-subgids 200000-265535 "$SERVICE_USER"
fi

if [[ -n ${SUDO_USER:-} && $SUDO_USER != root ]]; then
    if ! getent passwd "$SUDO_USER" >/dev/null; then
        printf 'Cannot resolve sudo user %s through NSS.\n' "$SUDO_USER" >&2
        exit 1
    fi
    if ! getent group "$ADMIN_GROUP" | awk -F: -v user="$SUDO_USER" '
        {
            count = split($4, members, ",")
            for (member_index = 1; member_index <= count; member_index++) {
                if (members[member_index] == user) {
                    found = 1
                }
            }
        }
        END { exit !found }
    '; then
        gpasswd --add "$SUDO_USER" "$ADMIN_GROUP"
    fi
fi

install -d -m 0755 -o root -g root "$CONFIG_DIR"
install -d -m 0751 -o "$SERVICE_USER" -g "$SERVICE_GROUP" \
    /var/lib/llama-stack
install -d -m 0755 -o "$SERVICE_USER" -g "$SERVICE_GROUP" \
    /var/lib/llama-stack/models
install -d -m 0750 -o "$SERVICE_USER" -g "$SERVICE_GROUP" \
    /var/cache/llama-stack \
    /var/lib/llama-stack/artifacts \
    /var/lib/llama-stack/open-webui \
    /var/lib/llama-stack/users

if [[ ! -e "$CONFIG_DIR/config.toml" ]]; then
    install -m 0644 -o root -g root \
        "$REPO_DIR/config/config.example.toml" \
        "$CONFIG_DIR/config.toml"
fi
chown root:root "$CONFIG_DIR/config.toml"
chmod 0644 "$CONFIG_DIR/config.toml"

if [[ $skip_packages == false ]]; then
    "$SCRIPT_DIR/install-gpu.sh" "$CONFIG_DIR/config.toml"
fi

make -C "$REPO_DIR" build
install -m 0755 "$REPO_DIR/build/llama-stack" /usr/local/bin/llama-stack
install -d -m 0755 /usr/local/libexec/llama-stack
install -m 0755 "$REPO_DIR/build/llama-stackd" /usr/local/libexec/llama-stack/llama-stackd
install -m 0755 "$REPO_DIR/build/llama-stack-resource-mcp" /usr/local/libexec/llama-stack/llama-stack-resource-mcp
install -m 0755 "$REPO_DIR/build/llama-stack-search-mcp" /usr/local/libexec/llama-stack/llama-stack-search-mcp
install -d -m 0755 /usr/share/llama-stack/toolbox
install -m 0644 "$REPO_DIR/containers/toolbox/Containerfile" /usr/share/llama-stack/toolbox/Containerfile
install -m 0644 "$REPO_DIR/containers/toolbox/AGENTS.md" /usr/share/llama-stack/toolbox/AGENTS.md

if [[ $skip_llama_build == false ]]; then
    "$SCRIPT_DIR/install-llama-cpp.sh" "$CONFIG_DIR/config.toml"
fi

/usr/local/bin/llama-stack install --config "$CONFIG_DIR/config.toml" --no-start

if ! command -v nvidia-smi >/dev/null || ! nvidia-smi >/dev/null 2>&1; then
    printf '\nNVIDIA packages were installed, but the driver is not active. Reboot, then run:\n'
    printf '  sudo systemctl enable --now llama-stack.target\n'
    printf '  sudo llama-stack doctor\n'
    exit 0
fi

if [[ $no_start == true ]]; then
    printf 'Stack installed and rendered but not started (--no-start).\n'
    exit 0
fi

systemctl enable --now llama-stack.target
/usr/local/bin/llama-stack doctor
