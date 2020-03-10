#!/bin/bash

# Copyright 2019 The gVisor Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

source $(dirname $0)/common.sh

# TODO(b/112165693): "test --test_tag_filters=runsc_kvm" can be used
# when the "manual" tag will be removed for kvm tests.
#test --test_timeout=200 //test/syscalls:socket_ip_tcp_generic_loopback_test_runsc_kvm

bazel build runsc:runsc
bazel build //test/syscalls/linux:socket_ip_tcp_generic_loopback_test
bazel-bin/runsc/linux_amd64_pure_stripped/runsc --rootless --network=none --debug --alsologtostderr do bazel-bin/test/syscalls/linux/socket_ip_tcp_generic_loopback_test '--gtest_filter=AllTCPSockets/TCPSocketPairTest.TCPResetDuringClose_NoRandomSave/*'

# test `bazel query "attr(tags, runsc_kvm, tests(//test/syscalls/...))"`
