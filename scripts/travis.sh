#!/bin/bash

uname -a

set -x -e

DOCKER_IMAGE=$1
RUNSC_PATH=$2

make DOCKER_RUN_OPTIONS="" BAZEL_OPTIONS="build runsc:runsc" DOCKER_IMAGE="$DOCKER_IMAGE" bazel

for i in `seq 10`; do
  $RUNSC_PATH --alsologtostderr --network none --strace --debug --TESTONLY-unsafe-nonroot=true --rootless do ls
done
