#!/usr/bin/env bash

# Builds the ddup binary for a single release target, then packages it into an archive.
#
# Usage:
#   BUILD_ID=<id> [BUILD_VERSION=<version>] [BUILD_LDFLAGS=<flags>] [CYCLONEDX_GOMOD=<path>] [DASHBOARD_SBOM=<path>] scripts/build-binaries.sh <target>
#
# Targets:
#   linux-amd64  linux-arm64  linux-386  linux-armv7
#   macos-x64    macos-arm64
#   windows-x64  windows-arm64
#   freebsd-amd64  freebsd-arm64
#
# The binary is staged in .bin/ddup-<BUILD_ID>-<target>, and the archive is written to .out
# Windows targets produce a .zip, every other target a .tar.gz
# When CYCLONEDX_GOMOD is the path to cyclonedx-gomod, a CycloneDX SBOM for the binary is also written to .out, as ddup-<BUILD_ID>-<target>.cdx.json
# When DASHBOARD_SBOM is the path to the CycloneDX SBOM of the dashboard, it's merged into the SBOM of the binary, which embeds the dashboard

set -euo pipefail

# Resolved before anything changes the working directory
script_path="${BASH_SOURCE[0]}"
dashboard_sbom=""
if [[ -n "${DASHBOARD_SBOM:-}" ]]; then
  if [[ ! -f "${DASHBOARD_SBOM}" ]]; then
    echo "Error: DASHBOARD_SBOM is set to '${DASHBOARD_SBOM}', which is not a file" >&2
    exit 1
  fi
  dashboard_sbom="$(cd "$(dirname "${DASHBOARD_SBOM}")" && pwd)/$(basename "${DASHBOARD_SBOM}")"
fi

# Release binaries are statically linked
export CGO_ENABLED=0

usage() {
  sed -n '3,17p' "${script_path}" | sed 's|^# \?||'
}

if [[ $# -ne 1 ]]; then
  echo "Error: expected a single target" >&2
  usage >&2
  exit 1
fi

target="$1"

if [[ -z "${BUILD_ID:-}" ]]; then
  echo "Error: the BUILD_ID environment variable is required" >&2
  usage >&2
  exit 1
fi

# Extension of the executable, and format of the archive: only Windows differs
ext=""
archive="tar.gz"
goarm=""

case "${target}" in
  linux-amd64) goos="linux"; goarch="amd64" ;;
  linux-arm64) goos="linux"; goarch="arm64" ;;
  linux-386) goos="linux"; goarch="386" ;;
  linux-armv7) goos="linux"; goarch="arm"; goarm="7" ;;
  macos-x64) goos="darwin"; goarch="amd64" ;;
  macos-arm64) goos="darwin"; goarch="arm64" ;;
  windows-x64) goos="windows"; goarch="amd64"; ext=".exe"; archive="zip" ;;
  windows-arm64) goos="windows"; goarch="arm64"; ext=".exe"; archive="zip" ;;
  freebsd-amd64) goos="freebsd"; goarch="amd64" ;;
  freebsd-arm64) goos="freebsd"; goarch="arm64" ;;
  *)
    echo "Error: unknown target '${target}'" >&2
    usage >&2
    exit 1
    ;;
esac

# Run from the root of the repository, so the paths below do not depend on where the script was invoked from
cd "$(dirname "${script_path}")/.."

name="ddup-${BUILD_ID}-${target}"
stage=".bin/${name}"
binary="${stage}/ddup${ext}"

echo -e "\n###\nBuilding ${goos}/${goarch}${goarm:+v${goarm}} into ${stage}\n"

mkdir -p .out "${stage}"

GOOS="${goos}" GOARCH="${goarch}" GOARM="${goarm}" \
  go build \
    -ldflags "${BUILD_LDFLAGS:-}" \
    -o "${binary}" \
    -trimpath \
    ./cmd/ddup

# The SBOM is generated from the module list that Go embeds in the binary, so it matches exactly what was compiled for this target
# cyclonedx-gomod reads the build settings from the binary, but takes the GOOS and GOARCH in package URLs from `go env`, so they must be set to match the build
if [[ -n "${CYCLONEDX_GOMOD:-}" ]]; then
  sbom=".out/${name}.cdx.json"

  echo "Generating SBOM for ddup${ext}"
  GOOS="${goos}" GOARCH="${goarch}" GOARM="${goarm}" \
    "${CYCLONEDX_GOMOD}" bin \
      -json \
      -std \
      -version "${BUILD_VERSION:-${BUILD_ID}}" \
      -output "${sbom}" \
      "${binary}"

  # The binary embeds the dashboard, so its SBOM lists the dashboard's dependencies too
  if [[ -n "${dashboard_sbom}" ]]; then
    echo "Merging the dashboard SBOM into the SBOM for ddup${ext}"
    jq \
      --slurpfile dashboard "${dashboard_sbom}" \
      --arg version "${BUILD_VERSION:-${BUILD_ID}}" \
      -f scripts/merge-dashboard-sbom.jq \
      "${sbom}" > "${sbom}.tmp"
    mv "${sbom}.tmp" "${sbom}"
  else
    echo "Warning: DASHBOARD_SBOM is not set, so the SBOM for ddup${ext} won't list the dependencies of the dashboard it embeds" >&2
  fi
fi

cp LICENSE.md "${stage}"
cp -r README.md "${stage}"

if [[ "${archive}" == "zip" ]]; then
  (cd "${stage}" && zip -r "../../.out/${name}.zip" .)
else
  (cd .bin && tar -czvf "../.out/${name}.tar.gz" "${name}")
fi
