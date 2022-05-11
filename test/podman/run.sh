#!/bin/bash

set -x -e

test_dir=$(mktemp -d /tmp/gvisor-podman.XXXXXX)
podman_runtime=$test_dir/runsc.podman

make copy TARGETS=runsc DESTINATION=$test_dir
cat > $podman_runtime <<EOF
exec $test_dir/runsc --ignore-cgroups --network host "\$@"
EOF

podman run --runtime $podman_runtime alpine echo Hello, world
