#!/usr/bin/env bash
# Retired after CMP50 P8/GSP wake failures. The replacement is NVML-only.
printf '%s\n' 'The P8 shell governor is disabled. Install the standalone NVML governor with install-governor.sh.' >&2
exit 1
