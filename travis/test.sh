#!/bin/bash

set -x -e

BAZEL_OPTS="--show_progress_rate_limit=5 --show_task_finish --show_timestamps"

if [ "$TEST_SUITE" == "make" ]; then
  make BAZEL_OPTIONS="build ..." bazel
  eval `make bazel-alias | sed 's/alias //'`
  $bazel build //runsc:runsc
  make runsc
  make bazel-shutdown
  exit 0
fi

sudo ./travis/install-bazel.sh

if [ "$TEST_SUITE" == "unit" ]; then
  bazel test $BAZEL_OPTS `bazel query pkg/... | grep -v kvm_test`
  exit 0

elif [ "$TEST_SUITE" == "docker" ]; then
  bazel build $BAZEL_OPTS runsc/tools/dockercfg/... runsc:runsc
  ./runsc/test/install.sh --runtime runsc
  docker run --runtime=runsc hello-world
  bazel test $BAZEL_OPTS --test_env=RUNSC_RUNTIME=runsc \
    //runsc/test/image:image_test \
    //runsc/test/integration:integration_test
  exit 0

elif [ "$TEST_SUITE" == "syscalls-aq" ]; then
  tests=$(bazel query $BAZEL_OPTS test/syscalls/... | grep -e 'syscalls:[a-q].*ptrace$')
  bazel test $BAZEL_OPTS $tests
  exit 0

elif [ "$TEST_SUITE" == "syscalls-rz" ]; then
  tests=$(bazel query $BAZEL_OPTS test/syscalls/... | grep -e 'syscalls:[^a-q].*ptrace$')
  bazel test $BAZEL_OPTS $tests
  exit 0

else
  exit 1
fi
