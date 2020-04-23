#!/bin/bash

set -xe

uname -a

make DOCKER_RUN_OPTIONS="" BAZEL_OPTIONS="build runsc:runsc" bazel
$RUNSC_PATH --alsologtostderr --network none --debug --TESTONLY-unsafe-nonroot=true --rootless do ls

# make DOCKER_RUN_OPTIONS="" BAZEL_OPTIONS="build --keep_going //test/syscalls/..." bazel

make DOCKER_RUN_OPTIONS="" BAZEL_OPTIONS="build //test/syscalls/linux:kill_test" bazel
$RUNSC_PATH --alsologtostderr --network none --debug --TESTONLY-unsafe-nonroot=true --rootless do bazel-bin/test/syscalls/linux/kill_test

