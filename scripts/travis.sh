#!/bin/bash

uname -a

set -x -e

RUNSC_PATH=$1

make DOCKER_RUN_OPTIONS="" BAZEL_OPTIONS="build runsc:runsc" bazel

for i in `seq 10`; do
  $RUNSC_PATH --alsologtostderr --network none --strace --debug --TESTONLY-unsafe-nonroot=true --rootless do ls
done
