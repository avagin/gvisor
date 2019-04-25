#!/bin/bash

ls -l /home/travis/.cache/
ls -l /home/travis/.cache/bazel/
ls -l /home/travis/.cache/bazel/_bazel_gvisor
sudo chown -R travis /home/travis/.cache/

set -x -e

if [ "$TEST_SUITE" == "make" ]; then
  make BAZEL_OPTIONS="build ..." bazel
  bazel build //runsc:runsc
  make runsc
  make bazel-shutdown
  exit 0

elif [ "$TEST_SUITE" == "unit" ]; then
  make bazel-server-start
  make BAZEL_OPTIONS="test pkg/..." bazel
  exit 0

elif [ "$TEST_SUITE" == "docker" ]; then
  make BAZEL_OPTIONS="build runsc/tools/dockercfg/..." bazel
  make runsc
  make bazel-shutdown
  ./runsc/test/install.sh --runtime runsc
  docker run --runtime=runsc hello-world
elif [ "$TEST_SUITE" == "syscalls-aq" ]; then
  make runsc
  eval `make bazel-alias | sed 's/alias //'`
  tests=$($bazel query test/syscalls/... | grep -e 'syscalls:[a-q].*ptrace$')
  $bazel test $tests
  exit 0
elif [ "$TEST_SUITE" == "syscalls-aq" ]; then
  make runsc
  eval `make bazel-alias | sed 's/alias //'`
  tests=$($bazel query test/syscalls/... | grep -e 'syscalls:[^a-q].*ptrace$')
  $bazel test $tests
  exit 0
else
  exit 1
fi
