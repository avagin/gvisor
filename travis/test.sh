#!/bin/bash

set -x -e

if [ "$TEST_SUITE" == "make" ]; then
  make BAZEL_OPTIONS="build ..." bazel
  bazel build //runsc:runsc
  make runsc
  make bazel-shutdown
  exit 0

elif [ "$TEST_SUITE" == "unit" ]; then
  make bazel-server-start
  make BAZEL_OPTIONS="test --test_tag_filters="-//pkg/sentry/platform/kvm:kvm_test,-//pkg/seccomp:seccomp_test" pkg/..." bazel
  exit 0

elif [ "$TEST_SUITE" == "docker" ]; then
  sudo ./travis/install-bazel.sh
  bazel build runsc/tools/dockercfg/... runsc:runsc
  ./runsc/test/install.sh --runtime runsc
  docker run --runtime=runsc hello-world
  bazel test --test_env=RUNSC_RUNTIME=runsc \
    //runsc/test/image:image_test \
    //runsc/test/integration:integration_test
elif [ "$TEST_SUITE" == "syscalls-aq" ]; then
  make runsc
  eval `make bazel-alias | sed 's/alias //'`
  tests=$($bazel query test/syscalls/... | grep -e 'syscalls:[a-q].*ptrace$')
  $bazel test $tests
  exit 0
elif [ "$TEST_SUITE" == "syscalls-rz" ]; then
  make runsc
  eval `make bazel-alias | sed 's/alias //'`
  tests=$($bazel query test/syscalls/... | grep -e 'syscalls:[^a-q].*ptrace$')
  $bazel test $tests
  exit 0
else
  exit 1
fi
