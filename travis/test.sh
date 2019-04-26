#!/bin/bash

set -x -e

if [ "$TEST_SUITE" == "make" ]; then
  make BAZEL_OPTIONS="build ..." bazel
  bazel build //runsc:runsc
  make runsc
  make bazel-shutdown
  exit 0
fi

sudo ./travis/install-bazel.sh

if [ "$TEST_SUITE" == "unit" ]; then
  bazel test pkg/...
  exit 0

elif [ "$TEST_SUITE" == "docker" ]; then
  bazel build runsc/tools/dockercfg/... runsc:runsc
  ./runsc/test/install.sh --runtime runsc
  docker run --runtime=runsc hello-world
  bazel test --test_env=RUNSC_RUNTIME=runsc \
    //runsc/test/image:image_test \
    //runsc/test/integration:integration_test
  exit 0

elif [ "$TEST_SUITE" == "syscalls-aq" ]; then
  tests=$(bazel query test/syscalls/... | grep -e 'syscalls:[a-q].*ptrace$')
  bazel test $tests
  exit 0

elif [ "$TEST_SUITE" == "syscalls-rz" ]; then
  tests=$(bazel query test/syscalls/... | grep -e 'syscalls:[^a-q].*ptrace$')
  bazel test $tests
  exit 0

else
  exit 1
fi
