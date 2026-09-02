#!/bin/sh
set -eu

repository=${SSHM_GITHUB_REPOSITORY:-savuerka/ssh-manager-tui}
version=${SSHM_VERSION:-latest}

if [ "$(uname -s)" != "Darwin" ]; then
    echo "This installer is intended for macOS." >&2
    exit 1
fi
if ! command -v curl >/dev/null 2>&1; then
    echo "curl is required to install sshm." >&2
    exit 1
fi

case "$(uname -m)" in
    x86_64|amd64) architecture=amd64 ;;
    arm64|aarch64) architecture=arm64 ;;
    *) echo "Unsupported macOS architecture: $(uname -m)" >&2; exit 1 ;;
esac

asset="sshm-darwin-${architecture}"
if [ "$version" = "latest" ]; then
    download_base="https://github.com/${repository}/releases/latest/download"
else
    download_base="https://github.com/${repository}/releases/download/${version}"
fi

temporary_directory=$(mktemp -d)
trap 'rm -rf "$temporary_directory"' EXIT HUP INT TERM
curl --fail --location --silent --show-error --retry 3 \
    --output "$temporary_directory/$asset" "$download_base/$asset"
curl --fail --location --silent --show-error --retry 3 \
    --output "$temporary_directory/checksums.txt" "$download_base/checksums.txt"

expected=$(awk -v asset="$asset" '$2 == asset || $2 == "*" asset { print $1; exit }' "$temporary_directory/checksums.txt")
if [ -z "$expected" ]; then
    echo "Checksum for $asset was not found in the release." >&2
    exit 1
fi
actual=$(shasum -a 256 "$temporary_directory/$asset" | awk '{print $1}')
if [ "$actual" != "$expected" ]; then
    echo "SHA-256 verification failed for $asset." >&2
    exit 1
fi

install_directory=${SSHM_INSTALL_DIR:-/usr/local/bin}

install_files() {
    install -d -m 0755 "$install_directory"
    install -m 0755 "$temporary_directory/$asset" "$install_directory/sshm"
    ln -sfn sshm "$install_directory/ssh-manager"
}

if [ -d "$install_directory" ] && [ -w "$install_directory" ]; then
    install_files
elif [ "$(id -u)" -eq 0 ]; then
    install_files
elif command -v sudo >/dev/null 2>&1; then
    echo "Installing sshm into $install_directory (sudo is required)…"
    sudo install -d -m 0755 "$install_directory"
    sudo install -m 0755 "$temporary_directory/$asset" "$install_directory/sshm"
    sudo ln -sfn sshm "$install_directory/ssh-manager"
else
    echo "sudo is required to install sshm into $install_directory." >&2
    exit 1
fi

echo "sshm $version installed in $install_directory"
echo "Run: sshm"
